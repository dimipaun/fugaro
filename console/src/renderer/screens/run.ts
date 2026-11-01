// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Run detail screen: the design's screen 4, drawn from `diagnose RUN
// --json`, a capped `logs RUN --json` window, `logs RUN --url` and the replay
// buffer (desktop-console plan Task 2.3; design section 6.2 screen 4).
//
// Seven things are on this screen, and each of them is a rule:
//
//   1. **The ledger.** `runstore.Cost`'s three figures, kept apart: the model
//      dollars (the **billed** part of the model figure -- `~$` only
//      when the whole figure is the subscription's), compute (`est.` when the
//      document says it is an estimate) and the total. The two accounts are
//      never summed on screen, exactly as on the Live and Spend screens.
//   2. **The stage timeline, with deadlines.** The stages are the verify records
//      `diagnose` carries (`build`/`test`/`lint`, `internal/verify/verify.go`),
//      each with its outcome, its test counts and its duration; the deadline is
//      the row's own `deadline` enum (`ok`/`near`/`over`) **when the row carries
//      one** -- a live `watch --json` entry does, a `runview.Row` from
//      `diagnose` does not -- and is an `unavailable` reading naming the field
//      when it does not. This screen does not invent a deadline, and it does not
//      invent one from `started_at` either.
//   3. **The run's own spend by stage, and what its mode let through.** The
//      document's top-level `stages` is the run's per-stage record
//      (`runstore.StageTiming`: the calls, the sub-agents and the dollars each
//      stage's model spend came to), and it is drawn as its own panel. A verify
//      record carries no spend, so the timeline's spend cell says that and
//      names that panel -- or, when the document carries no `stages`, says so
//      instead. The `tolerated` block is the findings the run's resolved mode
//      let through, drawn under the total it carries; a document with no block
//      gets one line naming the absent key, because a run that tolerated
//      nothing and a record written before the field existed look the same.
//      Neither panel sums, divides or derives a figure: every number on them is
//      one the document wrote, and the two splits `diagnose` does carry, by
//      **route** and by **model** (`route_by`, `model_by`), are drawn as their
//      own lists below. A screen that divided a total by the number of stages
//      would be inventing a figure about money. The two counts are pointers on
//      the wire (absent means nobody counted the stage) while the two dollars
//      are `omitempty` floats (absent is a zero), and the two dollar cells split
//      the stage's one figure rather than sum it: the billed cell is the figure
//      less the part the record carries when it carries one, and the plan's
//      figure marked notional when it carries none at all -- a record written
//      before C1.16 -- so no stage draws the same money in both cells. A carried
//      part equal to the whole figure is a subscription run's own split (the
//      plan paid all of it), not the fallback. The notional mark keeps the
//      plan's list price from being read as money.
//   4. **The pull request as text, and no link.** Design section 4.3's gap 7:
//      no document names the provider (GitHub or Bitbucket Cloud), so a link
//      rebuilt from the repository and the number **cannot** be built for every
//      run, and on a Bitbucket repository the GitHub-shaped construction names a
//      repository the operator does not own. So the screen draws the repository,
//      the number and the `pr_url` **as text**, and `link` is null with the
//      one-line reason beside it. The per-provider construction is written and
//      unit-tested below (`pullRequestUrl`) so that the day gap 7 lands the
//      screen only has to start offering it -- and `linkFor` refuses to build a
//      link from a `pr_url` alone, which is what keeps the inference from
//      creeping back in.
//   5. **The log panel is bounded.** A capped window of `logs RUN --json`'s
//      lines and a link to the full transcript, which is `logs RUN --url`'s own
//      line and goes through the same `data-href` treatment as every other
//      destination. `--follow` is never passed: it would be an unbounded child
//      process writing an unbounded amount of installation data into a renderer
//      that cannot read it (design section 2.3) -- `spawnlog.test.ts` holds the
//      whole spawn log to the absence of the flag, and this file never mentions
//      the argv.
//   6. **The diagnose view.** The halt, the failed and flaky tests, the review's
//      findings, the agent's own message, the log tail, the report path, the
//      draft note, the follow-up, and the two warnings the document carries:
//      `reported_warning` (what the providers said the calls cost beside what
//      the table charged) and `pin_warnings` (the model prices the cap counted
//      on a guess). Each is drawn verbatim through `putText`, because each is a
//      CLI sentence a person has to be able to read unedited.
//   7. **The replay buffer.** `shared/replay.ts`'s ring, fed the same arrival
//      shape the live feed produces, with the moment scrubbed to drawn beside
//      its span. The design puts it here because this is where "the run was slow
//      at 14:20" is asked (section 6.1 rule 4).
//
// Everything is Task 2.0's two rules: a number enters through `readNumber` and
// is drawn through `putReading`; a document's string is drawn through `putText`
// with the class its wire key declares. Nothing here builds markup, sets a
// navigating attribute or handles an event.

import { UNAVAILABLE_MARK, measured, notional, readNumber, unavailable } from '../../shared/availability'
import type { Reading } from '../../shared/availability'
import { ReplayBuffer, REPLAY_MAX_RECORDS, REPLAY_WINDOW_SECONDS } from '../../shared/replay'
import type { ReplayArrival, ReplayDocument, ReplayRecord } from '../../shared/replay'
import type { OperationName } from '../../main/ipc'
import { launcherFull } from '../../shared/launcher'
import { readRigor, rigorLayerWord, rigorModeWord } from '../../shared/rigor'
import type { RigorView } from '../../shared/rigor'
import { sanitizeText } from '../../shared/sanitize'
import type { TextClass } from '../../shared/sanitize'
import { putLink, putReading, putText } from '../app'
import { BUSY_SLOT_CLASS } from '../section'
import type { SkeletonBlock, SkeletonShape } from '../skeleton'
import { disabledWrite } from '../writes'
import type { ElementLike } from '../app'

/**
 * RUN_LOG_CAP is how many lines of `logs RUN --json` this screen draws. The
 * CLI's own `logs` writes every entry the run produced; a renderer that cannot
 * read the transcript is not a place to put a hundred thousand lines, so the
 * console reads a bounded window and links to the rest (design section 2.3).
 * The cap is the *tail*: the newest lines are the ones a person is looking for.
 */
export const RUN_LOG_CAP = 200

/**
 * RUN_THEMES is the set of themes a screenshot golden is taken for, the same
 * two the Live, Spend and Projects screens name.
 */
export const RUN_THEMES = Object.freeze(['light', 'dark'] as const)

/** RunTheme is one of them. */
export type RunTheme = (typeof RUN_THEMES)[number]

/**
 * PR_NO_LINK_REASON is the one line the screen puts beside a pull request's
 * number: the design's gap 7, in the design's own words -- section 5.1 rule 2's
 * sentence, verbatim. It was written here as a two-clause explanation of the
 * same thing (what a rebuilt link would name, and which providers there are),
 * and the owner's report of 2026-10-21 named it "the long explanatory sentence
 * under it"; the design already had the one line.
 */
export const PR_NO_LINK_REASON =
  'this CLI does not say which provider this run’s repository is on, so the console will not build a link from it'

/**
 * GitProvider is the set of providers Fugaro opens pull requests on
 * (`docs/git-providers.md`; the kinds `internal/task/task.go`'s `Slug` hashes
 * together with the repository). **No document reports one** -- that is gap 7 --
 * which is why this type exists for the construction below and appears nowhere
 * in the model.
 */
export type GitProvider = 'github' | 'bitbucket'

/**
 * pullRequestUrl builds the pull request URL for a provider, a repository and a
 * number. It is the construction design section 5.1 rule 2 describes, one
 * function per provider, and it is **unit-tested now** and unused by the screen
 * until gap 7 lands: the day a document names the provider, the screen calls
 * this and offers a link.
 *
 * The two shapes differ in more than the host, which is why one template cannot
 * serve both: GitHub's path is `/<owner>/<name>/pull/<n>`, Bitbucket Cloud's is
 * `/<workspace>/<name>/pull-requests/<n>`. A `repo` that is not exactly
 * `owner/name` -- and a number that is not a positive integer -- produce no URL
 * rather than a guessed one.
 */
export function pullRequestUrl(provider: GitProvider, repo: string, number: number): string | null {
  const parts = repo.split('/')
  if (parts.length !== 2) return null
  const [owner, name] = parts as [string, string]
  if (owner === '' || name === '' || owner.includes('..') || name.includes('..')) return null
  if (!Number.isInteger(number) || number <= 0) return null
  const host = provider === 'github' ? 'github.com' : 'bitbucket.org'
  const path = provider === 'github' ? 'pull' : 'pull-requests'
  return `https://${host}/${owner}/${name}/${path}/${String(number)}`
}

/**
 * linkFor is what the screen calls, and it takes the provider as an argument
 * rather than inferring one. **There is no overload that takes a `pr_url`**: a
 * URL the host gave is not evidence of which host gave it -- a Bitbucket URL and
 * a GitHub URL look alike, and the design's allow-list would admit the wrong one
 * -- so a caller with no provider gets `null` and the screen draws text. That is
 * the "refuses to build a link from a `pr_url` alone" rule, as a function
 * signature rather than a convention.
 */
export function linkFor(provider: GitProvider | null, repo: string | null, number: number | null): string | null {
  if (provider === null || repo === null || number === null) return null
  return pullRequestUrl(provider, repo, number)
}

// ---------------------------------------------------------------------------
// The model.
// ---------------------------------------------------------------------------

/** RunRow is the run's own record: `diagnose`'s `row`, which is `runview.Row`. */
export interface RunRow {
  readonly run: string
  readonly runId: string
  readonly repo: string | null
  readonly workflow: string | null
  readonly recipe: string | null
  readonly oauthAccount: string | null
  readonly status: string
  readonly stage: string | null
  readonly reason: string | null
  readonly branch: string | null
  readonly outcome: string | null
  readonly requestedBy: string | null
  /** the run's whole recorded launcher (`launched_by`), `null` when the record
   * carries none. The **whole** value and not the short form a column has room
   * for: this screen is the run view, which is where the owner's spec puts the
   * full identity (`shared/launcher.ts`) */
  readonly launchedBy: string | null
  readonly created: string | null
  readonly startedAt: string | null
  readonly imageAge: Reading
  readonly terminal: boolean
  readonly settled: boolean
  readonly followUp: boolean
  readonly staleDraft: boolean
  /** the run's rigor block -- the mode it resolved, the layer that decided it
   * and the rules that matched -- or `null` for a record that carries none this
   * reader can show (`shared/rigor.ts`). The header draws the Rigor row exactly
   * when it is not null: **absence is "no record of this run's rigor"**, which
   * is not a mode, and a row saying so would be a row about the console. */
  readonly rigor: RigorView | null
}

/** RunLedger is the run's spend: the three accounts, apart, and the plan's
 * part of the model figure, which is none of them.
 *
 * `total` is the dollars the table **billed** (`runstore.Cost.TotalUSD`); it is
 * the hero, and nothing on this screen adds anything to make it. `model` is the
 * billed part of the model figure and `compute` the estimate beside it.
 *
 * `notional` is `model_notional_usd`, the part of the model figure the run's
 * plan paid. It is **not money anybody was charged**, so it is drawn as its own
 * line, is never a segment of the bar and is never inside the hero: a hero of
 * `total + notional` would be a bill nobody received. The record carries it only
 * from C1.16, so an absent one is `unavailable` and no line is drawn -- and a
 * record whose whole figure is the plan's (a subscription run written before the
 * key existed) draws that figure once, on the model line, under the notional
 * mark. */
export interface RunLedger {
  readonly model: Reading
  readonly compute: Reading
  readonly total: Reading
  readonly notional: Reading
}

/** RunStage is one stage of the timeline. */
export interface RunStage {
  /** `stage` is the verify record's `kind` -- `build`, `test` or `lint` -- or,
   * when the document has no verify records, the row's own `stage`. */
  readonly stage: string
  readonly passed: boolean | null
  readonly tests: Reading
  readonly failures: Reading
  readonly duration: Reading
  /** `spend` is `unavailable` on every document this tree can produce: see the
   * header. The sentence names what the document carries instead. */
  readonly spend: Reading
  /** `deadline` is the row's own enum word -- `ok`, `near` or `over` -- when the
   * document carries one (a live `watch --json` entry does; a `runview.Row` does
   * not), and null when it does not. It is not a `Reading`: a deadline's state is
   * a word the CLI wrote from its own table and not a number, and drawing it
   * through the availability vocabulary would be putting a measurement's clothes
   * on an enum. */
  readonly deadline: string | null
  /** `deadlineBecause` is why there is no deadline word, and it is null exactly
   * when `deadline` is not. */
  readonly deadlineBecause: string | null
}

/** RunPullRequest is the pull request as text. `link` is null -- always, until
 * design section 4.3's gap 7 lands -- and `because` says why. */
export interface RunPullRequest {
  readonly repository: string | null
  readonly number: number | null
  readonly url: string | null
  readonly link: null
  readonly because: string
}

/** RunLogLine is one `logLine` of the capped window. */
export interface RunLogLine {
  readonly time: string | null
  readonly severity: string
  readonly stage: string | null
  readonly message: string
}

/** RunLogs is the capped window, its bounds, and the link to the transcript. */
export interface RunLogs {
  readonly lines: readonly RunLogLine[]
  readonly total: Reading
  readonly dropped: Reading
  /** `refused` is how many lines the read refused -- torn, oversized or not a
   * `logLine` -- and is a reading so that zero is drawn only when it was read. */
  readonly refused: Reading
  readonly link: string | null
  readonly linkBecause: string | null
}

/** RunFinding is one review finding (`runner.Finding`). */
export interface RunFinding {
  readonly severity: string
  readonly file: string
  readonly summary: string
}

/** RunSpendSplit is one entry of `route_by` or `model_by`: a name and a dollar
 * figure, kept as a reading so a name with no figure is drawn as such. */
export interface RunSpendSplit {
  readonly name: string
  readonly usd: Reading
}

/**
 * RunSpendStage is one stage of the run's own spend record: the document's
 * top-level `stages`, one `runstore.StageTiming` each. It is the run's spend by
 * stage, which the verify timeline beside it is not -- a verify record carries
 * no spend at all.
 *
 * `calls` and `agents` are **pointers** on the wire, so a stage nobody counted
 * has the key absent and a stage counted at zero has a zero: the two are
 * different facts, and the absent one is drawn as an `unavailable` reading
 * saying so rather than as a zero.
 */
export interface RunSpendStage {
  readonly name: string
  readonly duration: Reading
  readonly calls: Reading
  readonly agents: Reading
  /** `usd` is the stage's billed dollars and `notional` the part of its model
   * figure the run's plan paid (C1.16, D140). The two are a split of one figure
   * and are never summed, here or anywhere else: `usd` is the figure less that
   * part whenever the record carries one, so a plan-paid stage reads as a
   * measured `$0.00` beside the part that paid it rather than as the plan's
   * figure in both cells. Only the record with no part at all takes
   * `modelReading`'s subscription fallback, which is the one shape where the
   * whole figure is known to be the plan's. */
  readonly usd: Reading
  readonly notional: Reading
}

