// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// The launch's value rules and its reply decoder (docs/design/desktop-console.md
// §5.1 rule 11; the reply is `internal/cli/run.go`'s `launchResult`).
//
// SECURITY-SENSITIVE, and it is the half of the launch that decides what may
// become a word of the child's argv and what may be drawn. Everything here is a
// pure function of its arguments -- no Electron, no spawn, no window -- so every
// rule is a test rather than an observation, and the operation that calls them
// is what turns a refusal into the sentence a screen shows.
//
// Three kinds of rule, and each has its own reason:
//
//   1. **The task text is trimmed the way the CLI trims it** (`strings.TrimSpace`
//      in `internal/cli/run.go`'s `taskText`, whose own refusal is "the task is
//      empty"), because the bytes the window counts must be the bytes the run
//      keeps: a task that is only whitespace is refused here, before a window
//      opens, rather than by the CLI after the operator confirmed it.
//   2. **A title and a plan task are `=`-form words of their own kind.** They are
//      not `value` arguments: `isSafeArgumentValue`'s alphabet refuses a space
//      and a leading `-`, and a title may have both. So they are checked by the
//      rules below -- one line, no control character, no format character, a cap
//      -- and `ARGUMENT_RE` is untouched for every other field, which is the
//      point: widening it for a title would widen it for `--repo` too. Each is
//      also held to the CLI's own list for it (`internal/task`'s `TitleProblem`
//      and `PlanTaskProblem`, the two lists the launcher and the runner share),
//      because a value the CLI refuses is a launch that fails *after* the
//      operator confirmed the window. A plan task is a shape and not prose: an
//      id that is not `<plan>/<task>` is refused here, whatever a title may be.
//      A title is also returned the way the CLI would store it -- trimmed and
//      rewritten by `safetext.Field`, which drops invisible characters and
//      collapses runs of whitespace -- because the value in the window has to
//      be the value in the argv.
//   3. **The reply is decoded closed.** A field the decoder does not know
//      refuses the whole reply rather than being ignored, because the values it
//      carries are drawn as the CLI's own facts.

/** MAX_LAUNCH_TASK_BYTES is the task's cap, in bytes of UTF-8 *after* the
 * trim: 16 KiB, rule 11's own number -- the CLI sets no cap of its own on a
 * task (`internal/cli/run.go`'s `taskText` reads stdin to EOF and refuses only
 * an empty one), so this bound is the console's and it is here because the
 * operator confirms by reading the whole task. */
export const MAX_LAUNCH_TASK_BYTES = 16384

/** MAX_LAUNCH_TITLE_RUNES is the title's cap, in characters: a title is a
 * label (`fugaro runs ls` prints it) and not a document. */
export const MAX_LAUNCH_TITLE_RUNES = 100

/** MAX_LAUNCH_PLAN_TASK_BYTES is the plan task's cap, in bytes: the CLI's own
 * `--plan-task` shape is a short instruction, not a second task text. */
export const MAX_LAUNCH_PLAN_TASK_BYTES = 128

/** MAX_LAUNCH_RESULT_STRING bounds every string the reply may carry: the
 * values are the CLI's own (`run`, `repo`, `branch`, …), printed by a program
 * this console spawned, so a longer one is not a value this decoder will hold
 * and draw. */
export const MAX_LAUNCH_RESULT_STRING = 4096

/**
 * LaunchValue is one rule's answer: the value it admits, or the console's own
 * sentence for refusing it (rule 11's "Results, and every refusal"). The
 * sentence is the console's -- the CLI never wrote it -- so it names what was
 * wrong with the renderer's value and never composes the value into itself as
 * anything but text.
 */
export type LaunchValue<T> =
  | { readonly ok: true; readonly value: T }
  | { readonly ok: false; readonly reason: string }

/** LaunchTask is a task text that may be sent: the trimmed text and the bytes
 * the child is given on stdin, which are the bytes the window counted. */
export interface LaunchTask {
  readonly text: string
  readonly bytes: Uint8Array
}

// ---------------------------------------------------------------------------
// The task text.
// ---------------------------------------------------------------------------

/**
 * GO_TRIM_SPACE is `strings.TrimSpace`'s set (`unicode.IsSpace`), written out
 * rather than reached for through `String.prototype.trim`: the two are not the
 * same set. JavaScript also trims U+FEFF, which Go does not, and JavaScript
 * does *not* trim U+0085, which Go does -- so a task the console trimmed with
 * `trim()` and the CLI trimmed with `strings.TrimSpace` would be two different
 * texts, and the window would count bytes the run did not keep.
 *
 * It is a set of **code points** and not a regular-expression class, because
 * the two anchors are not the same cost: `/^[<set>]+/` reads the text once, but
 * `/[<set>]+$/` tries every offset of the text, so a task a renderer filled with
 * whitespace costs the main process seconds per call -- seconds in which the
 * window does not answer, before any of this was about the argv. Two index scans
 * over `charCodeAt` are linear in the text and cannot be made to stall.
 *
 * Every character in the set is in the BMP, so a scan by code unit is exact:
 * none of them is half of a surrogate pair.
 */
