// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The hand-scrub of a real `fugaro watch --json` document (owner decision
// 2026-10-19: "record from the real installation, scrub by hand").
//
// The three stall / kill / cap cases cannot be recorded without cloud writes and
// stay invented. THIS is for `normal`: the shape is the real CLI's (every key, its
// type, its order, which optional keys are present are all copied from the
// recording), and every VALUE that could identify the installation is replaced
// by an invented one. Every invented value is a constant, so the same recording
// scrubs to the same values -- except the clock, which is shifted by an offset
// DRAWN AT RANDOM FOR THIS RUN (`randomTimeShiftMs`; owner review of PR #643,
// item 2). The offset is applied to every timestamp the scrub writes, is never
// printed, and is never written down, so the committed fixture's real instant
// is not recoverable from it: a constant here would have been undone by adding
// it back to the fixture's `now`.
//
// Deny by default: a key this file does not know is a refusal, never a pass
// through, so a CLI that grows a field fails the scrub instead of leaking it.
// The same rule governs the values it copies (VOCAB, VERIFY_*): each list is
// derived from the Go source that writes the field, and
// `TestDocsDesktopConsoleScrubVocabularyMatchesTheGoSource` fails when the
// source and this file disagree.
//
// What is replaced, and by what:
//   project                     example-project  (then `<project>` via the redactor)
//   repo                        example-owner/example-repo, -2, -3 ... by first sight
//   slug                        github-com-example-owner-example-repo, -2 ...
//   run                         YYYYMMDD-HHMMSS-eeNN, the time being document.now minus the
//                               run's own age_seconds, so IDs agree with the clock
//   title                       "# Task: example task N" (the CLI's "# Task:" prefix is kept)
//   requested_by, kill.by       redacted@example.invalid
//   pr_url                      https://example.invalid/example-owner/example-repo/pull/N
//   workflow, recipe            example-workflow, example-recipe
//   every dollar amount         an invented fixed amount; totals are recomputed so the
//                               document's invariants hold (see `recompute`)
//   models                      claude-sonnet-4-5-20250929 (the real one was a `vendor/model` string,
//                               which the redactor reads as a repository)
// Kept as the CLI wrote them, ONLY when the value is in the fixed CLI vocabulary
// enumerated below (anything else is a refusal, never a pass-through): the enums
// and flags (mode, stage, round, health, deadline, auth, verify, status, outcome,
// connection.state), the numbers (ages, burn) and queued_note (a fixed sentence).
// The clock is SHIFTED by this run's random offset so no real instant survives:
// now, day, kill.at and the run IDs all come from the shifted clock, and every
// age_seconds stays as it was, so they stay consistent.
//
// `verify` is the one kept value whose middle the CLI fills from the repository
// it ran in, so it has two rules: `isVerifySummary`, which the scrub applies and
// which takes any text in those slots, and `isKnownVerifySummary`, which the
// fixture's own scan applies and which pins the glob list to the CLI's own
// vocabulary (a test name has none -- see VERIFY_SLOTS). The scrub's rule has to
// be the open one: a real recording's test names are the writer's own output,
// and a vocabulary that refused one would refuse an ordinary run, which is the
// failure the lists below exist to avoid.
//
// Nothing about a removed value is ever persisted, not even a hash: a bare hash of
// a low-entropy value (a small integer, an amount, a repo-style name) is recovered
// by dictionary. `removedValues` hands the removed values to the caller in memory
// only; the command line prints a COUNT.

import type { JSONDoc } from '../../src/source/source'

/** SCRUB_PROJECT is the invented project name; the redactor turns it into the marker. */
export const SCRUB_PROJECT = 'example-project'

/** INVENTED_SPEND is the invented per-run spend, in order, then repeating. */
export const INVENTED_SPEND: readonly number[] = Object.freeze([0.15, 0.4, 0.11, 0.03, 0.1, 0.07, 0.03, 0.04, 0.01])
/** INVENTED_CAP_USD is the invented daily cap, in place of the real one. */
export const INVENTED_CAP_USD = 50
/** INVENTED_LEASE_USD is the invented outstanding lease, in place of the real
 * one: what the installation currently holds, which is a real dollar figure
 * whatever the amount. */
export const INVENTED_LEASE_USD = 0.2
/** INVENTED_MAX_PARALLEL is the invented concurrent-run cap, in place of the
 * real one: a cap the operator chose for the installation, so it is their
 * configuration rather than a shape, and it is replaced like the daily cap. */
export const INVENTED_MAX_PARALLEL = 4
/**
 * TIME_SHIFT_RANGE_MS bounds the clock shift drawn for one run: plus or minus
 * 400 days. Wide enough that the offset cannot be guessed, and narrow enough
 * that the invented clock stays a plausible one. Exported so a test can pin the
 * ends of the range rather than restate the number.
 */
export const TIME_SHIFT_RANGE_MS = 400 * 24 * 60 * 60 * 1000

/**
 * randomTimeShiftMs draws this run's clock offset, in milliseconds.
 *
 * Crypto-random, drawn once per scrub, added to every timestamp the scrub
 * writes. It is deliberately NOT exported as a value and never printed: the
 * point (owner review of PR #643, item 2) is that the committed fixture cannot
 * be unshifted, because the offset that made it exists only for the length of
 * the run. The draw is 53 bits wide but the mapping below is onto 2R + 1
 * offsets -- 69,120,000,001 of them, about 2^36 -- so the width costs nothing
 * and claims nothing: 2^36 offsets still make two runs landing on the same one
 * not a thing a test can flake on.
 */