/** RunToleratedFinding is one finding the run's resolved mode let through
 * (`runstore.ToleratedFinding`). The three score axes are numbers the reviewer
 * wrote and are drawn as such; `mode`, `label` and `class` are the write path's
 * own vocabulary, which a hand-edited record need not respect, so they are
 * strings this screen draws as text. */
export interface RunToleratedFinding {
  readonly title: string
  readonly label: string
  readonly mode: string
  readonly likelihood: Reading
  readonly impact: Reading
  readonly fixCost: Reading
  /** the wire key is `class`: the reviewer's own word for what kind of finding
   * this is, and not a CSS class or an ECMAScript keyword here */
  readonly scoreClass: string
}

/** RunTolerated is the findings the run's resolved mode let through, as the
 * document's `tolerated` block: the total it carries and the entries it lists.
 * `total` is how many findings the mode really tolerated and is **never below**
 * the entries listed, so a shorter list is not the whole of it. */
export interface RunTolerated {
  readonly total: Reading
  readonly findings: readonly RunToleratedFinding[]
  /** `dropped` is how many entries this screen's own bound left out, which is a
   * fact about the panel rather than about the run. */
  readonly dropped: Reading
}

/** RunDiagnosis is the rest of the document: what the run's diagnosis says. */
export interface RunDiagnosis {
  readonly failed: readonly string[]
  readonly flaky: readonly string[]
  readonly findings: readonly RunFinding[]
  readonly agentMessage: string | null
  readonly logTail: readonly string[]
  readonly reportPath: string | null
  readonly draftNote: string | null
  readonly commentsPath: string | null
  readonly byRoute: readonly RunSpendSplit[]
  readonly byModel: readonly RunSpendSplit[]
  readonly reportedUsd: Reading
  readonly chargeUsd: Reading
  readonly reportedWarning: string | null
  readonly pinWarnings: readonly string[]
  readonly followUpPr: Reading
  readonly followUpComments: Reading
  readonly followUpSession: string | null
}

/** RunHalt is why the run halted (`runstore.Halt`): the reason, the scope it
 * applied at and when. */
export interface RunHalt {
  readonly reason: string
  readonly scope: string | null
  readonly at: string | null
  readonly detail: string | null
}

/** RunReplay is the replay buffer, as the screen draws it. */
export interface RunReplay {
  readonly records: Reading
  readonly windowSeconds: Reading
  readonly dropped: Reading
  readonly from: string | null
  readonly to: string | null
  /** `stamps` is every record's own stamp, in the ring's order. The scrubber's
   * ticks are placed from these and not from a count: a tick is a record that
   * arrived, at the moment it arrived. */
  readonly stamps: readonly string[]
  /** the frame in effect at the moment scrubbed to, or null */
  readonly scrubbed: ReplayDocument | null
  readonly scrubbedBecause: string | null
}

/** RunModel is the whole screen. */
export interface RunModel {
  readonly row: RunRow
  readonly ledger: RunLedger
  /** the run's spend by stage: the document's top-level `stages`, which is the
   * run's own per-stage record and not the verify timeline below. */
  readonly spendStages: readonly RunSpendStage[]
  /** `spendStagesDropped` is how many entries of `document.stages` the panel did
   * not draw -- the bound's overflow and the entries that were not objects, so
   * the rows and this count together are the whole array. */
  readonly spendStagesDropped: Reading
  /** the findings the run's resolved mode tolerated, or `null` when the document
   * carries no `tolerated` block -- which is what a run that tolerated nothing
   * looks like. */
  readonly tolerated: RunTolerated | null
  readonly stages: readonly RunStage[]
  readonly halt: RunHalt | null
  readonly pullRequest: RunPullRequest
  readonly logs: RunLogs
  readonly diagnosis: RunDiagnosis
  readonly replay: RunReplay
  readonly warnings: readonly string[]
}

/**
 * RunInputs is everything the screen is drawn from. `diagnosis` is `diagnose
 * RUN --json`; `logs` is the parsed `logLine` objects of the capped window (the
 * reading of the lines is `parseLogWindow` below, and a caller that read a
 * document longer than the cap hands the whole list over -- the cap is this
 * screen's, so this screen is where it is applied); `logLink` is `logs RUN
 * --url`'s one line. `replay` is the ring the caller **keeps** (a
 * `ReplayBuffer` it observes each arrival into once, so what it holds is
 * bounded), and `scrubAt` is the moment a person scrubbed to. A list of
 * arrivals is accepted too, and a fresh ring is built from it -- for a test or
 * a one-off draw; a caller that kept every arrival to redraw from would be
 * keeping an unbounded list, which is what the ring exists to avoid.
 */
export interface RunInputs {
  readonly diagnosis: unknown
  readonly logs: unknown
  readonly logLink: string | null
  readonly replay?: ReplayBuffer | readonly ReplayArrival[] | undefined
  readonly scrubAt?: string | null | undefined
  readonly sentences?: Partial<Record<OperationName, string>> | undefined
}

// ---------------------------------------------------------------------------
// Reading the document.
// ---------------------------------------------------------------------------

function objectAt(raw: unknown): Record<string, unknown> | null {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return null
  return raw as Record<string, unknown>
}

function arrayAt(raw: unknown): readonly unknown[] | null {
  return Array.isArray(raw) ? raw : null
}

function textAt(raw: unknown): string | null {
  return typeof raw === 'string' ? raw : null
}

function textsAt(raw: unknown): readonly string[] {
  const array = arrayAt(raw)
  if (array === null) return []
  return array.filter((item): item is string => typeof item === 'string')
}

function boolAt(raw: unknown): boolean {
  return raw === true
}

/** numberAt is an integer the document carries, used for a **count drawn as
 * text** (`pr`, `pr_number`): a pull request's number is an identifier, not a
 * measurement, and drawing it through a reading would invite the availability
 * vocabulary onto a fact that is either present or absent. */
function numberAt(raw: unknown): number | null {
  if (typeof raw !== 'number' || !Number.isFinite(raw)) return null
  if (!Number.isInteger(raw) || raw <= 0) return null
  return raw
}

/** THE PR NUMBER, TWO SPELLINGS. `runview.Row` (which is what `diagnose`'s
 * `row` is) spells it `pr`; a finished run's `watch --json` entry spells it
 * `pr_number` (design section 4.3's gap 6). A live entry carries neither, and
 * then there is no number -- which the screen says by drawing none, not by
 * drawing a zero. */
function prNumberOf(row: Record<string, unknown>): number | null {
  return numberAt(row['pr']) ?? numberAt(row['pr_number'])
}

/**
 * COMPUTE_NOT_ESTIMATED is the sentence a not-estimated compute carries.
 * `ComputeEstimated` "is true only when prices and resources were known; when
 * false, ComputeUSD is 0 and means 'not estimated', not free"
 * (`internal/runstore/cost.go:15`), so the flag off is neither a measured
 * `$0.00` -- which reads as free, the one thing design section 10.1 forbids --
 * nor a bare `n/a`, which would leave out why. The Projects screen declares its
 * own copy of this sentence for the same field; the two screens are siblings and
 * neither imports the other, so the wording is held equal by the tests of both
 * rather than by a shared module that is not this task's to add.
 */
const COMPUTE_NOT_ESTIMATED =
  'compute is not estimated for this run: the price table or the run\'s resources were unknown, and a `compute_usd` of 0 with `compute_estimated` false means "not estimated", not free'

/** modelReading is the ledger's model figure: the row's model dollars as
 * **money** (C1.16, P12/D140). `model_usd` is the whole model figure -- the
 * money and the subscription's list price together -- and `model_notional_usd`
 * is the part of it the plan paid, so the billed dollars are the difference.
 * The basis alone cannot make that split: a mixed cascade run's basis is
 * `api-list` for the whole run while part of its figure is the plan's, which is
 * what this screen used to draw as billed money.
 *
 * A row written before C1.16 carries no `model_notional_usd`, and then the
 * subscription basis is the fallback: such a row's whole figure is the plan's.
 * The state follows the figure: the money part when the row has one, and
 * otherwise the notional figure marked notional -- the same figure and the same
 * mark a subscription row drew before the key existed. The Projects screen
 * declares its own copy of this rule for its own document: the four screens are
 * siblings and neither imports the other, so the rule is held equal by the
 * tests of both rather than by a shared module that is not this task's to add. */
function modelReading(model: Reading, notional: Reading, basis: string | null): Reading {
  if (model.value === null) return model
  const plan = notional.value ?? (basis === 'subscription' ? model.value : 0)
  const money = Math.max(0, model.value - plan)
  if (money > 0) return { ...model, value: money }
  if (plan > 0 || basis === 'subscription') return { ...model, value: plan, state: 'notional' }
  return model
}

/** modelBasisOf is `runstore.Cost.ModelBasis`, the run's own answer to which
 * price its model figure is in. The ledger reads it to split the row's figure
 * and the spend-by-stage panel reads it to place a stage's notional part, so the
 * traversal is stated once here rather than twice. */
function modelBasisOf(row: Record<string, unknown>): string | null {
  return textAt((objectAt(row['cost']) ?? {})['model_basis'])
}

/** costOf reads `runstore.Cost`. Compute is what `ComputeEstimated` says it is:
 * an estimate when the flag is on, and **not an estimate** -- `unavailable`,
 * with the CLI's own sentence -- when it is off, because the flag off is a
 * `compute_usd` of 0 that means "not estimated", not free
 * (`internal/runstore/cost.go:15`, design section 10.1). The sentence is the
 * Projects screen's own constant: the two screens draw the same field and say
 * the same thing about it. */
function ledgerOf(row: Record<string, unknown>): RunLedger {
  const cost = objectAt(row['cost']) ?? {}
  const basis = modelBasisOf(row)
  const model = readNumber(cost['model_usd'], 'row.cost.model_usd')
  const notional = readNumber(cost['model_notional_usd'], 'row.cost.model_notional_usd')
  const compute = readNumber(cost['compute_usd'], 'row.cost.compute_usd')
  return {
    model: modelReading(model, notional, basis),
    compute:
      cost['compute_estimated'] === true
        ? compute.value === null
          ? compute
          : { ...compute, state: 'estimated' }
        : compute.value === null || compute.value === 0
          ? unavailable(COMPUTE_NOT_ESTIMATED)
          : compute,
    total: readNumber(cost['total_usd'], 'row.cost.total_usd'),
    notional: planReading(model, notional),
  }
}

/** NOTIONAL_NONE_BECAUSE is what the ledger's notional line says when the record
 * carries no plan-paid part: `model_notional_usd` is `omitempty`, so a run that
 * paid nothing through its plan writes no key, and the absence is a measured
 * nothing rather than a part the console could not read. */
const NOTIONAL_NONE_BECAUSE = 'the record carries no row.cost.model_notional_usd'

/** planReading is the ledger's notional line: the part of the model figure the
 * record says the plan paid, marked **notional** so the plan's list price is
 * never read as a bill (`formatReading` draws it `~$X`). A hand-edited record
 * can carry a part above the figure it is part of; the part is read as the part
 * of that figure it can be, the same clamp `stagePartOf` applies to a stage. */
function planReading(model: Reading, part: Reading): Reading {
  if (part.value === null || part.value <= 0) return unavailable(NOTIONAL_NONE_BECAUSE)
  const ceiling = model.value ?? part.value
  return notional(Math.min(part.value, ceiling))
}

/** STAGE_SPEND_BECAUSE is the sentence a verify record's spend cell carries: a
 * verify record has no spend of its own, and the run's spend by stage is the
 * document's top-level `stages`, which the "spend by stage" panel draws. */
const STAGE_SPEND_BECAUSE =
  'a verify record carries no spend: the run\'s spend by stage is the "spend by stage" panel, and `diagnose`\'s own splits are drawn below'

/** STAGE_SPEND_NO_STAGES is the same honesty for a document with no `stages` at
 * all: there is no panel to point at, so the cell says which key is missing
 * rather than naming one that is not drawn. */
const STAGE_SPEND_NO_STAGES =
  'the document carries no stages: a verify record carries no spend, and no document gives spend by stage for this run'

/** DEADLINE_BECAUSE is the same honesty for a deadline the row does not carry. */
const DEADLINE_BECAUSE = 'the document has no row.deadline'

/** TOLERATED_ABSENT is the one line a document with no `tolerated` block draws.
 * The absence is **not** the claim "nothing was tolerated": the CLI writes no
 * block for a run that tolerated nothing, for every record written before the
 * field existed (`schemas/result.schema.json`), and for a run whose record could
 * not be read at all (`internal/cli/diagnose.go` only fills `Tolerated` when the
 * record is there). The document cannot tell those three apart, so the line
 * names the key that is missing and never the fact. */
const TOLERATED_ABSENT =
  'the document carries no tolerated block: a run that tolerated nothing, a record written before this field existed and a run whose record could not be read all look the same here'

/**
 * RUN_SPEND_STAGES_MAX and RUN_TOLERATED_MAX are how many entries of each new
 * panel this screen draws, and they are the CLI's own bounds: `diagnoseStages`
 * (`internal/cli/diagnose.go`) and `runstore.MaxTolerated`. The document is an
 * object a hand can edit, so the length it carries is not a length this screen
 * trusts -- it draws this many entries and counts the rest, rather than
 * building a list nobody bounded.
 */
export const RUN_SPEND_STAGES_MAX = 128
export const RUN_TOLERATED_MAX = 100

/** NOT_COUNTED_BECAUSE is what a stage's `calls` or `agents` says when the key
 * is absent: `calls` and `agents` are pointers on the wire, so absent means
 * nobody counted the stage, which is a different fact from a stage counted at
 * zero. */
const NOT_COUNTED_BECAUSE = 'not counted for this stage'

/** countedAt reads one of the two pointer counts: a number the document carries
 * is measured (zero included), an absent key is the sentence above, and anything
 * else is `readNumber`'s own refusal. */
function countedAt(record: Record<string, unknown>, key: string, field: string): Reading {
  if (record[key] === null || record[key] === undefined) return unavailable(NOT_COUNTED_BECAUSE)
  return readNumber(record[key], field)
}

/** moneyAt reads one of the stage's two **floats**, which are the opposite case
 * to the pointers above: `ModelUSD` and `ModelNotionalUSD` are `omitempty`
 * (`internal/runstore/runstore.go`), so `encoding/json` writes no key at all for
 * a zero and a reader cannot tell that zero from the key never having been
 * written. An absent key is therefore a measured zero -- the value the document
 * carries -- and never an `n/a`: reading it as unknown would draw `n/a` for the
 * whole spend of every stage that cost nothing, which is most of them. */
function moneyAt(record: Record<string, unknown>, key: string, field: string): Reading {
  if (record[key] === null || record[key] === undefined) return measured(0)
  return readNumber(record[key], field)
}

/** stagePartOf reads the stage's `model_notional_usd`: the part of the stage's
 * model figure the run's plan paid (`runstore.StageTiming`, C1.16). The key is
 * `omitempty`, so a part of zero is written as no key at all, and the reading is
 * an `unavailable` with **no value** then -- which is what tells `modelReading`
 * to apply the one fallback it has for a record written before the key existed:
 * a subscription run's whole figure is the plan's.
 *
 * A hand-edited record can carry a part above the figure it is part of, or below
 * zero; the record's own writer clamps it into that range
 * (`runstore.Cost.ModelSubscriptionUSD` is `min(ModelNotionalUSD, ModelUSD)` and
 * `WithNotional` clamps what it records), so the part is read as the part of the
 * stage's figure it can be. */