const GO_TRIM_SPACE: ReadonlySet<number> = new Set([
  0x09,
  0x0a,
  0x0b,
  0x0c,
  0x0d,
  0x20,
  0x85,
  0xa0,
  0x1680,
  0x2000,
  0x2001,
  0x2002,
  0x2003,
  0x2004,
  0x2005,
  0x2006,
  0x2007,
  0x2008,
  0x2009,
  0x200a,
  0x2028,
  0x2029,
  0x202f,
  0x205f,
  0x3000,
])

/** A surrogate that is not half of a pair is not valid UTF-8: `TextEncoder`
 * would replace it with U+FFFD, and the task that reached the run would be a
 * different text from the one the operator confirmed. */
const LONE_SURROGATE_RE = /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/

/**
 * trimTaskText is the CLI's `strings.TrimSpace`, which trims the leading and
 * trailing run of `unicode.IsSpace` characters and nothing inside the text.
 */
function trimTaskText(text: string): string {
  let start = 0
  let end = text.length
  while (start < end && GO_TRIM_SPACE.has(text.charCodeAt(start))) start += 1
  while (end > start && GO_TRIM_SPACE.has(text.charCodeAt(end - 1))) end -= 1
  if (start === 0 && end === text.length) return text
  return text.slice(start, end)
}

/**
 * checkLaunchTask answers whether one task text may be launched, and returns
 * the trimmed text with the exact bytes that go to the child on stdin.
 *
 * A NUL byte is refused although it travels on stdin and not in the argv: the
 * task is stored, printed and read back by the CLI, and a NUL is the one byte
 * no text surface in this pipeline carries. An over-long task is refused rather
 * than cut, because a cut task is a run asked for something the operator did
 * not write.
 */
export function checkLaunchTask(text: string): LaunchValue<LaunchTask> {
  const trimmed = trimTaskText(text)
  if (trimmed === '') return { ok: false, reason: 'the task is empty' }
  if (LONE_SURROGATE_RE.test(trimmed)) {
    return { ok: false, reason: 'the task is not valid UTF-8' }
  }
  if (trimmed.includes('\u0000')) return { ok: false, reason: 'the task carries a NUL byte' }
  const bytes = new TextEncoder().encode(trimmed)
  if (bytes.length > MAX_LAUNCH_TASK_BYTES) {
    return {
      ok: false,
      reason: `the task is ${String(bytes.length)} bytes after the trim, over the ${String(MAX_LAUNCH_TASK_BYTES)} this console sends`,
    }
  }
  return { ok: true, value: { text: trimmed, bytes } }
}

/** CONTROL_RE is `\p{Cc}`: every C0 and C1 control character, the newline,
 * the carriage return and the tab among them, and DEL and friends above it. */
const CONTROL_RE = /\p{Cc}/u

/** FORMAT_RE is `\p{Cf}`: the format characters, which are *not* control
 * characters and pass a `Cc` check while still reversing or joining the line a
 * person reads -- U+202E and the rest of the bidi family, U+200B-U+200D,
 * U+2066-U+2069 and U+FEFF. The CLI's own sanitiser drops this class
 * (`internal/safetext`'s `unicode.Is(unicode.Cf, r)`); the console refuses on
 * its side rather than letting the CLI silently drop a character, so the value
 * in the window is the value in the argv. */
const FORMAT_RE = /\p{Cf}/u

/** LINE_BREAK_RE is what makes a value more than one line. The newline, the
 * carriage return and U+0085 (NEL) are Go's own line breaks for a title
 * (`task.TitleProblem` names all three); U+2028 and U+2029 are not, and both
 * end a line in every rendered document a person reads. */
const LINE_BREAK_RE = /[\n\r\u0085\u2028\u2029]/u

/** SPOOFING_RE is `internal/safetext`'s `spoofing(r)`: the bidi embeddings,
 * overrides and isolates, the zero-width space, joiners and word joiner, the
 * soft hyphen, the grapheme joiner, the Hangul fillers, the invisible
 * operators, the variation selectors and the tag characters. It is **wider
 * than `FORMAT_RE`**, and that is the point of it being written out here:
 * U+034F, U+115F, U+1160, U+17B4, U+17B5, U+3164, U+FFA0, U+FE00-U+FE0F and
 * U+E0100-U+E01EF are not format characters, so a `Cf` test passes them while
 * `safetext.Field` -- which the CLI puts a title through -- drops every one. */
