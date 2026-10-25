// The Run detail screen: the design's screen 4, drawn from `diagnose RUN
// --json`, a capped `logs RUN --json` window, `logs RUN --url` and the replay
// buffer (desktop-console plan Task 2.3; design section 6.2 screen 4).
//
// Seven things are on this screen, and each of them is a rule:
//
//   1. **The ledger.** `runstore.Cost`'s three figures, kept apart: the model
//      dollars (the **billed** part of the model figure -- `NOTIONAL~` only
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
//   3. **Per-stage spend, which no document carries.** `diagnose` gives spend by
//      **route** and by **model** (`route_by`, `model_by`), not by stage, so
//      each stage's spend is an `unavailable` reading whose sentence says
//      exactly that, and the two splits the document does carry are drawn as
//      their own lists. A screen that divided a total by the number of stages
//      would be inventing a figure about money.
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

import { measured, readNumber, unavailable } from '../../shared/availability'
import type { Reading } from '../../shared/availability'
import { ReplayBuffer, REPLAY_MAX_RECORDS, REPLAY_WINDOW_SECONDS } from '../../shared/replay'
import type { ReplayArrival, ReplayDocument, ReplayRecord } from '../../shared/replay'
import type { OperationName } from '../../main/ipc'
import { launcherFull } from '../../shared/launcher'
import { readRigor, rigorLayerWord, rigorModeWord } from '../../shared/rigor'
import type { RigorView } from '../../shared/rigor'
import type { TextClass } from '../../shared/sanitize'
import { putLink, putReading, putText } from '../app'
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

/** RunLedger is the run's spend: the three accounts, apart. */
export interface RunLedger {
  readonly model: Reading
  readonly compute: Reading
  readonly total: Reading
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
  /** the frame in effect at the moment scrubbed to, or null */
  readonly scrubbed: ReplayDocument | null
  readonly scrubbedBecause: string | null
}

/** RunModel is the whole screen. */
export interface RunModel {
  readonly row: RunRow
  readonly ledger: RunLedger
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

/** costOf reads `runstore.Cost`. Compute is what `ComputeEstimated` says it is:
 * an estimate when the flag is on, and **not an estimate** -- `unavailable`,
 * with the CLI's own sentence -- when it is off, because the flag off is a
 * `compute_usd` of 0 that means "not estimated", not free
 * (`internal/runstore/cost.go:15`, design section 10.1). The sentence is the
 * Projects screen's own constant: the two screens draw the same field and say
 * the same thing about it. */
function ledgerOf(row: Record<string, unknown>): RunLedger {
  const cost = objectAt(row['cost']) ?? {}
  const basis = textAt(cost['model_basis'])
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
  }
}

/** STAGE_SPEND_BECAUSE is the sentence every stage's spend carries, and it says
 * what the document gives instead -- which is the whole point: `route_by` and
 * `model_by` are the splits `diagnose` produces, and a per-stage split is not
 * one of them. */
const STAGE_SPEND_BECAUSE =
  'no document carries spend by stage: `diagnose` reports it by route and by model, and those are drawn below'

/** DEADLINE_BECAUSE is the same honesty for a deadline the row does not carry. */
const DEADLINE_BECAUSE = 'the document has no row.deadline'

/** stagesOf builds the timeline. Each verify record is a stage; when the
 * document carries none, the row's own stage is the one row of the timeline --
 * a run that has not reached its verify gate has a stage and no records, and
 * drawing nothing for it would be drawing a run with no stages at all. */
