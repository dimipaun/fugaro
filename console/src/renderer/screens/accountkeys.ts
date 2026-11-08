// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The review account keys, as a section (the managed-screens plan's CM6; F16,
// F17, D1478, D1479).
//
// The ask was "the review account keys (`default_claude_account`,
// `claude_account_stages`: names only, never a token)", and the reconciliation
// answered where they come from: **`settings --json`** (D1478), the read the
// Project tab already makes. So this section adds **no operation and no argv**
// -- it is a second reader of a document already on the wire -- and it names the
// operation it draws (`ACCOUNT_KEYS_OPERATION`) rather than one of its own, which
// is what keeps it from being a place the console's read fence has to move for.
//
// **The two keys.** The read prints one row per key the settings registry has,
// under the registry's own spellings, so the project's default is the row
// `default_claude_account` and the per-stage accounts are the row
// `stages.*.claude_account` (F16: the registry's spelling, which is a wildcard
// and not a stage). This module matches that shape -- `stages.<stage>.
// claude_account` -- and draws the stage segment the key carries, so a document
// that carried one row per configured stage would draw one row per stage, and
// the CLI's own wildcard row draws its `*` verbatim. The console invents no
// stage name: the segment in the key is the only thing the read says about which
// stage an account belongs to.
//
// **An account is drawn only against a key that names its stage.** The registry
// has one wildcard key, so the read prints one row for every stage account a
// project decides -- and resolves its `value` from `stages.*.claude_account` by
// taking **the first stage in sorted order** (`valueAtPath`, `stepInto`,
// `internal/cli/settings_read.go`). The row therefore carries an account that
// belongs to a stage the row does not name: a project deciding
// `stages.review.claude_account` alone arrives as `*` with that account, which
// drawn would say every stage uses it, and a project deciding `review` and `fix`
// arrives as the one of the two that sorts first (`fix`). So the value cell of
// the wildcard row -- the one key whose subject is a set of stages rather than a
// single key -- is the console's own word for what the read actually says (an
// account is set for some stage, which this row does not identify), and never
// that account. Nothing is lost by it: the row is still drawn, under the read's
// own key and with its own `*`, and a document that ever names its stages draws
// each account against the stage it belongs to.
//
// **`sensitive` is dropped whole** (D1479). "Names only, never a token" is held
// by the read's rule rather than by a promise: an account is a name by
// construction, and a row the read marks `sensitive` carries no `value` member
// at all -- so this module drops such a row before it reads a key, a stage or a
// value from it, and the row leaves no trace on the screen rather than being
// drawn with a blank. `buildAccountKeys` is where that happens, so no drawing
// code can be handed one.
//
// **A key with no account behind it is `unset`, not empty.** An empty cell would
// read as "the value is the empty string", a different fact from "no account is
// decided here". The read writes the second fact in its own words: a key no
// layer decides arrives with the registry's sentence for it
// (`unset = <what it means>`) as its value, which is no more an account than an
// empty cell is -- an account is a name, and that sentence is the registry
// talking about the key (`UNSET_PREFIX`). A key the read carries no row for --
// or the subject whose only row was `sensitive`, which this module dropped -- is
// drawn the same way, from a row the section supplies itself. The section always
// draws both subjects: the project's default row, and the stage rows it found or
// the registry's own wildcard key when it found none.
//
// **The reason a read gave is drawn the way every screen draws it.** The
// section's two reasons are its own sentences and the CLI's refusal sentence on
// the `SourceRefusal` channel, and both go through `sanitizeRefusal`
// (`drawnRefusal`) -- the door the design's section 6.3 keeps for it, which
// leaves a path, a project and a role as the CLI printed them, because for a
// permission refusal the sentence *is* the remedy. `sentence` would draw
// `roles/datastore.viewer` as `[redacted]` and the project's own config path as
// `<path>`; the tab's settings panel would draw the very same `settings.read`
// refusal unedited beside this one.

import { sanitizeRefusal } from '../../shared/sanitize'
import { putText } from '../app'
import type { ElementLike } from '../app'
import { REFUSAL_UNREADABLE } from './degraded'

/** ScreenElement and ScreenFactory are the drawing surface, declared here as
 * every screen declares its own: the entry's `DomScreenFactory` is assignable
 * to it, and so is the test document's. */