const SPOOFING_RE =
  /[\u00ad\u034f\u061c\u115f\u1160\u17b4\u17b5\u180e\u200b-\u200f\u2028-\u202e\u2060-\u2064\u2066-\u2069\u3164\ufe00-\ufe0f\ufeff\uffa0\u{e0000}-\u{e007f}\u{e0100}-\u{e01ef}]/u

/**
 * fieldRewrite is `internal/safetext`'s own `fieldRewrite`, which the CLI
 * applies to a title before it stores it (`titleFlag`'s `safetext.Field`):
 * every whitespace character becomes a space, every spoofing character is
 * dropped, and the runs of spaces left behind collapse to one.
 *
 * The rest of `safetext.Field` is not mirrored, because it cannot be reached
 * from here: `Strip`'s escape sequences, C0/C1 controls, tab and line
 * separators are refused by `textCharacterProblem` before this runs, and so
 * are the format characters -- which is why the half of the spoofing set that
 * is *not* `Cf` is what this function exists for. `strings.ToValidUTF8` has
 * nothing to do here for the same reason (a lone surrogate is refused first),
 * and neither has `Field`'s cut to `FieldMax`: `MAX_LAUNCH_TITLE_RUNES` is far
 * below it, and a cut title would be one the run does not keep.
 *
 * It is only ever called on a value that passed that cap, so its own loop is
 * bounded by it.
 */
function fieldRewrite(text: string): string {
  let spaced = ''
  for (const character of text) {
    const code = character.codePointAt(0) ?? 0
    if (GO_TRIM_SPACE.has(code)) spaced += ' '
    else if (!SPOOFING_RE.test(character)) spaced += character
  }
  // Splitting on " " alone is exact: every `unicode.IsSpace` character became
  // one, and the only whitespace JavaScript knows that Go's set does not --
  // U+FEFF -- was dropped as a spoofing character.
  return spaced.split(' ').filter((part) => part !== '').join(' ')
}

/** PLAN_TASK_ELEM_RE is one "/"-separated element of a plan task id, and it is
 * `internal/task`'s `PlanTaskElemRE` (`task.PlanTaskProblem`, the list the
 * launcher and the runner share): letters, digits, '.', '_' and '-', starting
 * with a letter or a digit. An allow-list rather than a list of refusals,
 * because the id is shown as text and matched against ids that came out of file
 * names -- a space, a quote, a control or a Unicode look-alike is not one. */
const PLAN_TASK_ELEM_RE = /^[A-Za-z0-9][A-Za-z0-9._-]*$/

/**
 * textCharacterProblem is the rule the title and the plan task share: valid
 * UTF-8, one line, no control character, no format character. It returns the
 * problem as the console's own sentence, or "" when there is none.
 *
 * The UTF-8 check is not a formality: a lone surrogate is not valid UTF-8, and
 * `TextEncoder` would send U+FFFD in its place -- so the child would be given a
 * value the window never showed, which is the mismatch the format-character
 * rule and `checkLaunchTask`'s own check exist to prevent.
 */
function textCharacterProblem(value: string, noun: string): string {
  if (LONE_SURROGATE_RE.test(value)) return `${noun} is not valid UTF-8`
  if (LINE_BREAK_RE.test(value)) return `${noun} is more than one line`
  if (CONTROL_RE.test(value)) return `${noun} carries a control character`
  if (FORMAT_RE.test(value)) return `${noun} carries a format character`
  return ''
}

/**
 * checkLaunchTitle answers whether `--title` may carry this value, and returns
 * the title the CLI would store.
 *
 * Its absence is allowed and is not an empty title: every surface then falls
 * back to the task's first line, exactly as the CLI does when the flag is not
 * passed, and `null` is that absence. Its presence with nothing in it is
 * refused, whitespace included (`titleFlag` trims the flag and
 * `task.TitleProblem` refuses what is left: "is empty (or nothing but
 * whitespace)").
 *
 * The value returned is the one the run stores: the trimmed value with the
 * CLI's own rewrite applied to it (`titleFlag`'s `safetext.Field`, mirrored by
 * `fieldRewrite`: every whitespace character to one space, the spoofing
 * characters dropped). The window is drawn from what this returns, so a title
 * of "a  b" is confirmed and sent as "a b" -- the value the run keeps -- rather
 * than shown as the operator typed it and stored as something else.
 *
 * That rewrite can empty a value the trim did not, so the emptiness check runs
 * a second time on the rewritten one, with the CLI's own sentence for it: a
 * title of nothing but Hangul fillers, joiners or variation selectors is
 * refused here, before a window opens, instead of by the CLI *after* the
 * operator confirmed it.
 *
 * The order is the CLI's: the length and character rules are the ones
 * `task.TitleProblem` applies to the **trimmed** flag, and `safetext.Field`
 * runs after them. Checking the rewritten value instead would admit a title of
 * 100 characters plus a variation selector, which the CLI counts as 101 and
 * refuses.
 */
