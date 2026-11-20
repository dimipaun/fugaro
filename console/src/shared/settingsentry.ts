// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
//
// The settings entry form's own shape and rules (SU1, D2902, R5, R6).
//
// **Why this module sits in `src/shared/` and not beside the view builder.** The
// window is built in the main process but **drawn** in the renderer, and R5's
// pre-check travels with the drawing: the sandboxed renderer imports nothing from
// `src/main/`, so a rule the two halves must agree on lives here, as
// `src/shared/new-ops.ts` does for the operations' payloads. Nothing in this
// module reads a window, a file or a clock -- the view is the shape main builds
// and the renderer draws, and the sentence functions are the working-over of it.
//
// **One modal does entry, the dry run and the confirmation** (D2902, overruling
// the pencil-then-dry-run shape of C2). This half is what that window is shown
// **before** any dry run exists: the metadata SM-E emits, the value a run reads
// now, and the built-in default R6 names.
//
// **What the client validation is, and what it is not.** R5: "the dialog
// validates on the client from the registry metadata (type, enum, min, max,
// format) and the dry run is the authority at OK". So `settingsEntryError` is a
// *pre-check for the OK button* -- OK is disabled while it answers a sentence --
// and never the decision: a value it passes still reaches the CLI, which refuses
// what its own reader refuses. That is why each grammar below is the permissive
// one (a duration as `config.ParseDuration` reads it, an https URL as
// `repourl.CloneURL` takes one) rather than a second copy of the CLI's rules, and
// why these sentences are the console's own plain language rather than the CLI's:
// the CLI's own refusal is what the operator sees when one gets through.

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
export function controlFor(meta: SettingsEntryMeta): SettingsEntryControl {
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
