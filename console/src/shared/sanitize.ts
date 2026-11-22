// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The text path: every string a job wrote is untrusted, and this is where it
// stops being able to be anything but text (desktop-console plan Task 2.0;
// design section 5.1 rule 7; console/docs/json-contract.md section 9 rule 4).
//
// The design's rule is that the sanitization `watch` applies is **re-applied at
// the display layer rather than trusted to have survived the subprocess
// boundary**: `title`, `message`, `note`, `warnings[]`, `queued_note`,
// `reason` and `halted` are written by a job's own container, redacted by the
// CLI, carried through a pipe the console does not own, and read back as
// `unknown`. Three things can go wrong between the job and the screen, and this
// module answers all three:
//
//   1. **the text can be markup.** `<script>alert(1)</script>` is a legal run
//      title. Nothing here interprets it -- the value comes out as the same
//      characters -- but a screen that assigns it to `innerHTML` would run it,
//      which is why `renderer/app.ts` writes `textContent` and a test in
//      `sanitize.test.ts` scans `src/renderer/` for the properties that do not.
//   2. **the text can be a credential.** A job that pastes a key into a title,
//      or an operator who redacts by hand into something the recorder's rules
//      do not recognise, would put a live secret in a screenshot. This module
//      re-runs the recorder's own credential rules (`src/source/redact.ts`)
//      rather than a cheaper approximation of them: the credential classes
//      below are that file's list, and the corpus test in `sanitize.test.ts`
//      asserts the property that makes them a re-run -- in free text, this
//      layer never draws a credential the recorder would have changed. It is
//      **not** a re-run of the recorder's identity masks: since D3213
//      (2026-11-21) the console draws an address, a repository path, a branch,
//      a project name, a host and a path as they came, because it is the
//      operator's own tool and those are the values the operator works with,
//      while the recorder still redacts them in the fixtures it writes. So the
//      corpus property is one-directional: this layer may draw more than the
//      recorder, and may never leave a credential the recorder would have
//      taken. (The `url` class is the exception in the other direction: the
//      recorder replaces every url field, and this class keeps a clean https
//      link and refuses one that carries a credential.) The two rules this
//      layer cannot reproduce (the people pass and the installation's
//      literals) are named where the list is, in `FREE_TEXT_RULES`, and
//      neither is approximated.
//   3. **the text can be a shape it must not be.** A repository name that is
//      not `owner/name`, a URL with `javascript:` in it, a run id that is not
//      one: the classes below are deny-by-default, so a value that is not the
//      class the wire key declares is *refused* -- and a refusal is drawn as
//      `n/a` with a sentence saying why, never as the value.
//
// **Nothing here imports anything at runtime.** The renderer loads this module
// and has no Node builtins, no network and no Electron. That is why the
// recorder's vocabulary is *copied* rather than imported: `src/source/redact.ts`
// reaches for `node:crypto`, and a `shared/` module that imported it would be a
// module the renderer cannot load. `sanitize.test.ts` holds the vocabulary
// (markers, placeholders, bounds) equal to the recorder's by equality, and the
// rule list equal to it by running both over the same corpus, so a drift on
// either side fails a test rather than a screen. The corpus test is
// one-directional on purpose: this layer is allowed to change *more* than the
// recorder -- it always removes the invisible characters, and it keeps the word
// `Bearer` where the recorder replaces it -- and is not allowed to change less.

/**
 * TextClass is what a wire key's string is allowed to be. Every string the
 * console draws has one, and `schema.ts`'s `WIRE_TEXT_CLASSES` is the map from
 * the contract's own key names to this list -- a key with no class is a key the
 * decoder refuses to guess about.
 *
 * The classes are the plan's list, one member each: run titles and the other
 * free text a job writes (`sentence`), branch names, repository names, slugs,
 * project names, people, ids, paths and URLs. `label` is the CLI's own
 * short rendered fields -- a stage, a round, a verify state, a model pair --
 * which are not free text but are still text from the wire and so still come
 * through here. `host` is a machine name and `path` is a filesystem path: the
 * recorder redacts both in the fixtures it writes, and this layer draws each as
 * it came (D3213), cleaned of the invisible characters and at the class's own
 * bound like every other value a screen shows.
 *
 * `repoPattern` and `imageRef` were added by the managed-project screens' CM7
 * (the policy notes, review of 2026-11-07). The plan requires that section to
 * draw a published policy's `allow_repos` and its image catalog, and neither
 * value is a shape the set had a class for: an allow-list entry is
 * `provider/owner/name` or a per-segment pattern (`repository` draws
 * `owner/name`, one slash, and refuses both), and a catalog entry's `image` is
 * a container image reference. `label`, the class for an identifier a policy
 * wrote, is not the answer for either: it draws whatever it is given, up to a
 * line and 200 characters, so an entry the CLI's own writer refuses -- a `..`
 * segment, a star across a slash, a tag a registry cannot serve -- would be
 * drawn as though the policy held it. So the two are validating classes of the
 * same kind as `repository`, each holding the grammar the CLI enforces at its
 * own writer, cited in its comment.
 *
 * `scope` was added by the Manage section's People & access kind (task MO-P,
 * review of 2026-11-20), and it is the same case a third time. A stored grant's
 * scope is one string -- the word `project`, the word `all-repos`, or a
 * comma-separated list of canonical repository patterns (`internal/access`'s
 * `ParseScope`, `Scope.String`) -- and `label` would draw any list at all,
 * where the class holds `ParseScope`'s own two rules. That is the whole of the
 * cell for the scopes whose Copy-command the builder refuses -- `*` and `,` are
 * not characters `command.ts` copies, so a `github/acme/*` or a list has no
 * Copy -- which leaves the value the grant really covers nowhere on the row.
 */
export type TextClass =
  | 'sentence'
  | 'repository'
  | 'repoPattern'
  | 'scope'
  | 'imageRef'
  | 'slug'
  | 'branch'
  | 'project'
  | 'person'
  | 'runId'
  | 'label'
  | 'host'
  | 'path'
  | 'url'

/**
 * TEXT_CLASSES is the whole list, in the order the tests walk it. It is the
 * closed set: `sanitize.test.ts` asserts every member has a presentation in
 * `sanitizeText` and a test of its own, and `schema.test.ts` asserts every wire
 * string key names one of them (or is one of the strings that are not text).
 */
export const TEXT_CLASSES: readonly TextClass[] = Object.freeze([
  'sentence',
  'repository',
  'repoPattern',
  'scope',
  'imageRef',
  'slug',
  'branch',
  'project',
  'person',
  'runId',
  'label',
  'host',
  'path',
  'url',
])

// ---------------------------------------------------------------------------
// The vocabulary, shared with the recorder (Task 1.4) and with the IPC layer
// (Task 1.2). These strings are not spelled differently here on purpose: a
// placeholder this layer cannot read is a marker drawn as text, which tells a
// person a redaction is a value. `sanitize.test.ts` holds each of them equal to
// its counterpart by importing the other module.
// ---------------------------------------------------------------------------

/** PATH_PLACEHOLDER is the recorder's `<path>`. This layer no longer writes it
 * (D3213): it draws a path as it came, and the marker is still here because it
 * is a value a fixture carries and this layer must recognise it. */
export const PATH_PLACEHOLDER = '<path>'
/** PROJECT_PLACEHOLDER is the recorder's `<project>`, recognised and not
 * written -- see `PATH_PLACEHOLDER`. */
export const PROJECT_PLACEHOLDER = '<project>'
/** BUCKET_PLACEHOLDER is the recorder's `<bucket>`, recognised and not written
 * -- see `PATH_PLACEHOLDER`. */
export const BUCKET_PLACEHOLDER = '<bucket>'
/** HOST_PLACEHOLDER is the recorder's `<host>`, recognised and not written --
 * see `PATH_PLACEHOLDER`. */
export const HOST_PLACEHOLDER = '<host>'
/** BRANCH_PLACEHOLDER replaces a git branch or ref. */
export const BRANCH_PLACEHOLDER = '<branch>'
/** TOKEN_PLACEHOLDER replaces anything credential-shaped, or the value of a key
 * that names one. */
export const TOKEN_PLACEHOLDER = '[redacted-token]'
/** URL_PLACEHOLDER replaces a URL in free text that carries a credential -- the
 * whole URL, as the recorder replaces every url field. */
export const URL_PLACEHOLDER = '[redacted-url]'
/** ADDRESS_PLACEHOLDER is the recorder's marker for an email address, recognised
 * and not written -- see `PATH_PLACEHOLDER`. Only this exact string is the
 * marker: `ana@example.invalid` is an address this layer now draws as it came. */
export const ADDRESS_PLACEHOLDER = 'redacted@example.invalid'
/** LITERAL_PLACEHOLDER replaces one of the installation's own literals. */
export const LITERAL_PLACEHOLDER = '[redacted]'

/**
 * MARKERS is the closed set of fixed placeholders, in the recorder's order.
 * `isMarkerText` below recognises a member of this list, or one of the
 * pseudonym grammars, and nothing else: "this is a redaction the CLI already
 * made" is not a judgement this layer may make loosely, because the alternative
 * reading of a marker is that it is a value a job chose.
 */
export const MARKERS: readonly string[] = Object.freeze([
  PATH_PLACEHOLDER,
  PROJECT_PLACEHOLDER,
  BUCKET_PLACEHOLDER,
  HOST_PLACEHOLDER,
  BRANCH_PLACEHOLDER,
  TOKEN_PLACEHOLDER,
  URL_PLACEHOLDER,
  ADDRESS_PLACEHOLDER,
  LITERAL_PLACEHOLDER,
])

/**
 * MAX_TEXT_INPUT_LENGTH is how much text this layer will look at, and it is the
 * recorder's own bound (64 KiB). A longer value is **refused, never
 * truncated**, and the bound is measured twice: on the value as it came, before
 * anything runs (a hostile 40 MB title costs the renderer one comparison, not a
 * frame), and again on the form the rules read -- after NFKC, which can grow a
 * string eighteen-fold (U+FDFA is one code unit and eighteen after it), so a
 * value inside the first bound can still be a million characters by the time a
 * rule sees it. Every rule below is linear in what it reads, so bounding what it
 * reads is what bounds the cost.
 */
export const MAX_TEXT_INPUT_LENGTH = 64 * 1024

/**
 * MAX_TEXT_LENGTH is how much text a screen may draw from one field. Past it
 * the text is cut and the cut is marked (`clipped`), because a title silently
 * shortened is a title a person reads as complete.
 */
export const MAX_TEXT_LENGTH = 512

/**
 * MAX_LABEL_LENGTH bounds a `label`: a stage, a round, a model pair. It is
 * smaller than a sentence's bound because a label is drawn in a table cell, and
 * a label longer than this is not a label -- it is a document in a cell, and it
 * is refused rather than cut.
 */
export const MAX_LABEL_LENGTH = 200

/** MAX_URL_LENGTH bounds a URL, and it is the IPC layer's own bound (Task 1.2):
 * the main process refuses a URL argument longer than this, so a URL the
 * renderer accepted and the main process refused would be a link that does
 * nothing when clicked. */
export const MAX_URL_LENGTH = 2048

/** MAX_REPOSITORY_LENGTH bounds an `owner/name`, as the IPC layer bounds the
 * repository argument it accepts. */
export const MAX_REPOSITORY_LENGTH = 200

/** MAX_REPO_PATTERN_LENGTH bounds a `repoPattern`, and it is the CLI's own bound
 * (`projectpolicy.MaxPatternLen`, which `fugaro project repos add` holds its
 * argument to as well): a pattern the CLI refused to write is not one a console
 * should draw, and a bound of this layer's own would refuse an entry a real
 * node carries -- a 255-character nested path is one Decode admits. */
export const MAX_REPO_PATTERN_LENGTH = 255

/** MAX_IMAGE_REF_LENGTH bounds an `imageRef`, and it is the CLI's own bound
 * (`maxCatalogImageRefLen`, `internal/config/catalog.go`): a reference the CLI
 * refused to write is not one a console should draw. */
export const MAX_IMAGE_REF_LENGTH = 255

/** ELLIPSIS marks a clipped sentence. It is three periods rather than U+2026 so
 * that the cut is visible on a terminal and a screenshot alike, and so that a
 * document that legitimately contains an ellipsis is not confused with one this
 * layer cut. */
const ELLIPSIS = '...'

/**
 * SanitizeFlag names one thing this layer did to a value. Flags are part of the
 * result rather than a log line because a screen may want to mark a value it
 * had to change -- and because a test can assert the *reason* a value came out
 * different instead of asserting on the output string alone.
 */
export type SanitizeFlag =
  /** invisible formatting characters were removed */
  | 'stripped-invisible'
  /** a credential-shaped run, or the value of a credential-named key, was replaced */
  | 'neutralised-credential'
  /** a URL in free text was replaced whole */
  | 'replaced-url'
  /** the userinfo of a URL was dropped, keeping the rest of the URL */
  | 'userinfo-removed'
  /** a git branch in free text became `<branch>` */
  | 'branch-placeholder'
  /** the text was cut at `MAX_TEXT_LENGTH` */
  | 'clipped'

/**
 * Sanitized is the result: the text a screen may draw, or the reason it may
 * not. It is a union rather than a struct with an `ok` boolean so that a caller
 * cannot reach for `.text` on a refusal -- the reason is a sentence this layer
 * wrote, and it **never contains the value it refused**, which is the whole
 * point: a refusal that echoed its input would put the input on screen through
 * the back door.
 */
export type Sanitized =
  | { readonly ok: true; readonly text: string; readonly flags: readonly SanitizeFlag[] }
  | { readonly ok: false; readonly reason: string }

function ok(text: string, flags: readonly SanitizeFlag[] = []): Sanitized {
  return { ok: true, text, flags }
}

function refuse(reason: string): Sanitized {
  return { ok: false, reason }
}

// ---------------------------------------------------------------------------
// The recorder's rules, copied (see the header for why they are not imported).
// Each pattern below is the same pattern `src/source/redact.ts` carries, and
// `sanitize.test.ts` runs both modules over the same inputs to hold that claim.
// ---------------------------------------------------------------------------

/**
 * The one definition of the characters the console treats as invisible, shared
 * by the three readers of CLI-derived text: this layer, the recorder's redactor
 * (`src/source/redact.ts`) and the IPC argument check (`src/main/ipc.ts`), which
 * both import it from here (this module imports nothing, so it is the end of
 * the chain). It used to be three copies, and the copies drifted: the
 * recorder's lacked U+FFF9..U+FFFB, so a credential split by an interlinear
 * annotation character was neither redacted nor caught by the audit while this
 * layer stripped the character and drew the credential joined back.
 * `test/unit/invisible.test.ts` walks the whole Unicode range and holds every
 * layer to the categories below, so a narrower copy cannot come back unseen.
 */

/**
 * INVISIBLE_CLASS is a regular-expression character class (for the `u` flag):
 * every format character (`Cf`: zero-width spaces and joiners, the soft hyphen,
 * the BOM, the bidi embeddings, overrides and isolates, the interlinear
 * annotation characters, the tag characters, the prepended concatenation
 * marks), every other default-ignorable code point (variation selectors,
 * fillers, the Mongolian free variation selectors), every bidi control, every
 * private-use code point (`Co`), and every C0/C1 control but tab, line feed and
 * carriage return, which every layer handles as line structure. U+FFF9..U+FFFB
 * and U+180E are `Cf` already; they are named so a reader of this line finds
 * them.
 *
 * **`\p{Co}` is named for two reasons, and the second is the console's own.**
 * A private-use code point is undrawable -- no font has one, so a terminal, a
 * screen and a diff show nothing where it sits -- and that makes it a splitter
 * like the format characters: a credential with one in the middle is joined by
 * the prepared form (so it is redacted) and whole in the form as written, and a
 * layer that read one and not the other would draw what the other had redacted.
 * And U+E000/U+E001, the two the sentinel hold below is built from, are private
 * use: a value carrying them is a value interacting with that hold, which is why
 * `SENTINEL_RE` existed before this class named them (review round 5 of PR 478).
 * The two lines agree now: the hold's code points are stripped by the same rule
 * every layer applies, and a value cannot carry one into the hold by reading a
 * narrower class than the recorder does.
 */
export const INVISIBLE_CLASS = String.raw`[\p{Cf}\p{Co}\p{Default_Ignorable_Code_Point}\p{Bidi_Control}\u{fff9}-\u{fffb}\u{180e}\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f]`

/** SEPARATOR_CLASS is the line and paragraph separators (`Zl`, `Zp`): not
 * invisible, but a line break no rule expects inside a value, so the display
 * layer removes them and the recorder must too. */
export const SEPARATOR_CLASS = String.raw`[\p{Zl}\p{Zp}]`

/** invisibleRe is a fresh global matcher for `INVISIBLE_CLASS`. */
export function invisibleRe(): RegExp {
  return new RegExp(INVISIBLE_CLASS, 'gu')
}

/** invisibleOrSeparatorRe is a fresh global matcher for both classes, merged
 * into one class (one test per character rather than an alternation). */