export function randomTimeShiftMs(): number {
  const draw = new Uint32Array(2)
  globalThis.crypto.getRandomValues(draw)
  // 53 bits, which is what a double holds exactly: the top 32 and the top 21 of
  // the next draw, so the fraction is uniform over [0, 1) rather than the range
  // a single 32-bit word can reach.
  return timeShiftFromDraw((((draw[0] ?? 0) * 2 ** 21) + ((draw[1] ?? 0) >>> 11)) / 2 ** 53)
}

/**
 * timeShiftFromDraw maps a fraction of the unit interval onto the offset, and is
 * the only place that mapping lives: the draw above is the only caller in
 * production, and a test can pin the ends without one.
 *
 * The range is the CLOSED interval [-TIME_SHIFT_RANGE_MS, +TIME_SHIFT_RANGE_MS],
 * of 2R + 1 integer offsets, so the mapping is `floor(unit * (2R + 1)) - R`:
 * `unit = 0` is -R, and the largest fraction below 1 is +R. Rounding instead of
 * flooring -- which is what this used to do -- overshot by one at the top,
 * because `round(x)` for x just below 2R + 1 is 2R + 1, giving R + 1.
 */
export function timeShiftFromDraw(unit: number): number {
  const span = 2 * TIME_SHIFT_RANGE_MS + 1
  return Math.floor(unit * span) - TIME_SHIFT_RANGE_MS
}

/**
 * VOCAB is the fixed CLI vocabulary per field, derived from the Go source that
 * writes it. A value outside its list fails the scrub closed. Each list names
 * its writer; `TestDocsDesktopConsoleScrubVocabularyMatchesTheGoSource` reads
 * this object back out of the file and fails when the two drift apart.
 */
export const VOCAB = {
  // internal/budget/model.go: ModeObserve / ModeEnforce are the only two the
  // watch document can carry (internal/watch/build.go's View.Mode).
  mode: ['enforce', 'observe'],
  connectionState: ['live', 'stale', 'offline', 'refused', 'polling'],
  // Every name a runs[].stage can carry, which is the run's REGISTRY entry and
  // nothing else: internal/watch/build.go writes `dash(e.Stage)` into the row,
  // and the entry is given a stage by exactly two writers -- the literal it is
  // created with, `bootstrap` (internal/runner/budget_session.go's
  // registryEntry), and beginBudgetStage, whose only caller is the runner's
  // `r.stage(ctx, name, ...)`, so the rest of them are that helper's call
  // sites. `queued` is not the entry at all: internal/watch/queued.go gives it
  // to a runs-bucket row that has no entry yet. The runner's other stage
  // strings are other documents: `render`, `writeback`, `check` and `finalize`
  // are `r.rec.Stage` (the run record in result.json) and `provision` is the
  // name provisioning records its own time under, so none can appear here.
  stage: ['bootstrap', 'queued', 'plan', 'implement', 'review_first', 'review', 'fix'],
  // internal/config/defaults.go's `AgentAuths` — the one list the validator,
  // the layer's own auth key and the launcher's check of a run's spec all read
  // — plus the `-` internal/watch/build.go's `dash` writes for an empty one.
  auth: ['vertex', 'api-key', 'oauth', 'cascade', '-'],
  health: ['ok', 'silent', 'lost'],
  deadline: ['ok', 'near', 'over'],
  status: ['succeeded', 'failed', 'halted', 'cancelled', 'infra_error'],
  outcome: ['ready', 'draft', 'none', 'reviewed', 'artifacts'],
} as const

/** VERIFY_KINDS mirrors internal/verify/verify.go's Kind constants. */
export const VERIFY_KINDS = ['build', 'test', 'lint'] as const
/** VERIFY_STATUSES mirrors verify.go's `Record.Summary`: the failure word is
 * upper case there, and the passing one lower. */
export const VERIFY_STATUSES = ['passed', 'FAILED'] as const
/** VERIFY_RERUN_FLAG is verify.go's own suffix for a `--rerun-failed` run. */
export const VERIFY_RERUN_FLAG = '--rerun-failed'
/** VERIFY_CLEAN_TREE is verify.go's note for a run that found the tree dirty. */
export const VERIFY_CLEAN_TREE = 'working tree not clean, so this run cannot mark the PR ready: commit first'
/** VERIFY_WARNING_PREFIX is verify.go's own opening for the one warning it
 * writes -- `rec.Warning = "no JUnit reports found under " + strings.Join(
 * s.Reports, ", ")` -- so the sentence is the CLI's, and only the globs after it
 * come from the repository. */
export const VERIFY_WARNING_PREFIX = 'no JUnit reports found under '
/**
 * VERIFY_CLIP_RUNES is internal/runner's `verifySummaryRunes`: the registry
 * clips the summary to this many runes before the document carries it, so a
 * long line reaches the fixture cut in the middle of a word.
 */
export const VERIFY_CLIP_RUNES = 120

/**
 * VerifySlot names the two places `Record.Summary` writes text this file cannot
 * know: the failed and flaky name lists, and the report globs after the warning
 * sentence.
 */
