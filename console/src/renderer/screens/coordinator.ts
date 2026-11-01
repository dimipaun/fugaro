// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Coordinator screen: the project view (Bitbucket credentials T1.2, D364).
//
// One read -- `fugaro coordinator status --from-store --all --json`, pinned in
// `COORDINATOR_SOURCE_LINE` -- and one screen: every repository the
// installation's local config lists, drawn from the view the coordinator's sweep
// last stored for it. It is the console's only read that asks nothing of the
// host (the stored view is the caller's own database read through
// `roles/firebasedatabase.viewer`: no App key, no Bitbucket credential, no host
// call), and it is the only one that answers rather than refuses for a
// repository the coordinator has never reconciled -- the runs half, with
// `"stored": false`.
//
// Three rules shape the drawing, and each is a sentence the CLI wrote that this
// screen must not quietly improve on:
//
//   - **A stored node is the *last stored* view.** The console cannot tell a
//     coordinator that is running from one that was switched off after its last
//     sweep, and it must not imply the difference: a stored node is labelled
//     `COORDINATOR_STORED_WORD` ("the last stored view") and an unstored one
//     with the runs fallback, so neither reads as "current".
//   - **The view claims no age.** The stored view carries no time by design
//     (`ee/coordinator/sweep.go`: it is a function of its inputs), so a
//     screen that turned the marker's `since` into "stale for three hours" would
//     be claiming a clock the document does not have. The marker is drawn
//     exactly as stored, and `COORDINATOR_NO_AGE_WORD` is the console's own
//     sentence about the one thing a reader looks for and does not find.
//   - **Every field is text.** A repository name, a pull request's title and a
//     branch are written by whoever opened the pull request, so each is drawn
//     through `putText` with the class its wire key declares -- never through
//     `literal`, never into an attribute a browser navigates from, and never
//     into a link. The pull request's `url` is **not drawn at all**: it is
//     carried as data, and no element, attribute or link is built from it
//     (design section 5.1 rule 2).
//
// The counts are this console's own off the document's arrays, and they are
// readings: they enter through `measured` and leave through `putReading`, so
// there is no path by which a number reaches the screen without its state.
//
// A refusal is not this screen's: the entry point draws the CLI's own sentence
// through `refusedScreen` (`src/renderer/entry.ts`), and `renderCoordinator` is
// only ever handed a document. That is why the model's `view` is not nullable.

import { measured } from '../../shared/availability'
import type { Reading } from '../../shared/availability'
import { nameableText } from '../../shared/sanitize'
import type { TextClass } from '../../shared/sanitize'
import type {
  CoordinatorRecord,
  CoordinatorRepoView,
  CoordinatorStatusView,
} from '../../shared/schema'
import { putReading, putText } from '../app'
import type { ElementLike } from '../app'

// ---------------------------------------------------------------------------
// The console's own words.
// ---------------------------------------------------------------------------

/** COORDINATOR_SOURCE_LINE is the command line this screen's document comes
 * from, in the console's own words. It is the operation's whole argv, which is
 * why it is one string and not a table: the read fixes `--from-store` (the
 * stored view, no key), `--all` (every repository the config lists, which is the
 * `repos` wrapper) and `--json`, and a screen has no control that turns any of
 * them off (`src/main/spawn.ts`). */
export const COORDINATOR_SOURCE_LINE = 'fugaro coordinator status --from-store --all --json'

/** COORDINATOR_STORED_WORD labels a repository the database held a view for. It
 * says *last stored* and not "stored": the console cannot tell a coordinator
 * that is running from one that was switched off after its final sweep, and a
 * node left behind is drawn as what it is -- the last view written. */
export const COORDINATOR_STORED_WORD = 'the last stored view'

/** COORDINATOR_RUNS_WORD labels the fallback the CLI answers for a repository
 * with no node at all: the runs half alone (`coordinatorStoredLine`,
 * `ee/coordinator/cli/coordinator_view.go`). */
export const COORDINATOR_RUNS_WORD = 'no stored view: the runs alone'

/** COORDINATOR_NO_AGE_WORD is the console's own sentence about the one thing a
 * reader will look for and not find, said before the panels rather than left to
 * be inferred from a marker's stamp. */
export const COORDINATOR_NO_AGE_WORD =
  'the stored view carries no time, so this console claims no age for it'

