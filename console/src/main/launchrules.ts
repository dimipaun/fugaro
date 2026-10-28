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
//      point: widening it for a title would widen it for `--repo` too.
//   3. **The reply is decoded closed.** A field the decoder does not know
//      refuses the whole reply rather than being ignored, because the values it
//      carries are drawn as the CLI's own facts.

/** MAX_LAUNCH_TASK_BYTES is the task's cap, in bytes of UTF-8 *after* the
 * trim: the same 16 KiB the CLI's own task carries, so a task this console
 * sends is one the CLI keeps whole. */
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
 * GO_TRIM_SPACE is `strings.TrimSpace`'s set (`unicode.IsSpace`), and it is
 * written out rather than reached for through `String.prototype.trim`: the two
 * are not the same set. JavaScript also trims U+FEFF, which Go does not, and
 * JavaScript does *not* trim U+0085, which Go does -- so a task the console
 * trimmed with `trim()` and the CLI trimmed with `strings.TrimSpace` would be
 * two different texts, and the window would count bytes the run did not keep.
 */
const GO_TRIM_SPACE = '\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000'

/** A surrogate that is not half of a pair is not valid UTF-8: `TextEncoder`
 * would replace it with U+FFFD, and the task that reached the run would be a
 * different text from the one the operator confirmed. */
const LONE_SURROGATE_RE = /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/

/**
 * trimTaskText is the CLI's `strings.TrimSpace`, which trims the leading and
 * trailing run of `unicode.IsSpace` characters and nothing inside the text.
 */
function trimTaskText(text: string): string {
  return text.replace(new RegExp(`^[${GO_TRIM_SPACE}]+`), '').replace(new RegExp(`[${GO_TRIM_SPACE}]+$`), '')
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

/** LINE_BREAK_RE is what makes a value more than one line. The newline and the
 * carriage return are `Cc` already; U+2028 and U+2029 are not, and both end a
 * line in every rendered document a person reads. */
const LINE_BREAK_RE = /[\n\r\u2028\u2029]/u

interface WordRules {
  /** the sentence's noun: "the title", "the plan task" */
  readonly noun: string
  /** the cap, and its unit, as the refusal names them */
  readonly cap: number
  readonly unit: 'character' | 'byte'
}

const TITLE_RULES: WordRules = { noun: 'the title', cap: MAX_LAUNCH_TITLE_RUNES, unit: 'character' }
const PLAN_TASK_RULES: WordRules = { noun: 'the plan task', cap: MAX_LAUNCH_PLAN_TASK_BYTES, unit: 'byte' }

/**
 * checkLaunchWord is the one rule the title and the plan task share: non-empty
 * when present, one line, no control character, no format character, and inside
 * the cap of its own kind. A space and a leading `-` are allowed -- they are
 * what `ARGUMENT_RE` refuses and what makes these two words of their own kind.
 */
function checkLaunchWord(value: string, rules: WordRules): LaunchValue<string | null> {
  if (value === '') return { ok: false, reason: `${rules.noun} is empty` }
  const size = rules.unit === 'character' ? [...value].length : new TextEncoder().encode(value).length
  if (size > rules.cap) {
    return {
      ok: false,
      reason: `${rules.noun} is ${String(size)} ${rules.unit}s, over the ${String(rules.cap)} this console sends`,
    }
  }
  if (LINE_BREAK_RE.test(value)) return { ok: false, reason: `${rules.noun} is more than one line` }
  if (CONTROL_RE.test(value)) return { ok: false, reason: `${rules.noun} carries a control character` }
  if (FORMAT_RE.test(value)) return { ok: false, reason: `${rules.noun} carries a format character` }
  return { ok: true, value }
}

/**
 * checkLaunchTitle answers whether `--title` may carry this value. Its absence
 * is allowed and is not an empty title: every surface then falls back to the
 * task's first line, exactly as the CLI does when the flag is not passed, and
 * `null` is that absence.
 */
export function checkLaunchTitle(title: string | null | undefined): LaunchValue<string | null> {
  if (title === null || title === undefined) return { ok: true, value: null }
  return checkLaunchWord(title, TITLE_RULES)
}

/**
 * checkLaunchPlanTask answers whether `--plan-task` may carry this value, by
 * the same rules and its own cap. Its absence is allowed: the flag is optional
 * to the CLI too, and a run without it plans nothing.
 */
export function checkLaunchPlanTask(planTask: string | null | undefined): LaunchValue<string | null> {
  if (planTask === null || planTask === undefined) return { ok: true, value: null }
  return checkLaunchWord(planTask, PLAN_TASK_RULES)
}

/**
 * displayTaskText is the task as the confirmation draws it: the same text with
 * every format character stripped, so no bidi override can reorder the
 * paragraph a person is confirming.
 *
 * **This is the displayed copy only.** The task that goes to the child is the
 * one `checkLaunchTask` returned, character for character: the CLI sanitises
 * the task's stored copy itself, and a window that showed a different text from
 * the one the run keeps would be the lie the refusal above exists to avoid.
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

function canonicalSegment(segment: string): boolean {
  return LAUNCH_REPO_SEGMENT_RE.test(segment) && segment !== '.' && segment !== '..'
}

/**
 * launchRepoArgument converts one recorded identity into the `owner/name` the
 * argv's `--repo` carries: `<provider>/<owner>/<name>` from the installation's
 * records, with the provider segment dropped.
 *
 * The provider must be one of the two this tree serves, and what is left must
 * be a canonical `owner/name` -- exactly two segments of the CLI's own alphabet,
 * neither of them `.` or `..` -- which is re-checked here rather than trusted:
 * the record comes from a state file on this machine, and `..` in a repository
 * argument is a path the CLI would resolve somewhere else.
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
  if (!canonicalSegment(owner) || !canonicalSegment(name)) {
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
  readonly baseBranch: string
  readonly repositories: readonly { readonly repository: string; readonly baseBranch: string }[]
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
 */
export function recordedBaseBranch(installation: LaunchRecords, repository: string): LaunchValue<string> {
  const converted = launchRepoArgument(repository)
  if (!converted.ok) return converted
  const row = installation.repositories.find((record) => record.repository === repository)
  const recorded = row !== undefined && row.baseBranch !== '' ? row.baseBranch : installation.baseBranch
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