export function invisibleOrSeparatorRe(): RegExp {
  return new RegExp(`${INVISIBLE_CLASS.slice(0, -1)}${SEPARATOR_CLASS.slice(1)}`, 'gu')
}

const ONE_INVISIBLE = new RegExp(`^${INVISIBLE_CLASS}$`, 'u')
const ONE_SEPARATOR = new RegExp(`^${SEPARATOR_CLASS}$`, 'u')

/** isInvisibleCodePoint answers for one code point. */
export function isInvisibleCodePoint(code: number): boolean {
  return ONE_INVISIBLE.test(String.fromCodePoint(code))
}

/** isSeparatorCodePoint answers for one code point. */
export function isSeparatorCodePoint(code: number): boolean {
  return ONE_SEPARATOR.test(String.fromCodePoint(code))
}

/**
 * INVISIBLE_RE matches what `prepare` strips before any rule runs: the shared
 * set above. None of them is visible, so none of them may split a token, an
 * address or a literal into pieces no rule recognises.
 */
const INVISIBLE_RE = invisibleRe()

/** PREPARE_STRIP_RE is everything `prepare` and the resolver's prepared form
 * drop after the sequences: `INVISIBLE_RE` and the two separators `CONTROL_RE`
 * removes. */
const PREPARE_STRIP_RE = invisibleOrSeparatorRe()

/**
 * AmbiguousSite is one place where a sequence the scanner removed swallowed
 * printable characters that, in text never meant for a terminal, may have been
 * text: the final byte (`ESC [ A` is "cursor up", but `ESC [` + an AWS key id is
 * drawn `KIA...`), and the parameter and intermediate bytes before it (`ESC [ 1`
 * + a token loses `1` and the token's first letter). ECMA-48 leaves the reading
 * open, so `resolveAmbiguousSequences` reads each site every way -- dropped (as
 * a terminal draws it), only the final byte kept, the whole body kept -- and
 * redacts the union of what the credential rules match.
 *
 * `at` is where the swallowed text sat in the scanner's output; `final` is the
 * final byte when the sequence had one; `body` is every printable byte the
 * sequence consumed after its introducer, final byte included.
 */
export interface AmbiguousSite {
  readonly at: number
  readonly final: string | null
  readonly body: string
}

/** SEQUENCE_PAD_RE is what the scanner steps over *inside* an escape sequence,
 * as a terminal does: the invisible formatting characters (so ESC + a
 * zero-width space + `[31m` is the sequence it renders as), the C0 controls a
 * terminal executes in the middle of a sequence without ending it, and DEL.
 *
 * **Tab, line feed and carriage return are not padding** (review round 4 of PR
 * 494): they are text this layer keeps everywhere else, and stepping over them
 * made `ESC [ 1` + a line break + `x` lose the line break. Each of them ends the
 * sequence as a byte outside its grammar does -- the sequence is malformed, it
 * ends before that byte, and the byte is kept. BEL, ESC, CAN and SUB are not
 * padding either: they end or abort a sequence. */
const SEQUENCE_PAD_RE =
  /^[\p{Default_Ignorable_Code_Point}\p{Bidi_Control}\ufff9-\ufffb\u0000-\u0006\u0008\u000b\u000c\u000e-\u0017\u0019\u001c-\u001f\u007f]$/u

/** SEQUENCE_FAST_RE answers whether a string holds anything that can open a
 * sequence (ESC or a C1 control); most strings do not, and are returned as
 * they came. */
const SEQUENCE_FAST_RE = /[\u001b\u0080-\u009f]/

/** A significant character inside a sequence: its folded code, where it starts
 * and where the scan resumes after it. */
interface SequenceChar {
  readonly code: number
  readonly start: number
  readonly end: number
}

/**
 * sequenceChar is the next character of a sequence at or after `at`, stepping
 * over `SEQUENCE_PAD_RE` and folding the character through NFKC (so a fullwidth
 * `［` is the `[` it is drawn as, and a fullwidth digit a parameter byte). It is
 * how NFKC and the zero-width strip run *before* a sequence is recognised,
 * without a second pass over the string. `null` is the end of the input.
 */
function sequenceChar(text: string, at: number): SequenceChar | null {
  let index = at
  while (index < text.length) {
    const point = text.codePointAt(index) ?? 0
    const width = point > 0xffff ? 2 : 1
    const character = String.fromCodePoint(point)
    if (SEQUENCE_PAD_RE.test(character)) {
      index += width
      continue
    }
    let code = point
    if (point > 0x9f) {
      const folded = character.normalize('NFKC')
      if (folded.length === 1) code = folded.charCodeAt(0)
    }
    return { code, start: index, end: index + width }
  }
  return null
}

/** controlStringEnd is where a control string (OSC, DCS, SOS, PM, APC) whose
 * body starts at `from` ends: after BEL, after ST (ESC `\` or U+009C), *at* an
 * ESC that is not ST (a terminal abandons the string there, and the ESC opens
 * whatever follows), or at the end of the input. One pass, no backtracking. */
function controlStringEnd(text: string, from: number): number {
  for (let index = from; index < text.length; index += 1) {
    const code = text.charCodeAt(index)
    if (code === 0x07 || code === 0x9c) return index + 1
    if (code === 0x1b) {
      const next = sequenceChar(text, index + 1)
      return next !== null && next.code === 0x5c ? next.end : index
    }
  }
  return text.length
}

/** SequenceSpan is where one sequence ends, and -- when it consumed bytes that
 * could have been text -- where those bytes start (`bodyFrom`) and whether the
 * last of them was a final byte. A control string has no body here: a terminal
 * draws none of it, and neither does this layer. */
interface SequenceSpan {
  readonly end: number
  readonly bodyFrom: number | null
  readonly hasFinal: boolean
}

const NO_SEQUENCE: SequenceSpan = { end: -1, bodyFrom: null, hasFinal: false }

/**
 * sequenceEnd answers where the escape sequence whose introducer is `final`
 * (the byte after ESC, or a C1 control's ESC equivalent) and whose body starts
 * at `from` ends. It is ECMA-48's grammar, read leniently:
 *
 *   - CSI (`[`): parameter bytes `0-?`, intermediate bytes ` -/`, one final
 *     byte `@-~`. A malformed CSI ends before the byte that broke it;
 *   - OSC, DCS, SOS, PM, APC (`]`, `P`, `X`, `^`, `_`): a control string, to
 *     BEL, ST or the end of the input (`controlStringEnd`);
 *   - nF (intermediates ` -/` then a final `0-~`), and the two-character Fp,
 *     Fe and Fs escapes (`0-~`).
 *
 * Anything else after ESC is not a sequence, and only the ESC goes. There is
 * one reading -- the terminal's -- and the bytes it swallowed are reported
 * (`SequenceSpan`) so that `resolveAmbiguousSequences` can read them as text
 * too.
 */
function sequenceEnd(text: string, final: SequenceChar | null, from: number): SequenceSpan {
  if (final === null) return { end: from, bodyFrom: null, hasFinal: false }
  const f = final.code
  if (f === 0x5b) {
    let next = sequenceChar(text, from)
    while (next !== null && next.code >= 0x30 && next.code <= 0x3f) next = sequenceChar(text, next.end)
    while (next !== null && next.code >= 0x20 && next.code <= 0x2f) next = sequenceChar(text, next.end)
    if (next === null) return { end: text.length, bodyFrom: from, hasFinal: false }
    if (next.code < 0x40 || next.code > 0x7e) return { end: next.start, bodyFrom: from, hasFinal: false }
    return { end: next.end, bodyFrom: from, hasFinal: true }
  }
  if (f === 0x5d || f === 0x50 || f === 0x58 || f === 0x5e || f === 0x5f) {
    return { end: controlStringEnd(text, from), bodyFrom: null, hasFinal: false }
  }
  if (f >= 0x20 && f <= 0x2f) {
    let next = sequenceChar(text, from)
    while (next !== null && next.code >= 0x20 && next.code <= 0x2f) next = sequenceChar(text, next.end)
    if (next === null) return { end: text.length, bodyFrom: final.start, hasFinal: false }
    return next.code >= 0x30 && next.code <= 0x7e
      ? { end: next.end, bodyFrom: final.start, hasFinal: true }
      : { end: next.start, bodyFrom: final.start, hasFinal: false }
  }
  if (f >= 0x30 && f <= 0x7e) return { end: from, bodyFrom: final.start, hasFinal: true }
  return NO_SEQUENCE
}

/** siteOf reads the printable bytes a sequence swallowed between `from` and
 * `end` (padding stepped over, each byte folded as the grammar read it) into an
 * `AmbiguousSite`, or `null` when it swallowed nothing but whitespace -- a
 * kept space can only split text, never join it. */
function siteOf(text: string, at: number, from: number, end: number, hasFinal: boolean): AmbiguousSite | null {
  const characters: string[] = []
  let next = sequenceChar(text, from)
  while (next !== null && next.start < end) {
    characters.push(String.fromCharCode(next.code))
    next = sequenceChar(text, next.end)
  }
  const body = characters.join('')
  if (!/\S/.test(body)) return null
  const last = characters[characters.length - 1]
  return { at, final: hasFinal && last !== undefined ? last : null, body }
}

/**
 * stripTerminalSequences removes every terminal escape sequence and control
 * string from `text`, in one pass: what a terminal would swallow is gone, whole,
 * so nothing of it is left glued to the next word. It is the one definition the
 * three readers of CLI text share -- the sentence class and the refusal door
 * here, and the recorder (`src/source/redact.ts` imports it) -- and it replaces
 * a regular expression whose unterminated-OSC branch rescanned the rest of the
 * string from every U+009D (seconds per call on 64 KiB of them).
 *
 * **It is linear**: the index only moves forward, every loop consumes what it
 * reads, and a lookahead over padding that does not open a sequence is read at
 * most twice. ESC and the C1 controls (U+0080..U+009F, each the one-character
 * form of ESC + the byte 0x40 above it) introduce a sequence; the grammar is
 * `sequenceEnd`'s. An unterminated control string runs to the end of the input:
 * a terminal draws none of it, and neither does this. The other controls are
 * left for `INVISIBLE_RE`, which every caller applies afterwards.
 */
export function stripTerminalSequences(text: string): string {
  return scanTerminalSequences(text, null, null)
}

/** scanTerminalSequences is `stripTerminalSequences`, reporting into `sites`
 * (when given) every sequence that swallowed printable bytes, at its place in
 * the output. Reading the swallowed bytes back is linear too: they are bytes the
 * scan consumed, read once more. */
function scanTerminalSequences(text: string, sites: AmbiguousSite[] | null, cuts: number[] | null): string {
  if (!SEQUENCE_FAST_RE.test(text)) return text
  const kept: string[] = []
  let keptLength = 0
  let copyFrom = 0
  let index = 0
  while (index < text.length) {
    const code = text.charCodeAt(index)
    if (code !== 0x1b && (code < 0x80 || code > 0x9f)) {
      index += 1
      continue
    }
    const piece = text.slice(copyFrom, index)
    kept.push(piece)
    keptLength += piece.length
    let span: SequenceSpan = NO_SEQUENCE
    if (code === 0x1b) {
      const final = sequenceChar(text, index + 1)
      if (final !== null) span = sequenceEnd(text, final, final.end)
    } else if (code === 0x9b || code === 0x9d || code === 0x90 || code === 0x98 || code === 0x9e || code === 0x9f) {
      span = sequenceEnd(text, { code: code - 0x40, start: index, end: index + 1 }, index + 1)
    }
    if (sites !== null && span.bodyFrom !== null && span.end > span.bodyFrom) {
      const site = siteOf(text, keptLength, span.bodyFrom, span.end, span.hasFinal)
      if (site !== null) sites.push(site)
    }
    // Every removal is a place where two pieces of text now touch: the
    // resolver reads each such place as a boundary too (`resolveReadings`).
    if (cuts !== null) cuts.push(keptLength)
    index = span.end > index ? span.end : index + 1
    copyFrom = index
  }
  kept.push(text.slice(copyFrom))
  return kept.join('')
}

/**
 * CONTROL_RE matches the line and paragraph separators (`SEPARATOR_CLASS`) --
 * the two separators a screen must not draw. They
 * are removed rather than flattened, so a sentence that used one to smuggle a
 * second row past a single-line check has already lost it.
 */
const CONTROL_RE = new RegExp(SEPARATOR_CLASS, 'gu')

/**
 * prepare is the form every rule reads: NFKC first, so a fullwidth `＠` *is* an
 * `@` before the address rule looks and a fullwidth `ｓｋ-` prefix is the `sk-`
 * the token table looks for; then the invisible characters gone, so none of them
 * can split a shape into pieces no rule recognises, and the line and paragraph
 * separators with them. It is the recorder's `prepare`, copied.
 */
function prepare(text: string): string {
  return stripTerminalSequences(text.normalize('NFKC')).replace(PREPARE_STRIP_RE, '')
}

/**
 * CREDENTIAL_ASSIGNMENT_RE matches a key that names a credential and the value
 * after it, keeping the name and losing the value -- the recorder's rule, and
 * the reason `API_KEY: hunter2` is redacted even though `hunter2` has no shape
 * at all. The name starts at the start of its run, never inside one, so
 * `monkey=` is not a key called `key`.
 */
const CREDENTIAL_ASSIGNMENT_RE =
  /(?<![\p{L}\p{N}_-])([\p{L}\p{N}_-]*?(?:token|secret|password|passwd|pwd|api[_-]?key|apikey|private[_-]?key(?:[_-]id)?|credentials?|authorization|signature|cookie))(?![\p{L}\p{N}])(["']?\s*[:=]\s*["']?)([^\s"'&,;]+)/giu

/**
 * shape joins the fragments of one pattern. Every pattern below is the
 * recorder's, character for character, and is assembled from pieces at run time
 * so that no line of this file is itself a credential-shaped string.
 *
 * That is not decoration either. A literal forge prefix, a literal AWS key id
 * prefix or a literal PEM header in a source file is a line every secret
 * scanner flags, and a scanner's output cannot tell an invented prefix in a
 * regular expression from a leaked key -- which is why an earlier revision of
 * this task's commit could not be pushed at all. The fragments here are inert on
 * their own; `sanitize.test.ts` runs both this module and the recorder over the
 * same corpus and asserts that this layer never keeps what the recorder removed,
 * so the assembly cannot silently drift from the rule it claims to re-apply.
 */
function shape(fragments: readonly string[], unicode = false): RegExp {
  return new RegExp(fragments.join(''), unicode ? 'gu' : 'g')
}

/** BEARER_RE matches a `Bearer <credential>` header and keeps the word, so the
 * text reads "Bearer [redacted-token]" rather than losing its context. The
 * recorder replaces the word as well; keeping it is the one difference this
 * layer makes on purpose, and it is on the safe side of the boundary -- the
 * credential is gone either way. */
const BEARER_RE = /\b(Bearer)(\s+)[A-Za-z0-9._~+/-]{12,}=*/gi

/**
 * PEM_RE matches a PEM private-key block. The body may not run past the next
 * BEGIN or END line, so a string of BEGIN lines with no END is one short scan
 * each rather than a scan to the end each.
 */
const PEM_RE = shape([
  String.raw`-----BEGIN `,
  '[A-Z ]*',
  String.raw`PRIVATE KEY-----`,
  String.raw`(?:(?!-----(?:BEGIN|END) )[\s\S])*`,
  String.raw`-----END `,
  '[A-Z ]*',
  String.raw`PRIVATE KEY-----`,
])

/**
 * maskCredentialBlocks masks the one credential shape that is a **whole block
 * and not a run** (K3's review round 1).
 *
 * **It exists because a screen may split a value into lines before it draws it,
 * and the class then reads one line at a time.** `PEM_RE` needs the BEGIN and
 * END lines of a block in the value it is given, so a body split on its
 * newlines first gives it nothing to match: the markers are drawn as the text
 * they are and the body's own lines are left to the entropy net, which keeps any
 * run under its floor -- a wrapped body's short last line is key material drawn
 * whole. The body of a pull request is untrusted text and many lines long, so
 * the Coordinator is the caller this is for.
 *
 * **A line split is the caller's and the newlines are the caller's to keep.**
 * This masks the blocks and changes nothing else -- every newline the value
 * carried is still there, in its place -- so a screen still gets the author's
 * own lines out of the value afterwards. It is deliberately not a sanitizer: it
 * reports nothing, it strips no invisible character and it replaces no URL, and
 * what it hands back is still untrusted text that every line must still go
 * through `sanitizeText` before it is drawn.
 */
export function maskCredentialBlocks(raw: string): string {
  return raw.replace(PEM_RE, TOKEN_PLACEHOLDER)
}

/** GIT_FORGE, ANTHROPIC_STYLE and the rest are the fragments the family patterns
 * below are built around, so that no line here spells a prefix out. */
const GIT_FORGE = 'gh'
const ANTHROPIC_STYLE = 's'
const SLACK_STYLE = 'xo'
const AWS_STYLE = 'AK'
const GOOGLE_STYLE = 'AI'
const OAUTH_ACCESS = 'ya'
const OAUTH_REFRESH = '1/'
const JWT_STYLE = 'ey'