function stagePartOf(record: Record<string, unknown>, whole: Reading): Reading {
  const carried = readNumber(record['model_notional_usd'], 'stages[].model_notional_usd')
  if (carried.value === null) return carried
  const ceiling = whole.value ?? carried.value
  return measured(Math.min(Math.max(carried.value, 0), ceiling))
}

/** stageBilled is the usd cell: the stage's model dollars as **money**, which
 * is its whole figure less the part the record says the plan paid.
 *
 * The record's own split decides it whenever the record carries one. A
 * subscription run writes `model_notional_usd` equal to the stage's whole figure
 * for every stage it settled without a gateway (`internal/runner/cost.go`), so a
 * carried part equal to the figure means the plan paid all of it and the billed
 * dollars are a measured `$0.00` -- the notional cell beside it names what paid
 * them, which is what keeps that zero from reading as free. Reading that shape
 * through `modelReading`'s fallback instead would draw the same figure in both
 * cells and read as twice the stage's spend, the sum this panel exists not to
 * draw.
 *
 * The fallback is for the record that carries no part at all: a stage written
 * before C1.16, whose whole figure is the plan's, and which has no part to put
 * in the second cell. */
function stageBilled(whole: Reading, part: Reading, basis: string | null): Reading {
  if (part.value === null) return modelReading(whole, part, basis)
  if (whole.value === null) return whole
  return measured(Math.max(0, whole.value - part.value))
}

/** stageNotional is the notional cell: the part of the figure the record wrote,
 * drawn as **notional** and never as money (`formatReading` marks a notional
 * figure `~$X`), so the plan's list price is never read as a bill. A
 * part of zero is a measured `$0.00` rather than `~$0.00`, which would
 * mark nothing as a plan price.
 *
 * This cell answers only what the record carries, and the fallback for a record
 * that carries nothing is the billed cell's (`stageBilled`): a part invented
 * here as well would be the same figure in both cells, and the two together
 * would read as twice the stage's one figure. */
function stageNotional(part: Reading): Reading {
  if (part.value === null || part.value === 0) return measured(0)
  return notional(part.value)
}

/** spendStagesOf reads the document's top-level `stages` (`runstore.StageTiming`)
 * into the panel's rows, bounded at `RUN_SPEND_STAGES_MAX`. An entry that is not
 * an object is skipped rather than thrown on: the record is a hand-editable
 * object and a screen that threw would draw nothing at all. `basis` is the run's
 * `cost.model_basis`, which `stageBilled` needs for a stage whose part the
 * record does not carry. */
function spendStagesOf(raw: unknown, basis: string | null): readonly RunSpendStage[] {
  const records = arrayAt(raw) ?? []
  const stages: RunSpendStage[] = []
  for (const item of records.slice(0, RUN_SPEND_STAGES_MAX)) {
    const record = objectAt(item)
    if (record === null) continue
    const whole = moneyAt(record, 'model_usd', 'stages[].model_usd')
    const part = stagePartOf(record, whole)
    stages.push({
      name: textAt(record['name']) ?? '',
      duration: readNumber(record['duration_s'], 'stages[].duration_s'),
      calls: countedAt(record, 'calls', 'stages[].calls'),
      agents: countedAt(record, 'agents', 'stages[].agents'),
      // Only the billed part of the figure is money, and the notional cell
      // beside this one holds the part that was not billed: the two are the
      // stage's one figure, split, and never the same figure twice.
      usd: stageBilled(whole, part, basis),
      notional: stageNotional(part),
    })
  }
  return stages
}

/** toleratedOf reads the document's `tolerated` block, bounded at
 * `RUN_TOLERATED_MAX` on read as it is on write. `null` for a document that
 * carries no block, which is what a run that tolerated nothing looks like.
 *
 * `dropped` is the raw length less the rows drawn, so the rows and the count
 * together are the whole array: an entry skipped because it is not an object is
 * an entry this panel did not draw, and counting only the bound's overflow would
 * let a shorter array read as the whole record. */
function toleratedOf(raw: unknown): RunTolerated | null {
  const block = objectAt(raw)
  if (block === null) return null
  const entries = arrayAt(block['findings']) ?? []
  const findings: RunToleratedFinding[] = []
  for (const item of entries.slice(0, RUN_TOLERATED_MAX)) {
    const finding = objectAt(item)
    if (finding === null) continue
    findings.push({
      title: textAt(finding['title']) ?? '',
      label: textAt(finding['label']) ?? '',
      mode: textAt(finding['mode']) ?? '',
      likelihood: readNumber(finding['likelihood'], 'tolerated.findings[].likelihood'),
      impact: readNumber(finding['impact'], 'tolerated.findings[].impact'),
      fixCost: readNumber(finding['fix_cost'], 'tolerated.findings[].fix_cost'),
      scoreClass: textAt(finding['class']) ?? '',
    })
  }
  return {
    total: readNumber(block['total'], 'tolerated.total'),
    findings,
    dropped: measured(Math.max(0, entries.length - findings.length)),
  }
}

/** stagesOf builds the timeline. Each verify record is a stage; when the
 * document carries none, the row's own stage is the one row of the timeline --
 * a run that has not reached its verify gate has a stage and no records, and
 * drawing nothing for it would be drawing a run with no stages at all. */
function stagesOf(row: Record<string, unknown>, raw: unknown, spendBecause: string): readonly RunStage[] {
  const records = arrayAt(raw) ?? []
  const deadline = deadlineState(row)
  const stages: RunStage[] = []
  for (const item of records) {
    const record = objectAt(item)
    if (record === null) continue
    // `passed` is tri-state on purpose: a record whose `passed` is not a bool is
    // a record whose outcome this screen does not know, and `false` would draw
    // it as a failure.
    const passed = typeof record['passed'] === 'boolean' ? record['passed'] : null
    stages.push(stageOf(textAt(record['kind']) ?? textAt(record['stage']) ?? '', record, passed, deadline, spendBecause))
  }
  if (stages.length === 0) {
    const stage = textAt(row['stage'])
    if (stage !== null && stage !== '') stages.push(stageOf(stage, {}, null, deadline, spendBecause))
  }
  return stages
}

/** stageOf is one timeline row. The deadline comes from the **row** and not from
 * the record: it is the row that carries the enum, and one run has one of them
 * rather than one per stage. */
function stageOf(
  stage: string,
  record: Record<string, unknown>,
  passed: boolean | null,
  deadline: string | null,
  spendBecause: string,
): RunStage {
  return {
    stage,
    passed,
    tests: readNumber(record['tests'], 'verify[].tests'),
    failures: readNumber(record['failures'], 'verify[].failures'),
    duration: readNumber(record['duration_s'], 'verify[].duration_s'),
    spend: unavailable(spendBecause),
    deadline,
    deadlineBecause: deadline === null ? DEADLINE_BECAUSE : null,
  }
}

/** DEADLINE_STATES is the four words `JSONRun.deadline` can hold
 * (`internal/watch/json.go:69`): `ok`, `near`, `over`, and the empty string a
 * document with no deadline would carry. It is an enum the CLI writes from its
 * own table, so it is drawn as the CLI worded it and never parsed. */
const DEADLINE_STATES: readonly string[] = Object.freeze(['ok', 'near', 'over'])

/** deadlineState reads the row's own deadline: present on a live `watch --json`
 * entry, absent from `runview.Row`. A word this build does not know is no
 * deadline rather than a word drawn as if it were one. */
function deadlineState(row: Record<string, unknown>): string | null {
  const state = textAt(row['deadline'])
  if (state === null || state === '' || !DEADLINE_STATES.includes(state)) return null
  return state
}

/** splitOf reads a `map[string]float64` -- `route_by` or `model_by` -- as a
 * list of names with their figures, in the document's own order. */
function splitOf(raw: unknown, field: string): readonly RunSpendSplit[] {
  const map = objectAt(raw)
  if (map === null) return []
  return Object.entries(map).map(([name, value]) => ({ name, usd: readNumber(value, field) }))
}

/** diagnosisOf reads the diagnose view's own fields. */
function diagnosisOf(document: Record<string, unknown>): RunDiagnosis {
  const followUp = objectAt(document['follow_up'])
  const findings = (arrayAt(document['findings']) ?? []).flatMap((item) => {
    const finding = objectAt(item)
    if (finding === null) return []
    return [
      {
        severity: textAt(finding['severity']) ?? '',
        file: textAt(finding['file']) ?? '',
        summary: textAt(finding['summary']) ?? '',
      },
    ]
  })
  return {
    failed: textsAt(document['failed']),
    flaky: textsAt(document['flaky']),
    findings,
    agentMessage: textAt(document['agent_message']),
    logTail: textsAt(document['log_tail']),
    reportPath: textAt(document['report_path']),
    draftNote: textAt(document['draft_note']),
    commentsPath: textAt(document['comments_path']),
    byRoute: splitOf(document['route_by'], 'route_by'),
    byModel: splitOf(document['model_by'], 'model_by'),
    reportedUsd: readNumber(document['reported_usd'], 'reported_usd'),
    chargeUsd: readNumber(document['charge_usd'], 'charge_usd'),
    reportedWarning: textAt(document['reported_warning']),
    pinWarnings: textsAt(document['pin_warnings']),
    followUpPr: followUp === null ? unavailable('the document has no follow_up') : readNumber(followUp['pr'], 'follow_up.pr'),
    followUpComments:
      followUp === null ? unavailable('the document has no follow_up') : readNumber(followUp['comments'], 'follow_up.comments'),
    followUpSession: followUp === null ? null : textAt(followUp['session']),
  }
}

/** haltOf reads `runstore.Halt`. */
function haltOf(raw: unknown): RunHalt | null {
  const halt = objectAt(raw)
  if (halt === null) return null
  return {
    reason: textAt(halt['reason']) ?? '',
    scope: textAt(halt['scope']),
    at: textAt(halt['at']),
    detail: textAt(halt['detail']),
  }
}

/** LOG_NO_LINK is the sentence for a link the CLI did not print: `logs RUN
 * --url` is its own read, and a run whose link could not be read says so. */
const LOG_NO_LINK = 'no `logs RUN --url` line arrived: the full transcript is not linked'

/** LogWindow is what the log read handed over: its lines, how many entries the
 * read held in all (which can be more than it kept), and how many lines it
 * refused. */
export interface LogWindow {
  readonly lines: readonly RunLogLine[]
  readonly total: number
  readonly refused: number | null
}

/** parseLogWindow reads the log window. The CLI writes one compact `logLine`
 * object per line (contract section 6.2), and the main process's reader
 * (`parseLogEntries`, `src/source/cli.ts`) collects them into the window
 * document `{entries, total, refused}`; a plain list of `logLine` objects is
 * accepted too. The **newest** `RUN_LOG_CAP` are the ones drawn, and how many
 * were left out is a reading, so the panel cannot look like the whole
 * transcript. */
export function parseLogWindow(raw: unknown): LogWindow {
  const window = objectAt(raw)
  const lines = (window === null ? arrayAt(raw) : arrayAt(window['entries'])) ?? []
  const parsed: RunLogLine[] = []
  for (const item of lines) {
    const line = objectAt(item)
    if (line === null) continue
    parsed.push({
      time: textAt(line['time']),
      severity: textAt(line['severity']) ?? '',
      stage: textAt(line['stage']),
      message: textAt(line['message']) ?? '',
    })
  }
  const total = window === null ? null : wholeAt(window['total'])
  return {
    lines: parsed,
    // The read's own count when it carries one and it is not smaller than what
    // it handed over; otherwise the lines themselves are the whole count.
    total: total !== null && total >= parsed.length ? total : parsed.length,
    // A bare list carries no count of refused lines: unavailable, not zero.
    refused: window === null ? null : wholeAt(window['refused']),
  }
}

/** wholeAt is a count the log read carries: a non-negative integer, zero
 * included (unlike `numberAt`, whose identifiers start at one), or null. */
function wholeAt(raw: unknown): number | null {
  return typeof raw === 'number' && Number.isInteger(raw) && raw >= 0 ? raw : null
}

/** buildRunModel turns the document, the log window, the link and the ring into
 * the screen. It touches no element. */
export function buildRunModel(inputs: RunInputs): RunModel {
  const document = objectAt(inputs.diagnosis) ?? {}
  const row = objectAt(document['row']) ?? {}
  const repository = textAt(row['repo'])
  const number = prNumberOf(row)
  const url = textAt(row['pr_url'])

  const window = parseLogWindow(inputs.logs)
  const shown = window.lines.slice(Math.max(0, window.lines.length - RUN_LOG_CAP))

  // The run's own spend by stage, and the verify timeline beside it: the
  // timeline's spend cell names the panel exactly when the panel is drawn.
  const rawStages = arrayAt(document['stages'])
  const spendStages = spendStagesOf(rawStages, modelBasisOf(row))

  // The kept ring is read, never fed: drawing a screen is not an arrival.
  const buffer = inputs.replay instanceof ReplayBuffer ? inputs.replay : ringOf(inputs.replay ?? [])
  const bounds = buffer.bounds()
  const scrubAt = inputs.scrubAt ?? null
  const scrubbed = scrubAt === null ? null : buffer.scrub(scrubAt)

  return {
    row: {
      run: textAt(row['run']) ?? '',
      runId: textAt(row['run_id']) ?? '',
      repo: repository,
      workflow: textAt(row['workflow']),
      recipe: textAt(row['recipe']),
      oauthAccount: textAt(row['oauth_account']),
      status: textAt(row['status']) ?? '',
      stage: textAt(row['stage']),
      reason: textAt(row['reason']),
      branch: textAt(row['branch']),
      outcome: textAt(row['outcome']),
      requestedBy: textAt(row['requested_by']),
      launchedBy: textAt(row['launched_by']),
      created: textAt(row['created']),
      startedAt: textAt(row['started_at']),
      imageAge: readNumber(row['image_age_s'], 'row.image_age_s'),
      terminal: boolAt(row['terminal']),
      settled: boolAt(row['settled']),
      followUp: boolAt(row['follow_up']),
      staleDraft: boolAt(row['stale_draft']),
      rigor: readRigor(row['rigor']),
    },
    ledger: ledgerOf(row),
    spendStages,
    spendStagesDropped: measured(Math.max(0, (rawStages?.length ?? 0) - spendStages.length)),
    tolerated: toleratedOf(document['tolerated']),
    stages: stagesOf(
      row,
      document['verify'],
      spendStages.length > 0 ? STAGE_SPEND_BECAUSE : STAGE_SPEND_NO_STAGES,
    ),
    halt: haltOf(document['halt']),
    pullRequest: {
      repository,
      number,
      url,
      // Design section 4.3's gap 7: the link is not built, and the field is
      // typed `null` so that a later task cannot quietly fill it in without
      // changing this type.
      link: null,
      because:
        url === null && number === null
          ? 'the run\'s record carries no pull request'
          : PR_NO_LINK_REASON,
    },
    logs: {
      lines: shown,
      total: measured(window.total),
      dropped: measured(window.total - shown.length),
      refused: window.refused === null ? unavailable('the log read carries no count of refused lines') : measured(window.refused),
      link: inputs.logLink,
      linkBecause: inputs.logLink === null ? LOG_NO_LINK : null,
    },
    diagnosis: diagnosisOf(document),
    replay: {
      records: measured(bounds.records),
      windowSeconds: measured(bounds.windowSeconds),
      dropped: measured(bounds.dropped),
      from: bounds.from,
      to: bounds.to,
      stamps: buffer.records().map(recordStamp),
      scrubbed,
      scrubbedBecause:
        scrubAt === null
          ? 'nothing is scrubbed to: the panel shows the whole window'
          : scrubbed === null
            ? `the ring holds nothing at or before ${scrubAt}`
            : null,
    },
    warnings: [...textsAt(document['warnings']), ...textsAt(row['warnings'])],
  }
}