export function checkLaunchTitle(title: string | null | undefined): LaunchValue<string | null> {
  if (title === null || title === undefined) return { ok: true, value: null }
  const trimmed = trimTaskText(title)
  if (trimmed === '') return { ok: false, reason: 'the title is empty (or nothing but whitespace)' }
  const runes = [...trimmed].length
  if (runes > MAX_LAUNCH_TITLE_RUNES) {
    return {
      ok: false,
      reason: `the title is ${String(runes)} characters, over the ${String(MAX_LAUNCH_TITLE_RUNES)} this console sends`,
    }
  }
  const problem = textCharacterProblem(trimmed, 'the title')
  if (problem !== '') return { ok: false, reason: problem }
  const stored = fieldRewrite(trimmed)
  // `safetext.Field`'s result holds no leading, trailing or doubled space, so
  // the CLI's own `strings.TrimSpace(o.title) == ""` is this test.
  if (stored === '') {
    return {
      ok: false,
      reason: 'the title is empty (or nothing but whitespace and invisible characters)',
    }
  }
  return { ok: true, value: stored }
}

/**
 * checkLaunchPlanTask answers whether `--plan-task` may carry this value, and
 * returns the id the CLI would store.
 *
 * Its absence is allowed: the flag is optional to the CLI too, and a run
 * without it plans nothing. A present value is held to the CLI's own shape for
 * it (`task.PlanTaskProblem`) and not to the title's prose rules -- `<plan>/<task>`,
 * at least two elements, none of them empty, "." or "..", every element in
 * `PLAN_TASK_ELEM_RE`, no leading "/" -- because that is the list the launcher
 * applies and the runner applies again when it reads the task back: a value
 * this admitted and the CLI refused would fail the launch *after* the operator
 * confirmed the window, which is the outcome this module exists to prevent.
 * The id is a shape and not prose, so a space (which a title may have) is not
 * one, and the value is trimmed like the flag's own `strings.TrimSpace`.
 */
export function checkLaunchPlanTask(planTask: string | null | undefined): LaunchValue<string | null> {
  if (planTask === null || planTask === undefined) return { ok: true, value: null }
  const trimmed = trimTaskText(planTask)
  if (trimmed === '') return { ok: false, reason: 'the plan task is empty' }
  const bytes = new TextEncoder().encode(trimmed).length
  if (bytes > MAX_LAUNCH_PLAN_TASK_BYTES) {
    return {
      ok: false,
      reason: `the plan task is ${String(bytes)} bytes, over the ${String(MAX_LAUNCH_PLAN_TASK_BYTES)} this console sends`,
    }
  }
  const problem = textCharacterProblem(trimmed, 'the plan task')
  if (problem !== '') return { ok: false, reason: problem }
  if (trimmed.startsWith('/')) return { ok: false, reason: 'the plan task is an absolute path' }
  const elements = trimmed.split('/')
  if (elements.length < 2) {
    return { ok: false, reason: 'the plan task must name a task in a plan, as <plan>/<task>' }
  }
  for (const element of elements) {
    if (element === '') return { ok: false, reason: 'the plan task has an empty path element' }
    if (element === '.' || element === '..') {
      return { ok: false, reason: `the plan task has a ${JSON.stringify(element)} path element` }
    }
    if (!PLAN_TASK_ELEM_RE.test(element)) {
      return {
        ok: false,
        reason: `the plan task has ${JSON.stringify(element)}, where letters, digits, '.', '_' and '-' are allowed`,
      }
    }
  }
  return { ok: true, value: trimmed }
}

/**
 * displayTaskText is the task as the confirmation draws it: the same text with
 * every format character stripped, so no bidi override can reorder the
 * paragraph a person is confirming.
 *
 * **This is the displayed copy only.** The task that goes to the child is the
 * one `checkLaunchTask` returned, character for character: the run keeps the
 * task verbatim, format characters included, and only this displayed copy
 * strips them -- a window that showed a different text from the one the run
 * keeps would be the lie the refusal above exists to avoid.
 */