export interface ScreenElement extends ElementLike {
  readonly tag: string
  append(child: ScreenElement): void
  childNodes(): readonly ScreenElement[]
}

export interface ScreenFactory {
  create(tag: string): ScreenElement
}

/** ACCOUNT_KEYS_CLASS is the section itself: a sibling of the tab's other
 * panels, which is what gives this read its own busy mark and its own refusal
 * (D1488). The classes carry the `projects-` prefix because the tab they land in
 * is the Project tab and the theme's own selector licence is keyed by the prefix
 * of the screen a hook belongs to. */
export const ACCOUNT_KEYS_CLASS = 'projects-account-keys'

export const ACCOUNT_KEYS_TITLE_CLASS = 'projects-account-keys-title'
export const ACCOUNT_KEYS_STATE_CLASS = 'projects-account-keys-state'
export const ACCOUNT_KEYS_SOURCE_CLASS = 'projects-account-keys-source'
export const ACCOUNT_KEYS_BECAUSE_CLASS = 'projects-account-keys-because'
export const ACCOUNT_KEYS_BODY_CLASS = 'projects-account-keys-body'
export const ACCOUNT_KEYS_LIST_CLASS = 'projects-account-keys-list'
export const ACCOUNT_KEYS_ROW_CLASS = 'projects-account-keys-row'
export const ACCOUNT_KEYS_KEY_CLASS = 'projects-account-keys-key'
export const ACCOUNT_KEYS_STAGE_CLASS = 'projects-account-keys-stage'
export const ACCOUNT_KEYS_VALUE_CLASS = 'projects-account-keys-value'

/** ACCOUNT_KEYS_OPERATION is the read this section draws: **the one the Project
 * tab already makes**, so this section adds no operation to the console's
 * surface (D1478). It is written here as the operation's own name and not as a
 * second argv, because the argv is `src/main/spawn.ts`'s. */
export const ACCOUNT_KEYS_OPERATION = 'settings.read'

/** ACCOUNT_KEYS_SOURCE is the command line the panel names, the way every panel
 * of this console names its own source (design section 6.2). */
export const ACCOUNT_KEYS_SOURCE = 'fugaro settings --json'

/** ACCOUNT_KEYS_TITLE is the panel's own name: the console's word for what the
 * section shows, and never a document's string. */
export const ACCOUNT_KEYS_TITLE = 'Review account keys'

/** ACCOUNT_KEYS_BUSY is the section's mark while its read is out. The read is
 * the tab's own `settings.read`, so the mark names the keys rather than the
 * whole settings table: two panels of one tab wear one read's answer, and a mark
 * that said "settings" would name the other panel's subject. */
export const ACCOUNT_KEYS_BUSY = 'Reading the review account keys…'

/** ACCOUNT_KEYS_UNSET_WORD is what a key the read carries no value for is drawn
 * as. It is the console's own word -- no document prints it -- and it is a word
 * rather than an empty cell because the two facts differ: "the read carries no
 * value here" and "the value is the empty string". */
export const ACCOUNT_KEYS_UNSET_WORD = 'unset'

/** ACCOUNT_KEYS_STAGE_WORD is what the value cell of the wildcard row says. The
 * registry has one wildcard key for every stage account, and the read
 * resolves its `value` to the first stage in sorted order without saying which
 * one it took (`valueAtPath`), so the account in that row is not this row's to
 * draw -- drawn, `* team-c` would say every stage uses `team-c` when every stage
 * but `review` uses the default. The word states what the read does say -- an
 * account is set for some stage, which this row does not name -- and is the
 * console's own, like `unset` beside it. */
export const ACCOUNT_KEYS_STAGE_WORD = 'set for a stage'

/** DEFAULT_ACCOUNT_KEY and STAGE_ACCOUNT_KEY are the two subjects, in the
 * registry's own spellings (`internal/settings/registry.go`, F16). The stage key
 * is a **pattern** and the string below is the spelling the CLI prints for it:
 * the wildcard is the registry's, not this module's invention, and it is the one
 * key shape whose value this section does not draw (`ACCOUNT_KEYS_STAGE_WORD`). */
export const DEFAULT_ACCOUNT_KEY = 'default_claude_account'
export const STAGE_ACCOUNT_KEY = 'stages.*.claude_account'