type VerifySlot = 'names' | 'globs'

/**
 * VERIFY_TAIL is the writer's tail, part by part, after the `(exit <code>`
 * VERIFY_HEAD matches: `Record.Summary` in internal/verify/verify.go, in the
 * order it appends them. `parts` are the literal texts, `digits` a `%d`, and
 * `free` a slot the writer fills from the repository -- the test names its
 * reports carry, and the globs it was configured to read. The first group is the
 * counts: only a test run writes them (VERIFY_TEST_TAIL_FROM) and a test run
 * always writes them (VERIFY_TEST_GROUPS_REQUIRED).
 */
type VerifyPart = { readonly text: string } | { readonly digits: true } | { readonly free: VerifySlot }
const VERIFY_TAIL: readonly (readonly VerifyPart[])[] = [
  [{ text: ', ' }, { digits: true }, { text: ' tests, ' }, { digits: true }, { text: ' failures' }],
  [{ text: ': ' }, { free: 'names' }],
  [{ text: '; flaky: ' }, { free: 'names' }],
  [{ text: '; timed out' }],
  [{ text: '; warning: ' + VERIFY_WARNING_PREFIX }, { free: 'globs' }],
  [{ text: '; ' + VERIFY_CLEAN_TREE }],
  [{ text: ')' }],
]

/**
 * VERIFY_REPORT_GLOBS is the vocabulary the `globs` slot can carry: the report
 * patterns `internal/config/defaults.go` sets for `commands.reports` (a JUnit
 * glob and one under the build test-results directory) -- which are what
 * `internal/verify/verify.go` joins, in configuration order, into the warning
 * sentence.
 *
 * A repository may configure others, and `doublestar` accepts any glob, so no
 * finite list can enumerate what the CLI might write there. These two are what
 * it writes with no configuration, which is what a recording from a stock
 * repository carries. Exported so the Go drift test can hold them against
 * defaults.go.
 */
export const VERIFY_REPORT_GLOBS = ['**/junit*.xml', '**/build/test-results/**/*.xml'] as const

/** isGlobList reports whether `value` is a comma-separated list of
 * VERIFY_REPORT_GLOBS, or -- when `cut` -- a prefix of one: the clip may stop
 * inside a glob, between a whole glob and `, `, or at the slot's very start. */
function isGlobList(value: string, cut: boolean): boolean {
  for (const glob of VERIFY_REPORT_GLOBS) {
    if (value === glob) return true
    if (value.startsWith(glob)) {
      const rest = value.slice(glob.length)
      if (rest === ',') return cut
      if (rest.startsWith(', ') && isGlobList(rest.slice(2), cut)) return true
    }
    if (cut && glob.startsWith(value)) return true
  }
  return false
}

/**
 * VERIFY_SLOTS is the vocabulary of the slots that HAVE one, twice over: `full`
 * matches a whole piece, `cut` one the clip stopped inside.
 *
 * Only the report globs are such a slot. They are the two `commands.reports`
 * defaults (VERIFY_REPORT_GLOBS), so a glob the CLI was not given is refused.
 *
 * The test-name slots are not in this table, and cannot be. A name is the
 * report's own text: `internal/verify/junit.go` builds each ID as
 * `classname + "." + name`, verbatim from the JUnit XML, so a pytest
 * parametrisation (`test_mod.py::test_add[1-2]`), a name carrying a space or a
 * bare comma, the `#01` `go-junit-report` appends to a duplicated subtest, and
 * the escape `internal/pluginwire`'s Printable writes in place of a control or
 * bidi character are all ordinary names. Any character class narrow enough to
 * look like a test name refuses real ones, and a recording this matcher refuses
 * cannot be committed -- so the name slots are OPEN in both matchers, and the
 * guard for them is the fixture scan's forbidden-substring pass, which runs over
 * the whole verify value, name list included.
 *
 * Only `isKnownVerifySummary` uses this table, and only for the globs slot. The
 * scrub's own rule (`isVerifySummary`) takes any text in either slot on purpose:
 * a real recording's report globs come from its own configuration, so a
 * vocabulary that refused them would refuse an ordinary run, which is the
 * failure this file's lists exist to avoid.
 */
const VERIFY_SLOTS: Record<'globs', { full: (v: string) => boolean; cut: (v: string) => boolean }> = {
  globs: {
    full: (v) => isGlobList(v, false),
    cut: (v) => isGlobList(v, true),
  },
}
/** VERIFY_TEST_TAIL_FROM is the index of the first tail group a non-test kind
 * does not write: only a test run carries the counts, and `Record.Summary`
 * writes them under `if r.Kind == KindTest` alone, so for a build or a lint the
 * group is not optional but absent -- `TestDocsDesktopConsoleScrubVocabulary-
 * MatchesTheGoSource` pins that condition, because its tail scan skips `if`
 * lines (a condition names what it guards without writing it). */
const VERIFY_TEST_TAIL_FROM = 1

/**
 * VERIFY_TEST_GROUPS_REQUIRED is how many leading tail groups a test run always
 * writes: the counts, and `Record.Summary` writes them unconditionally for
 * KindTest. The clip cannot take them away either -- they end far inside
 * VERIFY_CLIP_RUNES runes -- so a test line that stops before them, or part way
 * through them, is one the writer cannot produce: the walk may not fall back on
 * "the writer had nothing to say here", which is what it does for every optional
 * segment.
 */
