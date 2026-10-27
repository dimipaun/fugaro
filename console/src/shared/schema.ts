// The document shapes the console admits, and nothing else (desktop-console
// plan Task 2.0; design section 5.1 rules 3 and 7; console/docs/json-contract.md
// sections 2 and 5).
//
// `watch --json` writes one of exactly two things:
//
//   * an ordinary **`JSONDoc`** -- the live feed, with a connection, a total, a
//     repository block per repository and the runs under each (contract
//     section 2);
//   * a **`watchDegradedDoc`**, which is `lsDoc` (contract section 5)
//     *embedded* plus `degraded: true` and a `notice`, written when there is no
//     budget backend at all. Nothing in it has a cap, a burn rate, a kill state
//     or a repository block, because none of those exist without the database.
//
// The two are discriminated on the **`degraded` key**, and that is the whole of
// the feed's half of this module's structure: a document that carries
// `degraded: true` is the second shape, a document that carries none of the live
// keys is neither, and neither is admitted by accident. A screen that rendered
// the degraded document
// as a live one would draw a `0` for every field the degraded shape cannot
// carry, which is the design's first UI rule broken in the worst possible way:
// it would say a project had spent nothing all day when what happened is that
// the console could not ask.
//
// **What this module refuses, and why refusing is the point.** A document with
// an unknown top-level key, a missing key the screens read, a string key that
// is not a string, an array key that is not an array, a `degraded` that is not
// `true` -- each is a document this console does not know how to read, and each
// is refused **by name** (`runs[0].round`, `missing`) rather than rendered
// half-empty. Task 3.2 turns that refusal into the skew banner; a decode that
// silently dropped the unknown half would turn a CLI the console does not
// understand into a screen that looks fine.
//
// **The two tolerant axes, named.** Numbers are read through `readNumber`, so a
// number that is absent, `null` or not a number becomes an *unavailable
// reading* with a sentence rather than a refusal: the contract's own `*float64`
// convention makes `null` a normal value ("no cap set", "no spend recorded"),
// and the availability model already says what to draw for it. And the keys
// *inside* `runstore.Cost` are not closed, because that struct is versioned
// independently of this one and the console reads six fields of it; the live
// half of this file closes every object it decodes. Everything else is closed.
//
// **A one-shot read's document is not a shape of the feed.** `plan status
// --json` is the first of those to be decoded here (GV2.1), and it gets its own
// door rather than a third member of `ConsoleDocument`: the two documents carry
// no common marker, so a feed document is refused by this door for the keys it
// is missing and a plan document is refused as an unknown shape by the feed's.
// The section at the end of this file is that document, and it follows the same
// rules -- closed objects, refusals by name, quantities as readings.
//
// **Nothing here imports anything at runtime**, for the same reason
// `sanitize.ts` does not: the renderer loads this module and has no Node, no
// network and no Electron. `sanitize.test.ts` scans `src/shared/` to keep it
// that way.

import { type Reading, readNumber, unavailable } from './availability'
import { sanitizeText } from './sanitize'
import type { TextClass } from './sanitize'

// ---------------------------------------------------------------------------
// Which text class every string key carries.
//
// This is the map that makes deny-by-default *total*: a wire string key with no
// entry here is a key the display layer has no class for, and `sanitize.test.ts`
// walks every string leaf of the corpus and refuses to let one exist. The keys
// are the contract's own (`console/docs/json-contract.md`), and the classes are
// `sanitize.ts`'s.
// ---------------------------------------------------------------------------

/**
 * WIRE_TEXT_CLASSES is the class of every string key the wire carries that is
 * *text*: something a job or an installation wrote, which must be sanitized
 * before it is drawn.
 *
 * A screen draws a key in the class this map gives it (`app.ts`'s `putText`),
 * **with one deliberate exception, and it is `launched_by`**: the Live and
 * Projects rows draw its *short form* -- the CLI's own cut, the part before an
 * address's first `@` (`shared/launcher.ts`) -- through the `label` class. The
 * class below is a validating one and refuses a dotted local part
 * (`dana.lopez@example.com`, the commonest corporate shape) along with every
 * display name, so the column the owner asked for on 2026-10-18 would read `n/a`
 * for most of the people who use it. The **whole** recorded value is drawn in
 * the Run screen's header, in `person`, exactly as this map says -- so no
 * address is drawn anywhere. `console/README.md`'s class table and
 * `docs/design/desktop-console.md` §6.2 record the decision; `launcher.ts` is
 * the module and carries the reasoning.
 */
export const WIRE_TEXT_CLASSES: Readonly<Record<string, TextClass>> = Object.freeze({
  // The document's own fields.
  project: 'project',
  // The live feed's connection: why the connection is what it is.
  reason: 'sentence',
  // A kill switch's operator and its reason (`JSONKill`).
  by: 'person',
  // Repositories: the bucket key, and `owner/name`.
  slug: 'slug',
  repo: 'repository',
  // A live or queued run.
  run: 'runId',
  run_id: 'runId',
  title: 'sentence',
  stage: 'label',
  round: 'label',
  verify: 'label',
  models: 'label',
  recipe: 'label',
  halted: 'sentence',
  workflow: 'label',
  requested_by: 'person',
  // A run record's own fields (`runview.Row`, the degraded shape).
  oauth_account: 'person',
  // 0.9.0's keys on the live run shapes (`JSONRun`/`JSONFinished`, the Live
  // screen's feed) and on `runview.Row`. `job`, `profile` and `provider` are
  // the Cloud Run job a run is in and the (profile, provider) its name was
  // computed from -- identifiers a policy wrote, drawn as labels, the class
  // `workflow` and `batch` carry. `account` and `launched_by` are people: the
  // account a run used, and who started it, as its own `launch.json` recorded
  // them -- the same kind of value as `requested_by` above, and the class the
  // recorder already gives them (`account` by name, `launched_by` by its `_by`
  // suffix, `src/source/redact.ts`).
  job: 'label',
  profile: 'label',
  provider: 'label',
  account: 'person',
  launched_by: 'person',
  // GV1.2's key on the live run shapes and on `runview.Row`: the plan task the
  // run says it works on, as its own `launch.json` recorded it. An **id** the
  // plan's document spells a node with (`<plan>/<task>`, its shape checked by
  // `task.PlanTaskProblem` where it is written, and where the CLI reads it back:
  // `runview.Join` and `watch.MergeFacts` drop an id that fails it, so the key
  // is absent), so a label -- the class `job`,
  // `profile`, `provider` and `workflow` carry -- and not `person` and not
  // `sentence`: nothing here is prose, and a value that is not an id draws the
  // free-text rules' refusal rather than a link or a path.
  plan_task: 'label',
  // 0.10.0's key on the live run shape (credentials-service C1.21): the **name**
  // -- never the value -- of the credential the run's cascade is spending on.
  // A label, the class an identifier a policy or the service wrote carries
  // (`job`, `profile`, `provider`), and not `person`: it names a token's
  // account, not a person. It goes through the free-text rules like every other
  // label, so a document that carried a *value* here instead of a name draws
  // the token marker -- which is the direction the contract's "never a value"
  // promise has to fail in. `waiting_until`, the other 0.10.0 key, is not here:
  // it is a stamp, and it is in `NON_TEXT_STRING_KEYS`.
  credential: 'label',
  branch: 'branch',
  log_url: 'url',
  // A pull request's URL. It is *text* -- design section 4.3's gap 7 is that
  // the console offers no link built from it, and `sanitize.ts`'s url class is
  // what keeps a `javascript:` one from becoming an attribute.
  pr_url: 'url',
  // The rest of `runview.Row`'s strings: the CLI's own identifiers and short
  // rendered fields, read off the struct (`internal/runview/runview.go:57`).
  // `name` is `project_layer.name` and `rigor.areas[].name` -- the name a
  // repository gave an area rule, which is a label it wrote -- and
  // `base_branch` and `previous_run` are a git ref and a run id, so they carry
  // the classes those are.
  name: 'label',
  batch: 'label',
  execution: 'label',
  // `pushed_head` is a commit digest, and it is deliberately **not** here: the
  // recorder classes it `digest` and keeps a hex value as written
  // (`src/source/redact.ts:409`, "the high-entropy rule would otherwise take
  // every one of them"). A digest handed to the free-text rules -- which is
  // what `label` means in this map -- is drawn as `[redacted-token]`, so the
  // key is in `NON_TEXT_STRING_KEYS` instead, where `sha256` and
  // `config_sha256` already are.
  base_branch: 'branch',
  previous_run: 'runId',
  // `rigor.revision`: the revision the run's mode was read from
  // (`runstore.RigorRecord.Revision`, docs/rigor-modes D25) -- `origin/main`,
  // the same kind of value as `base_branch` above and a string a branch name
  // reached, so it is text and carries that class.
  revision: 'branch',
  // `changed_files.paths[]`: repository-relative paths a run changed (`runstore.ChangedFiles`,
  // console v2 D27). A path can name a client or a person, so it is text a job
  // wrote and goes through the sanitizer; `total` and `truncated` are not strings.
  paths: 'path',
  // Provenance inside `runstore.Cost`: `model_source` is `gateway` or
  // `claude-code`, and `compute_source` is the price table's own line for the
  // figures the estimate came from. The console draws neither today; they are
  // classified as labels rather than exempted, because a string the console
  // *may* draw is a string that goes through the sanitizer when it does.
  model_source: 'label',
  compute_source: 'label',
  // `queued_note` is the one live key the shape may leave out (`omitempty`), and
  // leaving it out of *this* map was a hole with the same shape: a key the
  // corpus omits is a key the classification walk never sees, so the map could
  // claim to be total over the wire while missing exactly the key the corpus
  // does not carry.
  queued_note: 'sentence',
  // The degraded document's own fields.
  notice: 'sentence',
  // `warnings[]`: the lines a human listing prints before its table.
  warnings: 'sentence',
  // The plan status document (GV2.1). Every one of these is repository content
  // -- a graph artifact and the plan, tasks, releases and streams it names --
  // so each is text a job or a checkout wrote and goes through the sanitizer
  // like any other drawn string. `id` is an id the CLI derived from where an
  // artifact sits (a plan) or the artifact's own node key (a task), `artifact`
  // is a path under `docs/`, and the rest are short rendered fields: labels,
  // which is the class `job`, `profile` and the artifact's own `plan` name
  // carry. `mark` is the exception and is a `sentence`: it is the one reason a
  // state is not what its word alone says (`plan.MergeUnknown`), a sentence the
  // CLI fixes rather than composes, and it is drawn as one.
  id: 'label',
  artifact: 'path',
  plan: 'label',
  stream: 'label',
  release: 'label',
  version: 'label',
  theme: 'label',
  names: 'label',
  current: 'label',
  next: 'label',
  tag_prefix: 'label',
  mark: 'sentence',
})