/** AccountKeyValue is what a row's value cell says: an account the read printed
 * against a key that names the stage it belongs to, the console's own `unset`
 * for a key no layer decides, or the console's own word for a stage account the
 * read resolved to a stage the row does not name. */
export type AccountKeyValue =
  | { readonly kind: 'account'; readonly name: string }
  | { readonly kind: 'unset' }
  | { readonly kind: 'stage' }

/** AccountKeyRow is one drawn row: the read's own key, the stage segment the key
 * carries (`null` for the project's default), and what its value cell says. */
export interface AccountKeyRow {
  readonly key: string
  readonly stage: string | null
  readonly value: AccountKeyValue
}

/** AccountKeys is the section's model: the rows, and whether the read answered.
 * It is what `buildAccountKeys` answers and what the drawing takes, so the
 * section's rules -- which rows exist, which are dropped, which are unset -- are
 * testable with no element in sight.
 *
 * `because` is the reason **as it will be drawn**: `buildAccountKeys` puts it
 * through `sanitizeRefusal` (`drawnRefusal`) before it is stored, so the drawing
 * writes a sentence this console produced and never a document's string. */
export interface AccountKeys {
  readonly state: 'read' | 'unavailable'
  readonly because: string | null
  readonly rows: readonly AccountKeyRow[]
}

/** AccountKeysInputs is what the section is built from: the document **as it
 * arrived** and the CLI's own sentence for a read that was refused. The document
 * is read here rather than decoded elsewhere, because this section reads the
 * tab's document itself -- it checks the version and the members it draws, and
 * answers a refusal rather than a table it cannot vouch for. */
export interface AccountKeysInputs {
  readonly raw: unknown
  readonly because: string | null
}

/** SETTINGS_DOC_VERSION is the version of `settings --json` this module reads
 * (contract section 6.19). */
const SETTINGS_DOC_VERSION = 1

/** SETTINGS_VERSION_WORD is the console's own sentence for a document it does
 * not read, with the version the CLI printed interpolated. */
const SETTINGS_VERSION_WORD = 'settings --json is version '

/** STAGE_KEY_PREFIX and STAGE_KEY_SUFFIX are the shape of a stage account row:
 * the registry's `stages.*.claude_account`, whose `*` the read resolves to a
 * concrete stage when the layer has one. */
const STAGE_KEY_PREFIX = 'stages.'
const STAGE_KEY_SUFFIX = '.claude_account'

const SETTINGS_SHAPE_BECAUSE =
  'settings --json is not a document this console reads: its settings member is not a list of rows'

const ROW_SHAPE_BECAUSE =
  'settings --json carried a row this console does not read: the value of a key this section draws is not the type the contract gives it, so no row of it is drawn'

/**
 * drawnRefusal is a reason as a screen may draw it: `sanitizeRefusal` and
 * nothing else, which is the one treatment of the CLI's own refusal sentence
 * (design section 6.3, `src/shared/sanitize.ts`). `sentence` and the other
 * free-text classes run the pattern rules, and two of them eat the remedy -- a
 * role is two long segments around a slash and is drawn `[redacted]`, a path is
 * drawn `<path>` -- while the credential rules still redact a token a child
 * echoed into its stderr.
 *
 * Running it here, in the model, is also what keeps the panel's `literal` call
 * honest: by the time the drawing reads `model.because`, it is a sentence the
 * sanitizer produced or this module's own constant. A reason too long for the
 * console to read at all is `REFUSAL_UNREADABLE` (`./degraded`, the console's
 * one word for it) rather than a cell the operator reads as the refusal.
 */
function drawnRefusal(because: string): string {
  const drawn = sanitizeRefusal(because, 'the reason this section has nothing to draw')
  return drawn.ok ? drawn.text : REFUSAL_UNREADABLE
}

/** objectAt is a value that is a JSON object, or null. */
function objectAt(raw: unknown): Record<string, unknown> | null {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return null
  return raw as Record<string, unknown>
}

/** arrayAt is a value that is a JSON array, or null. */
function arrayAt(raw: unknown): readonly unknown[] | null {
  return Array.isArray(raw) ? raw : null
}

/** textAt is a value that is a JSON string, or null. */
function textAt(raw: unknown): string | null {
  return typeof raw === 'string' ? raw : null
}