const VERIFY_TEST_GROUPS_REQUIRED = 1

/**
 * VERIFY_HEAD matches the writer's head exactly: the kind, the rerun flag, the
 * record number, the status and the exit code that opens the parenthesised tail.
 * It is never itself cut short, which is a consequence rather than a choice: the
 * whole head is under VERIFY_CLIP_RUNES runes, so a clip can only land after it.
 *
 * The exit code is signed because `internal/procgroup/procgroup.go` leaves it at
 * -1 when the process never started or was killed by a signal -- its own timeout
 * kills the group -- and `internal/verify/verify.go` records that code as
 * `ExitCode` verbatim, so a timed-out verify writes `(exit -1; timed out)`.
 */
export const VERIFY_HEAD = new RegExp(
  `^fugaro verify (${VERIFY_KINDS.join('|')})( ${VERIFY_RERUN_FLAG})? #\\d+: (${VERIFY_STATUSES.join('|')}) \\(exit -?\\d+`,
)

/**
 * isVerifySummary is the rule for the run's `verify` field: `-` (what
 * `internal/watch/build.go`'s `dash` writes for an empty one), or one
 * `Record.Summary` line -- complete, or cut at VERIFY_CLIP_RUNES runes, which
 * is what the registry does to it.
 *
 * It is a matcher rather than one regular expression because the value is a
 * PREFIX of the writer's line, not the line: every optional tail part may be
 * absent (the writer appends it only when it has something to say), and the cut
 * may land inside any of them, so "is this a prefix of what the writer can
 * write" is the question, and this walks the parts to answer it. The counts of
 * a test run are the exception in both directions: the writer always writes
 * them, and the clip can never cut them, so a test line without them is one no
 * run can produce (VERIFY_TEST_GROUPS_REQUIRED).
 *
 * The slots are open: whatever the repository put in them, the writer wrote.
 */
export function isVerifySummary(value: string): boolean {
  return verifySummary(value, false)
}

/**
 * isKnownVerifySummary is the same question with the one pinnable slot pinned
 * (VERIFY_SLOTS): the report globs after the warning sentence are held to the
 * CLI's own vocabulary. The name list is not, and cannot be -- see VERIFY_SLOTS.
 *
 * The scrub does not use it, for the reason VERIFY_SLOTS gives. The fixture's
 * own scan does, because the globs slot is the one place a value could carry
 * text this file never vouched for: `isVerifySummary` alone would let a glob the
 * CLI was not configured with ride in the `; warning:` slot and pass the
 * positive allow-list.
 */
export function isKnownVerifySummary(value: string): boolean {
  return verifySummary(value, true)
}

function verifySummary(value: string, pinned: boolean): boolean {
  if (value === '-') return true
  // A control character is not something the writer produces (internal/runner
  // runs the summary through pluginwire.Printable first).
  if (/[\u0000-\u001f\u007f]/.test(value)) return false
  const head = VERIFY_HEAD.exec(value)
  if (head === null) return false
  const rest = value.slice(head[0].length)
  // A test run's counts are the one group that is not up to the writer's
  // discretion, so the tail starts there and the group is required whole; a
  // build or a lint never writes them, so its tail starts after them.
  const test = head[1] === 'test'
  return isPrefixOfTail(test ? VERIFY_TAIL : VERIFY_TAIL.slice(VERIFY_TEST_TAIL_FROM), rest, pinned,
    test ? VERIFY_TEST_GROUPS_REQUIRED : 0)
}

/** isPrefixOfTail reports whether `rest` is a prefix of some line the groups can
 * render: every optional group may be absent, every literal may be cut short, a
 * digit run is any number of digits (a cut may land inside it), and a free slot
 * is either open -- it absorbs whatever is left -- or pinned to its vocabulary,
 * in which case it takes a whole piece or the piece a cut left. The first
 * `required` groups are the exception: the writer always writes them and the cut
 * can never reach them, so they must be there and whole. Memoised on (group,
 * part, offset): the free slots make the walk branch, and the same positions are
 * reached by many routes. */