/**
 * NON_TEXT_STRING_KEYS is the closed set of string keys that are **not** text:
 * a value the console compares, sorts or stamps rather than draws. They are
 * listed rather than inferred because the list is the claim -- "these strings
 * carry no text a job wrote" -- and `sanitize.test.ts` holds both halves of it:
 * a key in this list must not be in `WIRE_TEXT_CLASSES`, and every other string
 * key of the corpus must be.
 *
 * `mode`, `state`, `health`, `status`, `outcome`, `auth` and `deadline` are
 * enums the CLI writes from its own constant tables; `day`, `now`, `at` and
 * `created` are timestamps; `model_basis` is the spend basis (`subscription`
 * or `api-list`) and is therefore a *value* the console compares and never a
 * label it draws -- and since C1.16 the split itself is `model_notional_usd`,
 * with the basis only the fallback for a row that carries none (P12).
 */
export const NON_TEXT_STRING_KEYS: readonly string[] = Object.freeze([
  'mode',
  // `rigor.from`: which layer decided the mode (`internal/rigor`'s `Layer`,
  // docs/rigor-modes D24) -- one of the words this build's own table writes,
  // the same kind of value as `mode` beside it -- and `rigor.areas[].mode`,
  // which is that same `mode` vocabulary again. `rigor.at` and the moments of
  // `rigor.areas[]` are `at`, already below.
  'from',
  'state',
  'health',
  'status',
  // `derived` is the read side's own derivation for a run whose record was
  // never finalized (`runview.DerivedPreempted`, the CLI's 2026-10-21 ruling:
  // a preempted Cloud Batch job is derived when the run is read and never
  // written back). It sits beside `status` rather than replacing it -- the row
  // reads `status: "failed"`, `derived: "preempted"` -- and it is an enum the
  // CLI writes from its own one-entry table, the same kind of value `status`
  // and `outcome` are. It is not drawn from free text and never sanitized.
  'derived',
  'outcome',
  'auth',
  'deadline',
  'day',
  'now',
  'at',
  'created',
  'model_basis',
  // Digests and the rest of `runview.Row`'s stamps: a sha256 is a value the
  // console compares, and a timestamp is one it sorts. None of them is text a
  // job wrote, and none is drawn.
  'sha256',
  'config_sha256',
  // `pushed_head` is the commit the push published: a digest, the same kind of
  // value as `sha256` above, and the one the recorder also keeps as written.
  'pushed_head',
  'started_at',
  'pr_status_at',
  // 0.10.0 (credentials-service C1.21): when a run waiting for a credential
  // expects to resume at, RFC 3339 UTC. It is a stamp for the same reason the
  // others are -- an instant a reader compares with `now`, and one whose
  // **presence** is the state ("this run is held up rather than spending")
  // rather than a string to draw. The scrub shifts it with the rest of the
  // clock (`tools/scrub-fixture/scrub.ts`'s `instant` rule, beside `now` and a
  // kill's `at`), so a fixture's copy is a reading and not the wire's own value.
  'waiting_until',
])

/**
 * wireTextClass is the class of a wire key, or `undefined` for a key that is
 * not text. A caller that got `undefined` has a key this module does not know,
 * and the honest thing to do with the value is refuse it -- the display layer
 * never guesses a class.
 */
export function wireTextClass(key: string): TextClass | undefined {
  return WIRE_TEXT_CLASSES[key]
}

// ---------------------------------------------------------------------------
// The refusal.
// ---------------------------------------------------------------------------

/**
 * SchemaRefusalName is *why* a document was refused, one member per way a
 * document can be the wrong shape. It is a name rather than a sentence alone so
 * a test can assert the reason without matching prose, and so the skew banner
 * of Task 3.2 can decide what to say.
 */
export type SchemaRefusalName =
  /** the document is not a JSON object at all */
  | 'not-an-object'
  /** the document carries `degraded` with a value other than `true` */
  | 'degraded-not-true'
  /** the document carries none of the keys either shape has */
  | 'unknown-shape'
  /** a key the shape requires is absent */
  | 'missing'
  /** a key the shape does not have */
  | 'unknown-key'
  /** a key that must be a string is not one */
  | 'not-a-string'
  /** a key that must be an array is not one */
  | 'not-an-array'
  /** a key that must be a boolean is not one */
  | 'not-a-boolean'
  /** a key that must be an RFC 3339 timestamp is not one */
  | 'not-a-timestamp'
  /** a key that must be a number is not one */
  | 'not-a-number'
  /** a key whose value must be one of a closed set of words carries none of them */
  | 'unknown-value'
  /** a live document was expected where a degraded one arrived */
  | 'wrong-shape'

/** SchemaRefusal is one refusal: which document, which field, and why. */
export interface SchemaRefusal {
  /** the wire path, as the contract spells it: `runs[0].round` */
  readonly field: string
  /** what the caller called the document: `the live feed document` */
  readonly where: string
  /** why, as one of the closed names above */
  readonly name: SchemaRefusalName
}

/**
 * Skew is what a screen gets instead of a document. It is deliberately not an
 * empty document: "the console cannot read this" and "the CLI said there is
 * nothing" are different facts, and a screen that drew the second for the first
 * would be the most comfortable lie in the whole console.
 */
export interface Skew {
  readonly name: 'skew'
  /** a sentence the banner shows, in the console's own words */
  readonly because: string
  readonly refusal: SchemaRefusal
}

/** Decoded is the result of admitting a document: a value, or a skew. */
export type Decoded<T> = { readonly ok: true; readonly value: T } | { readonly ok: false; readonly skew: Skew }

/** Refusal is thrown inside this module and caught at its two doors. Nothing
 * else in the console ever sees it -- the doors return `Decoded`. */
class Refusal extends Error {
  constructor(readonly refusal: SchemaRefusal) {
    super(`${refusal.where}: ${refusal.field} is ${refusal.name}`)
  }
}

function refuse(name: SchemaRefusalName, field: string, where: string): never {
  throw new Refusal({ name, field, where })
}

/** skewFor turns a refusal into the skew a screen draws. */
function skewFor(refusal: SchemaRefusal): Skew {
  return {
    name: 'skew',
    because: `${refusal.where} is not one the console reads: ${refusal.field} is ${refusal.name}`,
    refusal,
  }
}

/** at runs a decode and turns its refusal into a skew. */
function at<T>(where: string, decode: () => T): Decoded<T> {
  try {
    return { ok: true, value: decode() }
  } catch (error) {
    if (error instanceof Refusal) return { ok: false, skew: skewFor(error.refusal) }
    throw error
  }
}