/** COORDINATOR_EMPTY_WORD is drawn when the installation lists no repository. It
 * is a state and not a zero: the read succeeded and there was nothing in it. */
export const COORDINATOR_EMPTY_WORD = 'the installation lists no repository, so there is no view to draw'

/** COORDINATOR_STALE_WORD introduces the marker the sweep left, drawn exactly as
 * stored. */
export const COORDINATOR_STALE_WORD = 'stale since'

/** COORDINATOR_UNREAD_WORD introduces the pull requests whose host state a
 * sweep could not read. */
export const COORDINATOR_UNREAD_WORD = 'state unread'

/** COORDINATOR_CARRIED_WORD and COORDINATOR_NOT_CARRIED_WORD are the two answers
 * an unread entry gives to "is the state shown the last one a sweep did read". */
export const COORDINATOR_CARRIED_WORD = 'showing the last state a sweep read'
export const COORDINATOR_NOT_CARRIED_WORD = 'no state is shown for it'

/** COORDINATOR_OMITTED_WORD introduces the records and collisions the
 * document's own bound left out (`MaxViewRecords`, `MaxViewCollisions`). */
export const COORDINATOR_OMITTED_WORD = 'left out of the stored document'

/** COORDINATOR_IN_FLIGHT_WORD and COORDINATOR_FINISHED_WORD are a record's two
 * states, and COORDINATOR_NO_RUNS_WORD is a record with no run behind it. */
export const COORDINATOR_IN_FLIGHT_WORD = 'in flight'
export const COORDINATOR_FINISHED_WORD = 'finished'
export const COORDINATOR_NO_RUNS_WORD = 'no run behind it'

/** COORDINATOR_DRAFT_WORD marks a pull request the host reports as a draft. */
export const COORDINATOR_DRAFT_WORD = 'draft'

/** COORDINATOR_TRUNCATED_WORD marks a run whose recorded path set the store
 * itself says is short of the set it changed (`files_truncated`). */
export const COORDINATOR_TRUNCATED_WORD = 'paths truncated'

/** COORDINATOR_COLLISION_WORD introduces the pair a collision is about. */
export const COORDINATOR_COLLISION_WORD = 'in flight on the same files or services as'

/** COORDINATOR_DECLARED_WORD introduces the declaration a run's launch record
 * wrote about itself -- drawn as the person-shaped text it is, and never as a
 * claim about who ran anything (`Run.DeclaredBy` is unverified). */
export const COORDINATOR_DECLARED_WORD = 'declared by'

/** COORDINATOR_RECORDS_WORD, COORDINATOR_IN_FLIGHT_COUNT_WORD and
 * COORDINATOR_COLLISIONS_WORD are what the three counts beside a repository's
 * name count, in the order they are drawn. */
export const COORDINATOR_RECORDS_WORD = 'records'
export const COORDINATOR_IN_FLIGHT_COUNT_WORD = 'in flight'
export const COORDINATOR_COLLISIONS_WORD = 'collisions'

/** COORDINATOR_THEMES is the two themes the screen draws in, declared per screen
 * like every sibling's. */
export const COORDINATOR_THEMES = Object.freeze(['light', 'dark'] as const)

/** CoordinatorTheme is one of them. */
export type CoordinatorTheme = (typeof COORDINATOR_THEMES)[number]

/** COORDINATOR_LIST_CAP bounds one collision's drawn file or service list: a
 * pair that shares a thousand paths is a pair whose row is not a list of a
 * thousand, so the console draws the first `COORDINATOR_LIST_CAP` and says how
 * many it did not draw rather than drawing a silent prefix. */
export const COORDINATOR_LIST_CAP = 20

// ---------------------------------------------------------------------------
// The elements.
// ---------------------------------------------------------------------------

/**
 * ScreenElement is what a screen may draw into: Task 2.0's `ElementLike` (a text
 * slot and attributes), a tag, children and `append`. No markup-parsing slot, no
 * navigating attribute, no event handler and no style -- a screen that wanted one
 * would have to widen this interface first, in a diff a reviewer can see.
 *
 * It is declared here as well as in `live.ts`, `spend.ts`, `projects.ts` and
 * `run.ts` rather than imported from any of them, because the screens are
 * siblings and none owns another. The declarations are structurally identical,
 * and `coordinator.test.ts` draws this screen through another screen's factory
 * -- so a drift is a failing test rather than a screen that compiles and draws
 * nothing.
 */
