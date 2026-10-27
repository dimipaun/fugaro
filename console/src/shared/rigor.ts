// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// The rigor block a run's record carries: the mode the run resolved, the layer
// that decided it and the area rules that matched (docs/rigor-modes D22, D24,
// D25; `runstore.RigorRecord`).
//
// **Three screens draw a run's mode and this is the one reader they share**: the
// Projects screen's jobs list, the Live screen's run rows and the Run detail
// header. They read it here rather than three times because the two things that
// are easy to get differently -- what a block is allowed to say, and which word
// a mode is drawn with -- would then be three answers to one question.
//
// **The block is job-writable, so nothing in it is trusted.** The CLI bounds it
// before it reaches the console (`runstore.RigorRecord.Bounded`, through
// `runview.Join`), and this reader bounds it again, because a console that
// trusted a bound it did not apply would be reading a document rather than a
// decision: a block that is not an object, or that carries no mode or no layer,
// is no block at all (`null`, which is what a record written before the field
// existed answers too, and which every screen draws as "no record of this run's
// rigor" rather than as "it resolved nothing"), and the areas are capped here as
// well as there. The words themselves are *drawn*, so they take the text path:
// `rigorModeWord` and `rigorLayerWord` answer this build's own vocabulary for
// the five modes and the five layers it knows, and `null` for anything else --
// and a screen that gets `null` puts the raw value through `putText` with the
// `label` class rather than writing it into the document, so a mode string this
// build has never heard of is drawn as the bounded, escaped text it is.

/**
 * RIGOR_MAX_AREAS is how many matched rules this reader keeps, and it is
 * `rigor.MaxAreas`'s number (`internal/rigor`): the CLI drops the rest when it
 * bounds a block, and a console that drew an unbounded list of them would be
 * drawing a list no reader of the record could have checked.
 */
export const RIGOR_MAX_AREAS = 100

/**
 * RIGOR_MODE_WORDS is the word each mode is drawn with -- the owner's own five
 * names for the dial (`docs/rigor-modes/spec.md`), title-cased because the
 * record's own value is the lowercase enum. **A mode is not drawn as its wire
 * value**: `poc` is the CLI's spelling of a setting, and `POC` is the word a
 * person reads on the screen, which is the same distinction `runs ls`'s MODE
 * column makes in its own cell.
 */
export const RIGOR_MODE_WORDS: Readonly<Record<string, string>> = Object.freeze({
  poc: 'POC',
  startup: 'Startup',
  pro: 'Pro',
  critical: 'Critical',
  hotfix: 'Hotfix',
})

/**
 * RIGOR_LAYER_WORDS is what set the mode, in the console's words
 * (`internal/rigor`'s `Layer`, docs/rigor-modes D24): Fugaro's own default, the
 * project layer's `defaults.rigor.default`, the repository's own `rigor.default`,
 * a `rigor.areas` rule the run's paths matched, or the run's own request, which
 * may raise the mode and never lower it.
 *
 * `reached` is deliberately **not** here. It is the resolver's answer to a later
 * moment asking again (D22), it is never what a block this runner writes says,
 * and `Bounded` refuses a block that names it -- so a record carrying it is a
 * block the console does not understand, and it is drawn as the raw text it is
 * rather than given a word this build would be inventing for it.
 */
export const RIGOR_LAYER_WORDS: Readonly<Record<string, string>> = Object.freeze({
  default: 'default',
  project: 'the project layer',
  repo: 'the repository',
  area: 'an area rule',
  request: 'the request',
})

/** RigorAreaView is one matched area rule: the name the repository gave it and
 * the mode that rule sets (D23, strictest first in the record's own order). */
export interface RigorAreaView {
  readonly name: string
  readonly mode: string
}

/** RigorView is a run's rigor block as the screens draw it. It is the block's
 * `mode`, `from` and `areas` and nothing else: `at` (the moment) and `revision`
 * (where the configuration was read from) are the record's own stamps, no screen
 * on this tree draws them, and a reader that carried them would be carrying
 * fields nobody checks. */
export interface RigorView {
  readonly mode: string
  readonly from: string
  readonly areas: readonly RigorAreaView[]
}

/** wordFor is a vocabulary lookup that cannot be reached through the prototype:
 * the value is a job-written string, and `constructor` is a key every object
 * literal has. */
function wordFor(words: Readonly<Record<string, string>>, raw: string): string | null {
  if (!Object.hasOwn(words, raw)) return null
  return words[raw] ?? null
}