// ---------------------------------------------------------------------------
// The readers. Each is one shape check, and each names the field it refused.
// ---------------------------------------------------------------------------

function objectAt(raw: unknown, field: string, where: string): Record<string, unknown> {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return refuse('not-an-object', field, where)
  return raw as Record<string, unknown>
}

/**
 * keyName is an unknown key as the console may draw it.
 *
 * A key the console does not have is **text a document chose**, and it ends up
 * in the skew banner, so it goes through the same sanitizer every other
 * job-written string does: a key shaped like a credential is neutralised, and a
 * key no class admits is not drawn at all. The path prefix (`runs[0]`) is the
 * console's own and is not passed through here.
 */
function keyName(key: string): string {
  const clean = sanitizeText(key, 'label', 'the key')
  return clean.ok ? clean.text : 'a key the console will not draw'
}

/** closed refuses an object that carries a key the shape does not have. It is
 * what makes `{...doc, rogue: 1}` a refusal rather than a document with a field
 * nobody read. */
function closed(raw: Record<string, unknown>, allowed: readonly string[], field: string, where: string): void {
  for (const key of Object.keys(raw)) {
    if (allowed.includes(key)) continue
    const named = keyName(key)
    return refuse('unknown-key', field === '' ? named : `${field}.${named}`, where)
  }
}

/** required is the one door a key the shape needs comes through: absent, and the
 * document is refused by the field's own name. */
function required(raw: Record<string, unknown>, key: string, field: string, where: string): unknown {
  if (!(key in raw)) return refuse('missing', field === '' ? key : `${field}.${key}`, where)
  return raw[key]
}

function stringAt(raw: unknown, field: string, where: string): string {
  if (typeof raw !== 'string') return refuse('not-a-string', field, where)
  return raw
}

/** TIMESTAMP_RE is RFC 3339's date-time, as Go's `time.RFC3339Nano` writes it:
 * a full date, a `T`, a time with optional fractional seconds, and `Z` or an
 * offset. */
const TIMESTAMP_RE = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/

/** timestampAt is a string key that must be a timestamp the console can place
 * in time (`now`). It is refused by field name, as `boolAt` refuses a boolean
 * (review round 3 of PR 494): a `now` the burn history cannot parse is a frame
 * it can neither window nor prune by. */
function timestampAt(raw: unknown, field: string, where: string): string {
  const text = stringAt(raw, field, where)
  if (!TIMESTAMP_RE.test(text) || Number.isNaN(Date.parse(text))) return refuse('not-a-timestamp', field, where)
  return text
}

function boolAt(raw: unknown, field: string, where: string): boolean {
  if (typeof raw !== 'boolean') return refuse('not-a-boolean', field, where)
  return raw
}

function arrayAt(raw: unknown, field: string, where: string): readonly unknown[] {
  if (!Array.isArray(raw)) return refuse('not-an-array', field, where)
  return raw
}

/** optionalText is a string key the shape may leave out (`recipe`, `halted`,
 * `workflow`, `requested_by`, `queued_note`, `pr_url`): absent and `null` both
 * read as the empty string, because the contract says absent means the CLI had
 * nothing to say, not that the value is unknown. */
function optionalText(raw: Record<string, unknown>, key: string, field: string, where: string): string {
  const value = raw[key]
  if (value === undefined || value === null) return ''
  return stringAt(value, field === '' ? key : `${field}.${key}`, where)
}

/** optionalInt is a number key the shape may leave out (`pr_number`): `null` is
 * what "the run has no pull request" looks like, and it is not the same fact as
 * `0`. */