export interface ScreenElement extends ElementLike {
  readonly tag: string
  append(child: ScreenElement): void
  childNodes(): readonly ScreenElement[]
}

/** ScreenFactory is where elements come from. It is an argument rather than a
 * `document` reference so the screen can be drawn, and asserted on, in a test
 * with no window at all. */
export interface ScreenFactory {
  create(tag: string): ScreenElement
}

// ---------------------------------------------------------------------------
// The model.
// ---------------------------------------------------------------------------

/** CoordinatorRunPanel is one run behind a record. */
export interface CoordinatorRunPanel {
  readonly id: string
  readonly status: string
  readonly outcome: string
  readonly declaredBy: string
  readonly inFlight: boolean
  readonly filesTruncated: boolean
}

/** CoordinatorRecordPanel is one record: a pull request with the runs behind it,
 * or the in-flight runs alone. `label` is the console's own name for the record
 * -- the pull request's number, or the newest run's id -- and `title` is the
 * untrusted text whoever opened the pull request wrote. */
export interface CoordinatorRecordPanel {
  readonly label: string
  readonly title: string
  readonly state: string
  readonly draft: boolean
  readonly branch: string
  readonly release: string
  readonly inFlight: boolean
  readonly runs: readonly CoordinatorRunPanel[]
}

/** CoordinatorCollisionPanel is one collision, with each list already cut to
 * `COORDINATOR_LIST_CAP` and the count that was cut. */
export interface CoordinatorCollisionPanel {
  readonly a: string
  readonly b: string
  readonly files: readonly string[]
  readonly services: readonly string[]
  readonly filesDropped: number
  readonly servicesDropped: number
}

/** CoordinatorUnreadPanel is one pull request a sweep could not read. */
export interface CoordinatorUnreadPanel {
  readonly number: string
  readonly reason: string
  readonly carried: boolean
  readonly retryAfter: string
}

/** CoordinatorStalePanel is the sweep's marker, exactly as stored. */
export interface CoordinatorStalePanel {
  readonly since: string
  readonly reason: string
}

/** CoordinatorRepoPanel is one repository's panel. The three counts are the
 * console's own off the document's arrays, so a repository with no records reads
 * a measured zero rather than nothing -- the document said so. The two omission
 * counts are readings only when the document carried one: a document that
 * omitted nothing has no such count, which is not the same as a zero. */
export interface CoordinatorRepoPanel {
  readonly repo: string
  readonly stored: boolean
  readonly stale: CoordinatorStalePanel | null
  readonly records: readonly CoordinatorRecordPanel[]
  readonly collisions: readonly CoordinatorCollisionPanel[]
  readonly unread: readonly CoordinatorUnreadPanel[]
  readonly recordCount: Reading
  readonly inFlightCount: Reading
  readonly collisionCount: Reading
  readonly omitted: Reading | null
  readonly omittedCollisions: Reading | null
}

/** CoordinatorModel is what the screen draws. */
export interface CoordinatorModel {
  readonly source: string
  readonly repos: readonly CoordinatorRepoPanel[]
}

/** CoordinatorInputs is what the entry point hands the model: the decoded
 * document. */
export interface CoordinatorInputs {
  readonly view: CoordinatorStatusView
}

/**
 * recordLabel is the record's own naming rule in the console's words: the pull
 * request's number when the record has one, else the newest run's id, else a
 * dash (`ee/coordinator/view.go` names records the same way). It is the
 * view's own name for the record and never the host's text.
 */
function recordLabel(record: CoordinatorRecord): string {
  if (record.pr !== null && record.pr.number > 0) return `#${String(record.pr.number)}`
  const newest = record.runs[0]
  return newest === undefined ? '-' : newest.id
}

/** cut is one list with its first `COORDINATOR_LIST_CAP` members and how many
 * were left off, so a row can say what it did not draw. */
function cut(items: readonly string[]): { readonly drawn: readonly string[]; readonly dropped: number } {
  return {
    drawn: items.slice(0, COORDINATOR_LIST_CAP),
    dropped: Math.max(0, items.length - COORDINATOR_LIST_CAP),
  }
}