/** ringOf is a fresh ring over a list of arrivals (see `RunInputs.replay`). */
function ringOf(arrivals: readonly ReplayArrival[]): ReplayBuffer {
  const buffer = new ReplayBuffer()
  for (const arrival of arrivals) buffer.observe(arrival)
  return buffer
}

/** replayBoundsNote is the sentence the panel carries about the ring's own
 * bound: the cap is a fact about the panel, and `REPLAY_MAX_RECORDS` is where it
 * comes from. */
export const REPLAY_BOUND_NOTE = `the ring holds the last ${String(REPLAY_WINDOW_SECONDS / 60)} minutes, at most ${String(REPLAY_MAX_RECORDS)} records`

/** recordStamp is a frame's stamp, for the panel's span line. */
function recordStamp(record: ReplayRecord): string {
  return record.at
}

// ---------------------------------------------------------------------------
// The drawing.
// ---------------------------------------------------------------------------

/**
 * ScreenElement is what a screen may draw into: Task 2.0's `ElementLike`, a
 * tag, children and `append` -- and nothing else. Declared here as in `live.ts`,
 * `spend.ts` and `projects.ts` because the screens are siblings and the file the
 * interface belongs in (`renderer/app.ts`) is not this task's to edit; the four
 * declarations are structurally identical and the tests of each screen draw
 * **another screen's factory** through it, so a drift fails a test.
 */
export interface ScreenElement extends ElementLike {
  readonly tag: string
  append(child: ScreenElement): void
  childNodes(): readonly ScreenElement[]
}

/** ScreenFactory is where elements come from. */
export interface ScreenFactory {
  create(tag: string): ScreenElement
}

/**
 * RunFactory is a `ScreenFactory` that can also make an element in the **SVG**
 * namespace: a stage's mark and its bar are geometry, and an element made with
 * `create` is in the HTML namespace and is not drawn inside an `svg` at all.
 *
 * It is a second interface rather than a method added to `ScreenFactory`, for
 * the reason `live.ts`'s `LiveFactory` states: the two namespaces are two calls
 * in the DOM (`createElement` and `createElementNS`), and the cross-screen tests
 * hold the screens' factories assignable to each other -- a base factory that
 * had grown an SVG method would make that guard fail for a screen that draws no
 * SVG. `entry.ts`'s `DomScreenFactory` answers both.
 */
export interface RunFactory extends ScreenFactory {
  createNS(tag: string): ScreenElement
}

function make(factory: ScreenFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = factory.create(tag)
  element.setAttribute('class', classes.join(' '))
  return element
}

/** makeNS is `make` in the SVG namespace, so the class is still written in the
 * one place `make` writes it. */
function makeNS(factory: RunFactory, tag: string, ...classes: string[]): ScreenElement {
  return make({ create: (name) => factory.createNS(name) }, tag, ...classes)
}

function literal(element: ScreenElement, text: string): ScreenElement {
  element.textContent = text
  return element
}

function cell(parent: ScreenElement, factory: ScreenFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = make(factory, tag, ...classes)
  parent.append(element)
  return element
}

function textCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  tag: string,
  raw: unknown,
  textClass: TextClass,
  field: string,
  ...classes: string[]
): ScreenElement {
  const element = cell(parent, factory, tag, ...classes)
  putText(element, raw, textClass, field)
  return element
}

function numberCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  tag: string,
  reading: Reading,
  unit: Parameters<typeof putReading>[2],
  ...classes: string[]
): ScreenElement {
  const element = cell(parent, factory, tag, 'run-number', ...classes)
  putReading(element, reading, unit)
  return element
}

/**
 * putMaybe writes one of the run's own columns -- the repository, the
 * workflow, the recipe, the branch, the account, an actor -- or the console's
 * `n/a` with the reason in the `title` when the record carries none.
 *
 * **`n/a` and never an empty cell**, the rule `live.ts`'s `putActor` states: a
 * branch the record left out is not a run with no branch, and a blank cell says
 * nothing at all. Nothing is invented here: every value is the document's own
 * string on the one path and the console's mark on the other.
 */
function putMaybe(
  element: ScreenElement,
  raw: string | null,
  textClass: Parameters<typeof putText>[2],
  field: string,
): void {
  if (raw === null || raw === '') {
    literal(element, UNAVAILABLE_MARK)
    element.setAttribute('title', `the document carries no ${field}`)
    return
  }
  putText(element, raw, textClass, field)
}

/** maybeCell is `cell` with one of the run's own columns written into it
 * through `putMaybe`, in the class `run-meta-absent` when there is no value so
 * the mark is drawn in the muted tone. */
function maybeCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  tag: string,
  raw: string | null,
  textClass: Parameters<typeof putText>[2],
  field: string,
  ...classes: string[]
): ScreenElement {
  const element = cell(parent, factory, tag, ...classes, raw === null || raw === '' ? 'run-meta-absent' : 'run-meta-value')
  putMaybe(element, raw, textClass, field)
  return element
}

/** headerFrame draws the run's identity as far as the click carries it: where
 * the run sits (the crumb's way back to Live), the id the operator clicked and
 * the retry the console refuses. The meta line, the Rigor row, the status strip
 * and the halt banner are the record's and are `fillHeader`'s. */
function headerFrame(factory: RunFactory, run: string): { readonly header: ScreenElement; readonly crumb: ScreenElement } {
  const element = make(factory, 'header', 'run-header')
  // The crumb's first half is drawn here and its second by `fillCrumb`: the
  // breadcrumb's first word is the Live view and exists before any read, while
  // the repository and the run are the record's own.
  const crumb = make(factory, 'nav', 'run-crumb')
  crumb.setAttribute('aria-label', 'Breadcrumb')
  literal(cell(crumb, factory, 'button', 'run-crumb-live'), 'Live')
  crumbSeparator(factory, crumb)
  element.append(crumb)
  const head = cell(element, factory, 'div', 'run-head')
  const heading = cell(head, factory, 'h1', 'run-id')
  // **The id is drawn from the click and not from the record.** The operator
  // opened this run by clicking its id, so the one thing this screen owes them
  // in the frame the click happened in is that id in the heading; the record's
  // own `row.run` is drawn again in the crumb when it lands, and the two are the
  // same string for every door into a run (`entry.ts` draws each door's id from
  // the row it was drawn on).
  putText(heading, run, 'runId', 'row.run')
  // The one write this screen draws and does not perform (`writes.ts`): the
  // design's primary action, refused in the one place a reviewer reads.
  head.append(disabledWrite(factory, 'run-retry', 'Retry run'))
  return { header: element, crumb }
}

/**
 * fillCrumb is the rest of the breadcrumb: where the run sits -- the repository
 * and the run itself -- which is what the run's own record names.
 *
 * **The Live crumb is a real `button`.** The console navigates with buttons and
 * never with a navigating attribute (`entry.ts`'s own note on the view
 * navigation), and `entry.ts`'s `showRun` binds this one to the Live view's own
 * `show('live')` -- so the crumb is the way back from a run the operator
 * opened. The two slashes are `aria-hidden`: they separate the crumbs visually
 * and are not part of what a breadcrumb reads out.
 */
function fillCrumb(factory: RunFactory, model: RunModel, crumb: ScreenElement): void {
  // The repository's crumb: the mono value, or the console's mark with its
  // reason when the record carries no repository.
  const repo = cell(crumb, factory, 'span', 'run-crumb-repo', model.row.repo === null || model.row.repo === '' ? 'run-meta-absent' : 'run-meta-value')
  putMaybe(repo, model.row.repo, 'repository', 'row.repo')
  crumbSeparator(factory, crumb)
  const run = cell(crumb, factory, 'span', 'run-crumb-run')
  putText(run, model.row.run, 'runId', 'row.run')
}

/** fillHeader is the rest of the header: the run's own columns, its rigor, its
 * status strip and, for a halted run, the line naming why. All of them are the
 * record's, so all of them arrive with it. */
function fillHeader(factory: RunFactory, model: RunModel, header: ScreenElement): void {
  header.append(metaLine(factory, model))
  if (model.row.rigor !== null) header.append(rigorLine(factory, model.row.rigor))
  header.append(statusStrip(factory, model))
  if (model.halt !== null) header.append(haltLine(factory, model.halt))
}

/** crumbSeparator is the decorative slash between two crumbs. */
function crumbSeparator(factory: RunFactory, parent: ScreenElement): void {
  const element = literal(cell(parent, factory, 'span', 'run-crumb-sep'), '/')
  element.setAttribute('aria-hidden', 'true')
}

/**
 * metaLine is the run's own columns: repository, workflow, recipe, branch,
 * account, requester and launcher, each a small label over the value's own
 * string.
 *
 * **`workflow` and `recipe` are drawn where the design draws a "profile".** No
 * document this console reads carries a field called that, and inventing one
 * from two keys that look like it would be a figure about the run rather than
 * the run's own record; the two keys the record does carry are drawn instead,
 * and the departure is named in the pull request.
 *
 * An absent value is the console's `n/a` in the muted tone with the reason in
 * the `title` (`putMaybe`), never a blank cell: "no account was recorded" and
 * "this build did not ask" look the same on a blank cell and do not on the mark.
 */
function metaLine(factory: RunFactory, model: RunModel): ScreenElement {
  const element = make(factory, 'div', 'run-meta')
  const repo = cell(element, factory, 'span', 'run-meta-field')
  literal(cell(repo, factory, 'span', 'run-meta-label'), 'repo')
  maybeCell(repo, factory, 'span', model.row.repo, 'repository', 'row.repo', 'run-repo')
  const workflow = cell(element, factory, 'span', 'run-meta-field')
  literal(cell(workflow, factory, 'span', 'run-meta-label'), 'workflow')
  maybeCell(workflow, factory, 'span', model.row.workflow, 'label', 'row.workflow', 'run-workflow')
  const recipe = cell(element, factory, 'span', 'run-meta-field')
  literal(cell(recipe, factory, 'span', 'run-meta-label'), 'recipe')
  maybeCell(recipe, factory, 'span', model.row.recipe, 'label', 'row.recipe', 'run-recipe')
  const branch = cell(element, factory, 'span', 'run-meta-field')
  literal(cell(branch, factory, 'span', 'run-meta-label'), 'branch')
  maybeCell(branch, factory, 'span', model.row.branch, 'branch', 'row.branch', 'run-branch')
  const account = cell(element, factory, 'span', 'run-meta-field')
  literal(cell(account, factory, 'span', 'run-meta-label'), 'account')
  maybeCell(account, factory, 'span', model.row.oauthAccount, 'person', 'row.oauth_account', 'run-account')
  // The two actors keep the `person` class the wire key carries: an address is
  // drawn as the console's marker and a login as itself, on this line as in
  // every other cell of the console that draws one. Unlike the five columns
  // above, an absent actor draws **no field at all** rather than the mark: a
  // record written before `launch.json` existed has no launcher, and its
  // `n/a` would read as a launcher the console could not name instead of one
  // the record never carried.
  if (model.row.requestedBy !== null) {
    const requested = cell(element, factory, 'span', 'run-meta-field')
    literal(cell(requested, factory, 'span', 'run-meta-label'), 'requested by')
    const asked = cell(requested, factory, 'span', 'run-requested-by', 'run-meta-value')
    putText(asked, model.row.requestedBy, 'person', 'row.requested_by')
  }
  // Who started the run: the **whole** recorded identity, which is what the run
  // view is for -- the Live and Projects columns draw the short form
  // (`shared/launcher.ts`).
  const startedBy = launcherFull(model.row.launchedBy)
  if (startedBy !== null) {
    const started = cell(element, factory, 'span', 'run-meta-field')
    literal(cell(started, factory, 'span', 'run-meta-label'), 'started by')
    const value = cell(started, factory, 'span', 'run-started-by', 'run-meta-value')
    putText(value, startedBy, 'person', 'row.launched_by')
  }
  return element
}

/**
 * RUN_SUCCESS_OUTCOMES and RUN_FAILURE_OUTCOMES are the outcomes this build
 * knows a tone for. `runstore.Outcome` is the state of the run's **pull
 * request**, and its own comment supplies the reading: `ready` is the ready PR
 * the feed's `ready[]` is made of, `reviewed` is a review run that commented on
 * the pull requests it was pointed at, and `artifacts` is a render run whose
 * deliverables are in the bucket -- the three outcomes of a run that delivered
 * what it promised. `draft` is the one the CLI itself calls failed
 * (`StatusFailed` is "a draft PR exists"). `none` gets **no tone**: runstore
 * gives it two readings -- a check gate that found nothing to fix, and a review
 * mode that refused a pull request before checkout -- and painting it as one of
 * them would be a claim about the run rather than a reading of it.
 *
 * **The word is always drawn**: the tone is the second channel and never the
 * only one (styling design section 3.5), and an outcome a newer CLI resolved is
 * drawn in the primary tone with no tone of its own.
 */
const RUN_SUCCESS_OUTCOMES: readonly string[] = Object.freeze(['ready', 'reviewed', 'artifacts'])
const RUN_FAILURE_OUTCOMES: readonly string[] = Object.freeze(['draft'])

/** outcomeTone is the tone an outcome's own word earns, or null. */
function outcomeTone(outcome: string | null): string | null {
  if (outcome === null) return null
  if (RUN_SUCCESS_OUTCOMES.includes(outcome)) return 'success'
  if (RUN_FAILURE_OUTCOMES.includes(outcome)) return 'failure'
  return null
}

/** statusStrip is the four readings the run is: its lifecycle, its outcome, the
 * stage it is on and whether its ledger can still move.
 *
 * **The ledger cell is the terminal/settled pair**, which is what says whether
 * the numbers on this screen can still move -- a fact about reading them rather
 * than about the run. Each cell is a caption over a value, and the value's word
 * is drawn on every path: an absent outcome or stage is the console's `n/a`
 * with its reason, never a blank cell, and a tone is the second channel beside
 * the word and never the only one.
 */
