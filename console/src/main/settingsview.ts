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

/** SettingsDryRun is P3's dry-run document (`settingsWriteDoc`), read for the
 * ten members this window draws from: the types are strict and a member this
 * build does not know is ignored (the owner's ruling of 2026-10-30), so the
 * four optional entry members version 1 has since grown (D1164) neither shape a
 * window nor refuse one. */
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

// ---------------------------------------------------------------------------
// The entry form (SU1, D2902)
// ---------------------------------------------------------------------------
//
// **One main-process modal does entry, the dry run and the confirmation**
// (D2902, overruling the pencil-then-dry-run shape of C2): the window the
// operator types in is the window the dry run's diff is drawn in, so this half
// is what that window is shown **before** any dry run exists -- the metadata
// SM-E emits, the value a run reads now, and the built-in default R6 names.
//
// **What the client validation is, and what it is not.** R5: "the dialog
// validates on the client from the registry metadata (type, enum, min, max,
// format) and the dry run is the authority at OK". So `settingsEntryError` is a
// *pre-check for the OK button* -- OK is disabled while it answers a sentence --
// and never the decision: a value it passes still reaches the CLI, which
// refuses what its own reader refuses. That is why each grammar below is the
// permissive one (a duration as `config.ParseDuration` reads it, an https URL as
// `repourl.CloneURL` takes one) rather than a second copy of the CLI's rules,
// and why the sentences are the console's own plain language rather than the
// CLI's: the CLI's own refusal is what the operator sees when one gets through.

/** SettingsEntryControl is the control one key's value calls for, and it is the
 * four the design names (spec §3, R5): a select for a small enum, a toggle for a
 * bool, a number input with min and max, and a text field for everything else --
 * a string, and a duration, whose bound is a duration and whose value is
 * therefore typed rather than stepped. */
export type SettingsEntryControl = 'select' | 'toggle' | 'number' | 'text'

/** SETTINGS_ENTRY_CAVEAT is R6's second half: the window shows the built-in
 * default **and** says that a reset is not the end of the story, because the
 * layer this window writes is one of several and a layer it loses to may still
 * decide the key (F8: a reset writes no value at all). */
export const SETTINGS_ENTRY_CAVEAT =
  'unsetting writes no value here: the built-in default applies where no other layer decides the key'

/** SETTINGS_ENTRY_READONLY is what the window says where it draws no control,
 * and the command the read names for the key is drawn beside it. It is the
 * console's own sentence about a row the CLI's own `writable: false` marks --
 * a map entry, a block, a family-owned key, and every key spec §4.4 keeps for
 * another command -- and it is a **refusal of the write, not of the window**: the
 * operator reads the key, its value and the command that does write it.
 *
 * A key whose write is `--add V` or `--remove V` is the one class this cannot
 * reach from the read's own members: `settings set` accepts such a key, so the
 * read calls it writable, and no member says which spelling of that command a
 * key takes. The dry run -- R5's authority -- refuses a scalar written to one
 * with the CLI's own sentence naming the flag, and that sentence is shown
 * verbatim. */
export const SETTINGS_ENTRY_READONLY = 'this key is not written by this window'

/** SETTINGS_ENTRY_REQUIRED is what an empty text or number field is refused
 * with: a setting is written with a value, and the empty string is not one the
 * CLI's readers take for these types. */
export const SETTINGS_ENTRY_REQUIRED = 'this setting needs a value'

/** SETTINGS_ENTRY_TOGGLE is a value that is neither on nor off. */
export const SETTINGS_ENTRY_TOGGLE = 'this setting is on or off'

/** SETTINGS_ENTRY_SELECT is a value outside the set the CLI lists. */
export const SETTINGS_ENTRY_SELECT = 'this setting takes one of the values the CLI lists'

/** SETTINGS_ENTRY_WHOLE and SETTINGS_ENTRY_NUMBER are a value that is not the
 * number the key's type takes: a whole number for `int`, a number for `float`. */