function recordPanel(record: CoordinatorRecord): CoordinatorRecordPanel {
  const pr = record.pr
  return {
    label: recordLabel(record),
    title: pr === null ? '' : pr.title,
    state: pr === null ? '' : pr.state,
    draft: pr !== null && pr.draft,
    branch: record.branch,
    release: record.release,
    inFlight: record.inFlight,
    runs: record.runs.map((run) => ({
      id: run.id,
      status: run.status,
      outcome: run.outcome,
      declaredBy: run.declaredBy,
      inFlight: !run.terminal,
      filesTruncated: run.filesTruncated,
    })),
  }
}

function repoPanel(repo: CoordinatorRepoView): CoordinatorRepoPanel {
  const view = repo.view
  return {
    repo: repo.repo,
    stored: repo.stored,
    stale: repo.stale === null ? null : { since: repo.stale.since, reason: repo.stale.reason },
    records: view.records.map((record) => recordPanel(record)),
    collisions: view.collisions.map((collision) => {
      const files = cut(collision.files)
      const services = cut(collision.services)
      return {
        a: collision.a,
        b: collision.b,
        files: files.drawn,
        services: services.drawn,
        filesDropped: files.dropped,
        servicesDropped: services.dropped,
      }
    }),
    unread: view.unread.map((entry) => ({
      number: `#${String(entry.number)}`,
      reason: entry.reason,
      carried: entry.carried,
      retryAfter: entry.retryAfter ?? '',
    })),
    recordCount: measured(view.records.length),
    inFlightCount: measured(view.records.filter((record) => record.inFlight).length),
    collisionCount: measured(view.collisions.length),
    omitted: view.omitted > 0 ? measured(view.omitted) : null,
    omittedCollisions: view.omittedCollisions > 0 ? measured(view.omittedCollisions) : null,
  }
}

/**
 * buildCoordinatorModel turns the decoded document into the panels the screen
 * draws. Every string it composes itself goes to the screen through `literal`
 * (module constants, and the record labels and numbers the console made); every
 * string the *document* carried goes through `putText`, with the class its wire
 * key declares.
 */
export function buildCoordinatorModel(inputs: CoordinatorInputs): CoordinatorModel {
  return { source: COORDINATOR_SOURCE_LINE, repos: inputs.view.repos.map((repo) => repoPanel(repo)) }
}

// ---------------------------------------------------------------------------
// The drawing.
// ---------------------------------------------------------------------------

/** make is a fresh element with its classes. */
function make(factory: ScreenFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = factory.create(tag)
  element.setAttribute('class', classes.join(' '))
  return element
}

/** literal writes the console's own vocabulary -- never a document's string,
 * which comes through `putText` (`../app.ts`) or not at all. */
function literal(element: ScreenElement, text: string): ScreenElement {
  element.textContent = text
  return element
}

/** cell is a cell of the console's own making, appended to a parent. */
function cell(parent: ScreenElement, factory: ScreenFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = make(factory, tag, ...classes)
  parent.append(element)
  return element
}

/** textCell is `cell` with a document's string written into it through
 * `putText`, with the class its wire key declares. */
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

/** numberCell is `cell` with a reading written through `putReading`, so no
 * number can be drawn without its state. Its word is the console's own, so it
 * goes through `literal`. */
function numberCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  word: string,
  reading: Reading,
  ...classes: string[]
): ScreenElement {
  const element = cell(parent, factory, 'span', 'coordinator-count', ...classes)
  literal(cell(element, factory, 'span', 'coordinator-count-word'), word)
  putReading(cell(element, factory, 'span', 'coordinator-count-value'), reading, 'count')
  return element
}

/** listCell is a list of document strings, each in its own cell, with how many
 * were not drawn when the list was cut. */
function listCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  items: readonly string[],
  dropped: number,
  textClass: TextClass,
  field: string,
  ...classes: string[]
): void {
  for (const item of items) textCell(parent, factory, 'span', item, textClass, field, ...classes)
  if (dropped > 0) countCell(parent, factory, measured(dropped), 'coordinator-more')
}

/** countCell is one bare count, through `putReading` like every other number
 * here. */
function countCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  reading: Reading,
  ...classes: string[]
): ScreenElement {
  const element = cell(parent, factory, 'span', ...classes)
  putReading(element, reading, 'count')
  return element
}