function isPrefixOfTail(
  groups: readonly (readonly VerifyPart[])[], rest: string, pinned: boolean, required = 0,
): boolean {
  const width = rest.length + 1
  const memo = new Map<number, boolean>()
  const walkGroup = (group: number, at: number): boolean => {
    const parts = groups[group]
    if (parts === undefined) return at === rest.length
    const key = group * width + at
    const hit = memo.get(key)
    if (hit !== undefined) return hit
    // The writer appends an optional segment only when it has something to say,
    // so it may be absent; else it is present, possibly with the cut inside it.
    // A REQUIRED group is the one segment the writer always writes, so the walk
    // may not skip it -- which is how `fugaro verify test #5: passed (exit 0)`,
    // a line verify.go cannot produce, used to be accepted.
    const out = (group < required ? false : walkGroup(group + 1, at)) || walkPart(group, 0, at)
    memo.set(key, out)
    return out
  }
  const walkPart = (group: number, part: number, at: number): boolean => {
    const parts = groups[group]!
    if (part === parts.length) return walkGroup(group + 1, at)
    const piece = parts[part]!
    const tail = rest.slice(at)
    if ('text' in piece) {
      // A cut may land inside a literal -- but not in a required group, where
      // the whole literal has to be there.
      if (group < required || tail.length > piece.text.length) {
        return tail.startsWith(piece.text) && walkPart(group, part + 1, at + piece.text.length)
      }
      return piece.text.startsWith(tail)
    }
    if ('digits' in piece) {
      const run = /^\d+/.exec(tail)?.[0] ?? ''
      // A cut may land between the text before a `%d` and the digits
      // themselves (`(exit 0, ` is a prefix of `(exit 0, 0 tests, ...)`), but
      // only there: with digits still to come, the run is never empty. In a
      // required group even that much is missing text, so the whole number has
      // to be there.
      if (run === '') return group >= required && tail === '' && walkPart(group, part + 1, at)
      if (group < required) return walkPart(group, part + 1, at + run.length)
      for (let n = 1; n <= run.length; n++) if (walkPart(group, part + 1, at + n)) return true
      return false
    }
    // A free slot is open -- it absorbs whatever the repository put there --
    // unless the slot has a vocabulary and this is the pinned matcher: only the
    // globs slot does (VERIFY_SLOTS), because a test ID is the report's own text
    // and no character class can hold it without refusing real ones. No required
    // group has a free slot (the counts are two `%d`s and two literals), so
    // `required` does not have to be read here.
    const slot = pinned && piece.free === 'globs' ? VERIFY_SLOTS.globs : undefined
    if (slot === undefined) {
      for (let n = 0; n <= tail.length; n++) if (walkPart(group, part + 1, at + n)) return true
      return false
    }
    // A whole piece of the slot's vocabulary, then the rest of the line.
    for (let n = tail.length; n > 0; n--) {
      if (slot.full(tail.slice(0, n)) && walkPart(group, part + 1, at + n)) return true
    }
    // Or the cut stopped inside the slot, or exactly at its start (an empty
    // tail here is a prefix of a line whose slot the writer filled).
    return tail === '' || slot.cut(tail)
  }
  return walkGroup(0, 0)
}
const ROUND_SHAPE = /^(-|\d{1,2})$/
/** QUEUED_NOTE_SHAPE is the one queued_note sentence the CLI writes for a slow bucket. */
export const QUEUED_NOTE_SHAPE = /^queued runs unavailable: the runs bucket did not answer within \d{1,3}s$/
const KNOWN_TOP = [
  'project', 'mode', 'day', 'now', 'connection', 'total', 'repos', 'runs', 'finished', 'ready', 'queued_note',
]

type Obj = Record<string, unknown>