export const SETTINGS_ENTRY_WHOLE = 'this setting takes a whole number'
export const SETTINGS_ENTRY_NUMBER = 'this setting takes a number'

/** SETTINGS_ENTRY_DURATION, SETTINGS_ENTRY_MEMORY, SETTINGS_ENTRY_REPO,
 * SETTINGS_ENTRY_EMAIL, SETTINGS_ENTRY_URL and SETTINGS_ENTRY_BRANCH are the six
 * formats of `MetaFormat`, each named for the shape the CLI accepts rather than
 * for the key that carries it. */
export const SETTINGS_ENTRY_DURATION = 'this setting takes a duration, such as 90m or 14d'
export const SETTINGS_ENTRY_MEMORY = 'this setting takes a memory size, such as 512Mi or 16Gi'
export const SETTINGS_ENTRY_REPO = 'this setting takes a repository, as owner/name'
export const SETTINGS_ENTRY_EMAIL = 'this setting takes an e-mail address'
export const SETTINGS_ENTRY_URL = 'this setting takes an https URL'
export const SETTINGS_ENTRY_BRANCH = 'this setting takes a git branch name'

/** SETTINGS_ENTRY_BELOW_MIN and SETTINGS_ENTRY_ABOVE_MAX lead the bound the
 * metadata gives, in the value's own spelling, closed by the parenthesis the
 * window appends: a bound printed in a second spelling (a duration as seconds)
 * would be a number the operator never typed. */
export const SETTINGS_ENTRY_BELOW_MIN = 'this setting is below its lowest allowed value ('
export const SETTINGS_ENTRY_ABOVE_MAX = 'this setting is above its highest allowed value ('

/** SettingsEntryMeta is the eight metadata members of one row (SM-E), as the
 * read emitted them. Each is the CLI's own string and none is decoded here: an
 * absent member is the fallback (`label` empty means the raw key, `type` empty
 * means a text control), which is D2908's rule for a key the table has no entry
 * for -- and, for a value this console does not know (a type a newer CLI grew),
 * the same fallback rather than a refused screen. */
export interface SettingsEntryMeta {
  readonly label: string
  readonly description: string
  readonly group: string
  readonly type: string
  readonly enum: readonly string[]
  readonly min: string
  readonly max: string
  readonly format: string
}

/** SettingsEntryDefault is the built-in default R6 draws: the value and the
 * constant holding it, or the sentence saying what unset means. The two are the
 * read document's `default.value` and `default.unset`, and both are empty for a
 * row the CLI printed no default for. */
export interface SettingsEntryDefault {
  readonly value: string
  readonly unset: string
}

/** SettingsEntryRow is the read's own row as the entry form needs it: C2's four
 * members, plus the write's own answer (`writable`, D1102), the command the read
 * names for a key this path does not write (`owner`, D1178), and the two blocks
 * above. It is a structural superset of `SettingsViewRow`, so the flow decodes
 * one row and both halves of the window are built from it. */
export interface SettingsEntryRow extends SettingsViewRow {
  readonly writable: boolean
  readonly owner: string
  readonly meta: SettingsEntryMeta
  readonly default: SettingsEntryDefault
}

/** SettingsEntryInputs is what the form is built from: the project the window
 * writes (its identity, `settingsConfirmID`) and the read's row for the key the
 * operator asked to edit. */
export interface SettingsEntryInputs {
  readonly project: string
  readonly row: SettingsEntryRow
}

/**
 * SettingsEntryView is the whole of what the window is shown **before** the dry
 * run: the label, the description, the raw key (drawn subtly -- the raw key is a
 * key, not a label, and the window draws both), the value a run reads now, the
 * control the key's type calls for, and R6's built-in default.
 *
 * `control` is null exactly when the window draws no control at all, and the
 * three facts that make it so are one fact: this console does not write the key.
 * `command` is then the read's own `owner` where it has one, and the operator
 * copies it.
 */