/** stageOf is the stage a key belongs to: the segment between `stages.` and
 * `.claude_account`, or null for a key of any other shape. It is the only thing
 * that decides whether a row is one of this section's subjects, so the read's
 * neighbours in the registry -- `claude_accounts.*.allow_data_to`,
 * `job_profiles.*.claude_accounts`, `claude_account_auto` -- are matched by
 * neither the prefix nor the suffix and are ignored. */
function stageOf(key: string): string | null {
  if (!key.startsWith(STAGE_KEY_PREFIX) || !key.endsWith(STAGE_KEY_SUFFIX)) return null
  const stage = key.slice(STAGE_KEY_PREFIX.length, key.length - STAGE_KEY_SUFFIX.length)
  return stage === '' ? null : stage
}

/** ValueRead is the value of one drawn row read strictly: the value the read
 * printed (or null when it printed none), or a member of a type this console
 * does not read, which withholds the section. */
type ValueRead = { readonly kind: 'value'; readonly value: string | null } | { readonly kind: 'shape' }

/** UNSET_PREFIX is how the read spells a value no layer decides: `unset = ` and
 * the registry's own sentence for what unset means (`unset(what)`,
 * `internal/settings/registry.go`). **An account is a name**, so a value the
 * read spells that way is not a value this section draws: it is the same fact as
 * a row the read carries no value for -- `stages.*.claude_account` unset means
 * the project's default account decides, which is the registry's sentence about
 * the key and not an account any stage uses -- and it is drawn as the console's
 * own word. An empty value is the same fact again: an account is never the empty
 * string, and an empty cell is what this section may not draw. */
const UNSET_PREFIX = 'unset = '

/** valueAt reads a row's `value` member: absent is a row the read carries no
 * value for -- which is what a `sensitive` row is -- the read's own unset
 * spelling is that same fact written out (a key no layer decides arrives that
 * way), and a member of any other type is a row this console does not read
 * rather than a value it coerces. */
function valueAt(row: Record<string, unknown>): ValueRead {
  const raw = row['value']
  if (raw === undefined) return { kind: 'value', value: null }
  const value = textAt(raw)
  if (value === null) return { kind: 'shape' }
  if (value === '' || value.startsWith(UNSET_PREFIX)) return { kind: 'value', value: null }
  return { kind: 'value', value }
}

/** unsetRow is the row drawn for a key the read carries nothing for: the key
 * under its own spelling, its stage when it has one, and no value. */
function unsetRow(key: string): AccountKeyRow {
  return { key, stage: stageOf(key), value: { kind: 'unset' } }
}

/** WILDCARD_STAGE is the `*` of the registry's own `stages.*.claude_account`
 * (F16): the segment the read prints for the pattern rather than a stage any
 * project can configure (`CheckStageSegment`, `internal/config/stage_accounts.go`,
 * refuses a segment outside `RunStages`, and no stage is named `*`). */
const WILDCARD_STAGE = '*'

/** drawnValue is what a row's value cell says, from the stage the row's key
 * carries and the value the read printed. The rules live here, in the model, so
 * the drawing is handed a decision rather than making one:
 *
 *   - no value is `unset`, the console's word for "the read carries none";
 *   - a value against the wildcard row is a stage account the read resolved to a
 *     stage the row does not name, so the cell says that rather than the account
 *     (`ACCOUNT_KEYS_STAGE_WORD`);
 *   - anything else is an account, drawn as the name it is: the project's
 *     default (whose key names its one subject exactly) and a stage row whose
 *     key carries the stage the account belongs to. */
function drawnValue(stage: string | null, value: string | null): AccountKeyValue {
  if (value === null) return { kind: 'unset' }
  if (stage === WILDCARD_STAGE) return { kind: 'stage' }
  return { kind: 'account', name: value }
}