/**
 * TOKEN_SHAPES is the recorder's table of credential shapes: the token families
 * GitHub, GitLab, npm, Anthropic/OpenAI-style providers, Slack, AWS and Google
 * issue, a JSON Web Token, a Google OAuth access token and a refresh token. It
 * is a *floor*, not the rule: a value whose key names a credential goes whatever
 * it looks like, a percent-encoded or base64-wrapped candidate is decoded first,
 * and a long high-entropy run is a token whatever its prefix.
 *
 * The recorder's ninth and tenth shapes are `BEARER_RE` and `PEM_RE` below,
 * kept apart only because the first keeps its word.
 */
export const TOKEN_SHAPES: readonly RegExp[] = Object.freeze([
  shape([
    String.raw`\b(?:`,
    GIT_FORGE,
    String.raw`[pousr]_|`,
    'github',
    String.raw`_pat_|`,
    'gl',
    String.raw`pat-|`,
    'np',
    String.raw`m_)[A-Za-z0-9_]{4,}`,
  ]),
  shape([String.raw`(?<![A-Za-z0-9_-])`, ANTHROPIC_STYLE, String.raw`k-[A-Za-z0-9_-]{16,}\b`]),
  shape([String.raw`(?<![A-Za-z0-9_-])`, SLACK_STYLE, String.raw`x[baprs]-[A-Za-z0-9-]{8,}\b`]),
  shape([String.raw`\b`, AWS_STYLE, String.raw`IA[0-9A-Z]{16}\b`]),
  shape([String.raw`(?<![A-Za-z0-9_-])`, GOOGLE_STYLE, String.raw`za[0-9A-Za-z_-]{35}\b`]),
  shape([String.raw`\b`, OAUTH_ACCESS, String.raw`29\.[0-9A-Za-z_-]{8,}`]),
  shape([String.raw`(?<![\p{L}\p{N}])`, OAUTH_REFRESH, String.raw`/0[0-9A-Za-z_-]{8,}`], true),
  shape([
    String.raw`(?<![A-Za-z0-9_-])`,
    JWT_STYLE,
    String.raw`J[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`,
  ]),
  // A JWT with a short, empty or missing signature: its header and payload are
  // the readable part, so two JWT-style segments are a token whatever follows.
  // Without it the refusal door drew `<header>.<payload>.sig` while the
  // sentence class (whose host rule catches it) did not.
  shape([
    String.raw`(?<![A-Za-z0-9_-])`,
    JWT_STYLE,
    String.raw`J[A-Za-z0-9_-]{8,}\.`,
    JWT_STYLE,
    String.raw`J[A-Za-z0-9_-]{8,}(?:\.[A-Za-z0-9_-]*)?`,
  ]),
])

/** ENTROPY_RE is a candidate for the high-entropy rule: a long run of the
 * characters tokens are made of. */
const ENTROPY_RE = /(?<![\p{L}\p{N}_+/=.-])[A-Za-z0-9_+/=-]{20,}(?![\p{L}\p{N}_+/=-])/gu

/**
 * isHighEntropy is the recorder's split-token rule (`src/source/redact.ts`,
 * copied): either a 32+ character hexadecimal run with both letters and digits,
 * or a 20+ character run that mixes upper case, lower case and digits at 3.5 or
 * more bits of entropy per character.
 *
 * **It is the difference between a net and a shredder.** A model name
 * (`claude-sonnet-4-5-20250929`), a run id, a bucket slug and a workflow name
 * are all 20+ character runs of this alphabet, and they mix at most two of the
 * three character kinds -- so the rule keeps them. Without the rule the console
 * would draw `[redacted-token]` over a model's name, which is a false statement
 * about the document: it says a credential was found where the recorder found
 * none. Dropping the guard fails `sanitize.test.ts`'s recorder-agreement test.
 */
function isHighEntropy(run: string): boolean {
  const body = run.replace(/=+$/, '')
  if (body.length >= 32 && /^[0-9A-Fa-f]+$/.test(body) && /\d/.test(body) && /[A-Fa-f]/.test(body)) {
    return true
  }
  if (body.length < 20 || !/[A-Z]/.test(body) || !/[a-z]/.test(body) || !/\d/.test(body)) {
    return false
  }
  const counts = new Map<string, number>()
  for (const character of body) counts.set(character, (counts.get(character) ?? 0) + 1)
  let entropy = 0
  for (const count of counts.values()) {
    const p = count / body.length
    entropy -= p * Math.log2(p)
  }
  return entropy >= 3.5
}

/**
 * REPO_PSEUDONYM_RE, SLUG_PSEUDONYM_RE and PERSON_PSEUDONYM_RE are the grammars
 * of a redacted repository, slug and person. They are the recorder's, and they
 * matter here for one reason: a pseudonym is a long run of the entropy net's
 * own alphabet (`r-4c25cf20/r-f1aa9214`), so a net applied to it would turn a
 * redaction the fixture already made into a second, different lie.
 */
const REPO_PSEUDONYM_RE = /^r-[0-9a-f]{8}(?:\/r-[0-9a-f]{8})?$/
const SLUG_PSEUDONYM_RE = /^slug-[0-9a-f]{8}$/
const PERSON_PSEUDONYM_RE = /^person-[0-9a-f]{8}$/

/** ANY_PSEUDONYM_RE finds a pseudonym inside free text. */
const ANY_PSEUDONYM_RE = /(?<![\p{L}\p{N}_-])(?:r-[0-9a-f]{8}(?:\/r-[0-9a-f]{8})?|slug-[0-9a-f]{8}|person-[0-9a-f]{8})(?![\p{L}\p{N}_-])/gu

// ---------------------------------------------------------------------------
// The pattern classes, in the recorder's order. Every pattern below is the
// recorder's, character for character: the anchor at the start of a run is what
// keeps it linear in its input, and this layer runs over values the console does
// not own.
// ---------------------------------------------------------------------------

/** URL_RE matches every URL. It runs first, so the userinfo, the host and the
 * path inside a URL never get a chance to be what is replaced. A URL is
 * replaced WHOLE -- userinfo included -- rather than parsed and rebuilt:
 * `https://user:pass@host/x` keeps nothing but the marker, and a URL this layer
 * cannot parse is still a URL this layer will not draw. The scheme starts at the
 * start of its run, never inside one, so `_https://evil.invalid/x` is a URL and
 * `xhttps://evil.invalid/x` is not. */