function statusStrip(factory: RunFactory, model: RunModel): ScreenElement {
  const element = make(factory, 'div', 'run-strip')
  const lifecycle = cell(element, factory, 'div', 'run-strip-cell')
  literal(cell(lifecycle, factory, 'span', 'run-strip-label'), 'LIFECYCLE')
  // `status` is a string the run's record carries, not a word this console
  // chose, so it goes through `putText` like every other document string.
  putText(cell(lifecycle, factory, 'span', 'run-strip-value', 'run-status'), model.row.status, 'label', 'row.status')
  const outcome = cell(element, factory, 'div', 'run-strip-cell')
  literal(cell(outcome, factory, 'span', 'run-strip-label'), 'OUTCOME')
  const outcomeValue = cell(outcome, factory, 'span', 'run-strip-value', 'run-outcome')
  putMaybe(outcomeValue, model.row.outcome, 'label', 'row.outcome')
  // The tone is one of two closed words, written as a literal for the same
  // reason the words beside it are: a rule colours the cell by the attribute and
  // the console never spells a tone the theme has no colour for.
  const tone = outcomeTone(model.row.outcome)
  if (tone === 'success') outcomeValue.setAttribute('data-tone', 'success')
  if (tone === 'failure') outcomeValue.setAttribute('data-tone', 'failure')
  const stage = cell(element, factory, 'div', 'run-strip-cell')
  literal(cell(stage, factory, 'span', 'run-strip-label'), 'STAGE')
  putMaybe(cell(stage, factory, 'span', 'run-strip-value', 'run-stage'), model.row.stage, 'label', 'row.stage')
  const ledger = cell(element, factory, 'div', 'run-strip-cell')
  literal(cell(ledger, factory, 'span', 'run-strip-label'), 'LEDGER')
  const flags = cell(ledger, factory, 'span', 'run-strip-value', 'run-flags')
  literal(cell(flags, factory, 'span', 'run-settled'), model.row.settled ? 'settled' : 'still moving')
  literal(cell(flags, factory, 'span', 'run-flags-state'), model.row.terminal ? '· terminal' : '· still running')
  if (model.row.staleDraft) literal(cell(flags, factory, 'span', 'run-stale'), 'stale draft')
  if (model.row.followUp) literal(cell(flags, factory, 'span', 'run-follow-up'), 'follow-up')
  return element
}

/**
 * rigorLine is the run's rigor: the mode it resolved, the layer that decided it
 * and the area rules that matched (docs/rigor-modes D23, D24, D25).
 *
 * **This is the screen that has room for the whole answer.** `runs ls`'s MODE
 * column and the Projects screen's own cell are one word in a table, and the
 * Live screen's chip is one word on a row; here the reader is looking at one
 * run, so the layer that won and every rule that matched are drawn beside the
 * mode. The CLI's `fugaro diagnose` prints the same three facts on its own
 * `Rigor:` line (`internal/cli/diagnose.go`'s `rigorLine`).
 *
 * Every string is a document's: the five modes and the five layers have the
 * console's words and go through `literal`, and anything else -- a mode or a
 * layer from a CLI newer than this build, or a value a tampered record carries
 * -- goes through `putText` as the bounded, escaped text it is. The area names
 * are a repository's own words either way, so they are text on every path. A
 * moment (`at`) and the revision the configuration was read from are in the
 * record and are not drawn: this row is about what the run resolved, and the
 * two stamps answer different questions.
 *
 * Each cell is drawn by a `cell`/`textCell` call **here** rather than inside
 * `rigorWord`, because that is where the class literal is: the selector scan
 * (`test/unit/theme-selectors.test.ts`) reads the tag and the class off the
 * emitter call, and a class handed to a helper as a parameter is a hook the
 * scan cannot see.
 */
function rigorLine(factory: ScreenFactory, rigor: RigorView): ScreenElement {
  const element = make(factory, 'div', 'run-rigor')
  literal(cell(element, factory, 'span', 'run-rigor-word'), 'RIGOR')
  rigorWord(cell(element, factory, 'span', 'run-rigor-mode'), rigorModeWord, rigor.mode, 'row.rigor.mode')
  literal(cell(element, factory, 'span', 'run-rigor-from-word'), 'from')
  rigorWord(cell(element, factory, 'span', 'run-rigor-from'), rigorLayerWord, rigor.from, 'row.rigor.from')
  for (const area of rigor.areas) {
    const drawn = cell(element, factory, 'span', 'run-rigor-area')
    textCell(drawn, factory, 'span', area.name, 'label', 'row.rigor.areas[].name', 'run-rigor-area-name')
    rigorWord(cell(drawn, factory, 'span', 'run-rigor-area-mode'), rigorModeWord, area.mode, 'row.rigor.areas[].mode')
  }
  return element
}

/** rigorWord writes one mode or one layer into a cell the caller has already
 * drawn: the console's word when this build knows it, and the raw value through
 * `putText` when it does not. `word` is the vocabulary to look the value up in
 * -- `rigorModeWord` for a mode, which is the five the dial has, and
 * `rigorLayerWord` for a layer, which is the five that decide one
 * (`shared/rigor.ts`). */
function rigorWord(
  element: ScreenElement,
  word: (value: string) => string | null,
  value: string,
  field: string,
): void {
  const known = word(value)
  if (known !== null) {
    literal(element, known)
    return
  }
  putText(element, value, 'label', field)
}

/** haltLine is why the run halted: the word, the reason, and where and when the
 * halting applied, on the right. A halting is the money decision this console
 * exists to explain, so it is drawn on the header rather than buried in the
 * diagnose view -- and the scope and the moment are geometry of the banner
 * rather than a sentence inside it, because the reason is the half a reader
 * needs first. */
function haltLine(factory: RunFactory, halt: RunHalt): ScreenElement {
  const element = make(factory, 'div', 'run-halt')
  literal(cell(element, factory, 'span', 'run-halt-word'), 'HALTED')
  textCell(element, factory, 'span', halt.reason, 'sentence', 'halt.reason', 'run-halt-reason')
  const where = cell(element, factory, 'span', 'run-halt-where')
  if (halt.scope !== null) {
    literal(cell(where, factory, 'span', 'run-halt-scope-word'), 'scope')
    textCell(where, factory, 'span', halt.scope, 'label', 'halt.scope', 'run-halt-scope')
  }
  if (halt.at !== null) {
    if (halt.scope !== null) literal(cell(where, factory, 'span', 'run-halt-sep'), '·')
    literal(cell(where, factory, 'span', 'run-halt-at-word'), 'at')
    textCell(where, factory, 'span', halt.at, 'label', 'halt.at', 'run-halt-at')
  }
  if (halt.detail !== null) textCell(element, factory, 'span', halt.detail, 'sentence', 'halt.detail', 'run-halt-detail')
  return element
}

/**
 * The ledger's own words. Every one of them is the console's vocabulary and
 * goes to the screen through `literal`; the figures beside them are readings and
 * go through `numberCell`. `RECON_DIFF_WORD` is deliberately not the design's
 * "Over-charged": a difference the console can measure says nothing about *why*
 * the two accounts differ, and a word that names a cause is a claim the console
 * cannot support.
 */
const LEDGER_HERO_WORD = 'TOTAL CHARGED'

const LEDGER_MODEL_WORD = 'Model, as charged'

const LEDGER_COMPUTE_WORD = 'Compute, estimated'

const LEDGER_NOTIONAL_WORD = 'Paid by the plan, not in the total'

const RECON_WORD = 'RECONCILIATION'

const RECON_REPORTED_WORD = 'Providers reported'

const RECON_CHARGED_WORD = 'Table charged'

const RECON_DIFF_WORD = 'Difference'

const ROUTE_SPLIT_WORD = 'By provider route'

/** ROUTE_SPLIT_CAPTION is the one thing a reader needs to read `route_by`
 * correctly: a Claude call carries no provider route, so the split is the spend
 * of the run's *routed* calls and not of the whole run. */
const ROUTE_SPLIT_CAPTION = 'Claude calls have no route'

const MODEL_SPLIT_WORD = 'By model'

/** NO_ROUTE_SPLIT and NO_MODEL_SPLIT are what an absent split draws. A split
 * that arrived empty and one the record never carried are different facts, and
 * the field the reader looks for is named rather than a zero drawn (`omitempty`
 * writes no key at all for an empty map). */
const NO_ROUTE_SPLIT = 'the document carries no route_by: no provider route reported a cost for this run'

const NO_MODEL_SPLIT = 'the document carries no model_by: no per-model split arrived for this run'

/** NO_PROVIDER_REPORTED is why the reconciliation has no difference: both
 * figures are `omitempty`, so an absent one is a run whose routed calls no
 * provider priced, and a difference from it would be a subtraction against a
 * zero nobody wrote. */
const NO_PROVIDER_REPORTED = 'no provider reported a cost for this run\'s routed calls'

/**
 * RUN_LEDGER_BAR_BOX and its siblings are the bar's own axis: a hundred units
 * wide and fourteen tall, stretched to the panel by
 * `preserveAspectRatio="none"`. A segment's width is then the share itself, so
 * no pixel of a layout is ever a number this screen had to know -- and the two
 * shares are **of the total**, which is what makes the bar a picture of the
 * ledger's one billed figure and never of the plan's part of it.
 */
const RUN_LEDGER_BAR_BOX = '0 0 100 14'
const RUN_LEDGER_BAR_HEIGHT = '14'
const RUN_LEDGER_BAR_FULL = '100'
const RUN_LEDGER_BAR_ORIGIN = '0'

/** shareOfPercent is a figure as a share of the total, on the bar's axis. */
function shareOfPercent(value: number, total: number): string {
  return String((value / total) * 100)
}

/** trackCoord is one position on the scrubber's hundred-unit axis as a string.
 * Every coordinate the track draws reaches the document through here, so the
 * scan's vocabulary has an entry per position rather than one per attribute
 * name. */
function trackCoord(value: number): string {
  return String(value)
}

/** barDrawn says whether the ledger's bar can be drawn at all: the total has to
 * be a measured figure above zero (a share of zero is not a share), the model
 * line has to be **billed money** (a notional figure is not part of what the
 * table charged) and the compute has to be measured or estimated. When one of
 * the three is not, the bar is not drawn and the legend rows still carry their
 * values -- an `n/a` with its reason, never a gap a reader fills in. */
function barDrawn(ledger: RunLedger): boolean {
  if (ledger.total.value === null || ledger.total.value <= 0) return false
  if (ledger.total.state !== 'measured') return false
  if (ledger.model.state !== 'measured' || ledger.model.value === null) return false
  if (ledger.compute.value === null) return false
  return ledger.compute.state === 'measured' || ledger.compute.state === 'estimated'
}

/** ledgerBar is the model and the compute as two shares of the total: the
 * billed dollars solid, the estimate an outline. It is decorative -- the legend
 * below carries both figures as text -- so it is `aria-hidden` and holds no
 * word of its own. */
function ledgerBar(factory: RunFactory, ledger: RunLedger): ScreenElement {
  const billed = ledger.model.value ?? 0
  const estimate = ledger.compute.value ?? 0
  const total = ledger.total.value ?? 0
  const element = makeNS(factory, 'svg', 'run-ledger-bar')
  element.setAttribute('viewBox', RUN_LEDGER_BAR_BOX)
  element.setAttribute('preserveAspectRatio', 'none')
  element.setAttribute('height', RUN_LEDGER_BAR_HEIGHT)
  element.setAttribute('aria-hidden', 'true')
  const track = makeNS(factory, 'rect', 'run-ledger-rail')
  track.setAttribute('x', RUN_LEDGER_BAR_ORIGIN)
  track.setAttribute('y', RUN_LEDGER_BAR_ORIGIN)
  track.setAttribute('width', RUN_LEDGER_BAR_FULL)
  track.setAttribute('height', RUN_LEDGER_BAR_HEIGHT)
  element.append(track)
  const charged = makeNS(factory, 'rect', 'run-ledger-billed')
  charged.setAttribute('x', RUN_LEDGER_BAR_ORIGIN)
  charged.setAttribute('y', RUN_LEDGER_BAR_ORIGIN)
  charged.setAttribute('width', shareOfPercent(billed, total))
  charged.setAttribute('height', RUN_LEDGER_BAR_HEIGHT)
  element.append(charged)
  // The outline starts where the billed segment ends, so its `x` is the same
  // share written again rather than a second reading of it.
  const outlined = makeNS(factory, 'rect', 'run-ledger-estimate')
  outlined.setAttribute('x', shareOfPercent(billed, total))
  outlined.setAttribute('y', RUN_LEDGER_BAR_ORIGIN)
  outlined.setAttribute('width', shareOfPercent(estimate, total))
  outlined.setAttribute('height', RUN_LEDGER_BAR_HEIGHT)
  element.append(outlined)
  return element
}

/** differenceOf is the reconciliation's difference: what the table charged less
 * what the providers reported, **only when the document measured both**. One
 * absent figure is a run whose routed calls nobody priced, and subtracting from
 * it would be a difference against a zero the document never wrote. */
function differenceOf(diagnosis: RunDiagnosis): Reading {
  const reported = diagnosis.reportedUsd
  const charge = diagnosis.chargeUsd
  if (reported.value === null || charge.value === null) return unavailable(NO_PROVIDER_REPORTED)
  return measured(charge.value - reported.value)
}

/**
 * ledgerPanel is the run's money: the billed total under its caption, the bar
 * that shows the model and the compute as shares of it, the plan's part apart
 * from both, then the reconciliation and the two splits `diagnose` carries.
 *
 * **Nothing here is summed, and the three groups are three different scopes.**
 * The hero is `total_usd`, the table's own bill; `route_by` is the spend of the
 * run's *routed* calls (a Claude call has no route) and `model_by` is the spend
 * by model -- two splits of overlapping but different scopes, which is why their
 * figures do not add up to the hero and are not meant to. Adding any two of them
 * would invent a figure about money.
 */