/**
 * buildAccountKeys turns the tab's settings read into this section's model.
 *
 * **Three rules are decided here and nowhere else** (the plan's CM6):
 *
 *   - **a row the read marks `sensitive` is dropped whole** -- no key, no stage,
 *     no value (D1479). The test is the same one the settings table uses: the
 *     member is neither absent nor exactly `false`, whatever type the flag is
 *     spelled in, so a `sensitive: 'yes'` a later CLI printed would be dropped
 *     rather than drawn. It is decided before anything is read out of the row,
 *     so no drawing code can be handed one;
 *   - **a key the read does not carry is drawn as unset** rather than as an
 *     empty cell. Both subjects are always drawn: the project's default, and the
 *     stage rows the read carries or the registry's own wildcard key when it
 *     carries none -- because the registry has the key either way, and a section
 *     that drew nothing would be saying the key does not exist;
 *   - **an account is drawn only against a key that names its stage**
 *     (`drawnValue`). The wildcard row's `value` is the first stage account in
 *     sorted order, which the read does not identify, so its cell states that
 *     rather than drawing an account that would speak for every stage. It is
 *     decided here and not in the drawing, so the rule is testable in plain Node
 *     and the drawing cannot choose a different one;
 *   - **a member of the wrong type withholds the section** rather than being
 *     coerced: a value that is not a string is not the value the contract gives
 *     a row, and a table drawn from coerced members is a table of claims the CLI
 *     never made. The refusal is the console's own sentence, drawn like the
 *     CLI's own refusal for a read that did not answer.
 *
 * It touches no element, so every one of those is testable in plain Node.
 */
export function buildAccountKeys(inputs: AccountKeysInputs): AccountKeys {
  const document = objectAt(inputs.raw)
  if (document === null || document.version !== SETTINGS_DOC_VERSION) {
    const because =
      document === null
        ? (inputs.because ?? `no document arrived for \`${ACCOUNT_KEYS_SOURCE}\``)
        : `${SETTINGS_VERSION_WORD}${String(document.version)} and this console reads version ${String(SETTINGS_DOC_VERSION)}`
    return { state: 'unavailable', because: drawnRefusal(because), rows: [] }
  }
  const settings = arrayAt(document.settings)
  if (settings === null) {
    return { state: 'unavailable', because: drawnRefusal(SETTINGS_SHAPE_BECAUSE), rows: [] }
  }
  let defaultRow: AccountKeyRow | null = null
  const stageRows: AccountKeyRow[] = []
  for (const entry of settings) {
    const row = objectAt(entry)
    if (row === null) continue
    const sensitive = row['sensitive']
    if (sensitive !== undefined && sensitive !== false) continue
    const key = textAt(row['key'])
    if (key === null || key === '') continue
    const stage = stageOf(key)
    if (stage === null && key !== DEFAULT_ACCOUNT_KEY) continue
    const read = valueAt(row)
    if (read.kind === 'shape') {
      return { state: 'unavailable', because: drawnRefusal(ROW_SHAPE_BECAUSE), rows: [] }
    }
    const value = drawnValue(stage, read.value)
    if (stage === null) defaultRow = { key, stage: null, value }
    else stageRows.push({ key, stage, value })
  }
  return {
    state: 'read',
    because: null,
    rows: [
      defaultRow ?? unsetRow(DEFAULT_ACCOUNT_KEY),
      ...(stageRows.length > 0 ? stageRows : [unsetRow(STAGE_ACCOUNT_KEY)]),
    ],
  }
}

/** literal writes this module's own vocabulary -- the panel's name, the state
 * word, the two value words and the reason, which `buildAccountKeys` sanitized
 * before it stored it. A document's string never reaches it: those come through
 * `putText` (`../app.ts`) or not at all. */
function literal(element: ScreenElement, text: string): ScreenElement {
  element.textContent = text
  return element
}

/** accountKeyRow is one row: the key the read printed, the stage it belongs to
 * when the key carries one, and the value or the console's own unset word. It
 * draws no control of any kind: these are the keys `settings set` writes, and
 * these screens are read-only (D1471). */