function runCell(parent: ScreenElement, factory: ScreenFactory, run: CoordinatorRunPanel): void {
  const row = cell(parent, factory, 'li', 'coordinator-run')
  textCell(row, factory, 'span', run.id, 'runId', 'runs[].id', 'coordinator-run-id')
  if (run.status !== '') {
    textCell(row, factory, 'span', run.status, 'label', 'runs[].status', 'coordinator-run-status')
  }
  if (run.outcome !== '') {
    textCell(row, factory, 'span', run.outcome, 'label', 'runs[].outcome', 'coordinator-run-outcome')
  }
  if (run.declaredBy !== '') {
    literal(cell(row, factory, 'span', 'coordinator-run-declared-word'), COORDINATOR_DECLARED_WORD)
    // The declaration and not an identity: the launch record wrote it about
    // itself and nothing verified it, so it is drawn as the person-shaped text
    // it is and never as a claim about who ran anything.
    textCell(row, factory, 'span', run.declaredBy, 'person', 'runs[].declared_by', 'coordinator-run-by')
  }
  if (run.inFlight) {
    literal(cell(row, factory, 'span', 'coordinator-run-open'), COORDINATOR_IN_FLIGHT_WORD)
  }
  if (run.filesTruncated) {
    literal(cell(row, factory, 'span', 'coordinator-run-short'), COORDINATOR_TRUNCATED_WORD)
  }
}

function recordCell(parent: ScreenElement, factory: ScreenFactory, record: CoordinatorRecordPanel): void {
  const row = cell(parent, factory, 'li', 'coordinator-record')
  textCell(row, factory, 'span', record.label, 'label', 'records[].pr.number', 'coordinator-record-label')
  if (record.title !== '') {
    // The pull request's own words, written by whoever opened it: text, in the
    // class the wire key declares, and never a link (design section 5.1 rule 2).
    textCell(row, factory, 'span', record.title, 'sentence', 'records[].pr.title', 'coordinator-record-title')
  }
  if (record.state !== '') {
    textCell(row, factory, 'span', record.state, 'label', 'records[].pr.state', 'coordinator-record-state')
  }
  if (record.branch !== '') {
    textCell(row, factory, 'span', record.branch, 'branch', 'records[].branch', 'coordinator-record-branch')
  }
  if (record.release !== '') {
    textCell(row, factory, 'span', record.release, 'label', 'records[].release', 'coordinator-record-release')
  }
  if (record.draft) {
    literal(cell(row, factory, 'span', 'coordinator-record-draft'), COORDINATOR_DRAFT_WORD)
  }
  const flight = cell(row, factory, 'span', 'coordinator-record-flight')
  if (record.inFlight) literal(flight, COORDINATOR_IN_FLIGHT_WORD)
  else literal(flight, COORDINATOR_FINISHED_WORD)
  if (record.runs.length === 0) {
    literal(cell(row, factory, 'span', 'coordinator-record-noruns'), COORDINATOR_NO_RUNS_WORD)
    return
  }
  const runs = cell(row, factory, 'ul', 'coordinator-runs')
  for (const run of record.runs) runCell(runs, factory, run)
}

function collisionCell(parent: ScreenElement, factory: ScreenFactory, collision: CoordinatorCollisionPanel): void {
  const row = cell(parent, factory, 'li', 'coordinator-collision')
  textCell(row, factory, 'span', collision.a, 'label', 'collisions[].a', 'coordinator-collision-a')
  literal(cell(row, factory, 'span', 'coordinator-collision-word'), COORDINATOR_COLLISION_WORD)
  textCell(row, factory, 'span', collision.b, 'label', 'collisions[].b', 'coordinator-collision-b')
  listCell(row, factory, collision.files, collision.filesDropped, 'path', 'collisions[].files', 'coordinator-file')
  listCell(
    row,
    factory,
    collision.services,
    collision.servicesDropped,
    'label',
    'collisions[].services',
    'coordinator-service',
  )
}

function unreadCell(parent: ScreenElement, factory: ScreenFactory, entry: CoordinatorUnreadPanel): void {
  const row = cell(parent, factory, 'li', 'coordinator-unread')
  literal(cell(row, factory, 'span', 'coordinator-unread-word'), COORDINATOR_UNREAD_WORD)
  textCell(row, factory, 'span', entry.number, 'label', 'unread[].number', 'coordinator-unread-number')
  textCell(row, factory, 'span', entry.reason, 'sentence', 'unread[].reason', 'coordinator-unread-reason')
  const carried = cell(row, factory, 'span', 'coordinator-unread-carried')
  if (entry.carried) literal(carried, COORDINATOR_CARRIED_WORD)
  else literal(carried, COORDINATOR_NOT_CARRIED_WORD)
  if (entry.retryAfter !== '') {
    textCell(row, factory, 'span', entry.retryAfter, 'sentence', 'unread[].retry_after', 'coordinator-unread-retry')
  }
}