function optionalInt(raw: Record<string, unknown>, key: string): number | null {
  const value = raw[key]
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

/** intAt is the strict axis, for a number that is **structure rather than a
 * quantity**: a key the document must carry and that must be a whole number.
 * A plan node's `level` is the one such key today -- a level is a place in a
 * dependency chain, not a figure anyone reads, so an absent or fractional one
 * is a document this console cannot draw rather than a reading it withholds.
 * Every number a *screen* prints goes through `readingOf` instead. */
function intAt(raw: unknown, field: string, where: string): number {
  if (typeof raw !== 'number' || !Number.isInteger(raw)) return refuse('not-a-number', field, where)
  return raw
}

/** optionalBool reads a key the shape may leave out (`queued`, `stuck`,
 * `compute_estimated`): absent or null means false, which is what the CLI's own
 * `omitempty` means. A value that is there and is not a boolean is refused by
 * field name, as `boolAt` refuses one (review round 2 of PR 494): reading
 * `"true"` as false would draw an estimated figure as a measured one. */
function optionalBool(raw: Record<string, unknown>, key: string, field: string, where: string): boolean {
  const value = raw[key]
  if (value === undefined || value === null) return false
  return boolAt(value, field === '' ? key : `${field}.${key}`, where)
}

/** readingOf is the tolerant axis: every number goes through `readNumber`, so an
 * absent, `null` or unparseable one becomes an unavailable reading with a
 * sentence rather than a refused document. */
function readingOf(raw: Record<string, unknown>, key: string, field: string): Reading {
  return readNumber(raw[key], field === '' ? key : `${field}.${key}`)
}

// ---------------------------------------------------------------------------
// The shapes.
// ---------------------------------------------------------------------------

/** KillView is a kill switch that is ON (`JSONKill`). `null` on the wire is
 * "not killed", which is a measured fact about the document, not an unknown. */
export interface KillView {
  readonly by: string
  readonly at: string
  readonly reason: string
}

/** BurnView is the five-minute rolling burn (`JSONBurn`). `known` is the
 * *state* of `usdPerMin`: when it is false the rate is unknown, and the zero
 * the wire carries beside it is not a measurement. */
export interface BurnView {
  readonly known: boolean
  readonly usdPerMin: Reading
  readonly fast: boolean
}

/** ConnectionView is the live feed's connectivity (`JSONConn`). */
export interface ConnectionView {
  readonly state: string
  readonly reason: string
  readonly ageSeconds: Reading
}

/** TotalsView is a total, or a repository's own figures -- the two carry the
 * same fields, which is why one type serves both. */
export interface TotalsView {
  readonly countedUsd: Reading
  readonly spentUsd: Reading
  readonly notionalUsd: Reading
  /** null on the wire = no cap set, which is unavailable and not a cap of zero */
  readonly dailyCapUsd: Reading
  readonly percent: Reading
  readonly burn: BurnView
  readonly kill: KillView | null
}

/** RepoView is one repository block (`JSONRepo`). */
export interface RepoView extends TotalsView {
  readonly slug: string
  readonly repo: string
}

/**
 * RunView is a live or queued run (`JSONRun`).
 *
 * Two fields deserve naming. `round` is a **string** here and an int in
 * `budget show --json` (contract section 2), because the CLI renders it through
 * `strconv.Itoa` and writes `"-"` when the registry has no round; a decoder that
 * expected a number would refuse every queued run. And `spentUsd` is a
 * `*float64`: `null` means the run has no spend recorded, and `notional` says
 * whether a spend that *is* recorded is a subscription's list price rather than
 * money.
 */
export interface RunView {
  readonly run: string
  readonly slug: string
  readonly repo: string
  readonly title: string
  readonly stage: string
  readonly round: string
  readonly verify: string
  readonly models: string
  readonly recipe: string
  readonly auth: string
  readonly notional: boolean
  readonly spentUsd: Reading
  readonly ageSeconds: Reading
  readonly health: string
  readonly deadline: string
  readonly halted: string
  readonly queued: boolean
  readonly stuck: boolean
  readonly workflow: string
  readonly requestedBy: string
  /** launchedBy is who started the run, as its own `launch.json` recorded the
   * launcher (0.9.0). It is the **whole** recorded value and not the short form
   * a column has room for: the cut belongs to the screen that draws a column
   * (`shared/launcher.ts`), so the document's own text stays whole for the Run
   * screen and for a reader of the model. */
  readonly launchedBy: string
}

/**
 * FinishedView is a finished run (`JSONFinished`). `prUrl` and `prNumber` are
 * `null` when the run has no pull request -- the contract's `omitempty`, and
 * design section 4.3's gap 7: the console shows the repository, the number and
 * the URL as *text* and builds no link from them.
 */
export interface FinishedView {
  readonly run: string
  readonly slug: string
  readonly repo: string
  readonly title: string
  readonly status: string
  readonly outcome: string
  readonly failed: boolean
  readonly prUrl: string | null
  readonly prNumber: number | null
  readonly ageSeconds: Reading
}

/** LiveDocument is an ordinary `watch --json` document. */
export interface LiveDocument {
  readonly shape: 'live'
  readonly project: string
  readonly mode: string
  readonly day: string
  readonly now: string
  readonly connection: ConnectionView
  readonly total: TotalsView
  readonly repos: readonly RepoView[]
  readonly runs: readonly RunView[]
  readonly finished: readonly FinishedView[]
  readonly ready: readonly FinishedView[]
  /** present only when the runs bucket could not be read for queued rows */
  readonly queuedNote: string | null
}

/** CostView is the seven fields of `runstore.Cost` the console reads. */
export interface CostView {
  readonly modelUsd: Reading
  /** modelNotionalUsd is the part of `modelUsd` a subscription paid for
   * (C1.16, P12/D140: "every reader of the run's dollars must split on the new
   * field rather than on `ModelBasis`"). `modelUsd` is the **whole** model
   * figure -- money and the plan's list price together -- so a reader draws
   * `modelUsd` minus this as the billed dollars, and never the whole figure. A
   * row written before C1.16 has no such key (`omitempty`), and then the
   * subscription basis is what says the row's whole figure was the plan's:
   * unavailable here, which is the state the screens fall back on. */
  readonly modelNotionalUsd: Reading
  readonly computeUsd: Reading
  readonly computeEstimated: boolean
  readonly totalUsd: Reading
  /** modelBasis is the basis the run's model figure was charged on
   * (`api-list`, or `subscription` for an all-notional row, and it is
   * `api-list` for the whole of a mixed cascade run -- `runstore.BasisSubscription`).
   * It is the **fallback** for a row that carries no `modelNotionalUsd`, never
   * the split itself. */
  readonly modelBasis: string
}

/** RecordView is one `runview.Row`: a run record as `runs ls --json` reads it,
 * which is also what the degraded feed carries. */
export interface RecordView {
  readonly run: string
  readonly runId: string
  readonly repo: string
  readonly workflow: string
  readonly oauthAccount: string
  readonly recipe: string
  readonly status: string
  readonly stage: string
  readonly reason: string
  readonly branch: string
  readonly outcome: string
  readonly prUrl: string | null
  readonly requestedBy: string
  /** launchedBy is who started the run, whole, as on `RunView`: the Run screen's
   * header draws it (`shared/launcher.ts`'s `launcherFull`). */
  readonly launchedBy: string
  readonly logUrl: string
  readonly created: string
  readonly cost: CostView
}

/** DegradedTotalsView is `runview.Totals`: the sums, with the two notional and
 * estimated accounts kept apart from the billed one, and the count of rows
 * whose compute is unknown -- which is what makes the sums a lower bound. */
export interface DegradedTotalsView {
  readonly runs: Reading
  readonly modelUsd: Reading
  readonly modelNotionalUsd: Reading
  readonly computeUsd: Reading
  readonly totalUsd: Reading
  readonly computeNotEstimated: Reading
}

/** DegradedDocument is `watchDegradedDoc`: `lsDoc` plus `degraded` and
 * `notice`. */
export interface DegradedDocument {
  readonly shape: 'degraded'
  readonly project: string
  readonly notice: string
  readonly warnings: readonly string[]
  readonly totals: DegradedTotalsView
  readonly runs: readonly RecordView[]
}

/** ConsoleDocument is either shape. A screen switches on `shape`, which is the
 * `degraded` key's own reading, not a second opinion about it. */
export type ConsoleDocument = LiveDocument | DegradedDocument

/** The exact key sets of the two shapes, in the contract's order. */
export const LIVE_KEYS: readonly string[] = Object.freeze([
  'project',
  'mode',
  'day',
  'now',
  'connection',
  'total',
  'repos',
  'runs',
  'finished',
  'ready',
  'queued_note',
])

/** DEGRADED_KEYS is `lsDoc`'s keys plus the two `watchDegradedDoc` adds. */
export const DEGRADED_KEYS: readonly string[] = Object.freeze([
  'project',
  'runs',
  'totals',
  'warnings',
  'degraded',
  'notice',
])

/**
 * LIVE_MARKERS are the keys that *identify* the live shape: the fields with no
 * counterpart in the degraded document. A document carrying one of them is the
 * live feed; a document carrying none of them is neither shape, and is refused
 * as an unknown shape rather than decoded as an empty live one.
 *
 * `project` and `runs` are in **both** shapes -- `watchDegradedDoc` embeds
 * `lsDoc` (`internal/cli/watch.go:401`), which carries them -- so neither can
 * discriminate, and a marker list that included them would refuse the degraded
 * document as a contradiction.
 */
export const LIVE_MARKERS: readonly string[] = Object.freeze([
  'mode',
  'day',
  'now',
  'connection',
  'total',
  'repos',
  'finished',
  'ready',
])

const CONNECTION_KEYS = ['state', 'reason', 'age_seconds']
const BURN_KEYS = ['known', 'usd_per_min', 'fast']
const KILL_KEYS = ['by', 'at', 'reason']
const TOTAL_KEYS = [
  'counted_usd',
  'spent_usd',
  'notional_usd',
  'daily_cap_usd',
  'percent',
  'burn',
  'kill',
  'runs',
  'run_hours',
]
/** REPO_KEYS is `JSONRepo`'s own key set (`internal/watch/json.go:42`): a
 * repository is a total, and the two keys it does *not* carry are `runs` and
 * `run_hours`, which are on `JSONTotal` alone. Review round 5 of PR 478: this
 * was `['slug', 'repo', ...TOTAL_KEYS]`, so `closed()` accepted those two on a
 * `repos[]` entry -- keys the struct cannot write, decoded as understood rather
 * than becoming the skew state this module promises for a key it does not know
 * ("the live half of this file closes every object it decodes"). The list is
 * written out rather than spread, so a later key added to a *total* is a
 * decision about repositories too. Exported so `schema.test.ts` can pin it
 * against the struct the way it pins `RECORD_KEYS`. */
export const REPO_KEYS = [
  'slug',
  'repo',
  'counted_usd',
  'spent_usd',
  'notional_usd',
  'daily_cap_usd',
  'percent',
  'burn',
  'kill',
]
/** RUN_KEYS is **every** JSON key of `JSONRun` (`internal/watch/json.go`), in
 * the struct's own order -- `runs[]`, the Live screen's feed. It is closed for
 * `RECORD_KEYS`'s reason and fails the same way: a key the struct writes and
 * this list does not have makes `closed()` refuse the whole document, so the
 * Live screen draws the skew state for a document it simply had not been told
 * about.
 *
 * **`job`, `profile`, `provider`, `account` and `launched_by` arrived with
 * 0.9.0** (the project-centric job triple, and who launched a run), and this
 * list did not have them: every 0.9.0 `watch --json` document was refused whole
 * as `runs[0].launched_by`. **`launched_by` is read now** -- the Live row draws
 * it as its Started-by cell (`shared/launcher.ts`, owner request 2026-10-18) --
 * and the other four are accepted and not read, like `title` on `runview.Row`:
 * no Live-screen column draws them yet. They are classified in
 * `WIRE_TEXT_CLASSES` all the same -- `job`, `profile` and `provider` as
 * labels, `account` and `launched_by` as people -- because a string this shape
 * may carry is a string the sanitizer must have a class for the moment a screen
 * draws it.
 *
 * **`credential` and `waiting_until` arrived with 0.10.0** (credentials-service
 * C1.21, the cascade's current credential and a held-up run's resume instant),
 * and they are `JSONRun`'s alone: a finished run is spending on nothing and
 * waiting for nothing, which is why `FINISHED_KEYS` does not carry them and the
 * test that pins both lists names them separately. They are accepted and not
 * read for the same reason as the five above -- no Live-screen column draws
 * them -- and classified the same way: `credential` as a label (a name an
 * operator or the service gave a token, never a value) and `waiting_until` as a
 * stamp in `NON_TEXT_STRING_KEYS`, an instant a reader compares with `now`.
 * The drift is the same drift, and it is worth naming as such: this list went
 * two keys short when the fields landed, both console jobs failed on it, and
 * nothing in the committed fixture could have caught it, because the recording
 * predates the fields.
 *
 * **`plan_task` arrived with the console dependency graph's GV1.2** (a run's
 * plan task, recorded at launch: decision GD5), between `launched_by` and
 * `credential` in `JSONRun`, and it is written on every run launched with
 * `--plan-task`. It is accepted and not read -- no Live-screen column draws it
 * yet, and the screen that does is GV2.3's graph -- and classified as a
 * **label**: an id the plan's own document spells a node with, never a person
 * and never free prose.
 *
 * Exported so `schema.test.ts` can pin it against the struct, as it pins
 * `RECORD_KEYS` and `REPO_KEYS`. */
export const RUN_KEYS = [
  'run',
  'slug',
  'repo',
  'title',
  'stage',
  'round',
  'verify',
  'models',
  'recipe',
  'auth',
  'notional',
  'spent_usd',
  'age_seconds',
  'health',
  'deadline',
  'halted',
  'queued',
  'stuck',
  'workflow',
  'requested_by',
  'job',
  'profile',
  'provider',
  'account',
  'launched_by',
  'plan_task',
  'credential',
  'waiting_until',
]
/** FINISHED_KEYS is **every** JSON key of `JSONFinished`
 * (`internal/watch/json.go`), in the struct's own order -- `finished[]` and
 * `ready[]`, the Live screen's other two lists. It carries 0.9.0's five new
 * keys too, for `RUN_KEYS`'s reason and with the same classification: a reader
 * that follows a run out of `runs[]` must not be refused the row it lands on.
 * GV1.2's `plan_task` as well, for the same reason: a run that finishes does
 * not stop having been asked to work on a task. The two keys it does *not*
 * carry -- `credential` and `waiting_until` -- are `JSONRun`'s alone, as its own
 * row in this file says.
 * Exported so `schema.test.ts` can pin it against the struct. */
export const FINISHED_KEYS = [
  'run',
  'slug',
  'repo',
  'title',
  'status',
  'outcome',
  'failed',
  'pr_url',
  'pr_number',
  'age_seconds',
  'job',
  'profile',
  'provider',
  'account',
  'launched_by',
  'plan_task',
]
const TOTALS_KEYS = ['runs', 'model_usd', 'model_notional_usd', 'compute_usd', 'total_usd', 'compute_not_estimated']
/**
 * RECORD_KEYS is **every** JSON key of `runview.Row`
 * (`internal/runview/runview.go:57`), in the struct's own order, and the four
 * that carry no `omitempty` are among them: `terminal`, `settled`, `pushed` and
 * `follow_up`. That is not politeness -- `closed` refuses a key the shape does
 * not list, so a list that omits a key the CLI writes is a list that refuses a
 * real `runs ls --json` document, which is exactly what an earlier revision of
 * this file did: eleven keys, and every degraded document rejected at
 * `runs[0].terminal`.
 *
 * The list is therefore derived from the struct rather than from the fields the
 * console happens to read, and a key the CLI adds in a later version is *still*
 * refused -- which is the point of closing the shape: a document the console
 * does not fully understand becomes Task 3.2's skew state, not a half-drawn
 * list. Keys accepted and not read (`title`, `project_layer`,
 * `config_sha256`, `image_age_s`, `changed_files`, `rigor`, `artifact_count`, `artifact_bytes`,
 * `task_pr`, `record_pr`, `pr`, `pushed_head`, `started_at`, `draft_fallback`,
 * `pr_status_at`, `stale_draft`, `execution`, `batch`, `halt`, `previous_run`,
 * `base_branch`, `derived`)
 * are the ones this screen has no use for yet; they are listed so that a
 * document carrying them is read rather than thrown away.
 *
 * **`artifact_count` and `artifact_bytes` were missing, and a review found it.**
 * They sit between `outcome` and `task_pr` in the struct, so every
 * `runs ls --json` row for a run that delivered artifacts carried two keys this
 * list did not have and `closed()` refused the whole document as
 * `runs[0].artifact_count`. The pinning test could not catch it because the
 * expected list in that test was a second hand-written copy of this one: the
 * two agreed with each other and both were wrong. That test now reads the struct
 * (`internal/runview/runview.go`) and derives both lists from its tags, so the
 * next omission fails there.
 *
 * **`title` arrived with `fugaro run --title` (0.8.5, #573)**, between `recipe`
 * and `project_layer` in the struct, and is written on nearly every row, so a
 * list without it would turn every 0.8.5 `runs ls --json` document into the
 * skew state. It is accepted and not read: the Runs screen's rows are this
 * document's records, and nothing here renders a record's title yet.
 *
 * **`derived` arrived with the read-side preemption derivation (2026-10-21)**,
 * between `reason` and `halt` in the struct, and it is written only on a row
 * whose status was derived while reading -- a run whose record was never
 * finalized and whose Cloud Batch job the platform preempted. It is the same
 * omission `title` and `launched_by` were: `closed()` refuses a key the shape
 * does not list, so a `runs ls --json` document carrying one preempted run
 * would be refused whole as `runs[0].derived` until this list had the key. It
 * is accepted and not read yet -- the Runs screens draw `status` and `reason`,
 * and no screen draws the derivation as its own marker.
 *
 * **`launched_by` arrived with 0.9.0**, between `requested_by` and `pr_url` in
 * the struct, and is written on every row whose `launch.json` recorded a
 * launcher -- which is every launched run. This is `title`'s omission again,
 * and it fails the same way: a list without the key refuses a real
 * `runs ls --json` document whole, as `runs[0].launched_by`. **It is read
 * now** -- the Run screen's header draws it and the Projects job row draws its
 * short form (`shared/launcher.ts`, owner request 2026-10-18) -- and it is
 * classified as a person's text in `WIRE_TEXT_CLASSES`, the class `requested_by`
 * carries and the class the recorder already gives it by its `_by` suffix.
 *
 * **`plan_task` arrived with the console dependency graph's GV1.2**, between
 * `launched_by` and `pr_url` in the struct, and it is written on every row whose
 * `launch.json` recorded a plan task -- every run launched with
 * `--plan-task`. It is `title`'s and `launched_by`'s omission a third time, and
 * it fails the same way: a list without the key refuses a real
 * `runs ls --json` document whole, as `runs[0].plan_task`. It is accepted and
 * not read: no Runs-screen column draws it, and the screen that joins a run to
 * a node of its plan's graph is GV2.3's, on its own document.
 *
 * **`rigor` arrived with the rigor modes (0.11.0)**, between `changed_files`
 * and `artifact_count` in the struct, and a run that resolved a mode writes it:
 * the mode, the layer that decided it (`from`), the areas that matched -- each
 * with the mode it sets and the moment it was applied at, strictest first
 * (D23) -- the moment of the block and the revision the configuration was read
 * from (`runstore.RigorRecord`, docs/rigor-modes D24, D25). **It is read now**
 * -- `shared/rigor.ts` parses it for the Live chip, the Projects MODE cell and
 * the Run screen's Rigor row (owner request 2026-10-25) -- and it is here for
 * `launched_by`'s reason: a list without the key refuses a real
 * `runs ls --json` document whole, as `runs[0].rigor`. Its string keys carry
 * classes rather than exemptions: `from` is an enum this build may not know and
 * `revision` is a git ref, so both go through the sanitizer if a later screen
 * ever draws them. The reader drops a key it does not know the class of; the
 * `mode` word is drawn only when this build's own table names it, and
 * otherwise as bounded text.
 */
export const RECORD_KEYS = [
  'run',
  'repo',
  'workflow',
  'oauth_account',
  'recipe',
  'title',
  'project_layer',
  'config_sha256',
  'run_id',
  'status',
  'stage',
  'reason',
  'derived',
  'halt',
  'batch',
  'requested_by',
  'launched_by',
  'plan_task',
  'pr_url',
  'execution',
  'log_url',
  'created',
  'cost',
  'image_age_s',
  'terminal',
  'settled',
  'branch',
  'outcome',
  'changed_files',
  'rigor',
  'artifact_count',
  'artifact_bytes',
  'task_pr',
  'record_pr',
  'pr',
  'pushed',
  'pushed_head',
  'started_at',
  'base_branch',
  'previous_run',
  'follow_up',
  'draft_fallback',
  'pr_status_at',
  'stale_draft',
]

function killAt(raw: unknown, field: string, where: string): KillView | null {
  if (raw === null || raw === undefined) return null
  const kill = objectAt(raw, field, where)
  closed(kill, KILL_KEYS, field, where)
  return {
    by: optionalText(kill, 'by', field, where),
    at: optionalText(kill, 'at', field, where),
    reason: optionalText(kill, 'reason', field, where),
  }
}

function burnAt(raw: unknown, field: string, where: string): BurnView {
  const burn = objectAt(raw, field, where)
  closed(burn, BURN_KEYS, field, where)
  const known = boolAt(required(burn, 'known', field, where), `${field}.known`, where)
  return {
    known,
    // `known` first, and the zero beside it is not a measurement: the contract
    // says read the flag, and a decoder that read the number would hand a
    // screen a burn rate of zero for a rate nobody measured.
    usdPerMin: known
      ? readingOf(burn, 'usd_per_min', `${field}.usd_per_min`)
      : unavailable('the document does not know the burn rate'),
    fast: boolAt(required(burn, 'fast', field, where), `${field}.fast`, where),
  }
}

function totalsAt(raw: unknown, field: string, where: string): TotalsView {
  const total = objectAt(raw, field, where)
  closed(total, TOTAL_KEYS, field, where)
  return totalsOf(total, field, where)
}

/**
 * totalsOf reads the budget fields off an object that has already been checked
 * against its own key set. A repository *is* a total with two more keys in front
 * of it (`RepoView extends TotalsView`), so `repoAt` reads the same fields
 * through here: running `closed` twice would refuse the repository's own `slug`
 * as a key the totals shape does not have.
 */
function totalsOf(total: Record<string, unknown>, field: string, where: string): TotalsView {
  return {
    countedUsd: readingOf(total, 'counted_usd', `${field}.counted_usd`),
    spentUsd: readingOf(total, 'spent_usd', `${field}.spent_usd`),
    notionalUsd: readingOf(total, 'notional_usd', `${field}.notional_usd`),
    dailyCapUsd: readingOf(total, 'daily_cap_usd', `${field}.daily_cap_usd`),
    percent: readingOf(total, 'percent', `${field}.percent`),
    burn: burnAt(required(total, 'burn', field, where), `${field}.burn`, where),
    kill: killAt(total.kill, `${field}.kill`, where),
  }
}

function repoAt(raw: unknown, index: number, where: string): RepoView {
  const field = `repos[${String(index)}]`
  const repo = objectAt(raw, field, where)
  closed(repo, REPO_KEYS, field, where)
  return {
    slug: stringAt(required(repo, 'slug', field, where), `${field}.slug`, where),
    repo: stringAt(required(repo, 'repo', field, where), `${field}.repo`, where),
    ...totalsOf(repo, field, where),
  }
}

function runAt(raw: unknown, index: number, where: string): RunView {
  const field = `runs[${String(index)}]`
  const run = objectAt(raw, field, where)
  closed(run, RUN_KEYS, field, where)
  return {
    run: stringAt(required(run, 'run', field, where), `${field}.run`, where),
    slug: stringAt(required(run, 'slug', field, where), `${field}.slug`, where),
    repo: stringAt(required(run, 'repo', field, where), `${field}.repo`, where),
    title: stringAt(required(run, 'title', field, where), `${field}.title`, where),
    stage: stringAt(required(run, 'stage', field, where), `${field}.stage`, where),
    // A string, always: the CLI renders the round and writes "-" for a queued
    // run. Typing it as a number here would refuse every queued row.
    round: stringAt(required(run, 'round', field, where), `${field}.round`, where),
    verify: stringAt(required(run, 'verify', field, where), `${field}.verify`, where),
    models: stringAt(required(run, 'models', field, where), `${field}.models`, where),
    recipe: optionalText(run, 'recipe', field, where),
    auth: stringAt(required(run, 'auth', field, where), `${field}.auth`, where),
    notional: boolAt(required(run, 'notional', field, where), `${field}.notional`, where),
    spentUsd: readingOf(run, 'spent_usd', `${field}.spent_usd`),
    ageSeconds: readingOf(run, 'age_seconds', `${field}.age_seconds`),
    health: stringAt(required(run, 'health', field, where), `${field}.health`, where),
    deadline: stringAt(required(run, 'deadline', field, where), `${field}.deadline`, where),
    halted: optionalText(run, 'halted', field, where),
    queued: optionalBool(run, 'queued', field, where),
    stuck: optionalBool(run, 'stuck', field, where),
    workflow: optionalText(run, 'workflow', field, where),
    requestedBy: optionalText(run, 'requested_by', field, where),
    launchedBy: optionalText(run, 'launched_by', field, where),
  }
}

function finishedAt(raw: unknown, index: number, list: string, where: string): FinishedView {
  const field = `${list}[${String(index)}]`
  const item = objectAt(raw, field, where)
  closed(item, FINISHED_KEYS, field, where)
  const prUrl = item.pr_url
  return {
    run: stringAt(required(item, 'run', field, where), `${field}.run`, where),
    slug: stringAt(required(item, 'slug', field, where), `${field}.slug`, where),
    repo: stringAt(required(item, 'repo', field, where), `${field}.repo`, where),
    title: stringAt(required(item, 'title', field, where), `${field}.title`, where),
    status: stringAt(required(item, 'status', field, where), `${field}.status`, where),
    outcome: stringAt(required(item, 'outcome', field, where), `${field}.outcome`, where),
    failed: boolAt(required(item, 'failed', field, where), `${field}.failed`, where),
    // `pr_url` and `pr_number` are `omitempty` on the wire: absent means the run
    // has no pull request, and `null` here is that fact rather than a value
    // nobody filled in.
    prUrl: prUrl === undefined || prUrl === null ? null : stringAt(prUrl, `${field}.pr_url`, where),
    prNumber: optionalInt(item, 'pr_number'),
    ageSeconds: readingOf(item, 'age_seconds', `${field}.age_seconds`),
  }
}

function costAt(raw: unknown, field: string, where: string): CostView {
  // Deliberately NOT closed: `runstore.Cost` is versioned independently of this
  // file and carries model maps, route maps and source lines the console does
  // not read. Closing it would refuse a document the console understands
  // perfectly well, which is the opposite of the rule -- the rule is that the
  // console must not *render* a half-read document, not that it must refuse one.
  const cost = objectAt(raw, field, where)
  return {
    modelUsd: readingOf(cost, 'model_usd', `${field}.model_usd`),
    modelNotionalUsd: readingOf(cost, 'model_notional_usd', `${field}.model_notional_usd`),
    computeUsd: readingOf(cost, 'compute_usd', `${field}.compute_usd`),
    // `compute_estimated`: when it is false, `compute_usd` 0 means "not
    // estimated", not "free" -- so the flag is the state of the number, exactly
    // as `burn.known` is.
    computeEstimated: optionalBool(cost, 'compute_estimated', field, where),
    totalUsd: readingOf(cost, 'total_usd', `${field}.total_usd`),
    modelBasis: stringAt(cost.model_basis ?? '', `${field}.model_basis`, where),
  }
}

function recordAt(raw: unknown, index: number, where: string): RecordView {
  const field = `runs[${String(index)}]`
  const record = objectAt(raw, field, where)
  closed(record, RECORD_KEYS, field, where)
  const prUrl = record.pr_url
  return {
    run: stringAt(required(record, 'run', field, where), `${field}.run`, where),
    runId: optionalText(record, 'run_id', field, where),
    repo: stringAt(required(record, 'repo', field, where), `${field}.repo`, where),
    workflow: optionalText(record, 'workflow', field, where),
    oauthAccount: optionalText(record, 'oauth_account', field, where),
    recipe: optionalText(record, 'recipe', field, where),
    status: stringAt(required(record, 'status', field, where), `${field}.status`, where),
    stage: optionalText(record, 'stage', field, where),
    reason: optionalText(record, 'reason', field, where),
    branch: optionalText(record, 'branch', field, where),
    outcome: optionalText(record, 'outcome', field, where),
    prUrl: prUrl === undefined || prUrl === null ? null : stringAt(prUrl, `${field}.pr_url`, where),
    requestedBy: optionalText(record, 'requested_by', field, where),
    launchedBy: optionalText(record, 'launched_by', field, where),
    logUrl: optionalText(record, 'log_url', field, where),
    created: stringAt(required(record, 'created', field, where), `${field}.created`, where),
    cost: costAt(required(record, 'cost', field, where), `${field}.cost`, where),
  }
}

function degradedTotalsAt(raw: unknown, where: string): DegradedTotalsView {
  const field = 'totals'
  const totals = objectAt(raw, field, where)
  closed(totals, TOTALS_KEYS, field, where)
  return {
    runs: readingOf(totals, 'runs', `${field}.runs`),
    modelUsd: readingOf(totals, 'model_usd', `${field}.model_usd`),
    modelNotionalUsd: readingOf(totals, 'model_notional_usd', `${field}.model_notional_usd`),
    computeUsd: readingOf(totals, 'compute_usd', `${field}.compute_usd`),
    totalUsd: readingOf(totals, 'total_usd', `${field}.total_usd`),
    computeNotEstimated: readingOf(totals, 'compute_not_estimated', `${field}.compute_not_estimated`),
  }
}

function liveAt(raw: Record<string, unknown>, where: string): LiveDocument {
  closed(raw, LIVE_KEYS, '', where)
  const connection = objectAt(required(raw, 'connection', '', where), 'connection', where)
  closed(connection, CONNECTION_KEYS, 'connection', where)
  const queuedNote = raw.queued_note
  return {
    shape: 'live',
    project: stringAt(required(raw, 'project', '', where), 'project', where),
    mode: stringAt(required(raw, 'mode', '', where), 'mode', where),
    day: stringAt(required(raw, 'day', '', where), 'day', where),
    now: timestampAt(required(raw, 'now', '', where), 'now', where),
    connection: {
      state: stringAt(required(connection, 'state', 'connection', where), 'connection.state', where),
      reason: optionalText(connection, 'reason', 'connection', where),
      ageSeconds: readingOf(connection, 'age_seconds', 'connection.age_seconds'),
    },
    total: totalsAt(required(raw, 'total', '', where), 'total', where),
    repos: arrayAt(required(raw, 'repos', '', where), 'repos', where).map((item, index) =>
      repoAt(item, index, where),
    ),
    runs: arrayAt(required(raw, 'runs', '', where), 'runs', where).map((item, index) =>
      runAt(item, index, where),
    ),
    finished: arrayAt(required(raw, 'finished', '', where), 'finished', where).map((item, index) =>
      finishedAt(item, index, 'finished', where),
    ),
    ready: arrayAt(required(raw, 'ready', '', where), 'ready', where).map((item, index) =>
      finishedAt(item, index, 'ready', where),
    ),
    queuedNote:
      queuedNote === undefined || queuedNote === null ? null : stringAt(queuedNote, 'queued_note', where),
  }
}

function degradedAt(raw: Record<string, unknown>, where: string): DegradedDocument {
  closed(raw, DEGRADED_KEYS, '', where)
  return {
    shape: 'degraded',
    project: stringAt(required(raw, 'project', '', where), 'project', where),
    notice: stringAt(required(raw, 'notice', '', where), 'notice', where),
    warnings: arrayAt(required(raw, 'warnings', '', where), 'warnings', where).map((item, index) =>
      stringAt(item, `warnings[${String(index)}]`, where),
    ),
    totals: degradedTotalsAt(required(raw, 'totals', '', where), where),
    runs: arrayAt(required(raw, 'runs', '', where), 'runs', where).map((item, index) =>
      recordAt(item, index, where),
    ),
  }
}

// ---------------------------------------------------------------------------
// The doors.
// ---------------------------------------------------------------------------

/**
 * decodeDocument admits either shape, discriminated on the `degraded` key.
 *
 * The order of the two tests is the rule. A document that carries `degraded` at
 * all must carry `true` -- `watchDegradedDoc` always writes `true`, so
 * `{"degraded": false}` is a document no version of the CLI writes, and reading
 * it as a live document would be a guess about what the writer meant. A
 * document with no `degraded` key must carry at least one of `LIVE_MARKERS`;
 * `{}` is not an empty live document, it is a document the console cannot read.
 * And a document that carries `degraded: true` **and** a live marker is neither
 * shape -- `wrong-shape` -- because no version of `watch` writes both.
 */
export function decodeDocument(raw: unknown, where = 'the document'): Decoded<ConsoleDocument> {
  return at(where, () => {
    const document = objectAt(raw, 'the document', where)
    if ('degraded' in document) {
      if (document.degraded !== true) return refuse('degraded-not-true', 'degraded', where)
      // A document that says it is degraded *and* carries the live feed's keys
      // is neither shape: no version of `watch` writes both, and reading it as
      // either would be a guess about what the writer meant.
      const contradiction = LIVE_MARKERS.find((marker) => marker in document)
      if (contradiction !== undefined) return refuse('wrong-shape', contradiction, where)
      return degradedAt(document, where)
    }
    if (!LIVE_MARKERS.some((marker) => marker in document)) return refuse('unknown-shape', '', where)
    return liveAt(document, where)
  })
}

/**
 * decodeSnapshot admits a **live** document and refuses the degraded one by
 * name. The Live screen renders a snapshot of the feed; a degraded document
 * handed to it is not an empty feed, and this door is what makes that a refusal
 * rather than a screen full of zeros.
 */
export function decodeSnapshot(raw: unknown, where = 'the snapshot'): Decoded<LiveDocument> {
  const decoded = decodeDocument(raw, where)
  if (!decoded.ok) return decoded
  if (decoded.value.shape !== 'live') {
    return {
      ok: false,
      skew: skewFor({ name: 'wrong-shape', field: 'degraded', where }),
    }
  }
  return { ok: true, value: decoded.value }
}

/**
 * knownTopLevelKeys is the key set of a shape, for a caller that wants to say
 * what it was reading -- and for the test that holds the negatives: a key that
 * is neither shape's is a key the console must refuse.
 */
export function knownTopLevelKeys(shape: ConsoleDocument['shape']): readonly string[] {
  return shape === 'live' ? LIVE_KEYS : DEGRADED_KEYS
}

// ---------------------------------------------------------------------------
// The plan status document (plan GV2.1).
// ---------------------------------------------------------------------------

/**
 * `fugaro plan status --json` is a **third** document, and it is not a shape of
 * the feed: it is one plan graph per plan under the checkout's `docs/`, each
 * joined to the runs of that checkout's repository (`internal/cli/plan_status.go`,
 * `planStatusDoc`; `internal/plan/state.go`, `StatePlan`). So it has its own
 * door, `decodePlanStatus`, and the feed's door does not admit it -- the two
 * documents share no marker, and a screen that read one as the other would draw
 * an empty graph or an empty feed.
 *
 * **`state` is the closed word, and it is why this decoder exists.** A task's
 * state is one of six the CLI writes from its own table (`plan.StateNode.State`),
 * and every field beside it is read in that word's light: `closed`, `mark`,
 * `level` and `critical` are the state's own qualifiers rather than independent
 * facts. A seventh word is a CLI whose rules this build does not know, so it is
 * refused by name -- `unknown-value` -- rather than drawn in whatever colour a
 * screen keeps for "not one of mine".
 *
 * **The counts are readings and the level is not.** `tasks`, `closed`, `open`
 * and a release box's four figures are quantities a header prints, so they go
 * through `readingOf` and a screen can only draw them through
 * `formatReading` (the availability rule); a node's `level` is a place in a
 * dependency chain, so it is a number the document must carry or the document
 * is one this console cannot draw.
 */

/** PLAN_STATES is `plan.StateNode.State`'s closed set, in the CLI's own order
 * (`internal/plan/state.go`). */
export const PLAN_STATES = Object.freeze([
  'running',
  'done',
  'cancelled',
  'blocked',
  'ready',
  'not-started',
] as const) satisfies readonly string[]

/** PlanState is one of them, as a type: a screen switches on it and the switch
 * is exhaustive, which is what `PLAN_STATES` being the whole list buys. */
export type PlanState = (typeof PLAN_STATES)[number]

/** The exact key sets of the plan status document and of each object in it, in
 * the structs' declaration order. A test reads the same fields off
 * `internal/cli/plan_status.go` and `internal/plan/state.go`, so a key one side
 * gains without the other is a failing test rather than an unread field. */
export const PLAN_STATUS_KEYS: readonly string[] = Object.freeze([
  'repo',
  'plans',
  'totals',
  'streams',
  'warnings',
])

export const PLAN_KEYS: readonly string[] = Object.freeze([
  'id',
  'artifact',
  'plan',
  'stream',
  'nodes',
  'releases',
  'tasks',
  'closed',
  'open',
])

export const PLAN_NODE_KEYS: readonly string[] = Object.freeze([
  'id',
  'title',
  'state',
  'closed',
  'level',
  'critical',
  'release',
  'run',
  'stage',
  'pr_number',
  'mark',
])

export const PLAN_RELEASE_KEYS: readonly string[] = Object.freeze([
  'version',
  'name',
  'stream',
  'finished',
  'total',
  'done',
  'closed',
  'open',
])

export const PLAN_TOTALS_KEYS: readonly string[] = Object.freeze(['plans', 'tasks', 'closed', 'open'])

export const PLAN_STREAM_KEYS: readonly string[] = Object.freeze([
  'name',
  'theme',
  'names',
  'current',
  'next',
  'tag_prefix',
])

/** PlanNodeView is one task: the plan's own node, and what the join said about
 * it. `state` is the whole of the colouring -- see `PLAN_STATES` -- and `mark`
 * is the one reason a state is not what its word alone says (`merge unknown`). */
export interface PlanNodeView {
  readonly id: string
  readonly title: string
  readonly state: PlanState
  readonly closed: boolean
  /** the longest dependency chain ending at this task */
  readonly level: number
  readonly critical: boolean
  /** the target version the plan gives it, empty when it gives none */
  readonly release: string
  /** the run's short suffix, empty when no run joins the task */
  readonly run: string
  readonly stage: string
  readonly prNumber: number | null
  readonly mark: string
}

/** PlanReleaseView is one target version's box: what it is called, whether the
 * stream's catalogue says it has shipped, and how its tasks stand. */
export interface PlanReleaseView {
  readonly version: string
  readonly name: string
  readonly stream: string
  readonly finished: boolean
  readonly total: Reading
  readonly done: Reading
  readonly closed: Reading
  readonly open: Reading
}

/** PlanView is one plan. `nodes` holds every task with `--all` and the open
 * ones alone without it, while `tasks`/`closed`/`open` count every task either
 * way -- so a header drawn from those three reads the same in both reads. */
export interface PlanView {
  readonly id: string
  readonly artifact: string
  readonly plan: string
  readonly stream: string
  readonly nodes: readonly PlanNodeView[]
  readonly releases: readonly PlanReleaseView[]
  readonly tasks: Reading
  readonly closed: Reading
  readonly open: Reading
}

/** PlanTotalsView is every plan's figures added up: the header line a screen
 * draws before the plans. */
export interface PlanTotalsView {
  readonly plans: Reading
  readonly tasks: Reading
  readonly closed: Reading
  readonly open: Reading
}

/** PlanStreamView is one row of the release streams catalogue, in the shape a
 * console draws a column from. */
export interface PlanStreamView {
  readonly name: string
  readonly theme: string
  readonly names: readonly string[]
  readonly current: string
  readonly next: string
  readonly tagPrefix: string
}

/** PlanStatusView is the whole document. `repo` is the checkout's own
 * repository and is what scopes the join: `''` is a checkout whose origin could
 * not be read, and then no run is joined at all. `warnings` are the CLI's own
 * sentences about what it could not read -- never a word a run wrote. */
export interface PlanStatusView {
  readonly repo: string
  readonly plans: readonly PlanView[]
  readonly totals: PlanTotalsView
  readonly streams: readonly PlanStreamView[]
  readonly warnings: readonly string[]
}

/** planStateAt is the closed word, refused by name when it is not one of the
 * six: a screen colours a node from this value and applies no rule of its own,
 * so a word this build does not know is a node it cannot draw. */
function planStateAt(raw: unknown, field: string, where: string): PlanState {
  const state = stringAt(raw, field, where)
  const known = PLAN_STATES.find((candidate) => candidate === state)
  if (known === undefined) return refuse('unknown-value', field, where)
  return known
}

/** PLAN_MAX_LEVEL bounds a node's `level`: a place in a dependency chain is a
 * small whole number, and one past this is a document no plan screen draws. */
export const PLAN_MAX_LEVEL = 1000

/** planLevelAt is `intAt` held to the level's own range, 0 to `PLAN_MAX_LEVEL`. */
function planLevelAt(raw: unknown, field: string, where: string): number {
  const level = intAt(raw, field, where)
  if (level < 0 || level > PLAN_MAX_LEVEL) return refuse('not-a-number', field, where)
  return level
}

/** planPrNumberAt is a node's `pr_number`: absent or `null` is "no pull request
 * recorded"; anything else must be a positive whole number. A fraction, a
 * negative or a numeral in a string is refused by name rather than read as no
 * pull request, because an identifier that is wrong is not one that is absent. */
function planPrNumberAt(raw: Record<string, unknown>, key: string, field: string, where: string): number | null {
  const value = raw[key]
  if (value === undefined || value === null) return null
  const at = `${field}.${key}`
  if (typeof value !== 'number' || !Number.isInteger(value) || value < 1) return refuse('not-a-number', at, where)
  return value
}

function planNodeAt(raw: unknown, index: number, plan: string, where: string): PlanNodeView {
  const field = `${plan}.nodes[${String(index)}]`
  const node = objectAt(raw, field, where)
  closed(node, PLAN_NODE_KEYS, field, where)
  return {
    id: stringAt(required(node, 'id', field, where), `${field}.id`, where),
    title: stringAt(required(node, 'title', field, where), `${field}.title`, where),
    state: planStateAt(required(node, 'state', field, where), `${field}.state`, where),
    closed: boolAt(required(node, 'closed', field, where), `${field}.closed`, where),
    level: planLevelAt(required(node, 'level', field, where), `${field}.level`, where),
    critical: boolAt(required(node, 'critical', field, where), `${field}.critical`, where),
    // The five `omitempty` keys of the node. Absent and `null` both mean the
    // CLI had nothing to say: no target version, no run joined, no stage, no
    // pull request recorded, no mark -- and the last is what makes a `done`
    // node unqualified rather than unknown.
    release: optionalText(node, 'release', field, where),
    run: optionalText(node, 'run', field, where),
    stage: optionalText(node, 'stage', field, where),
    prNumber: planPrNumberAt(node, 'pr_number', field, where),
    mark: optionalText(node, 'mark', field, where),
  }
}

function planReleaseAt(raw: unknown, index: number, plan: string, where: string): PlanReleaseView {
  const field = `${plan}.releases[${String(index)}]`
  const release = objectAt(raw, field, where)
  closed(release, PLAN_RELEASE_KEYS, field, where)
  return {
    version: stringAt(required(release, 'version', field, where), `${field}.version`, where),
    name: optionalText(release, 'name', field, where),
    stream: optionalText(release, 'stream', field, where),
    finished: boolAt(required(release, 'finished', field, where), `${field}.finished`, where),
    total: readingOf(release, 'total', `${field}.total`),
    done: readingOf(release, 'done', `${field}.done`),
    closed: readingOf(release, 'closed', `${field}.closed`),
    open: readingOf(release, 'open', `${field}.open`),
  }
}

function planAt(raw: unknown, index: number, where: string): PlanView {
  const field = `plans[${String(index)}]`
  const plan = objectAt(raw, field, where)
  closed(plan, PLAN_KEYS, field, where)
  return {
    id: stringAt(required(plan, 'id', field, where), `${field}.id`, where),
    artifact: stringAt(required(plan, 'artifact', field, where), `${field}.artifact`, where),
    plan: stringAt(required(plan, 'plan', field, where), `${field}.plan`, where),
    stream: optionalText(plan, 'stream', field, where),
    nodes: arrayAt(required(plan, 'nodes', field, where), `${field}.nodes`, where).map((item, at) =>
      planNodeAt(item, at, field, where),
    ),
    releases: arrayAt(required(plan, 'releases', field, where), `${field}.releases`, where).map((item, at) =>
      planReleaseAt(item, at, field, where),
    ),
    tasks: readingOf(plan, 'tasks', `${field}.tasks`),
    closed: readingOf(plan, 'closed', `${field}.closed`),
    open: readingOf(plan, 'open', `${field}.open`),
  }
}

function planTotalsAt(raw: unknown, field: string, where: string): PlanTotalsView {
  const totals = objectAt(raw, field, where)
  closed(totals, PLAN_TOTALS_KEYS, field, where)
  return {
    plans: readingOf(totals, 'plans', `${field}.plans`),
    tasks: readingOf(totals, 'tasks', `${field}.tasks`),
    closed: readingOf(totals, 'closed', `${field}.closed`),
    open: readingOf(totals, 'open', `${field}.open`),
  }
}

function planStreamAt(raw: unknown, index: number, where: string): PlanStreamView {
  const field = `streams[${String(index)}]`
  const stream = objectAt(raw, field, where)
  closed(stream, PLAN_STREAM_KEYS, field, where)
  return {
    name: stringAt(required(stream, 'name', field, where), `${field}.name`, where),
    theme: stringAt(required(stream, 'theme', field, where), `${field}.theme`, where),
    names: arrayAt(required(stream, 'names', field, where), `${field}.names`, where).map((item, at) =>
      stringAt(item, `${field}.names[${String(at)}]`, where),
    ),
    current: stringAt(required(stream, 'current', field, where), `${field}.current`, where),
    next: stringAt(required(stream, 'next', field, where), `${field}.next`, where),
    tagPrefix: stringAt(required(stream, 'tag_prefix', field, where), `${field}.tag_prefix`, where),
  }
}

function planStatusAt(raw: Record<string, unknown>, where: string): PlanStatusView {
  closed(raw, PLAN_STATUS_KEYS, '', where)
  return {
    repo: stringAt(required(raw, 'repo', '', where), 'repo', where),
    plans: arrayAt(required(raw, 'plans', '', where), 'plans', where).map((item, index) =>
      planAt(item, index, where),
    ),
    totals: planTotalsAt(required(raw, 'totals', '', where), 'totals', where),
    streams: arrayAt(required(raw, 'streams', '', where), 'streams', where).map((item, index) =>
      planStreamAt(item, index, where),
    ),
    warnings: arrayAt(required(raw, 'warnings', '', where), 'warnings', where).map((item, index) =>
      stringAt(item, `warnings[${String(index)}]`, where),
    ),
  }
}

/**
 * decodePlanStatus admits `plan status --json` and nothing else. It is a
 * separate door rather than a third shape of `decodeDocument` because the two
 * documents carry no common marker: a feed document handed to this door is
 * refused for the keys it is missing, and a plan document handed to the feed's
 * door is refused as an unknown shape. Neither is read as the other.
 */
export function decodePlanStatus(raw: unknown, where = 'the plan status document'): Decoded<PlanStatusView> {
  return at(where, () => planStatusAt(objectAt(raw, 'the document', where), where))
}