function accountKeyRow(factory: ScreenFactory, row: AccountKeyRow): ScreenElement {
  const element = factory.create('li')
  element.setAttribute('class', ACCOUNT_KEYS_ROW_CLASS)
  const key = factory.create('span')
  key.setAttribute('class', ACCOUNT_KEYS_KEY_CLASS)
  // `label` and not `branch`: the wildcard row's key carries a `*`, which the
  // branch class refuses, and a key drawn as the console's unavailable mark
  // would hide the very row this section exists to show.
  putText(key, row.key, 'label', 'settings[].key')
  element.append(key)
  if (row.stage !== null) {
    const stage = factory.create('span')
    stage.setAttribute('class', ACCOUNT_KEYS_STAGE_CLASS)
    putText(stage, row.stage, 'label', 'settings[].key')
    element.append(stage)
  }
  const value = factory.create('span')
  value.setAttribute('class', ACCOUNT_KEYS_VALUE_CLASS)
  // An account is a name, so it is drawn with the class the tab's accounts
  // panel draws its own names with, and only when the key it came from names the
  // stage it belongs to (`drawnValue`). The other two cells are this module's own
  // words rather than a document's string: `unset` for a key no layer decides,
  // and the stage word for the wildcard row, whose account belongs to a stage the
  // row does not name.
  if (row.value.kind === 'account') putText(value, row.value.name, 'person', 'settings[].value')
  else if (row.value.kind === 'unset') literal(value, ACCOUNT_KEYS_UNSET_WORD)
  else literal(value, ACCOUNT_KEYS_STAGE_WORD)
  element.append(value)
  return element
}

/** accountKeysBody is the rows, in the document's own order: the default first
 * because `buildAccountKeys` puts it there, the stages after it. */
function accountKeysBody(factory: ScreenFactory, model: AccountKeys): ScreenElement {
  const body = factory.create('div')
  body.setAttribute('class', ACCOUNT_KEYS_BODY_CLASS)
  const list = factory.create('ul')
  list.setAttribute('class', ACCOUNT_KEYS_LIST_CLASS)
  for (const row of model.rows) list.append(accountKeyRow(factory, row))
  body.append(list)
  return body
}

/** accountKeysPanel is the section: the panel's own name, its state as a word
 * and not as a tone alone (design section 3.5), the command line it names as its
 * source, the read's refusal when it did not answer, and the rows when it did. */
function accountKeysPanel(factory: ScreenFactory, model: AccountKeys): ScreenElement {
  const element = factory.create('section')
  element.setAttribute('class', ACCOUNT_KEYS_CLASS)
  element.setAttribute('data-operation', ACCOUNT_KEYS_OPERATION)
  if (model.state === 'read') element.setAttribute('data-state', 'read')
  else element.setAttribute('data-state', 'unavailable')
  const title = factory.create('h2')
  title.setAttribute('class', ACCOUNT_KEYS_TITLE_CLASS)
  literal(title, ACCOUNT_KEYS_TITLE)
  element.append(title)
  const state = factory.create('span')
  state.setAttribute('class', ACCOUNT_KEYS_STATE_CLASS)
  if (model.state === 'read') literal(state, 'read')
  else literal(state, 'not read')
  element.append(state)
  const source = factory.create('span')
  source.setAttribute('class', ACCOUNT_KEYS_SOURCE_CLASS)
  literal(source, ACCOUNT_KEYS_SOURCE)
  element.append(source)
  if (model.because !== null) {
    // The reason is drawn, not paraphrased: `buildAccountKeys` put it through
    // `sanitizeRefusal` (`drawnRefusal`), so this is a sentence this console
    // produced -- the CLI's refusal verbatim, which is what the design requires
    // and what the tab's other panels show for the same read.
    const because = factory.create('p')
    because.setAttribute('class', ACCOUNT_KEYS_BECAUSE_CLASS)
    literal(because, model.because)
    element.append(because)
  }
  if (model.state === 'read') element.append(accountKeysBody(factory, model))
  return element
}

/**
 * fillAccountKeys draws the section into the host the tab gave it, and it is one
 * read's filler: `settings.read` lands apart from the tab's other reads, so this
 * panel is drawn the moment its document arrives and nothing waits for another
 * (D1488).
 *
 * It appends to the host and not to a slot inside itself: the mark of a read
 * that is still out is the entry's, in a slot of its own beside this section
 * (`renderBusy`, `../section.ts`), which is what keeps one read's mark from
 * taking another read's claim down.
 */
export function fillAccountKeys(factory: ScreenFactory, model: AccountKeys, host: ScreenElement): void {
  host.append(accountKeysPanel(factory, model))
}

/** renderAccountKeys builds the model from the read's own document and draws it:
 * the one call for an entry that has the document in hand and no model to keep. */
export function renderAccountKeys(
  factory: ScreenFactory,
  inputs: AccountKeysInputs,
  host: ScreenElement,
): void {
  fillAccountKeys(factory, buildAccountKeys(inputs), host)
}