function ledgerPanel(factory: RunFactory, model: RunModel): ScreenElement {
  const element = make(factory, 'section', 'run-ledger')
  element.setAttribute('aria-label', LEDGER_PANEL_NAME)
  literal(cell(element, factory, 'h2', 'run-panel-title'), 'ledger')
  const ledger = model.ledger
  const hero = cell(element, factory, 'p', 'run-ledger-hero')
  literal(cell(hero, factory, 'span', 'run-ledger-word'), LEDGER_HERO_WORD)
  numberCell(hero, factory, 'span', ledger.total, 'usd', 'run-ledger-total-value')
  if (barDrawn(ledger)) element.append(ledgerBar(factory, ledger))
  const legend = cell(element, factory, 'ul', 'run-ledger-legend')
  const modelKey = cell(legend, factory, 'li', 'run-ledger-key')
  const modelSwatch = cell(modelKey, factory, 'span', 'run-ledger-swatch', 'run-ledger-swatch-billed')
  modelSwatch.setAttribute('aria-hidden', 'true')
  literal(cell(modelKey, factory, 'span', 'run-ledger-key-word'), LEDGER_MODEL_WORD)
  numberCell(modelKey, factory, 'span', ledger.model, 'usd', 'run-ledger-model-value')
  const computeKey = cell(legend, factory, 'li', 'run-ledger-key')
  const computeSwatch = cell(computeKey, factory, 'span', 'run-ledger-swatch', 'run-ledger-swatch-estimate')
  computeSwatch.setAttribute('aria-hidden', 'true')
  literal(cell(computeKey, factory, 'span', 'run-ledger-key-word'), LEDGER_COMPUTE_WORD)
  numberCell(computeKey, factory, 'span', ledger.compute, 'usd', 'run-ledger-compute-value')
  // The plan's part is drawn once and only once: a record whose whole model
  // figure is the plan's (`modelReading`'s fallback) draws that figure on the
  // model line under the notional mark, and a second line here would be the same
  // dollars twice.
  if (ledger.notional.value !== null && ledger.model.state !== 'notional') {
    const plan = cell(element, factory, 'p', 'run-ledger-notional')
    literal(cell(plan, factory, 'span', 'run-ledger-word'), LEDGER_NOTIONAL_WORD)
    numberCell(plan, factory, 'span', ledger.notional, 'usd', 'run-ledger-notional-value')
  }
  const reconciliation = cell(element, factory, 'div', 'run-recon')
  literal(cell(reconciliation, factory, 'h3', 'run-recon-word'), RECON_WORD)
  const reported = cell(reconciliation, factory, 'p', 'run-recon-line')
  literal(cell(reported, factory, 'span', 'run-recon-label'), RECON_REPORTED_WORD)
  numberCell(reported, factory, 'span', model.diagnosis.reportedUsd, 'usd', 'run-reported-usd')
  const charged = cell(reconciliation, factory, 'p', 'run-recon-line')
  literal(cell(charged, factory, 'span', 'run-recon-label'), RECON_CHARGED_WORD)
  numberCell(charged, factory, 'span', model.diagnosis.chargeUsd, 'usd', 'run-charge-usd')
  const difference = differenceOf(model.diagnosis)
  const differenceLine =
    difference.value !== null && difference.value !== 0
      ? make(factory, 'p', 'run-recon-diff', 'run-recon-diff-warn')
      : make(factory, 'p', 'run-recon-diff')
  reconciliation.append(differenceLine)
  literal(cell(differenceLine, factory, 'span', 'run-recon-label'), RECON_DIFF_WORD)
  numberCell(differenceLine, factory, 'span', difference, 'usd', 'run-recon-diff-value')
  // The two warnings are the CLI's own sentences about this pair and stay
  // verbatim under it: the console draws what the run wrote and never a
  // conclusion of its own about the difference above.
  if (model.diagnosis.reportedWarning !== null) {
    textCell(reconciliation, factory, 'p', model.diagnosis.reportedWarning, 'sentence', 'reported_warning', 'run-reported-warning')
  }
  for (const warning of model.diagnosis.pinWarnings) {
    textCell(reconciliation, factory, 'p', warning, 'sentence', 'pin_warnings[]', 'run-pin-warning')
  }
  const routeSplit = cell(element, factory, 'div', 'run-split')
  const routeWord = cell(routeSplit, factory, 'h3', 'run-split-word')
  literal(routeWord, ROUTE_SPLIT_WORD)
  literal(cell(routeWord, factory, 'span', 'run-split-caption'), ROUTE_SPLIT_CAPTION)
  if (model.diagnosis.byRoute.length === 0) {
    // The mark and its reason, the console's own treatment for a value the
    // document does not carry: an empty split and an absent key both read as
    // `n/a`, and never as a route that spent nothing.
    putReading(cell(routeSplit, factory, 'p', 'run-split-absent'), unavailable(NO_ROUTE_SPLIT))
  }
  for (const split of model.diagnosis.byRoute) {
    const line = cell(routeSplit, factory, 'p', 'run-route')
    textCell(line, factory, 'span', split.name, 'label', 'route_by', 'run-route-name')
    numberCell(line, factory, 'span', split.usd, 'usd', 'run-route-usd')
  }
  const modelSplit = cell(element, factory, 'div', 'run-split')
  literal(cell(modelSplit, factory, 'h3', 'run-split-word'), MODEL_SPLIT_WORD)
  if (model.diagnosis.byModel.length === 0) {
    putReading(cell(modelSplit, factory, 'p', 'run-split-absent'), unavailable(NO_MODEL_SPLIT))
  }
  for (const split of model.diagnosis.byModel) {
    const line = cell(modelSplit, factory, 'p', 'run-model')
    textCell(line, factory, 'span', split.name, 'label', 'model_by', 'run-model-name')
    numberCell(line, factory, 'span', split.usd, 'usd', 'run-model-usd')
  }
  return element
}

/** spendStagesPanel is the run's own spend by stage: the document's top-level
 * `stages`, one row per stage, with the calls, the sub-agents and the dollars
 * that stage's model spend came to. Nothing here is derived -- no sum, no share,
 * and no total divided by the number of stages.
 *
 * The panel is drawn exactly when the document carries at least one stage; a
 * document with none has no panel, and the timeline's spend cells say that the
 * document carries no stages instead. */
function spendStagesPanel(factory: ScreenFactory, model: RunModel): ScreenElement {
  const element = make(factory, 'section', 'run-spend-stages')
  element.setAttribute('aria-label', SPEND_STAGES_PANEL_NAME)
  literal(cell(element, factory, 'h2', 'run-panel-title'), 'spend by stage')
  const list = cell(element, factory, 'ol', 'run-spend-stage-list')
  for (const stage of model.spendStages) {
    const row = cell(list, factory, 'li', 'run-spend-stage')
    textCell(row, factory, 'span', stage.name, 'label', 'stages[].name', 'run-spend-stage-name')
    numberCell(row, factory, 'span', stage.duration, 'seconds', 'run-spend-stage-duration')
    numberCell(row, factory, 'span', stage.calls, 'count', 'run-spend-stage-calls')
    numberCell(row, factory, 'span', stage.agents, 'count', 'run-spend-stage-agents')
    numberCell(row, factory, 'span', stage.usd, 'usd', 'run-spend-stage-usd')
    numberCell(row, factory, 'span', stage.notional, 'usd', 'run-spend-stage-notional')
  }
  // What this screen's own bound and its skips left out is a reading, so a panel
  // that stopped early does not read as the whole of the run's record.
  if (model.spendStagesDropped.value !== null && model.spendStagesDropped.value > 0) {
    const note = cell(element, factory, 'p', 'run-spend-stages-more')
    literal(note, 'stages not drawn:')
    numberCell(note, factory, 'span', model.spendStagesDropped, 'count', 'run-spend-stage-more')
  }
  return element
}

/** toleratedPanel is the findings the run's resolved mode let through, under the
 * total the block carries. The total is the document's own figure and is drawn
 * beside the entries rather than replaced by their count: the block is bounded
 * when it is written and bounded again here, so a list shorter than the total is
 * not the whole of what the mode tolerated.
 *
 * A document with no block at all draws no panel, and `renderRun` draws one
 * line naming the absent key instead -- see `TOLERATED_ABSENT`. */
function toleratedPanel(factory: ScreenFactory, tolerated: RunTolerated): ScreenElement {
  const element = make(factory, 'section', 'run-tolerated')
  element.setAttribute('aria-label', TOLERATED_PANEL_NAME)
  literal(cell(element, factory, 'h2', 'run-panel-title'), 'tolerated by the mode')
  const total = cell(element, factory, 'p', 'run-tolerated-total')
  literal(total, 'findings the mode tolerated:')
  numberCell(total, factory, 'span', tolerated.total, 'count', 'run-tolerated-count')
  const list = cell(element, factory, 'ol', 'run-tolerated-list')
  for (const finding of tolerated.findings) {
    const row = cell(list, factory, 'li', 'run-tolerated-finding')
    textCell(row, factory, 'span', finding.title, 'sentence', 'tolerated.findings[].title', 'run-tolerated-title')
    textCell(row, factory, 'span', finding.label, 'label', 'tolerated.findings[].label', 'run-tolerated-label')
    // The mode is the one word of the finding this console has its own
    // vocabulary for, so it is drawn through the same table the header's Rigor
    // row uses; an unknown mode is the record's own word, as text.
    rigorWord(cell(row, factory, 'span', 'run-tolerated-mode'), rigorModeWord, finding.mode, 'tolerated.findings[].mode')
    numberCell(row, factory, 'span', finding.likelihood, 'count', 'run-tolerated-likelihood')
    numberCell(row, factory, 'span', finding.impact, 'count', 'run-tolerated-impact')
    numberCell(row, factory, 'span', finding.fixCost, 'count', 'run-tolerated-fix-cost')
    textCell(row, factory, 'span', finding.scoreClass, 'label', 'tolerated.findings[].class', 'run-tolerated-class')
  }
  if (tolerated.dropped.value !== null && tolerated.dropped.value > 0) {
    const note = cell(element, factory, 'p', 'run-tolerated-more')
    literal(note, 'findings not drawn:')
    numberCell(note, factory, 'span', tolerated.dropped, 'count', 'run-tolerated-dropped')
  }
  return element
}

/**
 * RUN_ICON_BOX is the viewBox a stage's mark is drawn in, and RUN_ICON_SIZE its
 * drawn size: a 16-unit box with the stroke inside it, so the mark is the same
 * size whatever the row's type does. The three marks are one shape family -- two
 * strokes and a dot -- and the geometry is the code's while the tone is the
 * theme's (`flight-deck.css`), because the tone is what says passed from failed
 * and the tone is a value set.
 */
const RUN_ICON_BOX = '0 0 16 16'
const RUN_ICON_SIZE = '16'
const RUN_ICON_CHECK = 'M3.5 8.5 L6.5 11.5 L12.5 4.5'
const RUN_ICON_CROSS = 'M4 4 L12 12 M12 4 L4 12'
const RUN_ICON_DOT_X = '5'
const RUN_ICON_DOT_Y = '5'
const RUN_ICON_DOT_SIDE = '6'
const RUN_ICON_DOT_RADIUS = '3'

/**
 * RUN_BAR_BOX, RUN_BAR_ORIGIN, RUN_BAR_WIDTH and RUN_BAR_HEIGHT are the bar's
 * own axis: a hundred units wide and eight tall, stretched to the column by
 * `preserveAspectRatio="none"`. The fill's width is then the share itself -- one
 * unit on this axis is one percent of the longest stage -- so no pixel of a
 * layout is ever a number this screen had to know.
 */
const RUN_AGENT_ICON = 'M4 5 h8 M4 8 h8 M4 11 h5'

const RUN_BAR_BOX = '0 0 100 8'
const RUN_BAR_ORIGIN = '0'
const RUN_BAR_WIDTH = '100'
const RUN_BAR_HEIGHT = '8'

/**
 * stageIcon is a stage's mark: a check for a stage that passed, a cross for one
 * that failed, and a neutral dot for a record whose outcome this build does not
 * know. It is decorative -- `aria-hidden`, no text of its own -- because the
 * word beside it is the reading, and a shape a reader cannot inspect is better
 * hidden than announced as an unlabelled mark (styling design section 3.5).
 */
function stageIcon(factory: RunFactory, passed: boolean | null): ScreenElement {
  const element = makeNS(factory, 'svg', 'run-stage-icon')
  element.setAttribute('viewBox', RUN_ICON_BOX)
  element.setAttribute('width', RUN_ICON_SIZE)
  element.setAttribute('height', RUN_ICON_SIZE)
  element.setAttribute('aria-hidden', 'true')
  if (passed === null) {
    const dot = makeNS(factory, 'rect', 'run-stage-dot')
    dot.setAttribute('x', RUN_ICON_DOT_X)
    dot.setAttribute('y', RUN_ICON_DOT_Y)
    dot.setAttribute('width', RUN_ICON_DOT_SIDE)
    dot.setAttribute('height', RUN_ICON_DOT_SIDE)
    dot.setAttribute('rx', RUN_ICON_DOT_RADIUS)
    element.append(dot)
    return element
  }
  if (passed) {
    const check = makeNS(factory, 'path', 'run-stage-check')
    check.setAttribute('d', RUN_ICON_CHECK)
    element.append(check)
    return element
  }
  const cross = makeNS(factory, 'path', 'run-stage-cross')
  cross.setAttribute('d', RUN_ICON_CROSS)
  element.append(cross)
  return element
}

/** agentIcon is the decorative mark beside the agent's own message: three
 * strokes, `aria-hidden`, and the caption below it is the whole of the reading. */
function agentIcon(factory: RunFactory): ScreenElement {
  const element = makeNS(factory, 'svg', 'run-agent-icon')
  element.setAttribute('viewBox', RUN_ICON_BOX)
  element.setAttribute('width', RUN_ICON_SIZE)
  element.setAttribute('height', RUN_ICON_SIZE)
  element.setAttribute('aria-hidden', 'true')
  const stroke = makeNS(factory, 'path', 'run-agent-stroke')
  stroke.setAttribute('d', RUN_AGENT_ICON)
  element.append(stroke)
  return element
}

/** longestDuration is the longest **measured** duration on the panel, or null
 * when no stage's duration was measured. The bars are shares of it: a panel
 * whose stages were never timed draws tracks and no fill. */
function longestDuration(stages: readonly RunStage[]): number | null {
  let longest: number | null = null
  for (const stage of stages) {
    const value = stage.duration.value
    if (value === null) continue
    longest = longest === null ? value : Math.max(longest, value)
  }
  return longest
}

/**
 * stageBar is a stage's bar: the track, and -- when the stage has a measured
 * duration -- the fill, whose width on the bar's own axis is that duration as a
 * share of the longest one drawn beside it.
 *
 * **A stage with no measured duration draws no fill.** Its duration cell still
 * carries the `n/a` and the reason, so the stage is never silently a zero-length
 * one; a fill of no width beside a track would say "this stage took no time",
 * which is a different fact from "nothing timed it".
 */
function stageBar(factory: RunFactory, duration: Reading, passed: boolean | null, longest: number | null): ScreenElement {
  const element = makeNS(factory, 'svg', 'run-stage-bar')
  element.setAttribute('viewBox', RUN_BAR_BOX)
  element.setAttribute('preserveAspectRatio', 'none')
  // The height is the `viewBox`'s own, written as the attribute the fill is
  // scaled inside: the stylesheet stretches the bar to the column and carries no
  // length of its own (`live.ts`'s cap bar is drawn the same way).
  element.setAttribute('height', RUN_BAR_HEIGHT)
  element.setAttribute('aria-hidden', 'true')
  const track = makeNS(factory, 'rect', 'run-stage-track')
  track.setAttribute('x', RUN_BAR_ORIGIN)
  track.setAttribute('y', RUN_BAR_ORIGIN)
  track.setAttribute('width', RUN_BAR_WIDTH)
  track.setAttribute('height', RUN_BAR_HEIGHT)
  element.append(track)
  const value = duration.value
  if (value === null || longest === null || longest <= 0) return element
  const fill = makeNS(factory, 'rect', passed === false ? 'run-stage-fill-failed' : 'run-stage-fill')
  fill.setAttribute('x', RUN_BAR_ORIGIN)
  fill.setAttribute('y', RUN_BAR_ORIGIN)
  fill.setAttribute('height', RUN_BAR_HEIGHT)
  fill.setAttribute('width', stageShare(value, longest))
  element.append(fill)
  return element
}

/** stageShare is a measured duration as a percentage of the longest one on the
 * panel, on the bar's hundred-unit axis. */
function stageShare(value: number, longest: number): string {
  return String((value / longest) * 100)
}

/**
 * stagePanel is the timeline: one row per stage, with its mark, its outcome, its
 * bar, its counts and its duration. Its spend is the figure no document carries
 * -- a verify record has none -- so the cell is an `n/a` naming the panel that
 * does carry the run's spend by stage, or naming the missing key when there is
 * no such panel.
 */