export interface SettingsEntryView {
  readonly kind: 'settings-entry'
  readonly project: string
  readonly key: string
  readonly label: string
  readonly description: string
  readonly current: string
  readonly defaultValue: string
  readonly defaultUnset: string
  readonly caveat: string
  readonly control: SettingsEntryControl | null
  readonly options: readonly string[]
  readonly min: string
  readonly max: string
  readonly format: string
  readonly type: string
  readonly command: string
}

/** controlFor is the type-to-control mapping (spec §3), and its default is the
 * **fallback** rather than a refusal: a key with no metadata, and a type this
 * console does not know, are both drawn as free text. An enum with no values is
 * text for the same reason -- a select with nothing to select can never be
 * answered. */
function controlFor(meta: SettingsEntryMeta): SettingsEntryControl {
  switch (meta.type) {
    case 'bool':
      return 'toggle'
    case 'enum':
      return meta.enum.length === 0 ? 'text' : 'select'
    case 'int':
    case 'float':
      return 'number'
    default:
      return 'text'
  }
}

/** settingsEntryView builds the form's content for one row. The label falls back
 * to the raw key (D2908) so that every row has a name, and the control falls
 * back to text so that every row can be typed into. */
export function settingsEntryView(inputs: SettingsEntryInputs): SettingsEntryView {
  const { row } = inputs
  const writable = row.writable && row.targetLayer !== ''
  return Object.freeze({
    kind: 'settings-entry' as const,
    project: inputs.project,
    key: row.key,
    label: row.meta.label !== '' ? row.meta.label : row.key,
    description: row.meta.description,
    current: row.value ?? '',
    defaultValue: row.default.value,
    defaultUnset: row.default.unset,
    caveat: SETTINGS_ENTRY_CAVEAT,
    control: writable ? controlFor(row.meta) : null,
    options: Object.freeze([...row.meta.enum]),
    min: row.meta.min,
    max: row.meta.max,
    format: row.meta.format,
    type: row.meta.type,
    command: writable ? '' : row.owner,
  })
}

/** numberRE and wholeRE are the spellings the two number controls take. They are
 * permissive on purpose: an exponent and a leading `+` are what a person may
 * type and what the CLI's own reader takes, while the dry run is what decides. */
const numberRE = /^[+-]?(\d+(\.\d+)?|\.\d+)([eE][+-]?\d+)?$/
const wholeRE = /^[+-]?\d+$/

/** repoRE, emailRE, urlRE, memoryRE and branchNameRE are the five formats whose
 * shape is a regular expression. Each is the CLI's own rule restated loosely:
 * `repourl.CloneURL`'s https-with-no-credentials URL, `config`'s pool e-mail,
 * `memoryRE`, and `config.ValidBranchName`'s conservative charset. */
const repoRE = /^[A-Za-z0-9][A-Za-z0-9._-]*\/[A-Za-z0-9][A-Za-z0-9._-]*$/
const emailRE = /^[^@\s]+@[^@\s]+\.[^@\s]+$/
const urlRE = /^https:\/\/[A-Za-z0-9][A-Za-z0-9.-]*(:[0-9]+)?(\/[^\s@]*)?$/
const memoryRE = /^[1-9][0-9]*(Mi|Gi)$/
const branchNameRE = /^[A-Za-z0-9._][A-Za-z0-9._/-]*$/
const badBranchRE = /\.\.|\/\/|(^|\/)\.|\.lock(\/|$)|[/.]$/

/** durationSeconds reads a duration as `config.ParseDuration` does -- an
 * unsigned leading day count and Go's own units after it, such as `14d`, `90m`
 * or `1d12h` -- and answers null for anything else. It is here so that a bound
 * the metadata spells in a duration (`5m`) is compared with a value the operator
 * typed in the same spelling. */