const URL_RE = /(?<![a-zA-Z0-9+.-])[a-zA-Z][a-zA-Z0-9+.-]*:\/\/[^\s"'`<>]+/g

/** PATH_SEGMENT is one segment of a filesystem path. Only two patterns are built
 * from it now: `ABS_PATH_SOURCE` below, which the recorder's `ABS_PATH_RE` and
 * the `PATH_RUN_RE` guard are both made of. The free-text path rule that used to
 * match a path inside a sentence went with D3213 (2026-11-21), and with it the
 * free-text address and branch rules -- the console draws an address and a
 * branch as they came.
 *
 * **A space is part of a segment when more path follows it**, so
 * `/Users/Zora Kell/plan/x` is one path and a sentence after a path is not. Two
 * slashes is what keeps a sentence from being eaten: `and/or` is not rooted.
 *
 * The separator between joined segments is `\s`, not `[ \t]` (review round 5 of
 * PR 478): the rules read the *prepared* form, which keeps its line breaks, and
 * a flattening that happens later means `checkout /home\nzora/.ssh here` was two
 * matches that neither rule took -- the operator's home drawn whole, in the one
 * class that is deny-by-default. A line break is whitespace between two segments
 * the same way a space is, and the whole run still has to be followed by more
 * path (`SEG/`) to join, so a sentence after a path is still not eaten. */
const PATH_SEGMENT = String.raw`[^\s\/\\"'\x60<>|;,:()\[\]{}*?]+`
/** ABS_PATH_SOURCE is the one definition of the absolute-path pattern: the
 * recorder (`src/source/redact.ts`) builds its `ABS_PATH_RE` from this same
 * string, so the two layers cannot drift (review round 2 of PR 494: c32b14f
 * changed this half to `\s` and left the recorder on `[ \t]`). Exported as a
 * source string, not a shared `RegExp`, so neither module shares the other's
 * `lastIndex`. */
export const ABS_PATH_SOURCE = [
  String.raw`(?<![\p{L}\p{N}])(?:~\/|\/)(?:SEG(?:\s+SEG)*\/)+SEG`,
  String.raw`(?:~\/|\/)(?:SEG\/)+SEG`,
]
  .join('|')
  .replaceAll('SEG', PATH_SEGMENT)

/**
 * PATH_RUN_RE is the absolute-path pattern, asked of a whole candidate run:
 * `ABS_PATH_SOURCE` -- the one string the recorder's own `ABS_PATH_RE` is built
 * from -- anchored, with a single trailing separator allowed. It is the first
 * half of `isPathRun`'s question, below.
 *
 * **A path is not a token, and the net read one.** The owner's report of
 * 2026-10-21, from the screenshot run: the console's header drew the CLI's binary
 * path as `[redacted-token]` -- `acme-platform [redacted-token] 0.8.1 · ok` -- and
 * the e2e entry spec had failed the same way in PR #751, where the drawn header
 * held `[redacted-token].local/bin/fugaro`. The value is the path the run
 * installed its fake CLI in: `mkdtemp(join(tmpdir(), 'fugaro-cli-stub-'))`, then
 * `home/.local/bin/fugaro` (`test/support/cliStub.ts`) -- and it reached the net
 * through `drawInstallation`, which draws the binary path with `sanitizeRefusal`,
 * the one door that ran **no path rule**, because a refusal that names the
 * operator's own path is the remedy and the design draws it as the CLI printed it
 * (`sanitizeRefusal`). That door runs no path rule still, and since D3213
 * (2026-11-21) no door does: the free-text path rule is gone and a path is drawn
 * as it came. The guard below is therefore the whole of what keeps a path from
 * being read as a key, in every door at once, and `sanitize-path-runs.test.ts`
 * holds both halves of it.
 *
 * **Why the net read a path.** `ENTROPY_RE`'s alphabet is every character a token
 * is spelled with *and* every character a path segment is spelled with -- `/`,
 * `-`, `_` and `=` are all in it -- and its two lookarounds ask only for a
 * character outside that class on either side. A root and a space are therefore
 * enough for the net to take a whole path as one candidate run, and that run's
 * length, case mix and digits are then exactly `isHighEntropy`'s three questions,
 * which a path answers by accident: a `mkdtemp` suffix mixes case and digits, and
 * a path is long. The one thing a path never has is **a token**: a name is words
 * run together by separators, while a token is one unbroken stretch of the
 * alphabet. So a run is a path when the absolute-path pattern matches it whole
 * (`PATH_RUN_RE`) **and no word of it is a candidate on its own**
 * (`isHighEntropy`, the net's own question, asked of one word at a time): the net
 * found this run only by running a path's words together, and no part of the path
 * is a secret it would have found without them.
 *
 * **Why the whole pattern, and not "rooted with a second slash".** The first cut
 * of this guard read `/^\/[^/]*\//` -- rooted, with a second slash behind it,
 * whatever stands between them -- and that exempted runs the absolute-path
 * pattern would never have taken. A root and a second slash are one run to the
 * net even when the segment between them is *empty*, so a run like
 * `//<name>/<name>`, whose words are each under 20 characters and so no candidate
 * by themselves, came out **written** in the sentence door -- where the pattern
 * cannot match an empty segment -- and in the refusal door, while the recorder,
 * which runs no such guard, still redacted it. That is a redaction the net used
 * to make and no longer did, and a disagreement between the two layers that
 * `sanitize-agreement.test.ts` and the module's account of it say cannot exist.
 * Asking the absolute-path pattern of the run is the honest reading of "this run
 * is a path", which is what makes a run path-shaped at all; a pattern match
 * *inside* a run (`name//a/b`) is a path in the middle of something else, and a
 * run that merely holds one is not exempt.
 * `sanitize-path-runs.test.ts` pins the empty-segment run in both console doors
 * and against the recorder.
 *
 * **The trailing separator is the one relaxation, and it is the shape the net's
 * runs have.** `ENTROPY_RE`'s lookarounds list the dot, so a run stops at the
 * first dotted segment -- and where a path's first dotted segment is a directory
 * (`.local`, `.config`, `.ssh`), the run the net takes ends in `/`:
 * `.../fugaro-cli-stub-<suffix>/home/` for the header's own path, which is the run
 * the owner's `[redacted-token]` replaced. Without `/?` that run is not a path to
 * the path rule's pattern, and the guard would not save the very value it exists
 * for.
 *
 * **What that keeps.** The words are asked, not the segment, so a *token written
 * inside a path* still goes -- a word of `/home/x/<32-character key>/key` is a
 * candidate by itself, so the run is not a path and the whole run is redacted
 * exactly as before, which is the invariant `split-secrets.test.ts` pins for the
 * refusal door. And because the second question is `isHighEntropy`'s, no second
 * threshold can drift from `ENTROPY_RE`'s.
 *
 * **The trade, said out loud.** A blob *spelled with separators* -- a base64url
 * body with a `-` or `_` in it, written into a path -- reads as a name here, and
 * every door draws it as written. Everything the shape rules find is still
 * replaced, including every token family, because every shape rule runs
 * **before** the net: that is what `sanitize-path-runs.test.ts` holds, both
 * halves of it.
 *
 * The recorder (`src/source/redact.ts`) carries no such guard and needs none: it
 * runs the path rule in every reading, so a rooted run never reaches its net. The
 * two layers therefore part company over a path run -- the recorder draws
 * `<path>`, this layer draws the path -- which is the one-way direction
 * `sanitize-agreement.test.ts` holds.
 */
const PATH_RUN_RE = new RegExp(`^(?:${ABS_PATH_SOURCE})/?$`, 'u')

/** ROOTED_RUN_RE is the weaker question `isPathShapedSecret` asks: rooted at `/`
 * with a second slash behind it, whatever stands between them -- including
 * nothing, which is why it is not `PATH_RUN_RE`. That rule has to stay on the
 * recorder's side of the line, whose other half of the same test is "the run does
 * not start with `/`"; the two agree wherever this one is asked, because the rule
 * reaches it only for a run holding two slashes or more. */
const ROOTED_RUN_RE = /^\/[^/]*\//

/** isRootedRun answers `ROOTED_RUN_RE`'s question -- see it for why it is the
 * weaker one. */
function isRootedRun(run: string): boolean {
  return ROOTED_RUN_RE.test(run)
}

/** PATH_WORD_RE reads a run into **the words a name is made of**, which is how a
 * path segment is spelled: words run together by `/`, `-` and `_`. It splits
 * nothing else -- `+` and `=` are token-alphabet characters and not separators a
 * name is spelled with, and `.` cannot appear inside a run at all, since
 * `ENTROPY_RE`'s lookarounds list it. None of the three is a boundary to
 * `ENTROPY_RE`: they are run characters, which is the whole of the defect
 * `PATH_RUN_RE` describes. So this is the reading the net does *not* do, and the
 * one that tells a path from a key.
 */
const PATH_WORD_RE = /[^/_-]+/g

/** isPathRun answers whether a candidate run is a path rather than a token: the
 * absolute-path pattern matches it whole (`PATH_RUN_RE`), and no word of it is a
 * candidate on its own. It is the guard the net reads, so that "a path is not a
 * token" is one decision in one place. */
function isPathRun(run: string): boolean {
  if (!PATH_RUN_RE.test(run)) return false
  for (const word of run.match(PATH_WORD_RE) ?? []) if (isHighEntropy(word)) return false
  return true
}

// The free-text rules that found a repository path (`REPO_TOKEN_RE`), a GCP
// project or bucket named in a sentence (`GCP_CONTEXT_RE`, `RUNS_BUCKET_RE`), a
// network address (`IPV4_RE`, `IPV6_RE`) or a dotted name a suffix table called a
// host (`HOST_RE` with `isHostname`) are gone: D3213 (2026-11-21) removed every
// non-secret mask, so the console draws each of those as it came. The *classes*
// are untouched -- `repository`, `repoPattern`, `scope`, `imageRef`, `project`,
// `host` and `path` each still hold the value's own grammar or bound, and the
// markers the recorder writes for those shapes (`PATH_PLACEHOLDER`,
// `PROJECT_PLACEHOLDER`, `BUCKET_PLACEHOLDER`, `HOST_PLACEHOLDER`,
// `LITERAL_PLACEHOLDER`) are still recognised by `isMarkerText` below.

// The sentinels hold a pseudonym aside while the rest of the pass runs. They are
// private-use code points, which no *screen* has a use for, and which
// `INVISIBLE_CLASS` removes with every other private-use code point -- so a
// pseudonym cannot be eaten by the net, and a value that reaches the hold cannot
// carry one of its own: the strip above has already taken them out.
//
// Review round 5 of PR 478 corrected the claim that used to stand here. It said
// the text "cannot contain" the sentinel, and that is false: a private-use code
// point is a code point like any other, and a document may carry one. A value
// that carried `U+E000` `0` `U+E001` of its own would have had the first restore
// land on the document's own characters, leaving a real sentinel in the drawn
// value and moving the pseudonym into text the document chose. No value leaked,
// but the drawn text was not the text the pass decided. So `SENTINEL_RE` takes
// the two code points out of every value before the sentinels go in: the value
// cannot contain one afterwards, which is what makes the restore exact and the
// first-occurrence `replace` below correct. The cost is that a value loses two
// characters nobody can read.
const SENTINEL_START = '\ue000'
const SENTINEL_END = '\ue001'
const SENTINEL_RE = /[\ue000\ue001]/g

/** PERCENT_RUN_RE is a run of the characters a percent-encoded value is made of:
 * one character class, scanned once. A run with an escape in it
 * (`PERCENT_ESCAPE_RE`) is decoded before any rule looks at it. */
const PERCENT_RUN_RE = /[A-Za-z0-9._~+=%-]+/g
const PERCENT_ESCAPE_RE = /%[0-9A-Fa-f]{2}/

/** UTF8_ENCODER and UTF8_DECODER are the Web standards for turning a code point
 * into the bytes a percent escape encodes, and back. They are globals rather
 * than imports: `shared/` may import nothing at run time, and the renderer has
 * no `Buffer` -- which is what the recorder's own decoder uses. */
const UTF8_ENCODER = new TextEncoder()
const UTF8_DECODER = new TextDecoder('utf-8')

/**
 * percentDecode decodes every valid escape in a run, leniently (an invalid
 * escape stays as written), and again while escapes remain -- up to three times,
 * so `%2540` is an `@`. It is the recorder's decoder, and it is why a token
 * whose `p` was written `%70` is the token it spells.
 */
function percentDecode(run: string): string {
  let text = run
  for (let round = 0; round < 3 && PERCENT_ESCAPE_RE.test(text); round += 1) {
    const bytes: number[] = []
    for (let i = 0; i < text.length; i += 1) {
      const hex = text.slice(i + 1, i + 3)
      if (text[i] === '%' && /^[0-9A-Fa-f]{2}$/.test(hex)) {
        bytes.push(Number.parseInt(hex, 16))
        i += 2
      } else {
        const point = text.codePointAt(i) ?? 0
        const character = String.fromCodePoint(point)
        for (const byte of UTF8_ENCODER.encode(character)) bytes.push(byte)
        i += character.length - 1
      }
    }
    text = UTF8_DECODER.decode(Uint8Array.from(bytes))
  }
  return text
}

/** WRAPPED_BASE64_RE is a run that could be base64 (standard or URL-safe). */
const WRAPPED_BASE64_RE = /(?<![A-Za-z0-9+/_-])[A-Za-z0-9+/_-]{16,}={0,2}(?![A-Za-z0-9+/_=-])/g

/** decodeBase64 returns printable text a base64 run decodes to, or null. It is
 * the recorder's rule with a Web decoder: a run whose text is not mostly
 * printable is not text, and a run that does not decode is not a candidate. */
function decodeBase64(run: string): string | null {
  // Read the way the recorder's `Buffer.from(run, 'base64')` reads it: the
  // padding is dropped, and a run whose length is 1 mod 4 loses its last
  // character (which carries fewer than eight bits) instead of being refused
  // by `atob` -- review round 2 of PR 494 found the two layers disagreeing on
  // exactly that run, which `WRAPPED_BASE64_RE` makes common by absorbing the
  // first letter of the next word.
  let standard = run.replace(/-/g, '+').replace(/_/g, '/').replace(/=+$/, '')
  if (standard.length % 4 === 1) standard = standard.slice(0, -1)
  const padded = standard + '='.repeat((4 - (standard.length % 4)) % 4)
  let decoded: string
  try {
    const latin1 = atob(padded)
    const bytes = Uint8Array.from(latin1, (character) => character.charCodeAt(0))
    decoded = UTF8_DECODER.decode(bytes)
  } catch {
    return null
  }
  if (decoded.length < 8) return null
  const printable = decoded.replace(/[^\x20-\x7e]/g, '').length
  return printable / decoded.length >= 0.9 ? decoded : null
}

/** hits is `RegExp.test` for a shared, global pattern, re-anchored first. */
function hits(pattern: RegExp, text: string): boolean {
  pattern.lastIndex = 0
  return pattern.test(text)
}

/** containsSecret answers whether decoded text is credential-shaped. */
function containsSecret(text: string): boolean {
  if (TOKEN_SHAPES.some((pattern) => hits(pattern, text))) return true
  if (hits(BEARER_RE, text) || hits(PEM_RE, text)) return true
  CREDENTIAL_ASSIGNMENT_RE.lastIndex = 0
  for (const match of text.matchAll(CREDENTIAL_ASSIGNMENT_RE)) {
    if (!isMarkerText(match[3] ?? '')) return true
  }
  return false
}

/** holdsCredential answers whether a whole value a *validating* class is about
 * to accept is credential-shaped: a token family's own shape, a bearer header,
 * a PEM header, an assignment, or a run the entropy rule calls a secret -- which
 * is the pair `containsSecret` and `isHighEntropy`, and the same pair the URL
 * class asks of a form it is about to link (`applyUrl`). The classes that
 * *redact* rather than validate run the whole rule list and would replace such a
 * run; a class that draws its value whole or not at all has to ask this instead,
 * because a placeholder in a login cell is a login that never existed. */
function holdsCredential(text: string): boolean {
  if (containsSecret(text)) return true
  ENTROPY_RE.lastIndex = 0
  for (const match of text.matchAll(ENTROPY_RE)) {
    if (isHighEntropy(match[0])) return true
  }
  return false
}

/** Changed is what one run of the rules did: the text, and which classes fired
 * on it. */
interface Changed {
  readonly text: string
  readonly flags: readonly SanitizeFlag[]
}

/** OneRule is one pattern class: the flag it raises, and what it does to a
 * string. A rule leaves a string with nothing of its class in it unchanged,
 * which is the property the tests walk. */
interface OneRule {
  readonly flag: SanitizeFlag
  readonly apply: (text: string, nested: boolean) => string
}

/**
 * FREE_TEXT_RULES are the credential classes a job's own prose goes through, in
 * the recorder's order -- and the order is the point: a URL first (it contains
 * everything else), then the credential pass, then the path-shaped secret and the
 * entropy net last, over what the shapes left.
 *
 * **Until D3213 (2026-11-21) this was the recorder's whole rule list.** It also
 * held an address rule, a branch rule, a path rule, a repository-path rule, a
 * GCP-name rule, a network-address rule and a host rule, and every one of them is
 * gone: the console is the operator's own tool, so it draws an address, a branch,
 * a path, a repository path, a project, a bucket, a host and an address literal as
 * they came. What is left is every rule that masks a **secret**, which is the whole
 * of what D3213 keeps. The recorder (`src/source/redact.ts`) still runs all of
 * them, so the two layers part company on those shapes -- in the one direction
 * that is allowed: this layer draws more than the recorder and never leaves a
 * credential the recorder would have taken (`sanitize-agreement.test.ts`).
 *
 * Three things this layer still cannot reproduce, and does not pretend to:
 *
 *   - **the people pass.** The recorder replaces a requester's name everywhere
 *     the document spells it, from a table of the document's own people; this
 *     layer sees one string at a time and has no such table. A name is a word
 *     with no shape, and no pattern can find it. (`schema.ts` gives every
 *     person-shaped key the `person` class, which is where that value is
 *     handled.)
 *   - **the installation's literals.** The recorder holds the operator's own
 *     project name, paths and hosts, folded; the renderer is not given them, and
 *     a literal list in the renderer would be a list of the very strings the
 *     redaction exists to remove.
 *   - **the net's guard on paths.** The net here declines a run the absolute-path
 *     pattern matches whole and that holds no word a candidate on its own
 *     (`isPathRun`, and `PATH_RUN_RE` for why the question is that pattern, asked
 *     whole). The recorder carries no such guard and needs none: it runs the path
 *     rule in every reading, so a run that is a path never reaches its net. Here
 *     the guard is the whole of what keeps the operator's own binary path from
 *     being drawn as a credential -- the owner's report of 2026-10-21, when the
 *     net marked it.
 *
 * A percent-decoded run is the one recursive case: the rules that find nothing
 * in the run's own text find something in what it decodes to, and the run is
 * replaced by the decoded text, redacted -- so `u=<percent-encoded token>` loses
 * the token, and `u=ana%40acme.com` keeps its address, because no rule takes one
 * any more. `nested` is what stops that from recurring.
 */
const FREE_TEXT_RULES: readonly OneRule[] = Object.freeze([
  {
    flag: 'replaced-url',
    apply: (text) =>
      text.replace(URL_RE, (url) => (urlCarriesCredential(url) ? URL_PLACEHOLDER : url)),
  },
  { flag: 'neutralised-credential', apply: (text, nested) => credentialPass(text, nested) },
  {
    flag: 'neutralised-credential',
    apply: (text) => text.replace(ENTROPY_RE, (run) => (isPathShapedSecret(run) ? TOKEN_PLACEHOLDER : run)),
  },
  {
    flag: 'neutralised-credential',
    apply: (text) =>
      text.replace(ENTROPY_RE, (run) =>
        isHighEntropy(run) && !isPathRun(run) ? TOKEN_PLACEHOLDER : run,
      ),
  },
])

/**
 * isPathShapedSecret is a high-entropy run that holds two or more slashes and is
 * not rooted (`isRootedRun`, the weaker question `ROOTED_RUN_RE` asks). One slash
 * is a repository path and is the `repository` class's. It is the recorder's own
 * rule in the recorder's own place, and this layer runs the recorder's credential
 * list, so it stays.
 *
 * It was written beside the free-text path rule, which D3213 (2026-11-21)
 * removed, and the weaker test is the trace of it: a run rooted at `/` was left
 * for the path rule to redact whole, so that a blob inside a path was drawn as the
 * path it was (`<path>`) rather than as `[redacted-token]`. With the path rule gone
 * the net's guard (`isPathRun`) is the whole of that decision, and the two rules
 * differ in one corner only: a `~/` run whose words are each under the net's floor
 * but whose whole is high-entropy -- a `~/` root is not what `ROOTED_RUN_RE` calls
 * rooted, and the net's pattern does match it.
 */
function isPathShapedSecret(run: string): boolean {
  return !isRootedRun(run) && (run.match(/\//g) ?? []).length >= 2 && isHighEntropy(run)
}

/**
 * CREDENTIAL_RULES are the first two of them, and they are the whole pass a
 * value whose *key* declares what it is goes through. Running the whole list over
 * such a value would be wrong in both directions: the two entropy rules answer
 * "is this run a secret" about a string with no key in front of it, so a
 * validating class would rewrite or refuse a long random-looking name the
 * recorder drew, and disagree with the fixture about a document it wrote. The
 * recorder makes the same split -- a field class short-circuits, and the pattern
 * classes are for free text -- and the credential rules stay on for the validating
 * classes because a token is a shape a validating pattern can accept: a Google
 * access token, dots and all, is a legal git branch name, and so is a forge token
 * with no underscore in it. A class that draws its own value whole asks
 * `holdsCredential` instead of the two entropy rules (`person`, `branch`).
 */
const CREDENTIAL_RULES: readonly OneRule[] = Object.freeze(FREE_TEXT_RULES.slice(0, 2))

/**
 * REFUSAL_RULES are every *redacting* rule of the free-text path, and nothing
 * else: the URL rule, the credential pass, the path-shaped secret and the entropy
 * net. They are what `sanitizeRefusal` runs, and a value the text path would
 * redact is redacted here too (`sanitize-agreement.test.ts` pins the two equal
 * over the credential corpus).
 *
 * Every rule of the free-text path is a credential rule since D3213 (2026-11-21),
 * so this filter selects the whole of that list. It is kept as a filter rather
 * than an alias so that the door's rule set is stated rather than inherited: the
 * non-credential classes that used to rewrite a remedy here (a role, a project, a
 * path, a host) are gone, and a non-credential rule added to the free-text list
 * must not reach the door by being added to it.
 */
const REFUSAL_RULES: readonly OneRule[] = Object.freeze(
  FREE_TEXT_RULES.filter(
    (rule) => rule.flag === 'neutralised-credential' || rule.flag === 'replaced-url',
  ),
)

/**
 * credentialPass is the recorder's "something credential-shaped" rule: a
 * percent-encoded run is decoded first, then a base64-wrapped candidate, then
 * the token families, then an assignment, which keeps its name and loses its
 * value.
 */
function credentialPass(text: string, nested: boolean): string {
  let out = text.replace(PERCENT_RUN_RE, (run) => {
    if (!PERCENT_ESCAPE_RE.test(run)) return run
    const decoded = percentDecode(run)
    if (containsSecret(decoded) || isHighEntropy(decoded)) return TOKEN_PLACEHOLDER
    if (nested) return run
    const redacted = runRules(prepare(decoded), FREE_TEXT_RULES, true).text
    return redacted === prepare(decoded) ? run : redacted
  })
  out = out.replace(WRAPPED_BASE64_RE, (run) => {
    const decoded = decodeBase64(run)
    return decoded !== null && containsSecret(decoded) ? TOKEN_PLACEHOLDER : run
  })
  for (const pattern of TOKEN_SHAPES) out = out.replace(pattern, TOKEN_PLACEHOLDER)
  out = out.replace(BEARER_RE, (_match, lead: string) => `${lead} ${TOKEN_PLACEHOLDER}`)
  out = out.replace(PEM_RE, TOKEN_PLACEHOLDER)
  out = out.replace(
    CREDENTIAL_ASSIGNMENT_RE,
    (whole, name: string, separator: string, value: string) =>
      isMarkerText(value) ? whole : `${name}${separator}${TOKEN_PLACEHOLDER}`,
  )
  return out
}

/**
 * runRules runs one list of rules in order and reports what fired. The
 * pseudonyms are held aside for the duration and put back at the end, so the
 * entropy net cannot eat a redaction the recorder already made. The value's own
 * sentinel code points are removed first, which is what makes the hold exact --
 * see `SENTINEL_RE`.
 */
function runRules(text: string, rules: readonly OneRule[], nested: boolean): Changed {
  const held: string[] = []
  let working = text.replace(SENTINEL_RE, '').replace(ANY_PSEUDONYM_RE, (found) => {
    held.push(found)
    return `${SENTINEL_START}${String(held.length - 1)}${SENTINEL_END}`
  })
  const flags: SanitizeFlag[] = []
  for (const rule of rules) {
    const next = rule.apply(working, nested)
    if (next !== working && !flags.includes(rule.flag)) flags.push(rule.flag)
    working = next
  }
  for (const [index, pseudonym] of held.entries()) {
    working = working.replace(`${SENTINEL_START}${String(index)}${SENTINEL_END}`, pseudonym)
  }
  return { text: working, flags }
}

// ---------------------------------------------------------------------------
// Ambiguous escape sequences: every reading, and the union of what they find.
// ---------------------------------------------------------------------------

/** COVER_TOKEN and COVER_URL are what a tracked rule found at a position of the
 * prepared form: a credential, or a URL. */
const COVER_TOKEN = 1
const COVER_URL = 2

/** TrackedEdit is what a tracked rule does to one match: keep its first `keep`
 * characters, replace the rest with `replacement`, and mark the replaced
 * characters `kind` (0 for a rewrite that is not a credential: the nested
 * pass over a percent-decoded run). */
interface TrackedEdit {
  readonly keep: number
  readonly replacement: string
  readonly kind: number
}

interface TrackedRule {
  readonly pattern: RegExp
  readonly decide: (match: RegExpExecArray, nested: boolean) => TrackedEdit | null
}

const TOKEN_EDIT: TrackedEdit = { keep: 0, replacement: TOKEN_PLACEHOLDER, kind: COVER_TOKEN }

/**
 * TRACKED_RULES are `REFUSAL_RULES` -- every credential redaction the free-text
 * path makes, and the URL rule -- written as edits on matches rather than as
 * string replacements, so that what each one replaced can be traced back to the
 * characters of the reading it read. They are the same patterns in the same
 * order, and `sanitize-ambiguity.test.ts` holds the text they produce equal to
 * `REFUSAL_RULES`' over a corpus.
 */
const TRACKED_RULES: readonly TrackedRule[] = Object.freeze<TrackedRule[]>([
  {
    pattern: URL_RE,
    decide: (match) =>
      urlCarriesCredential(match[0])
        ? { keep: 0, replacement: URL_PLACEHOLDER, kind: COVER_URL }
        : null,
  },
  {
    pattern: PERCENT_RUN_RE,
    decide: (match, nested) => {
      const run = match[0]
      if (!PERCENT_ESCAPE_RE.test(run)) return null
      const decoded = percentDecode(run)
      if (containsSecret(decoded) || isHighEntropy(decoded)) return TOKEN_EDIT
      if (nested) return null
      const redacted = runRules(prepare(decoded), FREE_TEXT_RULES, true).text
      return redacted === prepare(decoded) ? null : { keep: 0, replacement: redacted, kind: 0 }
    },
  },
  {
    pattern: WRAPPED_BASE64_RE,
    decide: (match) => {
      const decoded = decodeBase64(match[0])
      return decoded !== null && containsSecret(decoded) ? TOKEN_EDIT : null
    },
  },
  ...TOKEN_SHAPES.map((pattern) => ({ pattern, decide: () => TOKEN_EDIT })),
  {
    pattern: BEARER_RE,
    decide: (match) => ({ keep: (match[1] ?? '').length, replacement: ` ${TOKEN_PLACEHOLDER}`, kind: COVER_TOKEN }),
  },
  { pattern: PEM_RE, decide: () => TOKEN_EDIT },
  {
    pattern: CREDENTIAL_ASSIGNMENT_RE,
    decide: (match) =>
      isMarkerText(match[3] ?? '')
        ? null
        : { keep: (match[1] ?? '').length + (match[2] ?? '').length, replacement: TOKEN_PLACEHOLDER, kind: COVER_TOKEN },
  },
  { pattern: ENTROPY_RE, decide: (match) => (isPathShapedSecret(match[0]) ? TOKEN_EDIT : null) },
  {
    pattern: ENTROPY_RE,
    decide: (match) => (isHighEntropy(match[0]) && !isPathRun(match[0]) ? TOKEN_EDIT : null),
  },
])

/** Traced is one reading as the tracked rules rewrite it: its text, and for
 * every character the position in the prepared form it came from (-1 for a
 * character no position holds: a kept byte of a sequence, or a marker). */
interface Traced {
  readonly text: string
  readonly origin: Int32Array
}

/** applyTracked runs one tracked rule over a reading, marking in `covered`
 * every prepared position a replaced character came from. */
function applyTracked(state: Traced, rule: TrackedRule, nested: boolean, covered: Uint8Array): Traced {
  rule.pattern.lastIndex = 0
  const edits: { readonly index: number; readonly length: number; readonly edit: TrackedEdit }[] = []
  let length = state.text.length
  for (const match of state.text.matchAll(rule.pattern)) {
    const edit = rule.decide(match, nested)
    if (edit === null) continue
    edits.push({ index: match.index, length: match[0].length, edit })
    length += edit.replacement.length - (match[0].length - edit.keep)
  }
  rule.pattern.lastIndex = 0
  if (edits.length === 0) return state
  const parts: string[] = []
  const origin = new Int32Array(length)
  let from = 0
  let out = 0
  for (const { index, length: matched, edit } of edits) {
    const kept = index + edit.keep
    parts.push(state.text.slice(from, kept))
    origin.set(state.origin.subarray(from, kept), out)
    out += kept - from
    if (edit.kind !== 0) {
      for (let at = kept; at < index + matched; at += 1) {
        const position = state.origin[at] ?? -1
        if (position >= 0 && (covered[position] === 0 || edit.kind === COVER_TOKEN)) covered[position] = edit.kind
      }
    }
    parts.push(edit.replacement)
    origin.fill(-1, out, out + edit.replacement.length)
    out += edit.replacement.length
    from = index + matched
  }
  parts.push(state.text.slice(from))
  origin.set(state.origin.subarray(from), out)
  return { text: parts.join(''), origin }
}

/**
 * scanWork counts the work every reading costs, in characters scanned: each
 * `traceCredentials` call adds the length of the reading it runs the tracked
 * rules over, so the total is the quantity `MAX_AMBIGUOUS_SCAN` bounds --
 * readings times characters. `splitScanWork` is the part of that total the
 * split readings cost (`traceSplits`), which `MAX_SPLIT_SCAN` bounds through
 * the budget it sets: they are where a pathological input could cost more than
 * linear work, one reading per combination of split places per run, so they
 * are counted apart from the rest. Both are what `split-secrets.test.ts`
 * asserts against instead of a wall clock: a wall clock measures the machine
 * (a loaded runner turned a 45 ms pass into the 435 ms that failed the 0.9.6
 * and 0.9.7 release gates), where these count the work the code chose to do,
 * the same on every machine and every run.
 *
 * Nothing reads them at runtime -- the renderer loads this module and never
 * calls these -- and the one write per reading cannot change what any layer
 * draws.
 */
let scanWork = 0
let splitScanWork = 0

/** resetScanWork zeroes the counters (`scanWork`, `splitScanWork`), so a test
 * measures one call's own work. */
export function resetScanWork(): void {
  scanWork = 0
  splitScanWork = 0
}

/** scanWorkDone is the characters every reading has scanned since the last
 * `resetScanWork` (see `scanWork`). */
export function scanWorkDone(): number {
  return scanWork
}

/** splitScanWorkDone is the characters the split readings have scanned since
 * the last `resetScanWork`, a part of `scanWorkDone` (see `splitScanWork`). */
export function splitScanWorkDone(): number {
  return splitScanWork
}

/** traceCredentials runs every tracked rule over one reading, in order. */
function traceCredentials(
  reading: Traced,
  nested: boolean,
  covered: Uint8Array,
  rules: readonly TrackedRule[] = TRACKED_RULES,
): Traced {
  scanWork += reading.text.length
  let state = reading
  for (const rule of rules) state = applyTracked(state, rule, nested, covered)
  return state
}

/** tracedRefusalRules is `traceCredentials` over a plain string, for the test
 * that holds the tracked rules equal to `REFUSAL_RULES`. */
export function tracedRefusalRules(text: string): string {
  const origin = new Int32Array(text.length).map((_, index) => index)
  return traceCredentials({ text, origin }, false, new Uint8Array(text.length)).text
}

/**
 * MAX_AMBIGUOUS_COMBINATIONS bounds how many ways the sites of one cluster are
 * read (a cluster is the sites that sit in one run of text with no whitespace
 * between them, where a byte kept at one can join or split a token at
 * another). Each site is read two or three ways -- dropped, its final byte
 * kept, its whole body kept -- and every combination within a cluster is
 * read. A cluster with more combinations than this is not read at all: the
 * value is refused (**fail closed**), because a token split across that many
 * sequences is a token no bounded reading is guaranteed to find.
 */
export const MAX_AMBIGUOUS_COMBINATIONS = 64

/**
 * MAX_AMBIGUOUS_SCAN is the bound on the work: readings times characters. The
 * readings are full-length strings, so the cost is the number of readings
 * times the length, whatever the number of sites -- 10,000 sites in separate
 * words cost four readings -- and a value whose readings would cost more than
 * this is refused rather than read slowly.
 */
export const MAX_AMBIGUOUS_SCAN = 1 << 20

/** SPLIT_SIDE_RE is a character a credential can be made of: a split place
 * matters only between two of them (a boundary between two punctuation marks or
 * inside an emoji sequence changes no token shape's reading). */
const SPLIT_SIDE_RE = /[\p{L}\p{N}_\-+/=.]/u

/** SPLIT_MARK is what a split place read as a boundary holds: a character no
 * token shape contains and every lookbehind and lookahead treats as a
 * boundary, and not whitespace, so no rule that needs a space is handed one. */
const SPLIT_MARK = '\u200b'

/** MAX_SPLIT_EXHAUSTIVE is how many split places one whitespace-free run may
 * hold and still be read in every combination (2^6 = 64 readings, the same
 * bound as `MAX_AMBIGUOUS_COMBINATIONS`); a run with more is read with every
 * single place and every pair of places as boundaries, up to
 * `MAX_SPLIT_PLACES`. A run with more than that is masked whole: fail closed,
 * locally. */
const MAX_SPLIT_EXHAUSTIVE = 6
export const MAX_SPLIT_PLACES = 24

/** MAX_SPLIT_SCAN bounds the split readings' work, readings times characters,
 * at a quarter of `MAX_AMBIGUOUS_SCAN`: a value of a few KiB is read in every
 * combination, and in a 64 KiB value a run needing more than four readings is
 * masked whole rather than read (the price of a bounded worst case). */
export const MAX_SPLIT_SCAN = 1 << 18

/**
 * traceSplits reads every split place of `prepared` as a boundary or not, in
 * the combinations `MAX_SPLIT_EXHAUSTIVE` allows, marking what the tracked
 * rules match into `covered`. Runs are read side by side, as the escape-sequence
 * resolver reads its clusters: reading `j` takes combination `j` of every run,
 * which is exact for every rule that cannot cross whitespace. A run whose
 * combinations would make the readings cost more than `MAX_SPLIT_SCAN` is
 * masked whole instead of read.
 */
function traceSplits(
  prepared: string,
  places: readonly number[],
  covered: Uint8Array,
  rules: readonly TrackedRule[],
): void {
  interface Run {
    readonly start: number
    end: number
    readonly places: number[]
  }
  const runs: Run[] = []
  let previous = -1
  for (const place of places) {
    if (place === previous || place <= 0 || place >= prepared.length) continue
    previous = place
    if (!SPLIT_SIDE_RE.test(prepared[place - 1] ?? '') || !SPLIT_SIDE_RE.test(prepared[place] ?? '')) continue
    const last = runs[runs.length - 1]
    if (last !== undefined && place < last.end) {
      last.places.push(place)
      continue
    }
    let start = place
    while (start > 0 && !/\s/.test(prepared[start - 1] ?? '')) start -= 1
    let end = place
    while (end < prepared.length && !/\s/.test(prepared[end] ?? '')) end += 1
    runs.push({ start, end, places: [place] })
  }
  if (runs.length === 0) return
  const maskWhole = (run: Run): void => {
    covered.fill(COVER_TOKEN, run.start, run.end)
  }
  const budget = Math.max(1, Math.floor(MAX_SPLIT_SCAN / Math.max(1, prepared.length)))
  const runsRead: Run[] = []
  const combosRead: number[][][] = []
  for (const run of runs) {
    const m = run.places.length
    // The count first, so a run that will be masked costs nothing to list.
    const count = m <= MAX_SPLIT_EXHAUSTIVE ? (1 << m) - 1 : (m * (m + 1)) / 2 + 1
    if (m > MAX_SPLIT_PLACES || count > budget) {
      maskWhole(run)
      continue
    }
    const chosen: number[][] = []
    if (m <= MAX_SPLIT_EXHAUSTIVE) {
      for (let mask = 1; mask < 1 << m; mask += 1) chosen.push(run.places.filter((_, index) => (mask & (1 << index)) !== 0))
    } else {
      for (let i = 0; i < m; i += 1) {
        chosen.push([run.places[i] ?? 0])
        for (let k = i + 1; k < m; k += 1) chosen.push([run.places[i] ?? 0, run.places[k] ?? 0])
      }
      chosen.push([...run.places])
    }
    runsRead.push(run)
    combosRead.push(chosen)
  }
  const readings = combosRead.reduce((most, chosen) => Math.max(most, chosen.length), 0)
  for (let j = 0; j < readings; j += 1) {
    const marks: number[] = []
    for (const chosen of combosRead) marks.push(...(chosen[j % chosen.length] ?? []))
    const parts: string[] = []
    const origin = new Int32Array(prepared.length + marks.length)
    let from = 0
    let out = 0
    for (const mark of marks) {
      parts.push(prepared.slice(from, mark), SPLIT_MARK)
      for (let index = from; index < mark; index += 1) origin[out++] = index
      origin[out++] = -1
      from = mark
    }
    parts.push(prepared.slice(from))
    for (let index = from; index < prepared.length; index += 1) origin[out++] = index
    const text = parts.join('')
    splitScanWork += text.length
    traceCredentials({ text, origin }, false, covered, rules)
  }
}

/** Ambiguity is what `resolveAmbiguousSequences` hands the three layers: the
 * prepared form with every credential any reading found replaced, and whether
 * anything was. */
export interface Ambiguity {
  readonly text: string
  readonly masked: boolean
  /** sequences says whether an escape sequence swallowed printable bytes, as
   * opposed to the form as written alone holding a credential
   * (`resolveReadings`). */
  readonly sequences: boolean
  /** credential says whether any reading matched a credential (as opposed to
   * only a URL), which is what the url class refuses on. */
  readonly credential: boolean
}

/**
 * resolveAmbiguousSequences is the one rule the three readers of CLI text --
 * the sentence class, the refusal door and the recorder -- apply to a value
 * whose escape sequences swallowed printable bytes (`AmbiguousSite`), and it
 * replaces review round 3's "pick one reading" (review round 4 of PR 494: with
 * two keys and two readings that each found one, the pick drew the other).
 *
 * **The rule is the union, per sequence.** Each site is read dropped (as a
 * terminal draws it), with its final byte kept, and with its whole body kept;
 * the tracked credential rules (`TRACKED_RULES`) run over every combination of
 * the readings of the sites in one cluster, all clusters at once; and a
 * character of the terminal's reading is redacted if **any** reading's rules
 * matched it. So `ESC [ K` + a token + ` ` + `ESC [` + a key draws neither: the
 * first is found with the `K` dropped, the second with the key's first letter
 * kept, and each reading's find is mapped back to the characters a terminal
 * draws. Reading each sequence on its own is what keeps a real one-letter
 * command (`ESC [ K`) from gluing itself to the token after it.
 *
 * Clusters are read side by side (reading `j` takes combination `j` of every
 * cluster), which is exact for every rule that cannot cross whitespace -- the
 * token families, the entropy net, a URL, a base64 or percent-encoded run. The
 * three that can (a PEM block, a bearer header, an assignment) see each
 * cluster's combinations aligned by index, plus every sequence dropped, every
 * final byte kept and every body kept, all at once; a credential that needs
 * two clusters read two different ways at once is the bound's stated limit.
 *
 * Returns `undefined` when no sequence swallowed anything (the caller's own
 * prepared form is the reading), `null` when the readings would cost more than
 * the bounds allow (the caller refuses the value: fail closed), and otherwise
 * the prepared form, masked. The masked text is NFKC, sequence-free and free of
 * every invisible character either layer strips.
 */
export function resolveAmbiguousSequences(raw: string): Ambiguity | null | undefined {
  return resolve(raw, false)
}

/**
 * resolveReadings is the combining step both free-text readers call -- the
 * display layer's `withRules` and the recorder's `redactFreeText`
 * (`src/source/redact.ts`) -- so that the two cannot combine their readings two
 * different ways (review round 2 of PR 540).
 *
 * It is `resolveAmbiguousSequences` with **more readings in the union**, and
 * the union is taken over covered *characters* before anything is masked (review
 * round 3 of PR 540: masking what one reading found and re-scanning the cut
 * text left the head of a token no rule matched). The readings:
 *
 *  - the prepared form itself (NFKC, every sequence and invisible character
 *    gone), so its own credentials are in the union;
 *  - every **split place** -- where removing an invisible run or a terminal
 *    sequence made two token characters touch -- read as a boundary or not, in
 *    every combination per whitespace-free run (`traceSplits`). The prepared
 *    form has lost those characters, and with them the boundary a token
 *    shape's lookbehind (`(?<![\p{L}\p{N}_])`) needs: `J` + a zero-width space
 *    + a key id is a key id only with that place read as a boundary, and a key
 *    id that also splits itself (`J`, ZWSP, `AKIA..`, ZWSP, `..`) needs one
 *    place read each way. An OSC 8 hyperlink or an SGR between `J` and the key
 *    is the same case. The form as written is the every-place-a-boundary
 *    combination.
 *
 * Both layers used to read the form as written only when the prepared one
 * matched nothing (review round 2), so a path, an address or another token
 * anywhere in the value turned that reading off.
 *
 * Returns `undefined` when nothing was removed or no reading found anything (the
 * caller's ordinary path), `null` when the readings exceed the bounds (fail closed), and
 * otherwise the prepared form, masked.
 */
export function resolveReadings(raw: string): Ambiguity | null | undefined {
  return resolve(raw, true)
}

/** CREDENTIAL_TRACKED_RULES are `TRACKED_RULES` without the URL rule: the
 * url class asks whether a credential sits *inside* a link, which the URL rule,
 * run first, would hide by replacing the whole link. */
const CREDENTIAL_TRACKED_RULES: readonly TrackedRule[] = Object.freeze(TRACKED_RULES.slice(1))

/** resolveCredentialReadings is `resolveReadings` asking only about
 * credentials: what the url class reads. */
export function resolveCredentialReadings(raw: string): Ambiguity | null | undefined {
  return resolve(raw, true, CREDENTIAL_TRACKED_RULES)
}

/** resolve is the union over readings, with or without the form as written
 * (`resolveReadings`). */
function resolve(
  raw: string,
  withWritten: boolean,
  rules: readonly TrackedRule[] = TRACKED_RULES,
): Ambiguity | null | undefined {
  const hasSequence = SEQUENCE_FAST_RE.test(raw)
  if (!hasSequence && !withWritten) return undefined
  const sites: AmbiguousSite[] = []
  const cuts: number[] = []
  const stripped = hasSequence ? scanTerminalSequences(raw.normalize('NFKC'), sites, withWritten ? cuts : null) : raw.normalize('NFKC')
  // The prepared form, with each site's place carried through the strip.
  const removed: number[] = []
  const prepared = stripped.replace(PREPARE_STRIP_RE, (found: string, offset: number) => {
    removed.push(offset, found.length)
    return ''
  })
  if (sites.length === 0 && (!withWritten || (removed.length === 0 && cuts.length === 0))) return undefined
  const covered = new Uint8Array(prepared.length)
  if (withWritten) {
    // More readings, and their union -- never one reading masked and the cut
    // text re-scanned (review round 3 of PR 540: a token whose tail the written
    // form found was masked and the head it left behind no longer matched any
    // rule):
    //
    //   1. the prepared form itself, every invisible character and sequence
    //      gone, so its own credentials are in the union;
    //   2. every place where a removal made two characters touch (an invisible
    //      run, a sequence) read as a boundary or not, in every combination per
    //      whitespace-free run (`traceSplits`): a key id split twice, once to
    //      borrow a boundary and once to split itself, needs one place read
    //      each way. The form as written is one of those combinations (every
    //      place a boundary), so it is not read separately.
    const splitPlaces: number[] = []
    let at = 0
    let next = 0
    let r = 0
    let c = 0
    while (at <= stripped.length) {
      while (c < cuts.length && (cuts[c] ?? 0) <= at) {
        splitPlaces.push(next)
        c += 1
      }
      if (at === stripped.length) break
      const start = removed[r]
      if (start !== undefined && at === start) {
        splitPlaces.push(next)
        at += removed[r + 1] ?? 0
        r += 2
        continue
      }
      next += 1
      at += 1
    }
    traceCredentials({ text: prepared, origin: new Int32Array(prepared.length).map((_, index) => index) }, false, covered, rules)
    traceSplits(prepared, splitPlaces, covered, rules)
    if (sites.length === 0) return maskCovered(prepared, covered, false) ?? undefined
  }
  const places: number[] = []
  let shift = 0
  let r = 0
  for (const site of sites) {
    while (r < removed.length && (removed[r] ?? 0) < site.at) {
      shift += removed[r + 1] ?? 0
      r += 2
    }
    places.push(site.at - shift)
  }
  // Each site's readings, and the clusters.
  const options = sites.map((site) => {
    const kept = new Set<string>([''])
    if (site.final !== null) kept.add(site.final)
    kept.add(site.body)
    return [...kept]
  })
  const clusters: number[][] = []
  for (let index = 0; index < sites.length; index += 1) {
    const last = clusters[clusters.length - 1]
    const previous = last?.[last.length - 1]
    if (last !== undefined && previous !== undefined && !/\s/.test(prepared.slice(places[previous], places[index]))) {
      last.push(index)
    } else {
      clusters.push([index])
    }
  }
  let readings = 0
  const sizes: number[] = []
  for (const cluster of clusters) {
    let size = 1
    for (const index of cluster) {
      size *= options[index]?.length ?? 1
      if (size > MAX_AMBIGUOUS_COMBINATIONS) return null
    }
    sizes.push(size)
    readings = Math.max(readings, size)
  }
  const inserted = sites.reduce((sum, site) => sum + site.body.length, 0)
  if ((readings + (withWritten ? 3 : 2)) * (prepared.length + inserted) > MAX_AMBIGUOUS_SCAN) return null
  const read = (choose: (site: number, cluster: number, within: number) => number): void => {
    const parts: string[] = []
    const origins: { readonly at: number; readonly from: number; readonly length: number }[] = []
    let from = 0
    let out = 0
    for (const [c, cluster] of clusters.entries()) {
      for (const [within, index] of cluster.entries()) {
        const place = places[index] ?? 0
        const piece = prepared.slice(from, place)
        parts.push(piece)
        origins.push({ at: out, from, length: piece.length })
        out += piece.length
        const keep = options[index]?.[choose(index, c, within)] ?? ''
        parts.push(keep)
        out += keep.length
        from = place
      }
    }
    const tail = prepared.slice(from)
    parts.push(tail)
    origins.push({ at: out, from, length: tail.length })
    out += tail.length
    const origin = new Int32Array(out).fill(-1)
    for (const span of origins) {
      for (let offset = 0; offset < span.length; offset += 1) origin[span.at + offset] = span.from + offset
    }
    traceCredentials({ text: parts.join(''), origin }, false, covered, rules)
  }
  // Every combination within each cluster, all clusters at once: reading `j`
  // takes combination `j mod size` of each cluster, a mixed-radix number over
  // its sites' readings. Reading 0 is the terminal's.
  const digits = (j: number, c: number): number[] => {
    const cluster = clusters[c] ?? []
    let rest = j % (sizes[c] ?? 1)
    return cluster.map((index) => {
      const radix = options[index]?.length ?? 1
      const digit = rest % radix
      rest = Math.floor(rest / radix)
      return digit
    })
  }
  for (let j = 0; j < readings; j += 1) {
    const chosen = clusters.map((_, c) => digits(j, c))
    read((_site, c, within) => chosen[c]?.[within] ?? 0)
  }
  // Every final byte kept, and every body kept, everywhere at once.
  read((site) => (sites[site]?.final === null ? 0 : 1))
  read((site) => (options[site]?.length ?? 1) - 1)
  return maskCovered(prepared, covered, true) ?? { text: prepared, masked: false, sequences: true, credential: false }
}

/** maskCovered replaces every covered run of `prepared` with its placeholder,
 * or returns null when nothing is covered. */
function maskCovered(prepared: string, covered: Uint8Array, sequences: boolean): Ambiguity | null {
  let masked = false
  const out: string[] = []
  let index = 0
  while (index < prepared.length) {
    const kind = covered[index] ?? 0
    let end = index
    while (end < prepared.length && covered[end] === kind) end += 1
    if (kind === 0) {
      out.push(prepared.slice(index, end))
    } else {
      masked = true
      out.push(kind === COVER_URL ? URL_PLACEHOLDER : TOKEN_PLACEHOLDER)
    }
    index = end
  }
  return masked ? { text: out.join(''), masked, sequences, credential: covered.includes(COVER_TOKEN) } : null
}

/**
 * freeText is the pass over one value of one class -- the recorder's own split
 * between "a field class" and "free text".
 *
 * For a class that **draws its value** -- a sentence, a label, a person, a host,
 * a path -- the rules read the **prepared** form: NFKC, then the invisible
 * characters gone. A string in which no rule finds anything is drawn as it came,
 * minus the invisible characters, which this layer always removes; one in which a
 * rule finds something is drawn from the prepared form, because that is the form
 * the rule rewrote. The form as written is tried as well, for the rare match that
 * only it shows. The classes that *validate* a grammar -- a repository, a branch,
 * a slug -- run the credential rules only: the entropy net must not rewrite a
 * long random-looking name the class exists to check.
 */
function freeText(
  text: string,
  written: string,
  prepared: string,
  ambiguity: Ambiguity | undefined,
  textClass: TextClass,
): Changed {
  const drawn =
    textClass === 'sentence' ||
    textClass === 'label' ||
    textClass === 'person' ||
    textClass === 'host' ||
    textClass === 'path'
  return withRules(text, written, prepared, ambiguity, drawn ? FREE_TEXT_RULES : CREDENTIAL_RULES)
}

/** withRules runs `rules` over the prepared form and, when they find nothing
 * there, over the form as written: `freeText`'s rule, shared with
 * `sanitizeRefusal`. **Before either, the readings are combined**
 * (`resolveReadings`): when any reading -- an ambiguous escape sequence's, or
 * the form as written with its invisible characters still in place -- holds a
 * credential, the resolver hands back the prepared form with every such
 * credential masked, and that masked form is the only form read. So a token
 * whose boundary is only a zero-width split is masked whether or not another
 * rule fires in the prepared form (review round 2 of PR 540: it used to be
 * read only when the prepared form matched nothing, and was drawn whole beside
 * a path, an address or another token). The written-form retry below is left
 * for the non-credential rules, whose matches no reading masks.
 *
 * **`written` is the recorder's second reading, and it is not optional.**
 * `redactFreeText` (`src/source/redact.ts`) retries its rules over
 * `stripTerminalSequences(text)` -- the value as written, every invisible
 * character still in place -- and this layer used to retry only the *stripped*
 * form, which has already lost them. That is a divergence in the one direction
 * that matters: the prepared form has no character between `J` and the token
 * after it, so a lookbehind boundary (`(?<![\p{L}\p{N}_])`, the start of every
 * token shape) is satisfied by the zero-width space and is *gone* with it --
 * `J​AKIA...` is a token to the recorder and no token here, and the display
 * drew a key id the fixture beside it had redacted. Design section 5.1 rule 7
 * makes this layer the last line of defence for a live document, and the
 * agreement `sanitize-agreement.test.ts` asserts is only worth asserting over
 * readings both layers take. */
function withRules(
  text: string,
  written: string,
  prepared: string,
  ambiguity: Ambiguity | undefined,
  rules: readonly OneRule[],
): Changed {
  if (ambiguity?.masked === true) {
    const masked = runRules(ambiguity.text, rules, false)
    if (masked.flags.includes('neutralised-credential')) return masked
    return { text: masked.text, flags: [...masked.flags, 'neutralised-credential'] }
  }
  const fromPrepared = runRules(prepared, rules, false)
  if (fromPrepared.text !== prepared) return fromPrepared
  // The form as written: the escape sequences gone, the invisible characters
  // still there, which is what `redactFreeText` reads second.
  const fromWritten = runRules(written, rules, false)
  if (fromWritten.text !== written) {
    // What a rule rewrote in this reading is drawn without the invisible
    // characters it left around the replacement: keeping those out of a drawn
    // value is this layer's own rule and not one of the recorder's -- a value
    // drawn with a bidi override in it is a value a screen can be made to spell
    // something else -- and the top-level strip already reported them.
    return { text: stripInvisible(fromWritten.text).text, flags: fromWritten.flags }
  }
  if (prepared === text) return fromPrepared
  // `text` is the caller's already-stripped form, so the value returned here
  // carries no invisible character whatever the rules did: removing those is
  // this layer's own rule, not one of the recorder's, and a value drawn with a
  // bidi override in it is a value a screen can be made to spell something else.
  return runRules(text, rules, false)
}

/** isMarkerText says whether a whole string is one redaction, the way the
 * recorder writes one: a fixed marker, or a pseudonym. `redacted@example.invalid`
 * is a marker; `ana@example.invalid` is not. */
export function isMarkerText(text: string): boolean {
  if (MARKERS.includes(text)) return true
  return REPO_PSEUDONYM_RE.test(text) || SLUG_PSEUDONYM_RE.test(text) || PERSON_PSEUDONYM_RE.test(text)
}

/**
 * nameableText says whether a **sentence-style attribution** -- "declared by X",
 * "KILLED by X" -- has anybody to name.
 *
 * The owner's ruling of 2026-10-31: "when we don't have the email, let's simply
 * not display the 'declared by ...'". A value that is absent, empty, one of the
 * redactions the CLI itself made (`isMarkerText`: the fixed markers and the
 * pseudonym grammars), or an address with nothing before the `@` is not a name,
 * and a sentence introducing one would tell the operator who did something out
 * of a placeholder. The **whole phrase** goes rather than the value alone:
 * "declared by n/a" names nobody either, and it is the phrase the owner asked
 * for.
 *
 * The address test reads past leading whitespace (`trimStart`): a value has the
 * same nothing before its `@` whatever a caller did with the space in front of
 * it, and a bare `startsWith('@')` would call `' @ana'` a name and draw the
 * phrase over a placeholder.
 *
 * It is a predicate of the *string* and not of a screen: the Coordinator's
 * "declared by" and the Projects kill banner's "KILLED by" are the two sentences
 * this console writes in that shape, and both read this one rule rather than
 * spelling it twice. Launcher **cells** (`shared/launcher.ts`) are not one of
 * them: a column cell names its own field, and the mark a cell draws for an
 * absent value is that column's own vocabulary.
 */
export function nameableText(text: string): boolean {
  if (text.trim() === '') return false
  if (isMarkerText(text)) return false
  return !text.trimStart().startsWith('@')
}

// ---------------------------------------------------------------------------
// The classes.
// ---------------------------------------------------------------------------

/** REPOSITORY_RE is `owner/name`: one slash, neither half empty, each half made
 * of the characters a forge allows. `..` is refused by the class rather than by
 * the pattern, so `a/../../etc` is a refusal and not a repository. */
const REPOSITORY_RE = /^[A-Za-z0-9][A-Za-z0-9._-]*\/[A-Za-z0-9][A-Za-z0-9._-]*$/

/** REPO_PATTERN_RE is one entry of a published project policy's `allow_repos`:
 * the literal `*` (every repository), or a **provider-qualified** pattern of
 * three segments or more. The grammar is the CLI's own -- `internal/
 * projectpolicy`'s `ValidPattern` (what `Decode` applies to the node a run
 * reads) and `MatchRepo` over `internal/config`'s `MatchSegment` -- and it is
 * wider than `REPOSITORY_RE` in the two ways the policy is: a segment may carry
 * a `*` wildcard (which never crosses a `/`, so `github/acme/*` admits
 * `github/acme/app` and not `github/acme/a/b`), and the canonical repository a
 * policy writes (`Canonical`, `provider/owner/name`) has three segments rather
 * than two.
 *
 * Three segments is a **floor, not a count**: a repository path may nest
 * (`task.CanonicalRepo` admits `acme/group/app`), and `MatchRepo` compares
 * segment against segment, so `bitbucket/acme/group/app` admits exactly that
 * repository. A pattern of exactly three segments refused an entry the node
 * carries, which is the defect this class exists to remove, so the count is not
 * bounded here -- and the bound is `MAX_REPO_PATTERN_LENGTH`, the CLI's
 * `MaxPatternLen`.
 *
 * Two of `ValidPattern`'s rules are deliberately **not** mirrored: the first
 * segment must be one of `config.Providers` there, and a pattern may carry at
 * most four wildcards. Both are the CLI's *policy* vocabulary rather than a
 * fact about the value -- the provider kinds a slug may name, and how many
 * wildcards an intent may carry -- and both are rules a later release may widen.
 * A console that hard-coded them would refuse an entry a newer CLI writes and
 * draw `n/a` beside a count that still counts it, which is the defect this class
 * exists to remove. What *is* mirrored is what makes a value unreadable: the
 * lower-case segment alphabet (`ValidPattern` refuses upper case), an empty
 * segment, a `.` or `..` segment (`CanonicalRepo` refuses such a repository, so
 * a pattern carrying one enrolls nothing), and a last segment ending in `.git`
 * (a canonical repository never does). */
const REPO_PATTERN_RE = /^(?:\*|[a-z0-9*._-]+(?:\/[a-z0-9*._-]+){2,})$/

/** SCOPE_PATTERN_RE is one entry of a stored grant's scope: the same
 * provider-qualified pattern a policy entry is (`REPO_PATTERN_RE`'s grammar,
 * which is `internal/projectpolicy`'s `ValidPattern`). Two rules `ParseScope`
 * adds on top of that grammar -- a grant names its provider, so the provider
 * segment is never `*`, and a `*` matches inside one path segment, so no
 * segment carries `**` -- are read by the class below rather than by this
 * pattern. The literal `*` `REPO_PATTERN_RE` admits is absent here: it is
 * `all-repos`' meaning, and `ParseScope` refuses it, because `all-repos` is a
 * scope a project admin grants, and a grant spelling `*` would let its holder
 * mint the scope they do not hold. */
const SCOPE_PATTERN_RE = /^[a-z0-9*._-]+(?:\/[a-z0-9*._-]+){2,}$/

/** IMAGE_REF_RE is a container image reference, and it is `internal/config`'s
 * `catalogImageRE` (`ValidateCatalog`, the rule the local configuration and the
 * published policy node are both held to): an optional registry host with an
 * optional port, a repository path of lower-case components, an optional tag and
 * an optional sha256 digest. It is an allow-list rather than a scan for the same
 * reason the CLI's is: the reference is written into a job spec, and a value
 * carrying a newline, an escape sequence or a bidi override is one no registry
 * serves and no person reads back as the image it names. */
const IMAGE_REF_RE =
  /^[a-z0-9]+(?:[._-][a-z0-9]+)*(?::[0-9]{1,5})?(?:\/[a-z0-9]+(?:[._-][a-z0-9]+)*)*(?::[A-Za-z0-9_][A-Za-z0-9._-]{0,127})?(?:@sha256:[a-f0-9]{64})?$/

/** SLUG_RE is a bucket key or a task slug: lower case, digits, hyphens. */
const SLUG_RE = /^[a-z0-9][a-z0-9-]{0,126}$/

/** WHOLE_BRANCH_RE is a git branch or ref: the characters git allows, with the
 * spellings git refuses (`..`, `@{`, a leading `-`, `.lock` at the end). The
 * free-text `BRANCH_RE` that used to find a branch by its conventional prefix
 * inside a sentence is gone (D3213), so this is the only branch grammar left. */
const WHOLE_BRANCH_RE = /^(?!-)(?!.*\.\.)(?!.*@\{)(?!.*\/\/)[A-Za-z0-9._/-]{1,255}$/

/** PROJECT_RE is a Fugaro project name, or a GCP project id. */
const PROJECT_RE = /^[a-z][a-z0-9-]{0,62}$/

/** LOGIN_RE was a forge account name, and `WHOLE_ADDRESS_RE` an email address:
 * the two shapes the `person` class used to tell apart, drawing a login and
 * rewriting an address to the marker. D3213 (2026-11-21) removed both -- the
 * class draws whatever it is given, an address included -- so no pattern for
 * either shape is left in this module. */

/** RUN_ID_RE is a Fugaro run id: `YYYYMMDD-HHMMSS-xxxx`. */
const RUN_ID_RE = /^[0-9]{8}-[0-9]{6}-[a-z0-9]{4}$/

/** LABEL_BREAK_RE is what a label may not contain: a line break. A label with a
 * line break in it is not a long label, it is two rows wearing one cell -- and
 * flattening it would hide that the field is not the shape the CLI's own
 * renderer writes, so the class refuses instead. */
const LABEL_BREAK_RE = /[\n\r\t\u000b\u000c]/

/** stripInvisible removes the formatting characters and the controls, and says
 * whether it removed anything. */
function stripInvisible(text: string): { readonly text: string; readonly flags: readonly SanitizeFlag[] } {
  const flags: SanitizeFlag[] = []
  let working = stripTerminalSequences(text).replace(INVISIBLE_RE, '')
  if (working !== text) flags.push('stripped-invisible')
  const before = working
  working = working.replace(CONTROL_RE, '')
  if (working !== before && flags.length === 0) flags.push('stripped-invisible')
  return { text: working, flags }
}

/**
 * oneLine flattens a sentence to a single line. Every whitespace run becomes one
 * space, and the string is trimmed: a title is drawn in one row, and a title
 * that is four rows tall is a layout the screens are not built for. It is done
 * *after* the control strip, so a title that used `\n` to smuggle content past
 * a single-line check has already lost it.
 */
function oneLine(text: string): string {
  return text.replace(/\s+/gu, ' ').trim()
}

/**
 * clipped applies the display bound. The cut steps back one unit when it would
 * land between the two halves of a surrogate pair -- half an emoji is not text,
 * and a lone surrogate is not even valid UTF-16.
 */
function clipped(text: string): { readonly text: string; readonly flags: readonly SanitizeFlag[] } {
  if (text.length <= MAX_TEXT_LENGTH) return { text, flags: [] }
  let cut = MAX_TEXT_LENGTH - ELLIPSIS.length
  const last = text.charCodeAt(cut - 1)
  if (last >= 0xd800 && last <= 0xdbff) cut -= 1
  return { text: `${text.slice(0, cut).trimEnd()}${ELLIPSIS}`, flags: ['clipped'] }
}

/**
 * carriesCredential answers whether one piece of a URL -- its query, its
 * fragment, or one path segment -- holds something the credential rules would
 * change. It reads the piece as written and percent-decoded, each in the
 * prepared form, and runs the credential pass the validating classes run (the
 * token families, a bearer header, a PEM block, a base64-wrapped candidate, and
 * an assignment such as `access_token=...`, which loses its value whatever it
 * looks like). `entropy` adds the high-entropy net, for the query and the
 * fragment (and `hostCarriesCredential` adds it for the host): a path segment
 * is left out of it on purpose, because a commit link's 40-hex digest is a path
 * segment, and refusing every commit link would be a refusal of the forge's own
 * URLs. The webhooks that keep their secret in the path are `WEBHOOK_URLS`.
 */
function carriesCredential(piece: string, entropy: boolean): boolean {
  if (piece === '') return false
  for (const form of new Set([prepare(piece), prepare(percentDecode(piece))])) {
    if (credentialPass(form, true) !== form) return true
    if (entropy) {
      URL_ENTROPY_RE.lastIndex = 0
      for (const match of form.matchAll(URL_ENTROPY_RE)) {
        if (isHighEntropy(match[0])) return true
      }
    }
  }
  return false
}

/**
 * URL_ENTROPY_RE is `ENTROPY_RE` with a dot as a separator on *both* sides. The
 * free-text net will not start a run right after a dot (it is the recorder's
 * pattern, and the recorder-agreement test holds it there), which in a URL let
 * `?id=v1.<random>` hide its random half. In a query, a fragment or a host a dot
 * only ever separates, so each piece between dots is read on its own: `v1.2.3`
 * and `file.name.ext` are short pieces and stay, and a long piece is still held
 * to `isHighEntropy`'s guard.
 */
const URL_ENTROPY_RE = /(?<![\p{L}\p{N}_+/=-])[A-Za-z0-9_+/=-]{20,}(?![\p{L}\p{N}_+/=-])/gu

/** HOST_DOT_RE is every character the URL parser reads as a label separator. */
const HOST_DOT_RE = /[.。．｡]/

/** HOST_RUN_RE is a run of a host label between hyphens and underscores. */
const HOST_RUN_RE = /[A-Za-z0-9]{20,}/g

/**
 * looksRandom is the host's own entropy rule, for the run `isHighEntropy` cannot
 * judge: a host is case-folded, so a token written into one loses the case mix
 * that guard asks for. A run of 20 or more letters and digits that has both, at
 * 3.5 bits per character or more, is a key, not a name -- a dictionary-like
 * label (`internationalbusinessmachines`) has no digits, and the labels a cloud
 * writes (`us-east-1`, `edgeweb-abc123xyz-ue`) are short runs between hyphens.
 */
function looksRandom(label: string): boolean {
  for (const [run] of label.matchAll(HOST_RUN_RE)) {
    const body = run.toLowerCase()
    if (!/[a-z]/.test(body) || !/\d/.test(body)) continue
    const counts = new Map<string, number>()
    for (const character of body) counts.set(character, (counts.get(character) ?? 0) + 1)
    let entropy = 0
    for (const count of counts.values()) {
      const p = count / body.length
      entropy -= p * Math.log2(p)
    }
    if (entropy >= 3.5) return true
  }
  return false
}

/**
 * punycodeDigit is RFC 3492's digit value of one character: `a`-`z` and `A`-`Z`
 * are 0-25, `0`-`9` are 26-35, and anything else is -1 -- each range spelled
 * out, so no character outside `[0-9A-Za-z]` can map to a digit.
 */
function punycodeDigit(code: number): number {
  if (code >= 0x30 && code <= 0x39) return code - 0x30 + 26
  if (code >= 0x41 && code <= 0x5a) return code - 0x41
  if (code >= 0x61 && code <= 0x7a) return code - 0x61
  return -1
}

/** PUNYCODE_MAX_INT is the decoder's integer bound, RFC 3492's `maxint`. */
const PUNYCODE_MAX_INT = 0x7fffffff

/**
 * punycodeDecode is RFC 3492's decoder, for the part of an `xn--` label after
 * its prefix; `null` for a label that does not decode -- a character that is
 * not a digit, a digit sequence that runs out, an integer past the RFC's bound,
 * or a code point that is not a Unicode scalar value -- so it never throws. The
 * URL parser writes a non-ASCII host in this form, and a token at the start of
 * such a label is preceded by `xn--` -- a hyphen, which is exactly what a
 * family pattern's left edge refuses -- so the host rule reads the decoded
 * label as well, and refuses a label that does not decode
 * (`hostCarriesCredential`).
 */
export function punycodeDecode(input: string): string | null {
  const base = 36
  const output: number[] = []
  const delimiter = input.lastIndexOf('-')
  for (let i = 0; i < Math.max(delimiter, 0); i += 1) {
    const code = input.charCodeAt(i)
    if (code >= 0x80) return null
    output.push(code)
  }
  let n = 128
  let bias = 72
  let i = 0
  for (let index = delimiter > 0 ? delimiter + 1 : 0; index < input.length; ) {
    const oldi = i
    for (let w = 1, k = base; ; k += base) {
      if (index >= input.length) return null
      const digit = punycodeDigit(input.charCodeAt(index))
      index += 1
      if (digit < 0) return null
      if (digit > Math.floor((PUNYCODE_MAX_INT - i) / w)) return null
      i += digit * w
      const t = k <= bias ? 1 : k >= bias + 26 ? 26 : k - bias
      if (digit < t) break
      if (w > Math.floor(PUNYCODE_MAX_INT / (base - t))) return null
      w *= base - t
    }
    const length = output.length + 1
    let delta = i - oldi
    delta = oldi === 0 ? Math.floor(delta / 700) : delta >> 1
    delta += Math.floor(delta / length)
    let k = 0
    for (; delta > 455; k += base) delta = Math.floor(delta / 35)
    bias = Math.floor(k + (36 * delta) / (delta + 38))
    if (Math.floor(i / length) > PUNYCODE_MAX_INT - n) return null
    n += Math.floor(i / length)
    i %= length
    if (n < 0x80 || n > 0x10ffff || (n >= 0xd800 && n <= 0xdfff)) return null
    output.splice(i, 0, n)
    i += 1
  }
  return String.fromCodePoint(...output)
}

/**
 * rawHost is the host as the text spells it, not as the parser rewrote it: the
 * parser folds case, and the case mix is what `isHighEntropy` reads. The scheme
 * is dropped with any run of slashes or backslashes after it (the parser
 * accepts `https:host` and `https:\\host`), then everything from the first
 * slash, backslash, `?` or `#`, then the userinfo and the port.
 */
function rawHost(text: string): string {
  const authority = text.replace(/^https:[/\\]*/i, '').split(/[/\\?#]/, 1)[0] ?? ''
  const host = authority.slice(authority.lastIndexOf('@') + 1)
  return host.startsWith('[') ? host : host.replace(/:[^:]*$/, '')
}

/**
 * hostCarriesCredential answers whether a URL's host holds a credential or a
 * random-looking label. It reads the host as written (case kept, which is what
 * the entropy guard reads) and as parsed (percent-decoded and case-folded) --
 * whole, so a token with a dot in it is still one token, and label by label,
 * each `xn--` label also punycode-decoded, and refused when it does not
 * decode -- and runs on every form the
 * credential pass (which percent-decodes a form itself), the entropy net and
 * `looksRandom`.
 */
function hostCarriesCredential(text: string, hostname: string): boolean {
  const written = rawHost(text)
  const forms = new Set<string>()
  for (const host of [written, hostname]) {
    forms.add(host)
    for (const label of host.split(HOST_DOT_RE)) {
      forms.add(label)
      if (/^xn--/i.test(label)) {
        // A label that claims to be punycode and is not is no host a link
        // has: it is refused rather than read as plain text.
        const decoded = punycodeDecode(label.slice(4))
        if (decoded === null) return true
        forms.add(decoded)
      }
    }
  }
  for (const form of forms) {
    if (carriesCredential(form, true)) return true
    if (looksRandom(prepare(form))) return true
  }
  return false
}

/**
 * WEBHOOK_URLS are the well-known webhook shapes whose secret is a **path
 * segment**: Slack, Discord, Microsoft Teams, Zapier, IFTTT and Telegram. A path
 * segment is left out of the entropy net on purpose -- a commit link's digest is
 * one -- so these are refused by host and path instead. The trade-off, said out
 * loud: a service not on this list that keeps its secret in the path is drawn
 * (unless the segment matches a token family), and a page of a listed host that
 * is not a webhook is kept.
 */
const WEBHOOK_URLS: readonly { readonly host: RegExp; readonly path: RegExp }[] = Object.freeze([
  { host: /(?:^|\.)hooks\.slack\.com$/, path: /^\/(?:services|workflows|triggers)\//i },
  { host: /(?:^|\.)discord(?:app)?\.com$/, path: /^\/api\/(?:v\d+\/)?webhooks\//i },
  { host: /(?:^|\.)webhook\.office\.com$/, path: /^\/webhookb2\//i },
  { host: /(?:^|\.)outlook\.office(?:365)?\.com$/, path: /^\/webhook\//i },
  { host: /(?:^|\.)hooks\.zapier\.com$/, path: /^\/hooks\//i },
  { host: /(?:^|\.)maker\.ifttt\.com$/, path: /\/with\/key\//i },
  { host: /(?:^|\.)api\.telegram\.org$/, path: /^\/(?:file\/)?bot/i },
])

/**
 * webhookPath is a URL path in the form `WEBHOOK_URLS` reads: percent-decoded,
 * every run of slashes and backslashes collapsed to one slash (a server reads
 * `//services` as `/services`, the parser does not), and its `.` and `..`
 * segments resolved by the URL parser -- repeated until it stops changing, so a
 * separator or a dot that only appears once decoded is resolved too.
 */
function webhookPath(pathname: string): string {
  let path = pathname
  for (let round = 0; round < 4; round += 1) {
    const flat = percentDecode(path).replace(/[\\/]+/g, '/')
    let next: string
    try {
      next = new URL(flat.startsWith('/') ? flat : `/${flat}`, 'https://webhook.invalid').pathname
    } catch {
      return flat
    }
    if (next === path) return flat
    path = next
  }
  return percentDecode(path).replace(/[\\/]+/g, '/')
}

/**
 * isWebhook answers whether a parsed URL is one of `WEBHOOK_URLS`. The host
 * loses every trailing dot (`hooks.slack.com..` is that host) and a listed host
 * matches with any subdomain in front of it (`www.discord.com`); the path is
 * `webhookPath`'s.
 */
function isWebhook(parsed: URL): boolean {
  const host = parsed.hostname.replace(/\.+$/, '')
  const path = webhookPath(parsed.pathname)
  return WEBHOOK_URLS.some((hook) => hook.host.test(host) && hook.path.test(path))
}

/**
 * urlCarriesCredential answers whether a URL found in **free text** must be
 * replaced whole. It is `applyUrl`'s own question, asked of the match rather
 * than of a field, and it reuses that function's predicates rather than carrying
 * a second list: userinfo (dropped there, a replacement here), a host that
 * `hostCarriesCredential` reads as a key, one of the well-known webhook URLs
 * whose secret is a path segment (`isWebhook`, `WEBHOOK_URLS`), and a query, a
 * fragment or a path segment that `carriesCredential` reads as one.
 *
 * Before D3213 (2026-11-21) the free-text URL rule replaced **every** URL, as
 * the recorder replaces every `url` field. It replaces one only when the URL
 * itself is the credential's carrier now: a URL that carries none is drawn as it
 * came, which is what the console does with every other non-secret value, and
 * the credential rules that run after this one are what still catch a token the
 * URL only happens to contain.
 *
 * A match this parser cannot read is replaced, not kept: `applyUrl` refuses such
 * a value, and the free text has no way to check a URL whose parts it cannot
 * see -- `https://user:pass@` is the case that decides it. */
function urlCarriesCredential(raw: string): boolean {
  let parsed: URL
  try {
    parsed = new URL(raw)
  } catch {
    return true
  }
  if (parsed.username !== '' || parsed.password !== '') return true
  if (hostCarriesCredential(raw, parsed.hostname)) return true
  if (isWebhook(parsed)) return true
  const query = parsed.search.replace(/^\?/, '')
  const fragment = parsed.hash.replace(/^#/, '')
  if (carriesCredential(query, true) || carriesCredential(fragment, true)) return true
  return parsed.pathname.split('/').some((segment) => carriesCredential(segment, false))
}

/** applyUrl is the URL class: `https` only, a non-empty host, no userinfo,
 * nothing credential-shaped or random-looking in its host, nothing
 * credential-shaped in its path, query or fragment, and none of the well-known
 * webhook URLs (`WEBHOOK_URLS`), whose secret is a path segment. It is the only way
 * this layer produces a URL, and `safeUrl` below is the same function with a
 * `null` instead of a refusal, so a screen has one door and not two. */
function applyUrl(text: string, field: string): Sanitized {
  if (isMarkerText(text)) {
    // A redaction is not a link. It is text, and it is the truth about the
    // field, so the class accepts it and `safeUrl` refuses to make one of it.
    return ok(text)
  }
  if (text === '' || /\s/.test(text)) {
    // Whitespace is refused rather than parsed: the URL parser DELETES a tab or
    // a newline, so `https://a/x\nhttps://evil.invalid` would parse as one URL
    // whose path is the second one.
    return refuse(`${field} is not a valid url, so the console will not draw it`)
  }
  if (text.length > MAX_URL_LENGTH) {
    return refuse(
      `${field} is a url longer than the console accepts (${String(MAX_URL_LENGTH)} characters)`,
    )
  }
  let parsed: URL
  try {
    parsed = new URL(text)
  } catch {
    return refuse(`${field} is not a valid url, so the console will not draw it`)
  }
  if (parsed.protocol !== 'https:') {
    return refuse(`${field} is a url the console will not open: it is not https`)
  }
  if (parsed.hostname === '') {
    return refuse(`${field} is not a valid url, so the console will not draw it`)
  }
  // A credential in the host, the query, the fragment or a path segment is
  // refused, not rewritten: the URL is drawn as text, put in a title and offered
  // as a link, and a link with its key cut out is a different link. The
  // recorder replaces a url field whole; this class keeps a clean one and
  // refuses the rest.
  const query = parsed.search.replace(/^\?/, '')
  const fragment = parsed.hash.replace(/^#/, '')
  if (
    hostCarriesCredential(text, parsed.hostname) ||
    isWebhook(parsed) ||
    carriesCredential(query, true) ||
    carriesCredential(fragment, true) ||
    parsed.pathname.split('/').some((segment) => carriesCredential(segment, false))
  ) {
    return refuse(`${field} is a url that carries a credential, so the console will not draw it`)
  }
  if (parsed.username === '' && parsed.password === '') {
    // No userinfo to drop, so the text is returned exactly as it arrived: `URL`
    // re-encodes what it is given, and a link the console rewrote for no reason
    // is a link that can stop working.
    return ok(text)
  }
  // With userinfo, the rebuild IS the point: `URL` parses `%75ser:p%61ss@` as
  // userinfo, so a credential hidden in the encoding cannot survive this.
  parsed.username = ''
  parsed.password = ''
  const rebuilt = parsed.toString()
  return ok(rebuilt, ['userinfo-removed'])
}

/** ambiguityRefusal is the sentence for a value whose escape sequences would
 * take more readings than `resolveAmbiguousSequences` bounds. */
function ambiguityRefusal(field: string): string {
  return `${field} holds more terminal escape sequences than the console will read`
}

/**
 * sanitizeText is the one way a document's string becomes something a screen
 * may draw -- or a refusal saying why it may not.
 *
 * `field` is the wire path as the contract spells it (`runs[0].title`), and it
 * ends up in the refusal sentence, so a person reading an `n/a` learns which
 * field it was. The refusal **never contains the value**, only the field and
 * the class's own words.
 */
export function sanitizeText(
  raw: unknown,
  textClass: TextClass = 'sentence',
  field = 'the value',
): Sanitized {
  if (typeof raw !== 'string') {
    return refuse(`${field} is a ${typeof raw}, not text`)
  }
  if (raw.length > MAX_TEXT_INPUT_LENGTH) {
    return refuse(
      `${field} is longer than the console will read (${String(MAX_TEXT_INPUT_LENGTH)} characters)`,
    )
  }
  const stripped = stripInvisible(raw)
  const flags: SanitizeFlag[] = [...stripped.flags]
  let text = stripped.text
  // The recorder's second reading, computed here rather than inside the rule
  // pass so that it is the *raw* value's: the escape sequences gone and the
  // invisible characters still in place (`redactFreeText`,
  // src/source/redact.ts), which is the reading a zero-width character hides a
  // token from `prepared` in.
  const written = stripTerminalSequences(raw)
  // The second half of the bound (`MAX_TEXT_INPUT_LENGTH`): the form the rules
  // read, after NFKC, is held to the same 64 KiB as the form that arrived.
  const prepared = prepare(text)
  if (prepared.length > MAX_TEXT_INPUT_LENGTH) {
    return refuse(
      `${field} is longer than the console will read (${String(MAX_TEXT_INPUT_LENGTH)} characters)`,
    )
  }

  // Every reading -- each ambiguous escape sequence's, and the form as written
  // with its invisible characters in place -- and the union of what the
  // credential rules find in them (`resolveReadings`): the same combining step
  // the recorder runs, computed once.
  const ambiguity = resolveReadings(raw)
  if (ambiguity === null) return refuse(ambiguityRefusal(field))

  if (textClass === 'url') {
    // A link is drawn and opened exactly as written, so a url whose escape
    // sequences hide bytes no reading can settle is not a link.
    if (ambiguity?.sequences === true) {
      return refuse(`${field} is a url with a terminal escape sequence in it, so the console will not draw it`)
    }
    // The url class reads the union too (review round 3 of PR 540): a key id
    // the prepared form joins to the letter before it is still a key id in
    // the form as written, and a link carrying it is not drawn.
    if (resolveCredentialReadings(raw)?.credential === true) {
      return refuse(`${field} is a url that carries a credential, so the console will not draw it`)
    }
    return applyUrl(text, field)
  }

  if (
    ambiguity?.masked === true &&
    textClass !== 'sentence' &&
    textClass !== 'label' &&
    textClass !== 'host' &&
    textClass !== 'path'
  ) {
    // A class that draws its value masks the reading that holds a credential and
    // draws the rest; one that draws the value whole or not at all has no rest
    // to fall back on.
    return refuse(
      ambiguity.sequences
        ? `${field} holds a credential in one reading of its terminal escape sequences, so the console will not draw it`
        : `${field} holds a credential in the form it was written in, so the console will not draw it`,
    )
  }

  if (textClass === 'person') {
    // A marker the recorder already wrote is kept as it is: `person-2f9a4c1e`
    // and `redacted@example.invalid` are values a reader recognises, and
    // refusing one would turn a redacted document into a cell that says it is
    // broken.
    if (isMarkerText(text)) return ok(text, flags)
    // Every token family in `TOKEN_SHAPES` fits inside the login grammar
    // (`[A-Za-z0-9-]{1,39}`: an AWS key id, a Google API key, a GitLab PAT, a
    // Slack token), and so does any long high-entropy run, which is the one
    // shape no pattern can find. Every one of those is redacted in a title, a
    // reason and a stage, and the recorder pseudonymises the same value, so a
    // class that draws its value whole is where a credential comes out whole:
    // design section 5.1 rule 7 makes this layer the last line of defence for
    // live (non-fixture) documents. A value that is *not* a secret is drawn as
    // it is (D3213, 2026-11-21) -- an address is who started the run, which is
    // what this column is for -- but half of a login is not a person, so a
    // credential-shaped value is refused rather than cut down.
    if (holdsCredential(text)) {
      return refuse(`${field} is shaped like a credential and not like a person, so the console will not draw it`)
    }
  }

  const rules = freeText(text, written, prepared, ambiguity, textClass)
  flags.push(...rules.flags)
  text = rules.text

  if (textClass === 'sentence') {
    // An empty sentence is drawn as an empty cell rather than refused: whether
    // a field is *there* is the schema's business (`schema.ts` refuses a
    // missing `title` by name), and this class is about what a value may
    // contain. Refusing here would turn a run whose title the job had not
    // written yet into a document the whole screen is refused for.
    const cut = clipped(oneLine(text))
    return ok(cut.text, [...flags, ...cut.flags])
  }

  if (textClass === 'person' || textClass === 'host' || textClass === 'path') {
    // The classes that draw a value as written, one line and at the length this
    // layer draws: a machine name, a path in a finding and a person are values
    // the operator reads rather than values a screen is keyed on, so there is
    // nothing here for a placeholder to protect (D3213). What the credential
    // rules left of them is what is drawn.
    const cut = clipped(oneLine(text))
    return ok(cut.text, [...flags, ...cut.flags])
  }

  if (textClass === 'label') {
    if (LABEL_BREAK_RE.test(raw)) {
      return refuse(`${field} is not a single line, so the console will not draw it as one`)
    }
    const line = oneLine(text)
    if (line.length > MAX_LABEL_LENGTH) {
      return refuse(`${field} is longer than a label the console draws`)
    }
    return ok(line, flags)
  }

  if (textClass === 'repository') {
    if (isMarkerText(text)) return ok(text, flags)
    if (text.includes('..')) return refuse(`${field} is not a valid repository`)
    if (text.length > MAX_REPOSITORY_LENGTH) return refuse(`${field} is not a valid repository`)
    return REPOSITORY_RE.test(text)
      ? ok(text, flags)
      : refuse(`${field} is not a valid repository`)
  }

  if (textClass === 'repoPattern') {
    // A marker is kept as it is, as every validating class keeps one: the
    // recorder's `<slug>` and the pseudonym grammars are values a *reader*
    // recognises, and refusing one would turn a redacted policy entry into a
    // cell that says the document is broken.
    if (isMarkerText(text)) return ok(text, flags)
    if (text.length > MAX_REPO_PATTERN_LENGTH) {
      return refuse(`${field} is not a valid repository pattern`)
    }
    if (!REPO_PATTERN_RE.test(text)) {
      return refuse(`${field} is not a valid repository pattern`)
    }
    // A `.` or `..` *segment*, exactly as `ValidPattern` refuses it and for its
    // reason (`CanonicalRepo` refuses such a repository, so the entry admits
    // nothing). Reading the value whole instead would refuse `github/a..b/app`,
    // which the CLI admits -- the defect this class was added to remove.
    for (const segment of text.split('/')) {
      if (segment === '.' || segment === '..') {
        return refuse(`${field} is not a valid repository pattern`)
      }
    }
    return text.endsWith('.git')
      ? refuse(`${field} is not a valid repository pattern`)
      : ok(text, flags)
  }

  if (textClass === 'scope') {
    if (isMarkerText(text)) return ok(text, flags)
    // The two whole scopes, and they are the whole value: `ParseScope` reads
    // them spelled exactly once, and a canonical repository always carries a
    // provider and a `/`, so neither word can be one.
    if (text === 'project' || text === 'all-repos') return ok(text, flags)
    for (const entry of text.split(',')) {
      // The pattern's own reading is `repoPattern`'s, and the bound is the
      // CLI's: `ParseScope` canonicalises an entry through `ValidPattern`,
      // whose `MaxPatternLen` is the longest entry the grants node's reader
      // accepts. An empty entry and a lone `*` are refused by the pattern
      // alone -- neither carries a provider and two `/` -- so no case is
      // spelled twice here.
      if (entry.length > MAX_REPO_PATTERN_LENGTH || !SCOPE_PATTERN_RE.test(entry)) {
        return refuse(`${field} is not a valid scope`)
      }
      const segments = entry.split('/')
      // The two rules `ParseScope` adds to the allow-list's grammar. A `*` in
      // the provider segment is what an allow-list entry may carry and a grant
      // may not ("a pattern may wildcard a provider for the allow-list, and a
      // grant names the repositories it covers"), and `**` is refused rather
      // than read as a second wildcard, because a pattern that looks like it
      // crosses `/` is the one a reader would misread as covering a nested
      // repository. A `.` or `..` segment is refused for `repoPattern`'s
      // reason: `CanonicalRepo` refuses such a repository, so a grant carrying
      // one covers nothing.
      if (segments[0] === '*') return refuse(`${field} is not a valid scope`)
      for (const segment of segments) {
        if (segment === '.' || segment === '..' || segment.includes('**')) {
          return refuse(`${field} is not a valid scope`)
        }
      }
      // A canonical repository never ends in `.git` (`CanonicalRepo` trims it),
      // and `ParseScope` trims it before it stores an entry, so a stored scope
      // never carries one.
      if (entry.endsWith('.git')) return refuse(`${field} is not a valid scope`)
    }
    return ok(text, flags)
  }

  if (textClass === 'imageRef') {
    if (isMarkerText(text)) return ok(text, flags)
    if (text.length > MAX_IMAGE_REF_LENGTH) {
      return refuse(`${field} is not a valid container image reference`)
    }
    return IMAGE_REF_RE.test(text) ? ok(text, flags) : refuse(`${field} is not a valid container image reference`)
  }

  if (textClass === 'slug') {
    if (isMarkerText(text)) return ok(text, flags)
    return SLUG_RE.test(text) ? ok(text, flags) : refuse(`${field} is not a valid slug`)
  }

  if (textClass === 'branch') {
    if (isMarkerText(text)) return ok(text, flags)
    // The one validating class whose grammar is the credential alphabet whole:
    // `WHOLE_BRANCH_RE` allows upper case, lower case and digits, which is what
    // a token family and a base64 body are made of. The credential pass above
    // has answered for every shape `TOKEN_SHAPES` knows, but a run no pattern
    // can find is the entropy rule's, and this class was drawing one whole
    // (review of Task 4.1: a 20-character mixed-case run, and a 40-character hex
    // digest git would take as a ref).
    //
    // The answer is the recorder's own, and not a refusal -- which is where this
    // class parts company with `person`. The recorder writes `<branch>` for
    // *every* value under a branch key, whatever it holds, and this layer
    // already ships that marker (`BRANCH_PLACEHOLDER` is `BRANCH_MARKER`,
    // pinned by a test); drawing it is therefore not a redaction the document
    // does not contain but exactly the one the recorder would have made. The
    // `person` class has no such answer to fall back on -- the recorder writes a
    // pseudonym from the document's own table of people, and half a login is not
    // a person -- so it refuses instead. And the entropy rule is a heuristic:
    // `feat/ABC-123-add-thing` is a real branch name the rule calls a credential,
    // and a redacted cell is a better answer to that than a refused document.
    if (holdsCredential(text)) return ok(BRANCH_PLACEHOLDER, [...flags, 'branch-placeholder'])
    if (text.endsWith('.lock') || text.endsWith('/') || text.startsWith('/')) {
      return refuse(`${field} is not a valid branch`)
    }
    return WHOLE_BRANCH_RE.test(text) ? ok(text, flags) : refuse(`${field} is not a valid branch`)
  }

  if (textClass === 'project') {
    if (isMarkerText(text)) return ok(text, flags)
    return PROJECT_RE.test(text) ? ok(text, flags) : refuse(`${field} is not a valid project`)
  }

  if (textClass === 'runId') {
    if (isMarkerText(text)) return ok(text, flags)
    return RUN_ID_RE.test(text) ? ok(text, flags) : refuse(`${field} is not a valid run id`)
  }

  // Every member of `TextClass` is handled above. This line exists so that
  // adding a twelfth class without a presentation is a compile error rather
  // than a value that falls through unclassified.
  const exhaustive: never = textClass
  return refuse(`${field} has a text class the console does not draw: ${String(exhaustive)}`)
}

/**
 * sanitizeRefusal is the way the **CLI's own refusal sentence** becomes
 * something a screen may draw. It is a third door beside `sanitizeText` and
 * `safeUrl`, and it exists because the two rules that meet on that channel
 * disagreed (review round 5 of PR 478).
 *
 * The sentence is the child's stderr, delivered on the `SourceRefusal` channel
 * (`src/source/source.ts`, `refusalFrom`), and design section 6.3 requires it on
 * screen **"verbatim ... the console shows that text unedited"**, because for
 * `report`'s permission refusal the sentence *is* the remedy: it names
 * `roles/datastore.viewer` and `roles/serviceusage.serviceUsageConsumer`, and a
 * screen that redacted them would leave the operator with a refusal and no fix.
 *
 * `sentence` was the wrong class for it while the free-text path carried the
 * pattern classes: `roles/datastore.viewer` is two long segments around a slash,
 * so `REPO_TOKEN_RE` drew it `[redacted]`, and `project sandbox-db` is a GCP name
 * in a sentence, so `GCP_CONTEXT_RE` drew it `<project>`. Those rules are gone
 * (D3213, 2026-11-21), so the two doors now agree on a sentence that holds no
 * credential; this door stays because refusing to draw stderr at all would be the
 * opposite error: stderr is not a document, but it is not the console's own text
 * either, and a credential a child echoed into it must not reach a screen.
 *
 * So this pass is `REFUSAL_RULES` and only those -- the URL rule for a URL that
 * carries a credential, the credential pass, a path-shaped secret, the entropy
 * net -- read, as every class reads them, on the **prepared** form (NFKC, so a
 * fullwidth token is a token: review round 2 of PR 494), plus this layer's own
 * two: the invisible characters go (a value a screen is made to misspell is not a
 * value), and the display bound applies. A refusal sentence that echoes a token is
 * redacted; everything else is drawn as the CLI printed it, a role, a project, a
 * path and an address alike, which is what the design asks for and what makes the
 * screen useful: the operator's own refusal is the one message on this console the
 * console may not paraphrase.
 *
 * The whole of the `console` side is expected to reach this through one helper
 * per screen, so that "the CLI's sentence is drawn verbatim" is one rule in two
 * places rather than two rules that disagree.
 */
export function sanitizeRefusal(raw: unknown, field = 'the refusal'): Sanitized {
  if (typeof raw !== 'string') return refuse(`${field} is a ${typeof raw}, not text`)
  if (raw.length > MAX_TEXT_INPUT_LENGTH) {
    return refuse(
      `${field} is longer than the console will read (${String(MAX_TEXT_INPUT_LENGTH)} characters)`,
    )
  }
  const stripped = stripInvisible(raw)
  const prepared = prepare(stripped.text)
  if (prepared.length > MAX_TEXT_INPUT_LENGTH) {
    return refuse(
      `${field} is longer than the console will read (${String(MAX_TEXT_INPUT_LENGTH)} characters)`,
    )
  }
  const readings = resolveReadings(raw)
  if (readings === null) return refuse(ambiguityRefusal(field))
  const rules = withRules(stripped.text, stripTerminalSequences(raw), prepared, readings, REFUSAL_RULES)
  const cut = clipped(oneLine(rules.text))
  return ok(cut.text, [...stripped.flags, ...rules.flags, ...cut.flags])
}

/**
 * safeUrl is the only way a URL reaches a destination. It returns the URL a
 * screen may put in a `data-href`, or `null` -- and `null` is the answer for
 * every refusal: a marker (which is not a link), a `javascript:` URL, an `http`
 * URL, a URL with no host, a URL this parser cannot read.
 *
 * `src/renderer/app.ts`'s `putLink` takes its destination from here and never
 * from a document, which is the display-layer half of design section 5.1 rule
 * 2: the main process decides whether a URL may be *opened*, and this decides
 * whether a screen may even offer one.
 */
export function safeUrl(raw: unknown): string | null {
  const result = sanitizeText(raw, 'url', 'the link')
  return result.ok && result.text !== '' && !isMarkerText(result.text) ? result.text : null
}