function stagePanel(factory: RunFactory, model: RunModel): ScreenElement {
  const element = make(factory, 'section', 'run-stages')
  element.setAttribute('aria-label', STAGES_PANEL_NAME)
  literal(cell(element, factory, 'h2', 'run-panel-title'), 'stages')
  const list = cell(element, factory, 'ol', 'run-stage-list')
  const longest = longestDuration(model.stages)
  for (const stage of model.stages) {
    const row = cell(list, factory, 'li', 'run-stage')
    row.append(stageIcon(factory, stage.passed))
    textCell(row, factory, 'span', stage.stage, 'label', 'verify[].kind', 'run-stage-name')
    const body = cell(row, factory, 'div', 'run-stage-body')
    if (stage.passed !== null) {
      literal(cell(body, factory, 'span', 'run-stage-passed'), stage.passed ? 'passed' : 'failed')
    }
    body.append(stageBar(factory, stage.duration, stage.passed, longest))
    const counts = cell(body, factory, 'p', 'run-stage-count')
    numberCell(counts, factory, 'span', stage.tests, 'count', 'run-stage-tests')
    literal(cell(counts, factory, 'span', 'run-stage-tests-word'), 'tests ·')
    numberCell(counts, factory, 'span', stage.failures, 'count', 'run-stage-failures')
    literal(cell(counts, factory, 'span', 'run-stage-failures-word'), 'failed')
    const spend = cell(body, factory, 'p', 'run-stage-meta')
    numberCell(spend, factory, 'span', stage.spend, 'usd', 'run-stage-spend')
    if (stage.deadline !== null) {
      literal(cell(spend, factory, 'span', 'run-stage-deadline'), stage.deadline)
    } else {
      numberCell(spend, factory, 'span', unavailable(stage.deadlineBecause ?? DEADLINE_BECAUSE), 'count', 'run-stage-deadline')
    }
    numberCell(row, factory, 'span', stage.duration, 'seconds', 'run-stage-duration')
  }
  return element
}

/** pullRequestPanel is the pull request as **text**: repository, number, URL,
 * and the reason no link is offered. There is no link element at all -- not an
 * inert one, and not one waiting for a provider -- because an element carrying a
 * destination the console will not offer is a control that does nothing.
 *
 * The three facts are one line and the cells between them are **between** them,
 * not part of them: drawn side by side with nothing between, they read as one
 * string -- `acme/platform-core#412https://github.com/acme/platform-core/pull/412`,
 * the owner's report of 2026-10-21, where the URL is no longer a URL. A cell of
 * its own rather than a leading or trailing space keeps every value exactly the
 * document's string (`textCell` draws it) and keeps a panel missing a field free
 * of a stray separator: separators go between what is drawn, whatever that is.
 */
function pullRequestPanel(factory: ScreenFactory, model: RunPullRequest): ScreenElement {
  const element = make(factory, 'section', 'run-pr')
  element.setAttribute('aria-label', PR_PANEL_NAME)
  literal(cell(element, factory, 'h2', 'run-panel-title'), 'pull request')
  const facts: (() => void)[] = []
  if (model.repository !== null) {
    facts.push(() => void textCell(element, factory, 'span', model.repository, 'repository', 'row.repo', 'run-pr-repo'))
  }
  if (model.number !== null) {
    // A number an identifier: drawn as text, with no link and no reading.
    facts.push(() => void literal(cell(element, factory, 'span', 'run-pr-number'), `#${String(model.number)}`))
  }
  if (model.url !== null) {
    facts.push(() => void textCell(element, factory, 'span', model.url, 'url', 'row.pr_url', 'run-pr-url'))
  }
  for (const [index, fact] of facts.entries()) {
    if (index > 0) literal(cell(element, factory, 'span', 'run-pr-sep'), ' · ')
    fact()
  }
  literal(cell(element, factory, 'p', 'run-pr-reason'), model.because)
  return element
}

/** fillLogsPanel draws the capped window and the link to the transcript into
 * the section the frame drew. */
function fillLogsPanel(factory: ScreenFactory, model: RunLogs, element: ScreenElement): void {
  literal(cell(element, factory, 'h2', 'run-panel-title'), 'Logs · capped window')
  const list = cell(element, factory, 'ol', 'run-log-list')
  for (const line of model.lines) {
    const row = cell(list, factory, 'li', 'run-log-line')
    if (line.time !== null) textCell(row, factory, 'span', line.time, 'label', 'logs[].time', 'run-log-time')
    // `severity` is the cloud logging API's own word (`logLine`), not one of
    // this console's constants, so it goes through `putText` exactly as the
    // `message` beside it does -- a provider that wrote a token or an invisible
    // character into a severity does not get it drawn.
    textCell(row, factory, 'span', line.severity, 'label', 'logs[].severity', 'run-log-severity')
    if (line.stage !== null) textCell(row, factory, 'span', line.stage, 'label', 'logs[].stage', 'run-log-stage')
    // A `message` is text a job wrote (`logs.go`'s own comment), so it is
    // re-sanitized here like any other free text.
    textCell(row, factory, 'span', line.message, 'sentence', 'logs[].message', 'run-log-message')
  }
  if (model.dropped.value !== null && model.dropped.value > 0) {
    const note = cell(element, factory, 'p', 'run-log-note')
    literal(note, 'lines not drawn:')
    numberCell(note, factory, 'span', model.dropped, 'count', 'run-log-dropped')
    literal(cell(note, factory, 'span', 'run-log-of'), 'of')
    numberCell(note, factory, 'span', model.total, 'count', 'run-log-total')
  }
  if (model.refused.value !== null && model.refused.value > 0) {
    // A line the read refused -- torn, past the line cap, or not a `logLine` --
    // is counted rather than silently left out, and never echoed.
    const note = cell(element, factory, 'p', 'run-log-refused')
    literal(note, 'lines refused (torn, oversized or not a log entry):')
    numberCell(note, factory, 'span', model.refused, 'count', 'run-log-refused-count')
  }
  if (model.link !== null) {
    // `putLink` writes the destination as the element's own text and puts the
    // URL in `data-href`; the main process owns the allow-list, so an element
    // carrying it navigates nowhere by itself.
    putLink(cell(element, factory, 'a', 'run-log-link'), model.link, 'logs RUN --url')
  } else if (model.linkBecause !== null) {
    const note = cell(element, factory, 'p', 'run-log-link-note')
    literal(note, model.linkBecause)
  }
}

/** fillDiagnosePanel is the rest of `diagnose` -- everything else the document
 * says about why the run is where it is -- drawn into the section the frame
 * drew. */
function fillDiagnosePanel(factory: RunFactory, model: RunModel, element: ScreenElement): void {
  literal(cell(element, factory, 'h2', 'run-panel-title'), 'diagnosis')
  const diagnosis = model.diagnosis
  // The two test lists are chips: the word carries the tone *and* the reading,
  // because a chip that said only "failed" would be the same chip for every test
  // in the list (styling design section 3.5).
  for (const test of diagnosis.failed) {
    const chip = cell(element, factory, 'span', 'run-failed')
    literal(cell(chip, factory, 'span', 'run-failed-word'), 'failed:')
    textCell(chip, factory, 'span', test, 'label', 'failed[]', 'run-failed-test')
  }
  for (const test of diagnosis.flaky) {
    const chip = cell(element, factory, 'span', 'run-flaky')
    literal(cell(chip, factory, 'span', 'run-flaky-word'), 'flaky:')
    textCell(chip, factory, 'span', test, 'label', 'flaky[]', 'run-flaky-test')
  }
  for (const finding of diagnosis.findings) {
    const line = cell(element, factory, 'p', 'run-finding')
    textCell(line, factory, 'span', finding.severity, 'label', 'findings[].severity', 'run-finding-severity')
    textCell(line, factory, 'span', finding.file, 'path', 'findings[].file', 'run-finding-file')
    textCell(line, factory, 'span', finding.summary, 'sentence', 'findings[].summary', 'run-finding-summary')
  }
  if (diagnosis.agentMessage !== null) {
    // The caption over the message rather than a cell ahead of it: the agent's
    // own sentence is the largest prose on the screen and reads as the block it
    // is, with the decorative mark beside it saying where the sentence came
    // from without a word of it being the mark's.
    const line = cell(element, factory, 'div', 'run-agent-message')
    line.append(agentIcon(factory))
    const said = cell(line, factory, 'div', 'run-agent-said')
    literal(cell(said, factory, 'span', 'run-agent-word'), 'THE AGENT SAID')
    textCell(said, factory, 'p', diagnosis.agentMessage, 'sentence', 'agent_message', 'run-agent-text')
  }
  const tail = cell(element, factory, 'ol', 'run-log-tail')
  for (const line of diagnosis.logTail) {
    textCell(tail, factory, 'li', line, 'sentence', 'log_tail[]', 'run-log-tail-line')
  }
  if (diagnosis.reportPath !== null) {
    const line = cell(element, factory, 'p', 'run-report-path')
    literal(cell(line, factory, 'span', 'run-report-word'), 'report:')
    textCell(line, factory, 'span', diagnosis.reportPath, 'path', 'report_path', 'run-report-value')
  }
  if (diagnosis.draftNote !== null) textCell(element, factory, 'p', diagnosis.draftNote, 'sentence', 'draft_note', 'run-draft-note')
  if (diagnosis.commentsPath !== null) {
    const line = cell(element, factory, 'p', 'run-comments-path')
    literal(cell(line, factory, 'span', 'run-comments-word'), 'comments:')
    textCell(line, factory, 'span', diagnosis.commentsPath, 'path', 'comments_path', 'run-comments-value')
  }
  if (diagnosis.followUpSession !== null) {
    const line = cell(element, factory, 'p', 'run-follow-up')
    literal(cell(line, factory, 'span', 'run-follow-up-word'), 'for the pull request')
    numberCell(line, factory, 'span', diagnosis.followUpPr, 'count', 'run-follow-up-pr')
    literal(cell(line, factory, 'span', 'run-follow-up-comments-word'), 'comments reaching the agent:')
    numberCell(line, factory, 'span', diagnosis.followUpComments, 'count', 'run-follow-up-comments')
    textCell(line, factory, 'span', diagnosis.followUpSession, 'label', 'follow_up.session', 'run-follow-up-session')
  }
  // The money the diagnosis carries is not drawn here: the reported/charged
  // pair, the two warnings about it and the two splits are the ledger panel's
  // (step 3b), because they are all accounts of the same run's spend and belong
  // beside the total they are read against.
}

/**
 * The panel names are what each region is called to a reader who jumps between
 * them: every panel is a `section`, and a `section` with no name is a landmark
 * that announces nothing (`live.ts`'s `PANEL_NAME` is the same sentence). The
 * heading inside each one carries the same words as its caption.
 */
const STAGES_PANEL_NAME = 'Stages'

const SPEND_STAGES_PANEL_NAME = 'Spend by stage'

const TOLERATED_PANEL_NAME = 'Tolerated by the mode'

const DIAGNOSIS_PANEL_NAME = 'Diagnosis'

const LOGS_PANEL_NAME = 'Logs'

const REPLAY_PANEL_NAME = 'Replay'

const LEDGER_PANEL_NAME = 'Ledger'

const PR_PANEL_NAME = 'Pull request'

/**
 * RUN_RECORD_SHAPE and RUN_LOGS_SHAPE are the two sections' skeletons, and they
 * are exported because the **entry** places them: the record's read (`diagnose
 * RUN --json`, which draws the header's figures, the timeline, the ledger and
 * the diagnosis) and the capped window's (`logs RUN --json`) are two reads, so
 * the section whose read is still out wears the shape of what it will hold while
 * the other one is already filled.
 *
 * They are this screen's own and not `skeleton.ts`'s `SKELETON_SHAPES`, which is
 * the five **tabs** of the navigation: Run detail is reached from a row rather
 * than offered in the navigation, so it has no tab to hang a shape on and the
 * two sections carry theirs.
 */
export const RUN_RECORD_SHAPE: SkeletonShape = Object.freeze({
  section: 'run-record',
  // The record's own shape: the meta line under the header, the timeline's
  // rows, the ledger's total and the diagnosis' tail.
  blocks: Object.freeze<SkeletonBlock[]>(['line', 'row', 'row', 'row', 'line', 'row', 'row']),
})

export const RUN_LOGS_SHAPE: SkeletonShape = Object.freeze({
  section: 'run-logs',
  // The log window's: its heading, a run of lines and the link under them.
  blocks: Object.freeze<SkeletonBlock[]>(['line', 'row', 'row', 'row', 'row', 'line']),
})

/**
 * runBusyLabel names what one of the two reads is reading, and each section
 * carries its own: the record's mark is over the main column and the log
 * window's over its own section, and a mark that said "and" would be a section
 * naming a read it is not waiting for.
 *
 * **The id is the click's own text, and it goes through the same `runId` rule
 * the heading does.** The label is drawn with `literal` (`section.ts`), and a
 * string a screen refused to draw must not be printed into a label instead --
 * `UNAVAILABLE_MARK` is what the heading shows for it, so the two agree.
 */
export function runBusyLabel(run: string, reading: string): string {
  const id = sanitizeText(run, 'runId', 'row.run')
  return `Reading ${reading} of ${id.ok ? id.text : UNAVAILABLE_MARK}`
}

/** RUN_RECORD_BUSY and RUN_LOGS_BUSY are what each mark says it is reading, in
 * the words the screen's own panels carry: the operation is `run.diagnose` and
 * its panel is the diagnosis, and the second read is the capped log window. */
export const RUN_RECORD_BUSY = 'the diagnosis'

export const RUN_LOGS_BUSY = 'the log window'

/**
 * The scrubber's own axis: a 56-unit-high track with the rail twelve of those
 * units tall, so one vertical unit is one pixel and the geometry needs no
 * measurement of the panel. Horizontal shares are percentages of the window,
 * which is what `preserveAspectRatio="none"` stretches.
 */
const RUN_REPLAY_TRACK_BOX = '0 0 100 56'
const RUN_REPLAY_TRACK_SIZE = '56'
const RUN_REPLAY_RAIL_Y = '22'
const RUN_REPLAY_RAIL_HEIGHT = '12'
const RUN_REPLAY_TICK_TOP = '16'
const RUN_REPLAY_TICK_BOTTOM = '40'
const RUN_REPLAY_PLAYHEAD_TOP = '6'
const RUN_REPLAY_PLAYHEAD_BOTTOM = '50'

/** REPLAY_NO_POSITIONS is what a ring with no span draws: a tick is a moment on
 * the window's axis, and one distinct moment is no axis at all. */
const REPLAY_NO_POSITIONS = 'fewer than two records with distinct times arrived: there is no span to place a tick on'

/** positionOf is a stamp's share of `from`..`to`, 0..100, or null when the two
 * ends do not give a span to measure against -- a stamp the parser refuses, or
 * a window of no width. A record outside the window is clamped to its end rather
 * than dropped: it is a record that arrived, and a tick drawn nowhere would be
 * a record the screen hid. */