/**
 * rigorModeWord is the word to draw a mode with, or `null` for a mode this
 * build does not know. The two answers are the whole of what a screen needs and
 * neither of them is a fallback it may skip: a word is this build's own
 * vocabulary and is written with the screen's `literal` writer, and `null` means
 * the *value* is the text, so the screen draws it through `putText` with the
 * `label` class and the field `rigor.mode` -- bounded, escaped, and refused as
 * `n/a` when the sanitizer will not have it, exactly as every other string a job
 * wrote.
 */
export function rigorModeWord(mode: string): string | null {
  return wordFor(RIGOR_MODE_WORDS, mode)
}

/** rigorLayerWord is the same for the layer that decided the mode, with
 * `rigor.from` as the field of the raw fallback. */
export function rigorLayerWord(from: string): string | null {
  return wordFor(RIGOR_LAYER_WORDS, from)
}

/**
 * readRigor reads one record's `rigor` block, or answers `null`.
 *
 * The three ways to get `null` are one answer on purpose, because the screens
 * draw them the same way: the record carries no block (a run from before the
 * field, or one that died before bootstrap), the value is not a block at all, or
 * the block does not carry the two keys a decision is made of -- a mode and the
 * layer that set it, neither of which is `omitempty` in the record. An absent
 * mode is not a mode, and a console that drew one would be answering "what was
 * this run's rigor" with a blank.
 *
 * A block that carries both is kept even when its *values* are outside this
 * build's vocabulary: the mode and the layer are drawn as the raw, bounded text
 * they are (see the header), which is the direction this has to fail in -- the
 * alternative is a screen that hides a mode a newer CLI resolved.
 */
export function readRigor(raw: unknown): RigorView | null {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return null
  const block = raw as Record<string, unknown>
  const mode = block['mode']
  const from = block['from']
  if (typeof mode !== 'string' || mode === '') return null
  if (typeof from !== 'string' || from === '') return null
  const areas: RigorAreaView[] = []
  const listed = block['areas']
  if (Array.isArray(listed)) {
    for (const entry of listed) {
      if (areas.length >= RIGOR_MAX_AREAS) break
      if (entry === null || typeof entry !== 'object' || Array.isArray(entry)) continue
      const area = entry as Record<string, unknown>
      const name = area['name']
      const areaMode = area['mode']
      // A rule with no name is not a rule a reader can name, and one with no
      // mode is not one that set anything: both are dropped rather than drawn
      // with a placeholder, which is what `runstore.RigorRecord.Bounded` does
      // with them too.
      if (typeof name !== 'string' || name === '') continue
      if (typeof areaMode !== 'string' || areaMode === '') continue
      areas.push({ name, mode: areaMode })
    }
  }
  return { mode, from, areas }
}

/**
 * RIGOR_ABSENT is the compact cell for a run whose record carries no block this
 * reader can show -- the dash `runs ls`'s MODE column draws, and the same fact
 * `runview.Row.Rigor`'s nil is: **"no record of this run's rigor"**, never "it
 * resolved nothing". A run that resolved Fugaro's own default carries a block
 * saying `pro`, so the dash and the default are two different cells.
 */
export const RIGOR_ABSENT = '-'

/**
 * rigorIndex reads a `runs ls --json` document into the block of each run it
 * names, keyed by run id.
 *
 * **It is the Live screen's join, and the reason one is needed.** `watch --json`
 * carries no rigor block -- the live shape is `JSONRun`, the block lives on the
 * record (`runview.Row`, which `runs ls --json` and `diagnose --json` both
 * answer with) -- so a Live row's mode is read from the run list and matched to
 * the row by id. This is the one place a `runs ls` row becomes a key: the id is
 * the row's `run`, or its `run_id` where the CLI wrote the older spelling (the
 * same pair `projects.ts` reads, in the same order).
 *
 * A row that names no run, or whose block this reader will not show, is simply
 * **absent from the index** -- a run the index does not know is drawn with no
 * chip at all, never with a mode taken from another run, which is the direction
 * every reader in this module fails in.
 */
export function rigorIndex(document: unknown): ReadonlyMap<string, RigorView> {
  const index = new Map<string, RigorView>()
  const rows = objectAt(document)?.['runs']
  if (!Array.isArray(rows)) return index
  for (const row of rows) {
    const record = objectAt(row)
    if (record === null) continue
    const id = textAt(record['run']) ?? textAt(record['run_id'])
    if (id === null || id === '') continue
    const rigor = readRigor(record['rigor'])
    if (rigor === null) continue
    if (!index.has(id)) index.set(id, rigor)
  }
  return index
}

/** objectAt is a value that is a JSON object, or null. */
function objectAt(raw: unknown): Record<string, unknown> | null {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return null
  return raw as Record<string, unknown>
}

/** textAt is a value that is a JSON string, or null. */
function textAt(raw: unknown): string | null {
  return typeof raw === 'string' ? raw : null
}