export function displayTaskText(task: string): string {
  return task.replace(/\p{Cf}/gu, '')
}

// ---------------------------------------------------------------------------
// The repository and its branch.
// ---------------------------------------------------------------------------

/** LAUNCH_PROVIDERS is the set the conversion admits, and it is
 * `internal/config/validate.go`'s `Providers` exactly: a record naming another
 * forge is not one this app builds a `--repo` from. */
const LAUNCH_PROVIDERS: readonly string[] = Object.freeze(['github', 'bitbucket'])

/** LAUNCH_REPO_SEGMENT_RE is `internal/task`'s `repoRE` segment class, which is
 * the CLI's own grammar for an `owner/name`: anything else is a value the CLI's
 * canonical form could not have produced. */
const LAUNCH_REPO_SEGMENT_RE = /^[A-Za-z0-9._-]+$/

/**
 * canonicalSegment is one `owner` or `name` of the CLI's own grammar
 * (`repoRE`'s class), minus three spellings this conversion refuses although
 * that grammar admits them:
 *
 *   - `.` and `..`, which are path elements and not names.
 *   - **a segment beginning with `-`**, because what this function builds is a
 *     word of a command line: a word, or a part of one, that begins with a
 *     hyphen is a flag to whatever reads it next, and no forge names a
 *     repository that way.
 *   - **a name ending in `.git`**, in any case, because the CLI's own canonical
 *     form *strips* that suffix (`task.CanonicalRepo` lowers the identity and
 *     trims `.git`), so passing the record through would run on
 *     `owner/name` while the window said `owner/name.git` -- the same class of
 *     mismatch as a base branch that is not the one the run is cut from.
 */
function canonicalSegment(segment: string, isName: boolean): boolean {
  if (!LAUNCH_REPO_SEGMENT_RE.test(segment)) return false
  if (segment === '.' || segment === '..') return false
  if (segment.startsWith('-')) return false
  return !(isName && /\.git$/i.test(segment))
}

/**
 * launchRepoArgument converts one recorded identity into the `owner/name` the
 * argv's `--repo` carries: `<provider>/<owner>/<name>` from the installation's
 * records, with the provider segment dropped.
 *
 * The provider must be one of the two this tree serves, and what is left must
 * be a canonical `owner/name` -- exactly two segments of the CLI's own alphabet,
 * neither of them `.` or `..` (`canonicalSegment`'s own rules beside those) --
 * which is re-checked here rather than trusted: the record comes from a state
 * file on this machine, and `..` in a repository argument is a path the CLI
 * would resolve somewhere else.
 */
export function launchRepoArgument(recorded: string): LaunchValue<string> {
  const parts = recorded.split('/')
  if (parts.length !== 3) {
    return { ok: false, reason: `${JSON.stringify(recorded)} is not a provider and an owner/name` }
  }
  const [provider = '', owner = '', name = ''] = parts
  if (!LAUNCH_PROVIDERS.includes(provider)) {
    return { ok: false, reason: `${JSON.stringify(recorded)} names ${JSON.stringify(provider)}, which is not a provider this console launches on` }
  }
  if (!canonicalSegment(owner, false) || !canonicalSegment(name, true)) {
    return { ok: false, reason: `${JSON.stringify(recorded)} is not a provider and an owner/name` }
  }
  return { ok: true, value: `${owner}/${name}` }
}

/**
 * LaunchRecords is the part of an installation the launch reads a base branch
 * from: the installation's own recorded branch and every repository record it
 * carries (`installlist.ts`'s `DiscoveredInstallation`, kept structural so the
 * decoder's type is what the launch passes in).
 */
export interface LaunchRecords {
  readonly baseBranch: string | null
  readonly repositories: readonly { readonly repository: string; readonly baseBranch: string | null }[]
}

/**
 * recordedBaseBranch resolves the branch a launch of `repository` is cut from:
 * the repository's own recorded `repos.<r>.base_branch`, else the
 * installation's own `base_branch`, else a refusal.
 *
 * Both are layers of the CLI's own merged order that a launch can be cut from
 * *and* read (`config.EffectiveBaseBranch`): the layer above them is the
 * checkout's own committed config, which a launch never has (its working
 * directory is the app's own, empty), and the published policy node is behind a
 * bucket this app does not open. So neither is a fallback here, and the refusal
 * names the two settings an operator can move -- and never the repository's
 * committed `git` block, which they can set without moving this row.
 *
 * The identity is converted first (`launchRepoArgument`), whose refusal is
 * returned as it stands: a repository this app cannot name is not one it can
 * name a setting for. The caller has already established that the installation
 * records the repository at all; this answers which branch, not whether.
 *
 * **A `null` is not a branch that is missing, it is a document that does not
 * report branches.** `config list --json` carries `base_branch` only from the
 * release that adds it, and an older CLI's document has no such key at all: a
 * reader that read that absence as `""` would answer "this installation records
 * no base branch ... set `repos.<r>.base_branch`" to an operator whose config
 * records it, on a machine where the setting already is, and the sentence would
 * be about the wrong thing entirely (`console/docs/json-contract.md` §6.8: "a
 * reader must keep the two apart"). So the two absences get two sentences, and
 * this one says what is actually wrong -- the CLI in the record is too old for
 * the read -- rather than what the console guessed.
 */