function stagesOf(row: Record<string, unknown>, raw: unknown): readonly RunStage[] {
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
    stages.push(stageOf(textAt(record['kind']) ?? textAt(record['stage']) ?? '', record, passed, deadline))
  }
  if (stages.length === 0) {
    const stage = textAt(row['stage'])
    if (stage !== null && stage !== '') stages.push(stageOf(stage, {}, null, deadline))
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
): RunStage {
  return {
    stage,
    passed,
    tests: readNumber(record['tests'], 'verify[].tests'),
    failures: readNumber(record['failures'], 'verify[].failures'),
    duration: readNumber(record['duration_s'], 'verify[].duration_s'),
    spend: unavailable(STAGE_SPEND_BECAUSE),
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
    stages: stagesOf(row, document['verify']),
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

function make(factory: ScreenFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = factory.create(tag)
  element.setAttribute('class', classes.join(' '))
  return element
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

/** header draws the run's identity and its outcome: what it is, where it ran,
 * how it ended and what it is doing now. */
function header(factory: ScreenFactory, model: RunModel): ScreenElement {
  const element = make(factory, 'header', 'run-header')
  const heading = cell(element, factory, 'h1', 'run-id')
  putText(heading, model.row.run, 'runId', 'row.run')
  if (model.row.repo !== null) textCell(element, factory, 'span', model.row.repo, 'repository', 'row.repo', 'run-repo')
  if (model.row.workflow !== null) textCell(element, factory, 'span', model.row.workflow, 'label', 'row.workflow', 'run-workflow')
  // `status` is a string the run's record carries, not a word this console
  // chose, so it goes through `putText` like every other document string.
  textCell(element, factory, 'span', model.row.status, 'label', 'row.status', 'run-status')
  if (model.row.outcome !== null) textCell(element, factory, 'span', model.row.outcome, 'label', 'row.outcome', 'run-outcome')
  if (model.row.stage !== null) textCell(element, factory, 'span', model.row.stage, 'label', 'row.stage', 'run-stage')
  if (model.row.branch !== null) textCell(element, factory, 'span', model.row.branch, 'branch', 'row.branch', 'run-branch')
  if (model.row.requestedBy !== null) {
    textCell(element, factory, 'span', model.row.requestedBy, 'person', 'row.requested_by', 'run-requested-by')
  }
  // Who started the run: the **whole** recorded identity, which is what the run
  // view is for -- the Live and Projects columns draw the short form
  // (`shared/launcher.ts`). It goes through the `person` class, the class the
  // wire key carries, so an address is the console's marker here as it is in
  // the requester cell beside it and no address is ever drawn; a login is
  // drawn as itself. A record with no launcher draws no cell.
  const startedBy = launcherFull(model.row.launchedBy)
  if (startedBy !== null) {
    textCell(element, factory, 'span', startedBy, 'person', 'row.launched_by', 'run-started-by')
  }
  // The terminal/settled pair is what says whether the numbers on this screen
  // can still move, which is a fact about reading them.
  const flags = cell(element, factory, 'span', 'run-flags')
  literal(flags, model.row.terminal ? 'terminal' : 'still running')
  if (model.row.settled) literal(cell(flags, factory, 'span', 'run-settled'), 'settled')
  if (model.row.staleDraft) literal(cell(flags, factory, 'span', 'run-stale'), 'stale draft')
  if (model.row.followUp) literal(cell(flags, factory, 'span', 'run-follow-up'), 'follow-up')
  if (model.row.rigor !== null) element.append(rigorLine(factory, model.row.rigor))
  if (model.halt !== null) element.append(haltLine(factory, model.halt))
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

/** haltLine is why the run halted: the reason, the scope and the time. A halting
 * is the money decision this console exists to explain, so it is drawn on the
 * header rather than buried in the diagnose view. */
function haltLine(factory: ScreenFactory, halt: RunHalt): ScreenElement {
  const element = make(factory, 'div', 'run-halt')
  literal(cell(element, factory, 'span', 'run-halt-word'), 'HALTED')
  textCell(element, factory, 'span', halt.reason, 'sentence', 'halt.reason', 'run-halt-reason')
  if (halt.scope !== null) textCell(element, factory, 'span', halt.scope, 'label', 'halt.scope', 'run-halt-scope')
  if (halt.at !== null) textCell(element, factory, 'span', halt.at, 'label', 'halt.at', 'run-halt-at')
  if (halt.detail !== null) textCell(element, factory, 'span', halt.detail, 'sentence', 'halt.detail', 'run-halt-detail')
  return element
}

/** ledgerPanel is the three accounts, apart. */
function ledgerPanel(factory: ScreenFactory, model: RunLedger): ScreenElement {
  const element = make(factory, 'section', 'run-ledger')
  literal(cell(element, factory, 'h2', 'run-panel-title'), 'ledger')
  const model_ = cell(element, factory, 'p', 'run-ledger-model')
  literal(model_, 'model, as the run was charged:')
  numberCell(model_, factory, 'span', model.model, 'usd', 'run-ledger-model-value')
  const compute = cell(element, factory, 'p', 'run-ledger-compute')
  literal(compute, 'compute, an estimate by design:')
  numberCell(compute, factory, 'span', model.compute, 'usd', 'run-ledger-compute-value')
  const total = cell(element, factory, 'p', 'run-ledger-total')
  literal(total, 'total the table charged:')
  numberCell(total, factory, 'span', model.total, 'usd', 'run-ledger-total-value')
  return element
}

/** stagePanel is the timeline: one row per stage, with its outcome, its counts
 * and -- the two figures no document carries -- its spend and its deadline. */
function stagePanel(factory: ScreenFactory, model: RunModel): ScreenElement {
  const element = make(factory, 'section', 'run-stages')
  literal(cell(element, factory, 'h2', 'run-panel-title'), 'stages')
  const list = cell(element, factory, 'ol', 'run-stage-list')
  for (const stage of model.stages) {
    const row = cell(list, factory, 'li', 'run-stage')
    textCell(row, factory, 'span', stage.stage, 'label', 'verify[].kind', 'run-stage-name')
    if (stage.passed !== null) {
      literal(cell(row, factory, 'span', 'run-stage-passed'), stage.passed ? 'passed' : 'failed')
    }
    numberCell(row, factory, 'span', stage.tests, 'count', 'run-stage-tests')
    numberCell(row, factory, 'span', stage.failures, 'count', 'run-stage-failures')
    numberCell(row, factory, 'span', stage.duration, 'seconds', 'run-stage-duration')
    // The spend is n/a on every document this tree produces, with the sentence
    // that says so in the cell's title; the deadline is the row's own word when
    // it has one and the same n/a when it does not.
    numberCell(row, factory, 'span', stage.spend, 'usd', 'run-stage-spend')
    if (stage.deadline !== null) {
      literal(cell(row, factory, 'span', 'run-stage-deadline'), stage.deadline)
    } else {
      numberCell(row, factory, 'span', unavailable(stage.deadlineBecause ?? DEADLINE_BECAUSE), 'count', 'run-stage-deadline')
    }
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

/** logsPanel is the capped window and the link to the transcript. */
function logsPanel(factory: ScreenFactory, model: RunLogs): ScreenElement {
  const element = make(factory, 'section', 'run-logs')
  literal(cell(element, factory, 'h2', 'run-panel-title'), 'logs, capped window')
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
  return element
}

/** diagnosePanel is the rest of `diagnose`: everything else the document says
 * about why the run is where it is. */
function diagnosePanel(factory: ScreenFactory, model: RunModel): ScreenElement {
  const element = make(factory, 'section', 'run-diagnosis')
  literal(cell(element, factory, 'h2', 'run-panel-title'), 'diagnosis')
  const diagnosis = model.diagnosis
  for (const test of diagnosis.failed) {
    const line = cell(element, factory, 'p', 'run-failed')
    literal(cell(line, factory, 'span', 'run-failed-word'), 'failed:')
    textCell(line, factory, 'span', test, 'label', 'failed[]', 'run-failed-test')
  }
  for (const test of diagnosis.flaky) {
    const line = cell(element, factory, 'p', 'run-flaky')
    literal(cell(line, factory, 'span', 'run-flaky-word'), 'flaky:')
    textCell(line, factory, 'span', test, 'label', 'flaky[]', 'run-flaky-test')
  }
  for (const finding of diagnosis.findings) {
    const line = cell(element, factory, 'p', 'run-finding')
    textCell(line, factory, 'span', finding.severity, 'label', 'findings[].severity', 'run-finding-severity')
    textCell(line, factory, 'span', finding.file, 'path', 'findings[].file', 'run-finding-file')
    textCell(line, factory, 'span', finding.summary, 'sentence', 'findings[].summary', 'run-finding-summary')
  }
  if (diagnosis.agentMessage !== null) {
    const line = cell(element, factory, 'p', 'run-agent-message')
    literal(cell(line, factory, 'span', 'run-agent-word'), 'the agent said:')
    textCell(line, factory, 'span', diagnosis.agentMessage, 'sentence', 'agent_message', 'run-agent-text')
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
  // The two accounts of a call's cost, side by side, because the document's own
  // warning is about the *difference* between them: what the providers reported
  // and what the table charged. Nothing is subtracted here -- the warning is a
  // sentence the CLI wrote, and the console draws it as it is.
  const reported = cell(element, factory, 'p', 'run-reported')
  literal(reported, 'the providers reported:')
  numberCell(reported, factory, 'span', diagnosis.reportedUsd, 'usd', 'run-reported-usd')
  literal(cell(reported, factory, 'span', 'run-charged-word'), 'the table charged:')
  numberCell(reported, factory, 'span', diagnosis.chargeUsd, 'usd', 'run-charge-usd')
  if (diagnosis.reportedWarning !== null) {
    textCell(element, factory, 'p', diagnosis.reportedWarning, 'sentence', 'reported_warning', 'run-reported-warning')
  }
  for (const warning of diagnosis.pinWarnings) {
    textCell(element, factory, 'p', warning, 'sentence', 'pin_warnings[]', 'run-pin-warning')
  }
  for (const split of diagnosis.byRoute) {
    const line = cell(element, factory, 'p', 'run-route')
    textCell(line, factory, 'span', split.name, 'label', 'route_by', 'run-route-name')
    numberCell(line, factory, 'span', split.usd, 'usd', 'run-route-usd')
  }
  for (const split of diagnosis.byModel) {
    const line = cell(element, factory, 'p', 'run-model')
    textCell(line, factory, 'span', split.name, 'label', 'model_by', 'run-model-name')
    numberCell(line, factory, 'span', split.usd, 'usd', 'run-model-usd')
  }
  return element
}

/** replayPanel is the ring: its bound, its span, and the moment scrubbed to. */
function replayPanel(factory: ScreenFactory, model: RunReplay): ScreenElement {
  const element = make(factory, 'section', 'run-replay')
  literal(cell(element, factory, 'h2', 'run-panel-title'), 'replay buffer')
  const records = cell(element, factory, 'p', 'run-replay-records')
  literal(records, 'records held:')
  numberCell(records, factory, 'span', model.records, 'count', 'run-replay-count')
  literal(cell(records, factory, 'span', 'run-replay-window-word'), 'over a window of')
  // The window is drawn as a reading like every other number, so a build with
  // no window has an `n/a` and not a zero-second window.
  numberCell(records, factory, 'span', model.windowSeconds, 'seconds', 'run-replay-window')
  const span = cell(element, factory, 'p', 'run-replay-span')
  literal(span, 'window:')
  if (model.from !== null) textCell(span, factory, 'span', model.from, 'label', 'replay.from', 'run-replay-from')
  if (model.to !== null) textCell(span, factory, 'span', model.to, 'label', 'replay.to', 'run-replay-to')
  const scrubbed = cell(element, factory, 'p', 'run-replay-scrubbed')
  if (model.scrubbed !== null) {
    literal(scrubbed, 'showing the moment')
    textCell(scrubbed, factory, 'span', recordStamp(model.scrubbed), 'label', 'replay.at', 'run-replay-at')
  } else if (model.scrubbedBecause !== null) {
    // The sentence names the moment scrubbed to, which is an input the console
    // did not write, so it is drawn through `putText`.
    putText(scrubbed, model.scrubbedBecause, 'sentence', 'scrubbedBecause')
  }
  literal(cell(element, factory, 'p', 'run-replay-note'), REPLAY_BOUND_NOTE)
  return element
}

/** renderRun walks the model through Task 2.0's writers. */
export function renderRun(factory: ScreenFactory, model: RunModel, theme: RunTheme): ScreenElement {
  const root = make(factory, 'section', 'run')
  root.setAttribute('data-theme', theme)
  root.append(header(factory, model))
  root.append(ledgerPanel(factory, model.ledger))
  root.append(stagePanel(factory, model))
  root.append(pullRequestPanel(factory, model.pullRequest))
  root.append(logsPanel(factory, model.logs))
  root.append(diagnosePanel(factory, model))
  root.append(replayPanel(factory, model.replay))
  for (const warning of model.warnings) {
    textCell(root, factory, 'p', warning, 'sentence', 'warnings[]', 'run-warning')
  }
  return root
}