function isObj(value: unknown): value is Obj {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function refuse(path: string, what: string): never {
  throw new Error(`scrub: ${what} at ${path}`)
}

function round6(n: number): number {
  return Math.round(n * 1e6) / 1e6
}

type Rule = (v: unknown, path: string) => unknown

const num: Rule = (v, path) => (typeof v === 'number' && Number.isFinite(v) ? v : refuse(path, 'not a number'))
/** nullableNum is `num` for a Go field that is a pointer: the document writes
 * `null` for it, and refusing that would refuse an ordinary run. */
const nullableNum: Rule = (v, path) => (v === null ? null : num(v, path))
const bool: Rule = (v, path) => (typeof v === 'boolean' ? v : refuse(path, 'not a boolean'))
const oneOf = (allowed: readonly string[]): Rule => (v, path) =>
  typeof v === 'string' && allowed.includes(v) ? v : refuse(path, 'a value outside the CLI vocabulary')
/** shaped accepts a value only if its shape says so -- a plain regular expression
 * or a matcher (isVerifySummary), which is a function because the verify line is
 * a prefix of the writer's output rather than a whole one. */
const shaped = (ok: RegExp | ((value: string) => boolean)): Rule => (v, path) =>
  typeof v === 'string' && (typeof ok === 'function' ? ok(v) : ok.test(v)) ? v : refuse(path, 'a value outside the CLI vocabulary')
const invent = (value: unknown): Rule => (v, path) => (typeof v === 'string' ? value : refuse(path, 'not a string'))

/** applyRules copies a key only through its own rule; a key with no rule is refused. */
function applyRules(obj: Obj, path: string, rules: Readonly<Record<string, Rule>>): Obj {
  const out: Obj = {}
  for (const key of Object.keys(obj)) {
    const rule = rules[key]
    if (rule === undefined) refuse(`${path}.${key}`, 'an unknown key')
    out[key] = rule(obj[key], `${path}.${key}`)
  }
  return out
}

const BURN_RULES: Readonly<Record<string, Rule>> = { known: bool, usd_per_min: num, fast: bool }
/**
 * CONNECTION_REASONS is the connection header's own text: internal/watch/state.go
 * sets `access refused` beside ConnRefused and `connecting` beside the offline
 * kind a view starts in. Its third branch is the transport's error, one-lined
 * and clipped there -- free text, with no sentence known to be safe.
 */
export const CONNECTION_REASONS = ['access refused', 'connecting'] as const
/** INVENTED_CONNECTION_REASON is what that free text becomes. */
export const INVENTED_CONNECTION_REASON = 'example connection reason'
/** connectionReason keeps the CLI's two sentences and replaces anything else:
 * the transport's error is free text, so a rule that only accepted the two
 * would refuse an ordinary recording of an offline run -- which is what a scrub
 * with no rule for `reason` at all did. Nothing of the original survives the
 * replacement, so a sentence riding there is dropped rather than published. */
const connectionReason: Rule = (v, path) => {
  if (typeof v !== 'string') return refuse(path, 'not a string')
  return (CONNECTION_REASONS as readonly string[]).includes(v) ? v : INVENTED_CONNECTION_REASON
}
const CONNECTION_RULES: Readonly<Record<string, Rule>> = {
  state: oneOf(VOCAB.connectionState),
  age_seconds: num,
  reason: connectionReason,
}

/** shiftIso moves one timestamp by this run's offset (see randomTimeShiftMs).
 * The offset is a parameter rather than a module constant on purpose: it must
 * not be reachable from the module, or a reader could undo the shift. */
function shiftIso(iso: string, path: string, shiftMs: number): string {
  const t = Date.parse(iso)
  if (!Number.isFinite(t)) return refuse(path, 'not a timestamp')
  return new Date(t + shiftMs).toISOString().slice(0, 19) + 'Z'
}

/** instant is `shiftIso` as a rule: a wire timestamp, moved by this run's
 * offset. `waiting_until` is the second field it applies to (a kill's `at` is
 * the first), and in Go it is a plain string with `omitempty` rather than a
 * pointer -- absence is the "not waiting" state -- so a null here is a document
 * the writer cannot produce and is refused rather than copied through. */
const instant = (shiftMs: number): Rule => (v, path) =>
  typeof v === 'string' ? shiftIso(v, path, shiftMs) : refuse(path, 'not a timestamp')

function runStamp(now: string, ageSeconds: number): string {
  const t = new Date(Date.parse(now) - Math.round(ageSeconds) * 1000)
  const iso = t.toISOString() // 2026-10-19T01:19:30.000Z
  return `${iso.slice(0, 10).replaceAll('-', '')}-${iso.slice(11, 19).replaceAll(':', '')}`
}

/**
 * scrubDocument turns one real document into its invented twin.
 *
 * `shiftMs` is the offset applied to every timestamp; left out, it is drawn for
 * this call (randomTimeShiftMs) and nothing about it survives the call. It is a
 * parameter at all so that a test can hold it still and check the ages still
 * agree with the clock -- it is deliberately not a command-line argument of the
 * tool, because an offset someone can choose is an offset someone can publish.
 */
export function scrubDocument(raw: unknown, shiftMs: number = randomTimeShiftMs()): JSONDoc {
  if (!isObj(raw)) refuse('$', 'the document is not an object')
  for (const key of Object.keys(raw)) if (!KNOWN_TOP.includes(key)) refuse(`$.${key}`, 'an unknown key')

  const repoNames = new Map<string, number>()
  const repoIndex = (real: unknown, path: string): number => {
    if (typeof real !== 'string') return refuse(path, 'a repo that is not a string')
    let i = repoNames.get(real)
    if (i === undefined) {
      i = repoNames.size + 1
      repoNames.set(real, i)
    }
    return i
  }
  const inventedRepo = (i: number): string => (i === 1 ? 'example-owner/example-repo' : `example-owner/example-repo-${i}`)
  const inventedSlug = (i: number): string =>
    i === 1 ? 'github-com-example-owner-example-repo' : `github-com-example-owner-example-repo-${i}`

  const now = shiftIso(typeof raw['now'] === 'string' ? raw['now'] : refuse('$.now', 'no now'), '$.now', shiftMs)
  const out: Obj = {}
  for (const key of Object.keys(raw)) {
    const value = raw[key]
    const path = `$.${key}`
    switch (key) {
      case 'project':
        out[key] = SCRUB_PROJECT
        break
      case 'mode': out[key] = oneOf(VOCAB.mode)(value, path); break
      case 'now': out[key] = now; break
      case 'day':
        if (typeof value !== 'string') refuse(path, 'not a string')
        out[key] = now.slice(0, 10)
        break
      case 'connection':
        if (!isObj(value)) refuse(path, 'not an object')
        out[key] = applyRules(value, path, CONNECTION_RULES)
        break
      case 'queued_note': out[key] = shaped(QUEUED_NOTE_SHAPE)(value, path); break
      case 'total':
        out[key] = scrubTotalOrRepo(value, path, undefined, shiftMs)
        break
      case 'repos': {
        if (!Array.isArray(value)) refuse(path, 'repos is not an array')
        out[key] = value.map((repo, i) => {
          if (!isObj(repo)) return refuse(`${path}[${i}]`, 'a repo that is not an object')
          const n = repoIndex(repo['repo'], `${path}[${i}].repo`)
          return scrubTotalOrRepo(repo, `${path}[${i}]`, n, shiftMs)
        })
        break
      }
      case 'runs': {
        if (!Array.isArray(value)) refuse(path, 'runs is not an array')
        out[key] = value.map((run, i) =>
          scrubRun(run, `${path}[${i}]`, i, now, repoIndex, inventedRepo, inventedSlug, shiftMs))
        break
      }
      case 'finished': case 'ready': {
        if (!Array.isArray(value)) refuse(path, `${key} is not an array`)
        out[key] = value.map((run, i) =>
          scrubFinished(run, `${path}[${i}]`, i, now, repoIndex, inventedRepo, inventedSlug))
        break
      }
    }
  }
  return recompute(out)
}

function copyKeys(obj: Obj, path: string, allowed: readonly string[]): void {
  for (const key of Object.keys(obj)) if (!allowed.includes(key)) refuse(`${path}.${key}`, 'an unknown key')
}

function scrubKill(kill: unknown, path: string, shiftMs: number): unknown {
  if (kill === null) return null
  if (!isObj(kill)) return refuse(path, 'a kill that is neither null nor an object')
  copyKeys(kill, path, ['by', 'at', 'reason'])
  const out: Obj = {}
  for (const key of Object.keys(kill)) {
    if (key === 'by') out[key] = 'redacted@example.invalid'
    else if (key === 'at') out[key] = shiftIso(String(kill[key]), `${path}.at`, shiftMs)
    else out[key] = 'example reason'
  }
  return out
}

function scrubTotalOrRepo(value: unknown, path: string, repoN: number | undefined, shiftMs: number): Obj {
  if (!isObj(value)) return refuse(path, 'not an object')
  const inventedRepo = `example-owner/example-repo${repoN === 1 ? '' : `-${repoN}`}`
  const rules: Record<string, Rule> = {
    slug: invent(`github-com-example-owner-example-repo${repoN === 1 ? '' : `-${repoN}`}`),
    repo: invent(inventedRepo),
    counted_usd: num, spent_usd: num, notional_usd: num, runs: num, run_hours: num, // recomputed below
    // `daily_cap_usd` and `percent` are the two *float64 of
    // internal/watch/json.go's JSONTotal and JSONRepo: the document writes null
    // for both when the repository has no cap. `leased_usd` is a plain float64
    // there -- counted minus spent, floored at zero -- and a recording older
    // than the key carries none at all; its amount is invented like the other
    // dollar figures.
    percent: nullableNum,
    leased_usd: (v, p) => (v === null ? null : typeof v === 'number' ? INVENTED_LEASE_USD : refuse(p, 'not a number')),
    daily_cap_usd: (v, p) => (v === null ? null : typeof v === 'number' ? INVENTED_CAP_USD : refuse(p, 'not a number')),
    burn: (v, p) => (isObj(v) ? applyRules(v, p, BURN_RULES) : refuse(p, 'not an object')),
    kill: (v, p) => scrubKill(v, p, shiftMs),
  }
  // `max_parallel` is `JSONTotal`'s and not `JSONRepo`'s -- the concurrent-run
  // cap a launch is checked against bounds the project's launches, not one
  // repository's -- so it is named for `$.total` alone, which is the call with
  // no `repoN`; a `repos[]` entry carrying it is a document no version of the
  // CLI writes and stays refused as an unknown key. The field is a `*int` with
  // no `omitempty`, so `null` -- a document whose writer could read no cap -- is
  // a value to keep, and a number is the operator's own configuration and is
  // invented like the daily cap beside it.
  if (repoN === undefined) {
    rules['max_parallel'] = (v, p) =>
      (v === null ? null : typeof v === 'number' ? INVENTED_MAX_PARALLEL : refuse(p, 'not a number'))
  }
  return applyRules(value, path, rules)
}

/**
 * JOB_RULES is the five keys 0.9.0 added to **both** live run shapes: the Cloud
 * Run job a run is in, the (profile, provider, account) its name was computed
 * from, and who launched it (`internal/watch/json.go`, JSONRun and
 * JSONFinished). One table, because both shapes carry them and both must be
 * scrubbed the same way -- `applyRules` copies a key only through its own rule,
 * so until this table existed the scrub refused every 0.9.0 recording **whole**,
 * as `an unknown key at $.runs[0].job`, and the committed fixture predates the
 * fields and could not have caught it.
 *
 * `account` and `launched_by` are people: the account a run used and who
 * started it, the same kind of value as a kill's `by` and `runview.Row`'s
 * `oauth_account`, and replaced with the same invented address. `job`, `profile`
 * and `provider` are identifiers a policy wrote -- a label, the class
 * `workflow` carries -- and are replaced like `workflow`.
 */
const JOB_RULES: Readonly<Record<string, Rule>> = {
  job: invent('example-job'),
  profile: invent('example-profile'),
  provider: invent('example-provider'),
  account: invent('redacted@example.invalid'),
  launched_by: invent('redacted@example.invalid'),
  plan_task: invent('example-plan/example-task'),
}

function scrubRun(
  run: unknown, path: string, i: number, now: string, repoIndex: (r: unknown, p: string) => number,
  inventedRepo: (n: number) => string, inventedSlug: (n: number) => string, shiftMs: number,
): Obj {
  if (!isObj(run)) return refuse(path, 'a run that is not an object')
  const n = repoIndex(run['repo'], `${path}.repo`)
  const age = run['age_seconds']
  return applyRules(run, path, {
    run: invent(`${runStamp(now, typeof age === 'number' ? age : 0)}-ee${String(i + 1).padStart(2, '0')}`),
    slug: invent(inventedSlug(n)),
    repo: invent(inventedRepo(n)),
    title: invent(`# Task: example task ${i + 1}`),
    stage: oneOf(VOCAB.stage),
    round: shaped(ROUND_SHAPE),
    verify: shaped(isVerifySummary),
    models: (v, p) => (v === '-' ? '-' : typeof v === 'string' ? 'claude-sonnet-4-5-20250929' : refuse(p, 'not a string')),
    recipe: invent('example-recipe'),
    auth: oneOf(VOCAB.auth),
    notional: bool,
    spent_usd: (v, p) => (v === null ? null : typeof v === 'number' ? INVENTED_SPEND[i % INVENTED_SPEND.length] : refuse(p, 'not a number')),
    // AgeSecs is *float64 in JSONRun: a queued row the registry has not
    // described yet carries null, which is an ordinary document, not a fault.
    age_seconds: nullableNum,
    health: oneOf(VOCAB.health),
    deadline: oneOf(VOCAB.deadline),
    halted: invent('example halt reason'),
    queued: bool,
    stuck: bool,
    workflow: invent('example-workflow'),
    requested_by: invent('redacted@example.invalid'),
    ...JOB_RULES,
    // GV2.5: a live run carries its draft pull request number once it has one,
    // so this table needs a rule for it or a current recording is refused whole
    // (`applyRules` copies a key only through its own rule). A number, invented
    // the way the finished shape's own `pr_number` is, and never a URL: the
    // rule replaces a value, it does not reconstruct one.
    pr_number: () => 100 + i,
    credential: invent('example-credential'),
    waiting_until: instant(shiftMs),
  })
}

function scrubFinished(
  run: unknown, path: string, i: number, now: string, repoIndex: (r: unknown, p: string) => number,
  inventedRepo: (n: number) => string, inventedSlug: (n: number) => string,
): Obj {
  if (!isObj(run)) return refuse(path, 'a finished run that is not an object')
  const n = repoIndex(run['repo'], `${path}.repo`)
  const age = run['age_seconds']
  return applyRules(run, path, {
    run: invent(`${runStamp(now, typeof age === 'number' ? age : 0)}-ff${String(i + 1).padStart(2, '0')}`),
    slug: invent(inventedSlug(n)),
    repo: invent(inventedRepo(n)),
    title: invent(`# Task: example finished task ${i + 1}`),
    status: oneOf(VOCAB.status),
    outcome: oneOf(VOCAB.outcome),
    failed: bool,
    pr_url: invent(`https://example.invalid/example-owner/example-repo/pull/${100 + i}`),
    pr_number: () => 100 + i,
    age_seconds: num,
    ...JOB_RULES,
  })
}

/** recompute makes the invented amounts agree with each other the way the CLI's
 * do: a run's spend is a part of its repo's, a repo's of the total, `counted`
 * is spent plus notional, `percent` is counted over the cap, and `run_hours` and
 * `runs` come from the rows. The real recording's figures are not used. */
function recompute(doc: Obj): JSONDoc {
  const runs = (doc['runs'] as Obj[]) ?? []
  const repos = (doc['repos'] as Obj[]) ?? []
  const total = doc['total'] as Obj
  const perSlug = new Map<string, number>()
  let spentAll = 0
  let notionalAll = 0
  let hours = 0
  for (const run of runs) {
    const spent = typeof run['spent_usd'] === 'number' ? run['spent_usd'] : 0
    spentAll += run['notional'] === true ? 0 : spent
    notionalAll += run['notional'] === true ? spent : 0
    const slug = run['slug'] as string
    perSlug.set(slug, (perSlug.get(slug) ?? 0) + (run['notional'] === true ? 0 : spent))
    hours += typeof run['age_seconds'] === 'number' ? run['age_seconds'] / 3600 : 0
  }
  const fill = (block: Obj, spent: number, notional: number): void => {
    block['spent_usd'] = round6(spent)
    block['notional_usd'] = round6(notional)
    block['counted_usd'] = round6(spent)
    if (typeof block['daily_cap_usd'] === 'number') block['percent'] = round6((spent / block['daily_cap_usd']) * 100)
  }
  // A repo's spend is its runs' spend; the CLI counts only billed spend against the cap.
  const reposTotal = { spent: 0, notional: 0 }
  for (const repo of repos) {
    const spent = perSlug.get(repo['slug'] as string) ?? 0
    const notional = round6(
      runs.filter((r) => r['slug'] === repo['slug'] && r['notional'] === true).reduce((a, r) => a + (r['spent_usd'] as number), 0),
    )
    fill(repo, spent, notional)
    reposTotal.spent += spent
    reposTotal.notional += notional
  }
  fill(total, repos.length > 0 ? reposTotal.spent : spentAll, repos.length > 0 ? reposTotal.notional : notionalAll)
  total['runs'] = runs.length
  total['run_hours'] = hours
  return doc as JSONDoc
}

function leaves(value: unknown, into: Set<string>): void {
  if (typeof value === 'string') into.add(value)
  else if (typeof value === 'number') into.add(String(value))
  else if (Array.isArray(value)) for (const v of value) leaves(v, into)
  else if (isObj(value)) for (const v of Object.values(value)) leaves(v, into)
}

/** removedValues is every string and number leaf of the RAW document that does not
 * survive, as a leaf, into the scrubbed one: what the scan must never find. It is
 * returned in memory to the caller (a test, through an injected callback) and is
 * never written anywhere, and nothing derived from it (no hash) is either. */
export function removedValues(raw: unknown, scrubbed: unknown): string[] {
  const before = new Set<string>()
  const after = new Set<string>()
  leaves(raw, before)
  leaves(scrubbed, after)
  return [...before].filter((v) => !after.has(v) && v !== '')
}