function durationSeconds(value: string): number | null {
  const unit = new Map<string, number>([
    ['ns', 1e-9],
    ['us', 1e-6],
    ['µs', 1e-6],
    ['ms', 1e-3],
    ['s', 1],
    ['m', 60],
    ['h', 3600],
  ])
  let rest = value
  let total = 0
  let seen = false
  const days = /^([0-9]+)d/.exec(rest)
  if (days !== null && days[1] !== undefined && days[0] !== undefined) {
    total += Number(days[1]) * 86_400
    rest = rest.slice(days[0].length)
    seen = true
  }
  while (rest !== '') {
    const part = /^([0-9]+(?:\.[0-9]+)?)(ns|us|µs|ms|s|m|h)/.exec(rest)
    const count = part?.[1]
    const factor = part?.[2] === undefined ? undefined : unit.get(part[2])
    if (count === undefined || factor === undefined) return null
    total += Number(count) * factor
    rest = rest.slice(part?.[0]?.length ?? 0)
    seen = true
  }
  return seen ? total : null
}

/** numberError is the number controls' rule: the spelling, then the two bounds
 * the metadata gives, in the value's own unit. */
function numberError(view: SettingsEntryView, value: string): string | null {
  const whole = view.type === 'int'
  if (value === '' || !(whole ? wholeRE : numberRE).test(value)) {
    return whole ? SETTINGS_ENTRY_WHOLE : SETTINGS_ENTRY_NUMBER
  }
  const n = Number(value)
  if (view.min !== '' && n < Number(view.min)) return `${SETTINGS_ENTRY_BELOW_MIN}${view.min})`
  if (view.max !== '' && n > Number(view.max)) return `${SETTINGS_ENTRY_ABOVE_MAX}${view.max})`
  return null
}

/** textError is the text controls' rule: a duration (by type or by format) is
 * read and bounded as a duration, a format with a shape is matched, and free
 * text is anything but nothing. */
function textError(view: SettingsEntryView, value: string): string | null {
  if (value === '') return SETTINGS_ENTRY_REQUIRED
  if (view.type === 'duration' || view.format === 'duration') {
    const seconds = durationSeconds(value)
    if (seconds === null) return SETTINGS_ENTRY_DURATION
    const low = view.min === '' ? null : durationSeconds(view.min)
    const high = view.max === '' ? null : durationSeconds(view.max)
    if (low !== null && seconds < low) return `${SETTINGS_ENTRY_BELOW_MIN}${view.min})`
    if (high !== null && seconds > high) return `${SETTINGS_ENTRY_ABOVE_MAX}${view.max})`
    return null
  }
  switch (view.format) {
    case 'repo':
      return repoRE.test(value) ? null : SETTINGS_ENTRY_REPO
    case 'email':
      return emailRE.test(value) ? null : SETTINGS_ENTRY_EMAIL
    case 'url':
      return urlRE.test(value) ? null : SETTINGS_ENTRY_URL
    case 'memory':
      return memoryRE.test(value) ? null : SETTINGS_ENTRY_MEMORY
    case 'branch':
      return branchNameRE.test(value) && !badBranchRE.test(value) ? null : SETTINGS_ENTRY_BRANCH
    default:
      return null
  }
}

/**
 * settingsEntryError is why OK is disabled for what has been typed, or null when
 * it is enabled. It is a pure function of the view and one value, so the window
 * that draws the button and the test that pins "OK is disabled until the client
 * validation passes" (SU1's red step) read the same rule without a window.
 *
 * **A reset is always valid.** `null` is the operator's "Reset to default",
 * which writes no value at all (`settings unset`, A4) and which no control can
 * bound: the value that applies afterwards is the built-in default the window
 * draws, not something typed here. **A read-only view refuses everything else**,
 * which is the main process's second gate on a key it drew no control for; the
 * window draws no OK for one, and a document that sent a value anyway is refused
 * here rather than reaching an argv.
 */
export function settingsEntryError(view: SettingsEntryView, value: string | null): string | null {
  if (value === null) return null
  if (view.control === null) return SETTINGS_ENTRY_READONLY
  switch (view.control) {
    case 'toggle':
      return value === 'true' || value === 'false' ? null : SETTINGS_ENTRY_TOGGLE
    case 'select':
      return view.options.includes(value) ? null : SETTINGS_ENTRY_SELECT
    case 'number':
      return numberError(view, value)
    case 'text':
      return textError(view, value)
  }
}