function repoCell(parent: ScreenElement, factory: ScreenFactory, panel: CoordinatorRepoPanel): void {
  const section = cell(parent, factory, 'section', 'coordinator-repo')
  // The repository's own name, which the CLI read out of the local config: text
  // in the class the wire key declares, and never a link or a path.
  textCell(section, factory, 'h2', panel.repo, 'repository', 'repos[].repo', 'coordinator-repo-name')
  const state = cell(section, factory, 'p', 'coordinator-repo-state')
  if (panel.stored) literal(state, COORDINATOR_STORED_WORD)
  else literal(state, COORDINATOR_RUNS_WORD)
  if (panel.stale !== null) {
    const stale = cell(section, factory, 'p', 'coordinator-repo-stale')
    literal(cell(stale, factory, 'span', 'coordinator-stale-word'), COORDINATOR_STALE_WORD)
    textCell(stale, factory, 'span', panel.stale.since, 'sentence', 'stale.since', 'coordinator-stale-since')
    textCell(stale, factory, 'span', panel.stale.reason, 'sentence', 'stale.reason', 'coordinator-stale-reason')
  }
  const counts = cell(section, factory, 'p', 'coordinator-counts')
  numberCell(counts, factory, COORDINATOR_RECORDS_WORD, panel.recordCount, 'coordinator-count-records')
  numberCell(
    counts,
    factory,
    COORDINATOR_IN_FLIGHT_COUNT_WORD,
    panel.inFlightCount,
    'coordinator-count-inflight',
  )
  numberCell(counts, factory, COORDINATOR_COLLISIONS_WORD, panel.collisionCount, 'coordinator-count-collisions')
  if (panel.omitted !== null || panel.omittedCollisions !== null) {
    const omitted = cell(section, factory, 'p', 'coordinator-omitted')
    literal(cell(omitted, factory, 'span', 'coordinator-omitted-word'), COORDINATOR_OMITTED_WORD)
    if (panel.omitted !== null) countCell(omitted, factory, panel.omitted, 'coordinator-omitted-records')
    if (panel.omittedCollisions !== null) {
      countCell(omitted, factory, panel.omittedCollisions, 'coordinator-omitted-collisions')
    }
  }
  if (panel.records.length > 0) {
    const records = cell(section, factory, 'ul', 'coordinator-records')
    for (const record of panel.records) recordCell(records, factory, record)
  }
  if (panel.collisions.length > 0) {
    const collisions = cell(section, factory, 'ul', 'coordinator-collisions')
    for (const collision of panel.collisions) collisionCell(collisions, factory, collision)
  }
  if (panel.unread.length > 0) {
    const unread = cell(section, factory, 'ul', 'coordinator-unreads')
    for (const entry of panel.unread) unreadCell(unread, factory, entry)
  }
}

/**
 * renderCoordinator draws the project view: a `section` stamped with the theme
 * it was drawn for, the read's own source line, the console's sentence about the
 * view's missing age, and one section per repository.
 *
 * The theme stamp is the drawing's *record* and not a read of the machine (the
 * one value set ships, dark -- design section 3.6), which is why it is written
 * here rather than looked up.
 */
export function renderCoordinator(
  factory: ScreenFactory,
  model: CoordinatorModel,
  theme: CoordinatorTheme,
): ScreenElement {
  const section = make(factory, 'section', 'coordinator')
  section.setAttribute('data-theme', theme)
  literal(cell(section, factory, 'p', 'coordinator-source'), COORDINATOR_SOURCE_LINE)
  literal(cell(section, factory, 'p', 'coordinator-age'), COORDINATOR_NO_AGE_WORD)
  if (model.repos.length === 0) {
    literal(cell(section, factory, 'p', 'coordinator-empty'), COORDINATOR_EMPTY_WORD)
    return section
  }
  for (const panel of model.repos) repoCell(section, factory, panel)
  return section
}