function positionOf(stamp: string, from: string | null, to: string | null): number | null {
  if (from === null || to === null) return null
  const at = Date.parse(stamp)
  const start = Date.parse(from)
  const end = Date.parse(to)
  if (!Number.isFinite(at) || !Number.isFinite(start) || !Number.isFinite(end) || end <= start) return null
  return Math.min(100, Math.max(0, ((at - start) / (end - start)) * 100))
}

/** MIDPOINT_MS is how the axis's middle label is found: the moment halfway
 * between the window's two ends. It is a scale mark and not a fact about the
 * run, which is why it is drawn only as the axis's own label. */
function midpointOf(from: string, to: string): string {
  const start = Date.parse(from)
  const end = Date.parse(to)
  if (!Number.isFinite(start) || !Number.isFinite(end)) return to
  return new Date((start + end) / 2).toISOString()
}

/**
 * replayPanel is the ring: what it holds, the window's own span as a scrubber,
 * and the moment scrubbed to. The ticks are **real records** -- one per record,
 * at its timestamp's share of `from`..`to` -- so the track is what arrived and
 * not a count of arrivals. The playhead is drawn only when something was
 * scrubbed to, because with nothing scrubbed there is no moment to mark and the
 * whole window is what is shown.
 *
 * **The previous/next controls and "Show whole window" are not drawn.** They
 * would move nothing: `entry.ts` passes no ring to this screen, so on the real
 * screen the ring is empty and a control over it is a button that does nothing
 * -- the one thing design section 5.1 forbids.
 */
function replayPanel(factory: RunFactory, model: RunReplay): ScreenElement {
  const element = make(factory, 'section', 'run-replay')
  element.setAttribute('aria-label', REPLAY_PANEL_NAME)
  const head = cell(element, factory, 'div', 'run-replay-head')
  literal(cell(head, factory, 'h2', 'run-panel-title'), 'Replay')
  const note = cell(head, factory, 'p', 'run-replay-note')
  numberCell(note, factory, 'span', model.records, 'count', 'run-replay-count')
  literal(cell(note, factory, 'span', 'run-replay-records-word'), 'records')
  literal(cell(note, factory, 'span', 'run-replay-sep'), '·')
  literal(cell(note, factory, 'span', 'run-replay-bound'), REPLAY_BOUND_NOTE)
  const track = makeNS(factory, 'svg', 'run-replay-track')
  track.setAttribute('viewBox', RUN_REPLAY_TRACK_BOX)
  track.setAttribute('preserveAspectRatio', 'none')
  track.setAttribute('height', RUN_REPLAY_TRACK_SIZE)
  track.setAttribute('aria-hidden', 'true')
  const rail = makeNS(factory, 'rect', 'run-replay-rail')
  rail.setAttribute('x', '0')
  rail.setAttribute('y', RUN_REPLAY_RAIL_Y)
  rail.setAttribute('width', '100')
  rail.setAttribute('height', RUN_REPLAY_RAIL_HEIGHT)
  track.append(rail)
  const playheadAt = model.scrubbed === null ? null : positionOf(recordStamp(model.scrubbed), model.from, model.to)
  if (playheadAt !== null) {
    const fill = makeNS(factory, 'rect', 'run-replay-fill')
    fill.setAttribute('x', '0')
    fill.setAttribute('y', RUN_REPLAY_RAIL_Y)
    fill.setAttribute('width', trackCoord(playheadAt))
    fill.setAttribute('height', RUN_REPLAY_RAIL_HEIGHT)
    track.append(fill)
  }
  const positioned = model.stamps.flatMap((stamp) => {
    const position = positionOf(stamp, model.from, model.to)
    return position === null ? [] : [position]
  })
  const distinct = new Set(model.stamps).size
  for (const position of positioned) {
    const past = playheadAt !== null && position > playheadAt
    const tick = past
      ? makeNS(factory, 'line', 'run-replay-tick', 'run-replay-tick-past')
      : makeNS(factory, 'line', 'run-replay-tick', 'run-replay-tick-live')
    tick.setAttribute('x1', trackCoord(position))
    tick.setAttribute('x2', trackCoord(position))
    tick.setAttribute('y1', RUN_REPLAY_TICK_TOP)
    tick.setAttribute('y2', RUN_REPLAY_TICK_BOTTOM)
    track.append(tick)
  }
  if (playheadAt !== null) {
    const playhead = makeNS(factory, 'line', 'run-replay-playhead')
    playhead.setAttribute('x1', trackCoord(playheadAt))
    playhead.setAttribute('x2', trackCoord(playheadAt))
    playhead.setAttribute('y1', RUN_REPLAY_PLAYHEAD_TOP)
    playhead.setAttribute('y2', RUN_REPLAY_PLAYHEAD_BOTTOM)
    track.append(playhead)
  }
  element.append(track)
  // The moment scrubbed to sits in its own row directly under the track, before
  // the axis: it is the playhead's label, and a text node inside the drawing
  // would be stretched horizontally by `preserveAspectRatio`.
  const scrubbed = cell(element, factory, 'p', 'run-replay-scrubbed')
  if (model.scrubbed !== null) {
    literal(scrubbed, 'showing the moment')
    textCell(scrubbed, factory, 'span', recordStamp(model.scrubbed), 'label', 'replay.at', 'run-replay-at')
  } else if (model.scrubbedBecause !== null) {
    // The sentence names the moment scrubbed to, which is an input the console
    // did not write, so it is drawn through `putText`.
    putText(scrubbed, model.scrubbedBecause, 'sentence', 'scrubbedBecause')
  }
  const axis = cell(element, factory, 'p', 'run-replay-axis')
  if (model.from !== null) textCell(axis, factory, 'span', model.from, 'label', 'replay.from', 'run-replay-from')
  if (model.from !== null && model.to !== null && model.from !== model.to) {
    textCell(axis, factory, 'span', midpointOf(model.from, model.to), 'label', 'replay.midpoint', 'run-replay-mid')
  }
  if (model.to !== null) textCell(axis, factory, 'span', model.to, 'label', 'replay.to', 'run-replay-to')
  if (distinct < 2) literal(cell(element, factory, 'p', 'run-replay-no-span'), REPLAY_NO_POSITIONS)
  const window_ = cell(element, factory, 'p', 'run-replay-window-line')
  literal(window_, 'over a window of')
  // The window is drawn as a reading like every other number, so a build with
  // no window has an `n/a` and not a zero-second window.
  numberCell(window_, factory, 'span', model.windowSeconds, 'seconds', 'run-replay-window')
  return element
}

/**
 * DrawnRunFrame is the Run screen's shell: the two columns, the two sections
 * that fill as their own reads land, and the slot each one puts a busy mark
 * into.
 *
 * **The shell is drawn once and patched per section**, which is the owner's rule
 * of 2026-10-31 in this screen's own terms. Both reads used to be awaited before
 * anything was drawn, so a click on a run id answered with the *previous* screen
 * for as long as `diagnose` and `logs` took; the frame is what the click draws
 * instead, in its own turn: the id, the way back to Live and the shape of what
 * is coming.
 */
export interface DrawnRunFrame {
  readonly root: ScreenElement
  /** header and crumb are the parts of the run's own header the click can
   * already draw, and the two elements the record's fill adds the rest to. */
  readonly header: ScreenElement
  readonly crumb: ScreenElement
  readonly main: ScreenElement
  /** recordHost is the region the record's read owns and the one it wears its
   * mark on: the record's own busy slot, the panels that read draws and the
   * diagnosis. **It is the record's and not the column's.** The log window sits
   * beside it in `main` and is a section of its own, so a mark on the column
   * would dim and announce a section that has already answered while
   * `run.diagnose` is still out. */
  readonly recordHost: ScreenElement
  /** mainSlot holds the record read's busy mark. It is the host's first child
   * rather than anything inside the diagnosis, because the mark is the *top* of
   * what that read draws: the diagnosis panel is below the timeline and a mark
   * inside it would be a mark under the fold, which is the one place a section's
   * busy state must not live. */
  readonly mainSlot: ScreenElement
  readonly aside: ScreenElement
  readonly diagnosis: ScreenElement
  readonly logs: ScreenElement
  readonly logsSlot: ScreenElement
}

/**
 * renderRunFrame draws the run screen's shell and no figure at all: the
 * breadcrumb's way back to Live, the id that was clicked, the retry the console
 * refuses, the two columns and the two sections. It is what `showRun` puts up in
 * the click's own turn, and `renderRun` is this frame with both sections filled.
 *
 * **The main column is drawn empty, and both callers write it through
 * `runMainChildren` and `runRecordChildren`.** The column holds the record's
 * host, the log window and the arrivals, and the host in turn holds the record's
 * busy slot, the panels the record draws and the diagnosis -- two of which are
 * the frame's and three of which are a read's -- and the two reads answer in an
 * order nobody chooses, so there is no append order the frame could fix that
 * would still hold when the log window answered first. Drawing the column empty
 * is what lets `renderRun` append it in one pass and the entry *replace* it as
 * each read lands, with the same list describing both.
 */
export function renderRunFrame(factory: RunFactory, theme: RunTheme, run: string): DrawnRunFrame {
  const root = make(factory, 'section', 'run')
  root.setAttribute('data-theme', theme)
  const drawn = headerFrame(factory, run)
  root.append(drawn.header)
  const layout = cell(root, factory, 'div', 'run-layout')
  const main = cell(layout, factory, 'div', 'run-main')
  const recordHost = make(factory, 'div', 'run-record')
  const mainSlot = make(factory, 'div', BUSY_SLOT_CLASS)
  const diagnosis = make(factory, 'section', 'run-diagnosis')
  diagnosis.setAttribute('aria-label', DIAGNOSIS_PANEL_NAME)
  const logs = make(factory, 'section', 'run-logs')
  logs.setAttribute('aria-label', LOGS_PANEL_NAME)
  const logsSlot = make(factory, 'div', BUSY_SLOT_CLASS)
  logs.append(logsSlot)
  const aside = cell(layout, factory, 'aside', 'run-aside')
  return {
    root,
    header: drawn.header,
    crumb: drawn.crumb,
    main,
    recordHost,
    mainSlot,
    aside,
    diagnosis,
    logs,
    logsSlot,
  }
}

/** DrawnRunRecord is what the run's own read drew into the columns: the panels
 * that stand above the diagnosis, and the warnings under the layout. */
export interface DrawnRunRecord {
  /** the panels the record draws above the diagnosis: the verify timeline, the
   * spend by stage and the tolerated block (or the one line naming the block it
   * does not carry). */
  readonly before: readonly ScreenElement[]
}

/**
 * fillRunRecord draws everything the run's own record carries: the header's
 * figures, the timeline and the spend by stage, the tolerated block, the
 * diagnosis, the ledger and the pull request, and the record's own warnings.
 *
 * The by-stage panel is drawn only when the document has stages, and an absent
 * `tolerated` block draws the one line naming the missing key in the panel's own
 * place, because a run that tolerated nothing, a record written before the field
 * existed and a run whose record could not be read all write no block.
 *
 * It answers the panels the caller has to place **above** the diagnosis: they
 * are built here and not appended, because the log window's read may have been
 * the first to land and the column is written as one list.
 */
export function fillRunRecord(factory: RunFactory, model: RunModel, frame: DrawnRunFrame): DrawnRunRecord {
  fillCrumb(factory, model, frame.crumb)
  fillHeader(factory, model, frame.header)
  const before: ScreenElement[] = [stagePanel(factory, model)]
  if (model.spendStages.length > 0) before.push(spendStagesPanel(factory, model))
  before.push(
    model.tolerated === null
      ? literal(make(factory, 'p', 'run-tolerated-absent'), TOLERATED_ABSENT)
      : toleratedPanel(factory, model.tolerated),
  )
  fillDiagnosePanel(factory, model, frame.diagnosis)
  frame.aside.append(ledgerPanel(factory, model))
  frame.aside.append(pullRequestPanel(factory, model.pullRequest))
  for (const warning of model.warnings) {
    textCell(frame.root, factory, 'p', warning, 'sentence', 'warnings[]', 'run-warning')
  }
  return { before }
}

/** fillRunLogs draws the capped window into the section the frame drew, and
 * answers the arrivals panel that belongs under it -- the replay of the same
 * window. */
export function fillRunLogs(factory: RunFactory, model: RunModel, frame: DrawnRunFrame): readonly ScreenElement[] {
  fillLogsPanel(factory, model.logs, frame.logs)
  return [replayPanel(factory, model.replay)]
}

/** RunMainParts is what the two reads have drawn into the main column. */
export interface RunMainParts {
  readonly before: DrawnRunRecord['before']
  /** whether the diagnosis section belongs in the column at all: it does once
   * its read has answered, and it does not while that read is out or when it
   * refused -- a section holding nothing but its own busy slot is an empty card
   * above the log window, and a refusal is drawn in the column's own slot
   * instead. */
  readonly diagnosis: boolean
  /** the arrivals panel, drawn with the log window it replays. It is absent when
   * that read refused: a replay of a window nobody read would be figures about
   * logs the console could not ask for. */
  readonly arrivals: readonly ScreenElement[]
}

/**
 * runRecordChildren is the order inside the record read's own host: its busy
 * slot, the panels the record draws and the diagnosis.
 *
 * **The host is what the record's mark is on, and the mark is what makes the
 * split worth having.** The host holds only what `run.diagnose` draws, so the
 * busy state the entry puts on the host while that read is out dims and
 * announces exactly the region that is still coming -- never the log window
 * beside it, which may have landed first and would otherwise be dimmed and
 * announced as busy by a read that is not its own.
 */
export function runRecordChildren(frame: DrawnRunFrame, parts: RunMainParts): readonly ScreenElement[] {
  return [frame.mainSlot, ...parts.before, ...(parts.diagnosis ? [frame.diagnosis] : [])]
}

/**
 * runMainChildren is the order of the run screen's main column: the record
 * read's host, the capped log window and the arrivals.
 *
 * **The column is written as a whole list, and that is what the two independent
 * reads cost.** The log window's read can land before the record's, and a
 * section that can only be appended to cannot put the record's panels above the
 * diagnosis once the log window is already there: so each read draws its own
 * part, and the caller writes the column from `parts` in this order -- an append
 * for the one-shot `renderRun`, a replacement for the entry, where the second
 * read to land has to sit under the first.
 */
export function runMainChildren(frame: DrawnRunFrame, parts: RunMainParts): readonly ScreenElement[] {
  return [frame.recordHost, frame.logs, ...parts.arrivals]
}

/** renderRun walks the model through Task 2.0's writers, in one pass: the frame,
 * both fills and the column in its order. A caller that has both answers at once
 * -- a static draw, a test of the drawing alone -- is this; the entry draws the
 * frame first and fills each section as its read lands. */
export function renderRun(factory: RunFactory, model: RunModel, theme: RunTheme): ScreenElement {
  const frame = renderRunFrame(factory, theme, model.row.run)
  const record = fillRunRecord(factory, model, frame)
  const arrivals = fillRunLogs(factory, model, frame)
  const parts: RunMainParts = { before: record.before, diagnosis: true, arrivals }
  for (const child of runRecordChildren(frame, parts)) frame.recordHost.append(child)
  for (const child of runMainChildren(frame, parts)) frame.main.append(child)
  return frame.root
}