export function recordedBaseBranch(installation: LaunchRecords, repository: string): LaunchValue<string> {
  const converted = launchRepoArgument(repository)
  if (!converted.ok) return converted
  const row = installation.repositories.find((record) => record.repository === repository)
  const rowBranch = row === undefined ? undefined : row.baseBranch
  if (installation.baseBranch === null || rowBranch === null) {
    return {
      ok: false,
      reason:
        'this CLI does not report base branches: its `config list --json` has no `base_branch`, so the console ' +
        `cannot say which branch ${repository} would be cut from`,
    }
  }
  const recorded = rowBranch === undefined || rowBranch === '' ? installation.baseBranch : rowBranch
  if (recorded === '') {
    return {
      ok: false,
      reason:
        `the installation records no base branch for ${repository}: set repos.${converted.value}.base_branch ` +
        `in its project config, or the project's own base_branch`,
    }
  }
  return { ok: true, value: recorded }
}

/**
 * checkLaunchBranch answers whether the branch `recordedBaseBranch` resolved is
 * one a launch may be cut from, and it is the last check before the window: the
 * branch comes from a config file the app does not write, and it ends up in the
 * argv the operator confirms (`--ref` is excluded, so the CLI resolves it from
 * the same layers -- but the window shows it, and a value the window shows as a
 * branch must be one the CLI would accept as a ref).
 *
 * Its rules are the ones a git ref name has, and no more than the console can
 * check without re-implementing `git check-ref-format`: **it is non-empty** (a
 * branch is what the repository is cut from, and an empty one is a resolution
 * that failed rather than a branch named ""); **it does not begin with `-`**,
 * because a leading dash is the one spelling that would be read as a flag if it
 * ever reached an argv and a git ref may not begin with one anyway; **it carries
 * no control character and no format character** (`\p{Cc}`/`\p{Cf}`), the same
 * class `textCharacterProblem` refuses in a title and for the same reason -- a
 * newline or a bidi override can rewrite the line the operator is confirming;
 * and **it is at most 4096 characters** (`MAX_LAUNCH_RESULT_STRING`, the bound
 * the decoder holds the CLI's own branch string to, so a value read from a
 * config and a value read from a reply are held to one limit).
 *
 * It is deliberately not a branch-name grammar: the CLI is the authority on
 * what `--ref` takes, and a console that refused `release/1.2` or a name with a
 * `~` in it would refuse a branch the run would have used. What is here is what
 * the console can be sure about -- the classes that must never be displayed or
 * passed, and the length the rest of this file already bounds.
 */
export function checkLaunchBranch(branch: string): LaunchValue<string> {
  if (branch === '') {
    return { ok: false, reason: 'the recorded base branch is empty' }
  }
  if ([...branch].length > MAX_LAUNCH_RESULT_STRING) {
    return {
      ok: false,
      reason: `the recorded base branch is longer than ${String(MAX_LAUNCH_RESULT_STRING)} characters`,
    }
  }
  if (branch.startsWith('-')) {
    return { ok: false, reason: 'the recorded base branch begins with `-`, which a git ref may not do' }
  }
  if (CONTROL_RE.test(branch)) {
    return { ok: false, reason: 'the recorded base branch carries a control character' }
  }
  if (FORMAT_RE.test(branch)) {
    return { ok: false, reason: 'the recorded base branch carries a format character' }
  }
  return { ok: true, value: branch }
}

// ---------------------------------------------------------------------------
// The record a launch is cut from.
// ---------------------------------------------------------------------------

/**
 * LaunchRecordFacts is the part of the stored installation, and of the rail's
 * row for it, that a launch's identity is made of. It is deliberately the
 * *facts* and not either of the two objects (`installationstate.ts`'s `RowFacts`
 * is the same shape for the same reason): this module is the pure-rules module
 * and the entry point is the only place the record and the row exist together.
 *
 * Every field is one whose change alters what a `fugaro run` for this name
 * would do -- and **only** such a field. In particular the rail's `listed` flag
 * and its live `checkouts` list are *not* in here: both are answers of the CLI's
 * own `config list` read, which can fail for one read and succeed for the next,
 * and an identity that moved with a transient read failure would decline a
 * launch the operator had already confirmed, drawn as their own cancel with no
 * sentence naming the read.
 *
 * The rail's `saved` flag is not in here either, and it is not an omission: both
 * call sites answer `false` for an unsaved row **before** they build these facts
 * (a launch is not cut for a name this console does not carry, and the
 * answer-time read closes as a cancel), so the flag is true of every set of facts
 * this console can hold -- a field that can never differ is a field a reader
 * would take for a distinction.
 */
export interface LaunchRecordFacts {
  /** the stored record's own id: a removal and a re-add under the same name is
   * a new record, and possibly one with the same binary path */
  readonly id: string
  readonly project: string
  readonly binaryPath: string
  readonly distro: string
  readonly configPath: string
  readonly checkout: string
}

/**
 * launchFingerprint is the identity of the record a launch was cut from, as one
 * string a caller freezes and a test compares.
 *
 * **It exists because "the name is still saved" is not "the installation is
 * still the one this launch is for".** A record removed and added again under
 * the same name is a new `id` -- and possibly the same binary path, which is why
 * the id is in here -- and an operator who re-chooses the binary, the
 * distribution or the checkout has changed what a `fugaro run` for that name
 * would do. The entry point freezes this string into the launch and compares a
 * fresh one at the answer (`launchStillRecorded`), so the comparison is against
 * what the window was opened *for* and not against the name it was opened
 * under.
 */
export function launchFingerprint(facts: LaunchRecordFacts): string {
  return JSON.stringify([
    facts.id,
    facts.project,
    facts.binaryPath,
    facts.distro,
    facts.configPath,
    facts.checkout,
  ])
}

// ---------------------------------------------------------------------------
// The reply.
// ---------------------------------------------------------------------------

/** LaunchRecipe is the recipe the CLI says the run uses, as it printed it. */
export interface LaunchRecipe {
  readonly name: string
  readonly source: string
}

/** LaunchProjectLayer is the project layer the stored task carries, as far as
 * the CLI's own view of it goes: which one it was, never its text. */
export interface LaunchProjectLayer {
  readonly sha256: string
  readonly generation: number | null
}

/**
 * LaunchStatus is what the CLI says happened, drawn as a fact rather than as an
 * error: `already-launched` means the CLI's claim already holds this run id.
 */
export type LaunchStatus = 'launched' | 'already-launched'

/** LaunchResult is `internal/cli/run.go`'s `launchResult` as this console may
 * draw it: the values the window shows, and nothing the decoder did not
 * recognise. Every field the CLI emits is here. */
export interface LaunchResult {
  readonly project: string
  readonly run: string
  readonly repo: string
  readonly runId: string
  readonly branch: string
  readonly ref: string
  readonly execution: string
  readonly logUrl: string
  readonly status: LaunchStatus
  readonly pr: number | null
  readonly previousRun: string
  readonly recipe: LaunchRecipe | null
  readonly projectLayer: LaunchProjectLayer | null
}

/** LAUNCH_RESULT_KEYS is every key `launchResult` marshals: the seven always
 * present, and the six `omitempty` ones. A key outside this set is a reply from
 * a CLI this decoder does not know, and the whole reply is refused. */
const LAUNCH_RESULT_KEYS: readonly string[] = Object.freeze([
  'project',
  'run',
  'repo',
  'run_id',
  'branch',
  'ref',
  'execution',
  'log_url',
  'status',
  'pr',
  'previous_run',
  'recipe',
  'project_layer',
])

const LAUNCH_RESULT_REQUIRED_STRINGS: readonly string[] = Object.freeze([
  'project',
  'run',
  'repo',
  'run_id',
  'branch',
  'execution',
])

/**
 * decodeLaunchResult reads `fugaro run --json`'s reply, closed: the keys are the
 * ones above, the types are the ones the CLI writes, `status` is one of the two
 * words, and **any unknown key refuses the whole reply** rather than being
 * ignored (rule 11). Every string is kept as text and bounded; nothing here is
 * read as a sentence, a path or a link.
 */
export function decodeLaunchResult(text: string): LaunchValue<LaunchResult> {
  let value: unknown
  try {
    value = JSON.parse(text) as unknown
  } catch {
    return { ok: false, reason: 'the CLI’s launch reply is not JSON' }
  }
  if (!isRecord(value)) return { ok: false, reason: 'the CLI’s launch reply is not a JSON object' }
  for (const key of Object.keys(value)) {
    if (!LAUNCH_RESULT_KEYS.includes(key)) {
      return { ok: false, reason: `the CLI’s launch reply carries a field this console does not know: ${JSON.stringify(key)}` }
    }
  }
  const status = value['status']
  if (status !== 'launched' && status !== 'already-launched') {
    return { ok: false, reason: 'the CLI’s launch reply does not say whether the run launched' }
  }
  const strings: Record<string, string> = {}
  for (const key of [...LAUNCH_RESULT_REQUIRED_STRINGS, 'ref', 'log_url', 'previous_run']) {
    const field = resultString(value, key, LAUNCH_RESULT_REQUIRED_STRINGS.includes(key))
    if (field === null) return { ok: false, reason: `the CLI’s launch reply carries a \`${key}\` this console does not understand` }
    strings[key] = field
  }
  const pr = resultCount(value, 'pr')
  if (pr === undefined) return { ok: false, reason: 'the CLI’s launch reply carries a `pr` this console does not understand' }
  const recipe = resultRecipe(value['recipe'])
  if (recipe === undefined) return { ok: false, reason: 'the CLI’s launch reply carries a `recipe` this console does not understand' }
  const projectLayer = resultProjectLayer(value['project_layer'])
  if (projectLayer === undefined) {
    return { ok: false, reason: 'the CLI’s launch reply carries a `project_layer` this console does not understand' }
  }
  return {
    ok: true,
    value: Object.freeze({
      project: strings['project'] ?? '',
      run: strings['run'] ?? '',
      repo: strings['repo'] ?? '',
      runId: strings['run_id'] ?? '',
      branch: strings['branch'] ?? '',
      ref: strings['ref'] ?? '',
      execution: strings['execution'] ?? '',
      logUrl: strings['log_url'] ?? '',
      status,
      pr,
      previousRun: strings['previous_run'] ?? '',
      recipe,
      projectLayer,
    }),
  }
}

/** resultString reads one string field of the reply: `null` when it is there
 * and is not a string this decoder will hold, `''` when it is absent (the
 * `omitempty` fields) and a refusal from the caller when `required` and absent.
 * A value past `MAX_LAUNCH_RESULT_STRING` is refused rather than cut: it is a
 * string the CLI printed, and half of one is a value no screen may show. */
function resultString(value: Record<string, unknown>, key: string, required: boolean): string | null {
  const field = value[key]
  if (field === undefined || field === null) return required ? null : ''
  if (typeof field !== 'string' || field.length > MAX_LAUNCH_RESULT_STRING) return null
  return field
}

/** resultCount reads one non-negative integer field: `null` when absent
 * (`omitempty`), `undefined` when it is there and is not one. */
function resultCount(value: Record<string, unknown>, key: string): number | null | undefined {
  const field = value[key]
  if (field === undefined || field === null) return null
  if (typeof field !== 'number' || !Number.isInteger(field) || field < 0) return undefined
  return field
}

/** resultRecipe reads the reply's `recipe`: `null` when absent, `undefined`
 * when the object is not the CLI's own two strings, and then the value. */
function resultRecipe(field: unknown): LaunchRecipe | null | undefined {
  if (field === undefined || field === null) return null
  if (!isRecord(field)) return undefined
  for (const key of Object.keys(field)) {
    if (key !== 'name' && key !== 'source') return undefined
  }
  const name = boundedText(field['name'])
  const source = boundedText(field['source'])
  if (name === null || source === null) return undefined
  return Object.freeze({ name, source })
}

/** resultProjectLayer reads the reply's `project_layer`: `null` when absent,
 * `undefined` when it is not the CLI's own shape, and then the value. The
 * generation is the one field the CLI may leave out (`omitempty`). */
function resultProjectLayer(field: unknown): LaunchProjectLayer | null | undefined {
  if (field === undefined || field === null) return null
  if (!isRecord(field)) return undefined
  for (const key of Object.keys(field)) {
    if (key !== 'sha256' && key !== 'generation') return undefined
  }
  const sha256 = boundedText(field['sha256'])
  if (sha256 === null) return undefined
  const generation = resultCount(field, 'generation')
  if (generation === undefined) return undefined
  return Object.freeze({ sha256, generation })
}

/** boundedText is one string of a nested object, `null` when it is absent, of
 * another type, or past the bound. */
function boundedText(field: unknown): string | null {
  if (typeof field !== 'string' || field.length > MAX_LAUNCH_RESULT_STRING) return null
  return field
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}
