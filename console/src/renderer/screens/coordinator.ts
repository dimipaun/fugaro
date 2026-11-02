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
// only ever handed a document. That is why the body's `repos` is nullable in the
// model rather than the model being absent -- the screen's sections above it are
// drawn from three other reads.
//
// **The action queue and the fleet totals (console coordinator v2, K1)** are the
// two sections above that body, and they are what this screen exists to answer
// first: what is waiting for a person, and what the fleet holds. They are built
// from four reads the entry starts together -- the stored view above,
// `runs ls --json --since 24h`, `plan status --json --all` and
// `budget show --json` -- and each part is drawn the moment **its own** read
// answers (the owner's progressive-rendering rule of 2026-10-31). Nothing here
// invents a figure: an item is drawn only for a state a document carries, a
// count is drawn only over the items the console itself drew, and a read that
// has not answered is absent rather than zero (D819).

import { measured } from '../../shared/availability'
import type { Reading, Unit } from '../../shared/availability'
import { nameableText, sanitizeText } from '../../shared/sanitize'
import type { TextClass } from '../../shared/sanitize'
import { BUSY_SLOT_CLASS } from '../section'
import type { SkeletonBlock, SkeletonShape } from '../skeleton'
import type {
  CoordinatorRecord,
  CoordinatorRepoView,
  CoordinatorStatusView,
  PlanStatusView,
} from '../../shared/schema'
import { capsAtCap } from './spend'
import type { SpendCaps } from './spend'
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

/** COORDINATOR_BUSY is what the screen's section mark says it is reading. The
 * read is the longest one this console makes -- the stored view of every
 * repository at once -- so the mark names it rather than saying "reading", and
 * the section around the mark is where the figures already drawn stay. */
export const COORDINATOR_BUSY = 'Reading the coordinator’s stored view…'

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
 * COORDINATOR_COLLISIONS_WORD are what the three cells of a repository's
 * counter strip count, in the order they are drawn. Each is drawn inside its own
 * cell, beside the figure, because the strip's one state -- a repository whose
 * records are in flight on the same files -- is carried by the collisions cell
 * and a cell marked by a tone alone would be a state a greyscale reader loses. */
export const COORDINATOR_RECORDS_WORD = 'records'
export const COORDINATOR_IN_FLIGHT_COUNT_WORD = 'in flight'
export const COORDINATOR_COLLISIONS_WORD = 'collisions'

/** COORDINATOR_BODY_CLASS is the element the screen's body is filled into: the
 * summary (a repository's name, its state and its counts) the moment the read
 * answers, and each repository's per-pull-request, per-run detail after it --
 * one chunk per repository (`entry.ts`). The header above it -- the source line
 * and the console's sentence about the view's age -- is drawn before the read
 * answers at all, which is what gives the tab something to paint in the frame it
 * was opened in. */
export const COORDINATOR_BODY_CLASS = 'coordinator-body'

/** COORDINATOR_DETAIL_CLASS is one repository's detail host: the element the
 * records, the collisions and the unread markers of that repository are filled
 * into, after the summary. It is a `div` of the console's own making and not a
 * disclosure control: the detail is the same view of the same repository, so
 * hiding it behind a click would be hiding the answer. */
export const COORDINATOR_DETAIL_CLASS = 'coordinator-detail'

/** COORDINATOR_THEMES is the two themes the screen draws in, declared per screen
 * like every sibling's. */
export const COORDINATOR_THEMES = Object.freeze(['light', 'dark'] as const)

/** CoordinatorTheme is one of them. */
export type CoordinatorTheme = (typeof COORDINATOR_THEMES)[number]

/** COORDINATOR_LIST_CAP bounds one list this screen draws: a collision's files
 * or services, and the runs behind one pull request. A pair that shares a
 * thousand paths is a pair whose row is not a list of a thousand, and a pull
 * request a hundred runs deep is a row of a hundred lines in a panel a reader is
 * scanning -- so the console draws the first `COORDINATOR_LIST_CAP` and says how
 * many it did not draw rather than drawing a silent prefix. */
export const COORDINATOR_LIST_CAP = 20

// ---------------------------------------------------------------------------
// The action queue's own words (K1).
// ---------------------------------------------------------------------------

/** COORDINATOR_QUEUE_WORD is what the count beside the queue's head counts: the
 * items the console drew, which is a figure of the console's own and never one a
 * document carries. It is drawn only when the three reads the tab always makes
 * have answered -- a console does not count a queue it is still reading, and a
 * count drawn while a read is out would be a figure the operator cannot check.
 *
 * The jobs are the one read that is **not** made on entry (rule 5 of §6.2's
 * opening: `runs ls --json` is fetched only when the operator asks for it), so
 * the count stands over the parts the console has read and the jobs control
 * under it names the one it has not. The sentence for a queue that holds nothing
 * is the stricter one and stays whole-only (COORDINATOR_QUEUE_EMPTY_WORD). */
export const COORDINATOR_QUEUE_WORD = 'items need you'

/** COORDINATOR_QUEUE_EMPTY_WORD is the head's answer when the **whole** queue --
 * the jobs among them -- has been read and none of it is holding anything. It is
 * a state and not a zero: `0` beside `items need you` would be the same words a
 * queue nobody finished reading cannot say, and a queue whose jobs have not been
 * asked for is exactly that queue. */
export const COORDINATOR_QUEUE_EMPTY_WORD = 'nothing is waiting for you'

/** COORDINATOR_QUEUE_JOBS_WORD is the queue's one control that is not an item's:
 * the jobs are read when the operator asks for them and not before (rule 5), and
 * this is what asks. It says what the press reads rather than naming the command,
 * which is the queue's own rule for the words it draws. */
export const COORDINATOR_QUEUE_JOBS_WORD = 'Load the jobs waiting on you'

/** COORDINATOR_QUEUE_PR_WORD, COORDINATOR_QUEUE_JOB_WORD,
 * COORDINATOR_QUEUE_PLAN_WORD and COORDINATOR_QUEUE_BUDGET_WORD are the four
 * kinds of item, in the order the queue draws them -- the order of its four
 * reads. Each is the console's own name for the kind: no document says `JOB`,
 * and one that printed a document's word here would be drawing a vocabulary it
 * does not control. */
export const COORDINATOR_QUEUE_PR_WORD = 'PR'
export const COORDINATOR_QUEUE_JOB_WORD = 'JOB'
export const COORDINATOR_QUEUE_PLAN_WORD = 'PLAN'
export const COORDINATOR_QUEUE_BUDGET_WORD = 'BUDGET'

/** COORDINATOR_QUEUE_PR_WHY_WORD, COORDINATOR_QUEUE_JOB_WHY_WORD and
 * COORDINATOR_QUEUE_PLAN_WHY_WORD are why an item of that kind is waiting, in
 * the console's own words. Each states the rule the queue applied and not a
 * claim about the work: nothing in any document says a pull request is *ready to
 * merge*, and a queue that said so would be inventing a review result. */
export const COORDINATOR_QUEUE_PR_WHY_WORD = 'open, not a draft, and no run behind it is still going'
export const COORDINATOR_QUEUE_JOB_WHY_WORD = 'this run ended without succeeding'
export const COORDINATOR_QUEUE_PLAN_WHY_WORD = 'ready to launch: no run has joined it and everything it needs is done'

/** COORDINATOR_QUEUE_KILL_WORD, COORDINATOR_QUEUE_REPO_KILL_WORD and
 * COORDINATOR_QUEUE_CAP_WORD are the three grounds a budget alert states, and
 * the alert says which one it is: the fleet's kill switch refuses every run, a
 * repository's own kill refuses that repository, and a reached daily cap refuses
 * new runs until the day rolls. They are three facts and not one, which is why
 * one word does not cover them (D812). */
export const COORDINATOR_QUEUE_KILL_WORD = 'the kill switch is on: the fleet refuses every new run'
export const COORDINATOR_QUEUE_REPO_KILL_WORD = 'this repository is killed: it refuses new runs until it is resumed'
export const COORDINATOR_QUEUE_CAP_WORD = 'this repository has reached its daily cap: it refuses new runs'

/** COORDINATOR_QUEUE_NO_PLANS_WORD and COORDINATOR_QUEUE_CAPS_HIDDEN_WORD are
 * the two notes the queue draws where a read it would need was not made. They
 * are the console's own sentences about its own gap, and they are what stands in
 * place of a part of the queue: an installation that records no checkout gets no
 * `plan status` read at all (the command walks a checkout), and the reduced mode
 * withholds the caps. */
export const COORDINATOR_QUEUE_NO_PLANS_WORD =
  'this installation records no checkout, so the plan tasks are not known'
export const COORDINATOR_QUEUE_CAPS_HIDDEN_WORD =
  'the caps are withheld for the installation this console is driving'

/** COORDINATOR_OPEN_RUN_WORD, COORDINATOR_RETRY_WORD, COORDINATOR_LAUNCH_WORD and
 * COORDINATOR_OPEN_SPEND_WORD are the four buttons: one per kind of item, and
 * **one per item**, which is the queue's own rule (spec section 2.2). Retry and
 * Launch lead to the launch confirmation the console already has -- rule 11's
 * window and rule 11's argv, with the item's repository and plan task filled in
 * -- so they are not a second way to start a run (D811). Open Spend is a
 * navigation: the queue never calls `budget kill` or `budget resume`. */
export const COORDINATOR_OPEN_RUN_WORD = 'Open run'
export const COORDINATOR_RETRY_WORD = 'Retry'
export const COORDINATOR_LAUNCH_WORD = 'Launch'
export const COORDINATOR_OPEN_SPEND_WORD = 'Open Spend'

/** COORDINATOR_OPEN_PR_NAME_WORD is the verb the pull request numbers are named
 * with. It is a **name** and not a drawn word: the number keeps the same `#1458`
 * it had as text, and the name is what a reader who cannot see the row hears
 * instead -- the verb, the number and the repository, so two numbers that read
 * the same are still two different things to open. */
export const COORDINATOR_OPEN_PR_NAME_WORD = 'open pull request'

/** COORDINATOR_BLIND_WORD introduces the line under the queue, and
 * COORDINATOR_BLIND_STALE_WORD and COORDINATOR_BLIND_UNREAD_WORD are the two
 * things it counts: repositories the sweep has marked stale, and pull requests a
 * sweep could not read. Neither is a queue item, because no button on this page
 * can fix either -- the sweep is a scheduled job and the console may not start
 * it -- and a disabled button would be a control that exists to explain itself
 * (D813). */
export const COORDINATOR_BLIND_WORD = 'what this page cannot reach, and offers no button for:'
export const COORDINATOR_BLIND_STALE_WORD = 'repositories the sweep has not reconciled since'
export const COORDINATOR_BLIND_UNREAD_WORD = 'pull requests a sweep could not read'

/** The fleet strip's six words. The first three are counted off the stored view,
 * the fourth off `plan status`' own total, and the last two are `budget show`'s
 * global block -- the same read the rail draws its one figure from (D819).
 * `report` is not read for any of them: it is the money read that breaks spend
 * down, and it is roughly thirteen seconds of a first paint. */
export const COORDINATOR_TOTAL_REPOS_WORD = 'repositories'
export const COORDINATOR_TOTAL_IN_FLIGHT_WORD = 'records in flight'
export const COORDINATOR_TOTAL_COLLISIONS_WORD = 'file collisions'
export const COORDINATOR_TOTAL_PLANS_WORD = 'plans'
export const COORDINATOR_TOTAL_COUNTED_WORD = 'counted today'
export const COORDINATOR_TOTAL_CAP_WORD = 'daily cap'

/** COORDINATOR_QUEUE_BUSY and COORDINATOR_TOTALS_BUSY are what the two sections'
 * own marks say while their reads are out: the queue's four are started
 * together, and the totals wait on three of them. */
export const COORDINATOR_QUEUE_BUSY = 'Reading the queue…'
export const COORDINATOR_TOTALS_BUSY = 'Reading the fleet’s totals…'

/** COORDINATOR_QUEUE_CLASS and COORDINATOR_TOTALS_CLASS are the two sections'
 * hooks: the element each of the entry's marks is put on, and what a test reads
 * to say which section is still out. */
export const COORDINATOR_QUEUE_CLASS = 'coordinator-queue'
export const COORDINATOR_TOTALS_CLASS = 'coordinator-totals'

/** COORDINATOR_QUEUE_HEAD_CLASS holds the queue's own line -- what is waiting,
 * and the sentence that says when nothing is -- and COORDINATOR_QUEUE_PART_CLASS
 * is one of the four parts under it, one per read that contributes to the queue
 * (`QUEUE_SOURCES`). COORDINATOR_BLIND_HOST_CLASS is the blind-spot line.
 *
 * The parts are hosts and not content: a read that has not answered leaves its
 * own part empty, so "this read is still out" is a part with nothing in it,
 * beside three that are filled. */
export const COORDINATOR_QUEUE_HEAD_CLASS = 'coordinator-queue-head'
export const COORDINATOR_QUEUE_PART_CLASS = 'coordinator-queue-part'
export const COORDINATOR_BLIND_HOST_CLASS = 'coordinator-blind-host'

/** COORDINATOR_QUEUE_JOBS_CLASS is the jobs control, drawn in the part the jobs
 * would fill until the operator asks for them. It carries `aria-expanded` for
 * the part it fills, the way the launch control's own button does for its form. */
export const COORDINATOR_QUEUE_JOBS_CLASS = 'coordinator-queue-jobs'

// ---------------------------------------------------------------------------
// The job list (console coordinator v2, K4).
// ---------------------------------------------------------------------------

/**
 * COORDINATOR_JOBS_CLASS is the job list's own section, a sibling of the queue
 * and the fleet strip: the section the operator asks for, and the one section of
 * this screen that is **not** part of the tab's first paint.
 *
 * The queue answers "what is waiting for a person" and holds at most the two
 * dozen items the console can draw at once; the job list is the other question
 * -- every run the window holds, newest first -- and it is read, paged and
 * expanded on demand (D818, rule 5 of §6.2's opening). Keeping it a section of
 * its own is what makes its own read, its own mark and its own refusal possible:
 * a list that failed to read must not take the queue down with it, and a section
 * the operator never asked for must not be a section that was read.
 */
export const COORDINATOR_JOBS_CLASS = 'coordinator-jobs'

/** COORDINATOR_JOBS_HEAD_CLASS carries the window's own figures, and
 * COORDINATOR_JOBS_LIST_CLASS and COORDINATOR_JOBS_CONTROLS_CLASS are the two
 * hosts the paged rows and the section's controls are filled into. The three are
 * hosts rather than content for the queue's reason: the head is written once,
 * from the first page's `totals`, and the rows are appended to, so a page that
 * lands never redraws -- and never collapses -- the rows already read. */
export const COORDINATOR_JOBS_HEAD_CLASS = 'coordinator-jobs-head'
export const COORDINATOR_JOBS_LIST_CLASS = 'coordinator-jobs-list'
export const COORDINATOR_JOBS_CONTROLS_CLASS = 'coordinator-jobs-controls'

/** COORDINATOR_JOBS_WINDOW_WORD introduces the head's two figures and says what
 * they are figures **of**: the window the read was made over, and not the rows
 * under them. The distinction is the whole of why they are drawn from
 * `totals` -- a screen that summed the page would draw the page's count under a
 * word that claims the window. */
export const COORDINATOR_JOBS_WINDOW_WORD = 'the window holds'

/** COORDINATOR_JOBS_RUNS_WORD and COORDINATOR_JOBS_SPENT_WORD are the head's two
 * cells, in the order they are drawn: how many runs the window holds, and what
 * they cost. Both come off the **first page's** `totals` (`runview.Totals`), and
 * the runs figure is the window's own count -- the CLI sums the window it read,
 * not the page it printed. */
export const COORDINATOR_JOBS_RUNS_WORD = 'runs'
export const COORDINATOR_JOBS_SPENT_WORD = 'spent'

/** COORDINATOR_JOBS_LOAD_WORD is the control that asks for the list at all, and
 * COORDINATOR_JOBS_OLDER_WORD the control that asks for the page after it. The
 * first is drawn until the operator presses it; the second only while the page
 * last read filled every row it asked for, because that is the only state in
 * which the console knows there may be more. */
export const COORDINATOR_JOBS_LOAD_WORD = 'Load jobs'
export const COORDINATOR_JOBS_OLDER_WORD = 'Older 50'

/** COORDINATOR_JOBS_EMPTY_WORD is the console's own sentence for a window the
 * read answered with no run at all. It is drawn only over a page that was read,
 * and never while one is in flight. */
export const COORDINATOR_JOBS_EMPTY_WORD = 'the window holds no run'

/** COORDINATOR_JOBS_BUSY is what the section's own mark says while its read is
 * out. It names the list and not the page, because the operator is waiting for
 * the first page of it either way. */
export const COORDINATOR_JOBS_BUSY = 'Reading the job list…'

/** COORDINATOR_JOBS_LOAD_CLASS and COORDINATOR_JOBS_OLDER_CLASS are the two
 * controls' hooks, and COORDINATOR_JOB_DETAIL_CLASS is the body one row expands:
 * hidden until the press, and drawn with the row rather than on it. */
export const COORDINATOR_JOBS_LOAD_CLASS = 'coordinator-jobs-load'
export const COORDINATOR_JOBS_OLDER_CLASS = 'coordinator-jobs-older'
export const COORDINATOR_JOB_DETAIL_CLASS = 'coordinator-job-detail'

/** COORDINATOR_JOBS_PAGE is the page the section asks for, and `--limit`'s value:
 * fifty rows. It is deliberately **not** `COORDINATOR_LIST_CAP`: that cap is
 * what this screen may draw at once, and it is twenty. Fifty is the page the
 * console asks the CLI for, and it is drawn in full -- the cap exists to keep a
 * single screen's drawing bounded, and a page the operator asked for row by row
 * is the one list on this screen that is not a summary of something else. It is
 * well under `RUNS_LIST_MAX_LIMIT` (`src/main/ipc.ts`, 200), which is the
 * surface's ceiling rather than this screen's choice. */
export const COORDINATOR_JOBS_PAGE = 50

/** COORDINATOR_JOBS_FLOOR_WORD introduces the console's sentence for a CLI that
 * is too old to page: `runs ls --limit` cut after the sort and `--after` are
 * both #1545's, and a 0.15.x CLI either refuses `--after` outright or sums the
 * page it printed. A screen that asked anyway would draw the page's figures
 * under the word "the window" -- the one wrong number this section could draw --
 * so the section draws the sentence and spawns nothing. */
export const COORDINATOR_JOBS_FLOOR_WORD = 'the job list needs fugaro 0.16.0 and this CLI is'

/** COORDINATOR_JOBS_SHAPE is the section's own skeleton, worn while the first
 * page is out: the head's line and the rows under it. It is declared here rather
 * than in `skeleton.ts` because the job list is **not** one of the sections the
 * tab's first paint marks -- nothing is in flight for it until the operator
 * presses the control -- so it is not one of `SKELETON_SHAPES.coordinator`'s,
 * which is the list of the sections a tab's entry reads. */
export const COORDINATOR_JOBS_SHAPE: SkeletonShape = Object.freeze({
  section: 'jobs',
  blocks: Object.freeze<SkeletonBlock[]>(['line', 'row', 'row', 'row']),
})

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

/**
 * CoordinatorPullLink is the triple a pull request's number is opened with: the
 * host the repository is on, its `owner/name`, and the number.
 *
 * **It is the whole of what a press sends**, and it is deliberately not a URL:
 * `src/main` builds the destination from these three fields (`pullRequestUrl`,
 * D815) and hands *its own* string to the one validated shell call, so no URL a
 * document carries ever reaches a browser (rule 2, reconciliation F2). It is
 * also why the triple is a value of this screen's own making rather than a field
 * read back out of the DOM: what the entry point sends is what the number was
 * drawn for.
 */
export interface CoordinatorPullLink {
  readonly provider: string
  readonly repo: string
  readonly number: number
}

/**
 * CoordinatorPullButton is one number drawn as a button: the element, and the
 * triple pressing it opens. The screen answers these to the entry point, which
 * binds the press -- and it binds **this pair** rather than looking the element
 * up again by class, so the two cannot come apart.
 */
export interface CoordinatorPullButton {
  readonly element: ScreenElement
  readonly link: CoordinatorPullLink
}

/** CoordinatorRecordPanel is one record: a pull request with the runs behind it,
 * or the in-flight runs alone. `label` is the console's own name for the record
 * -- the pull request's number, or the newest run's id -- and `title` is the
 * untrusted text whoever opened the pull request wrote. `runs` is already cut to
 * `COORDINATOR_LIST_CAP` and `runsDropped` is the count that was cut.
 *
 * `link` is the triple the label opens when the console can form one (K2), and
 * `null` where it cannot: a record with no pull request at all, or one whose
 * repository or provider the console will not build a destination from. A `null`
 * is drawn as text, and a label that is a run's id is never a button. */
export interface CoordinatorRecordPanel {
  readonly label: string
  readonly link: CoordinatorPullLink | null
  readonly title: string
  readonly state: string
  readonly draft: boolean
  readonly branch: string
  readonly release: string
  readonly inFlight: boolean
  readonly runs: readonly CoordinatorRunPanel[]
  readonly runsDropped: number
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
 * omitted nothing has no such count, which is not the same as a zero.
 *
 * `hasCollisions` is the strip's one state rather than a fourth count: it is
 * true exactly when the collisions cell's figure is above zero, and it is what
 * the drawing marks the cell with. It is derived here, beside the count it
 * follows, so the drawing cannot mark a cell the count does not agree with. */
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
  readonly hasCollisions: boolean
  readonly omitted: Reading | null
  readonly omittedCollisions: Reading | null
}

/**
 * CoordinatorQueueSource names the four reads the action queue is built from,
 * and it is also the name of the part of the queue each one fills: the queue
 * paints in the part whose read answered, so the part is what the entry needs a
 * handle on and what a test reads to tell a waiting part from an answered one.
 */
export type CoordinatorQueueSource = 'document' | 'runs' | 'plans' | 'budget'

/** CoordinatorQueueKind is the four kinds of item, in the order the queue draws
 * them: a pull request waiting for review or merge, a job that ended without
 * succeeding, a plan task ready to launch, and a budget alert. */
export type CoordinatorQueueKind = 'pullRequest' | 'job' | 'plan' | 'budget'

/**
 * CoordinatorQueueAction is the **one** act an item's one button performs, and
 * it carries the values that act needs and nothing else.
 *
 * `openRun` moves to the Run screen and needs the run it opens. `retry` and
 * `launch` lead to the launch confirmation the console already has (rule 11's
 * window), and what they carry is exactly what rule 11's argv is given: the
 * repository, and the plan task when the item knows one. **Neither carries a run
 * id and neither carries a title**: the id a launch runs under is the main
 * process's own, drawn fresh per confirmation (`src/main/launch.ts`), so a Retry
 * is a new launch through the approved argv rather than a replay of the failed
 * run (D811), and the title is the operator's to write in the form. `spend` is a
 * navigation to the Spend screen and nothing else (D812).
 */
export type CoordinatorQueueAction =
  | { readonly kind: 'openRun'; readonly repo: string; readonly run: string }
  | { readonly kind: 'retry'; readonly repo: string; readonly planTask: string }
  | { readonly kind: 'launch'; readonly repo: string; readonly planTask: string }
  | { readonly kind: 'spend' }

/**
 * CoordinatorQueueItem is one thing waiting for a person. Every field is off a
 * document or is the console's own sentence about the rule it applied:
 *
 *   - `subject` is the id the document gave the thing (a pull request's number as
 *     the console labels a record, a run's id, a task's id, a repository's slug),
 *     empty for an alert about the whole fleet;
 *   - `scope` is where the subject sits -- the branch behind a pull request, the
 *     plan task a run recorded, the plan a task belongs to -- and is empty when
 *     the document says nothing;
 *   - `title` is the untrusted text whoever wrote the thing wrote, drawn as text;
 *   - `ground` is which of the two budget grounds an alert stands on, and is
 *     `null` for every other kind. It is a closed word rather than a sentence so
 *     the sentence itself is drawn from this file's constants, beside the words
 *     it belongs to;
 *   - `link` is the triple the subject opens where the subject is a pull
 *     request's number the console can build a destination for (K2), and `null`
 *     for every other item and for a number it cannot.
 */
export interface CoordinatorQueueItem {
  readonly kind: CoordinatorQueueKind
  readonly subject: string
  readonly link: CoordinatorPullLink | null
  readonly scope: string
  readonly repo: string
  readonly title: string
  readonly ground: 'kill' | 'repoKill' | 'cap' | null
  readonly action: CoordinatorQueueAction
}

/**
 * CoordinatorQueueNote is the gap a part of the queue can have instead of
 * items: the installation records no checkout, so `plan status` was not read and
 * the plan tasks are unknown rather than absent, or the reduced mode withholds
 * the caps, so `budget show` was not read.
 *
 * It is a closed word rather than a sentence, for the reason
 * `CoordinatorQueueItem.ground` is: the sentence is drawn from this file's own
 * constant at the moment the note is drawn, so no string the model composed
 * reaches the sink.
 */
export type CoordinatorQueueNote = 'noPlans' | 'capsHidden'

/** CoordinatorQueuePart is one source's part of the queue: the items it holds,
 * how many were left off by the console's own cap, and -- where a read that part
 * would need was not made -- the console's own note about that gap. `null` is a
 * part whose read has not answered yet, which the entry draws as a wait. */
export interface CoordinatorQueuePart {
  readonly items: readonly CoordinatorQueueItem[]
  readonly dropped: number
  readonly note: CoordinatorQueueNote | null
}

/** CoordinatorQueueParts is the queue: one part per read, in the order they are
 * drawn, each independently absent while its read is out. */
export interface CoordinatorQueueParts {
  readonly document: CoordinatorQueuePart | null
  readonly runs: CoordinatorQueuePart | null
  readonly plans: CoordinatorQueuePart | null
  readonly budget: CoordinatorQueuePart | null
}

/**
 * CoordinatorRunRow is one row of `runs ls --json` the queue draws a job for: a
 * row whose run was halted by a person, or one that is terminal and did not
 * succeed (D810). `halted` is the first of the two grounds and is kept apart
 * from `status` because a halt is somebody's own act on a run that then ended
 * however it ended -- a halted run that reports `succeeded` is still a run that
 * stopped for a person.
 */
export interface CoordinatorRunRow {
  readonly repo: string
  readonly run: string
  readonly status: string
  readonly title: string
  readonly planTask: string
  readonly halted: boolean
}

/**
 * CoordinatorJobRow is one row of the job list, off `runview.Row`'s own fields
 * and nothing else: the collapsed row draws the id, the repository, the status
 * and the title, and the expanded body draws the rest -- which is what
 * "expandable" buys, and why the reader keeps fields no collapsed row draws.
 *
 * Every string here is a document's, so every one of them is drawn through
 * `putText` in the class its wire key declares: a job's title and reason are
 * written by whoever wrote the task, a repository is a repository, and a branch
 * is a branch. `cost` is a reading and not a number, so a row whose cost the
 * document did not carry draws nothing rather than `$0.00`.
 */
export interface CoordinatorJobRow {
  readonly run: string
  readonly repo: string
  readonly status: string
  readonly stage: string
  readonly outcome: string
  readonly title: string
  readonly branch: string
  readonly reason: string
  readonly created: string
  readonly planTask: string
  readonly launchedBy: string
  readonly pr: number | null
  readonly cost: Reading | null
  readonly changed: readonly string[]
  readonly changedTotal: number | null
  readonly changedTruncated: boolean
}

/** CoordinatorJobsWindow is the head's two figures, and they are **the window's**
 * -- the first page's own `totals`, summed by the CLI over everything it read
 * rather than over the rows it printed. Both are readings, so neither can reach
 * the screen as a bare number, and both are absent together: a head with one
 * figure and not the other would be claiming half a window. */
export interface CoordinatorJobsWindow {
  readonly runs: Reading
  readonly spent: Reading
}

/** CoordinatorJobs is one page of the job list: the rows this page printed, the
 * window's figures off its `totals`, and the cursor the page after it is asked
 * with (the last row's own id, and `''` when the page named none). */
export interface CoordinatorJobs {
  readonly rows: readonly CoordinatorJobRow[]
  readonly window: CoordinatorJobsWindow | null
  readonly cursor: string
}

/** CoordinatorBlindSpot is the line under the queue: the console's own words for
 * what it cannot reach, and the two counts, each a reading so neither can be
 * drawn as a bare figure (D813). */
export interface CoordinatorBlindSpot {
  readonly stale: Reading
  readonly unread: Reading
}

/**
 * CoordinatorTotals is the fleet strip. Every figure is a count **off a
 * document** and never a sum over the rows this page happens to have drawn, and
 * each is absent (`null`) rather than zero while the read it comes from has not
 * answered -- a strip of zeroes is a claim about a fleet nobody read (D819).
 *
 * The strip is only drawn at all with the stored view in hand: the three figures
 * at its head are that document's, and the money beside them would be a fleet
 * total of a fleet the console cannot count.
 */
export interface CoordinatorTotals {
  readonly repos: Reading
  readonly inFlight: Reading
  readonly collisions: Reading
  readonly plans: Reading | null
  readonly counted: Reading | null
  readonly cap: Reading | null
}

/** CoordinatorModel is what the screen draws. `repos` is the body's summary and
 * is `null` while the stored view has not answered -- the queue and the totals
 * are drawn from reads of their own either way. */
export interface CoordinatorModel {
  readonly source: string
  readonly queue: CoordinatorQueueParts
  /** the console's own count of the items it drew, drawn once the three reads
   * the tab always makes have answered; `null` is "not known", which is drawn as
   * nothing */
  readonly queueCount: Reading | null
  /** whether that count stands over the **whole** queue, the jobs read among
   * them. The count is a floor until it does, and the sentence for a queue that
   * holds nothing is drawn only over a queue that does: the jobs are read when
   * the operator asks for them (rule 5), so a queue that has not been asked for
   * is a queue the console may not call empty */
  readonly queueWhole: boolean
  readonly blind: CoordinatorBlindSpot | null
  readonly totals: CoordinatorTotals | null
  readonly repos: readonly CoordinatorRepoPanel[] | null
}

/**
 * CoordinatorInputs is what the entry point hands the model: the four documents
 * the screen draws, in the shape each read answered with. A read that has not
 * answered is `null`, and `checkout`/`capsHidden` are the two conditions under
 * which a read is not made at all -- the installation records no checkout (the
 * `plan status` read is scoped to one), or the reduced mode withholds the caps.
 */
export interface CoordinatorInputs {
  readonly view: CoordinatorStatusView | null
  readonly runs: readonly CoordinatorRunRow[] | null
  readonly plans: PlanStatusView | null
  readonly caps: SpendCaps | null
  readonly checkout: boolean
  readonly capsHidden: boolean
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

/**
 * queueRuns reads the jobs out of a `runs ls --json` document: one row per run
 * that ended without succeeding, or that a person halted -- the two grounds D810
 * names. It is the same shape of reader as `rigor.ts`'s `rigorIndex`, and for
 * the same reason: the document is handed in as it arrived and this is the one
 * place a row becomes the console's own.
 *
 * **The CLI's order is kept.** `runs ls` answers newest first, and a queue that
 * re-sorted it would be a second opinion about which run is the newest -- the
 * order is the document's, and the cap below cuts the oldest off rather than
 * reordering anything. A row that names no run is dropped: there is no id to
 * draw and none to open.
 *
 * It reads the **whole** document, so the caller can hand the rows to the model
 * and to nothing else: the queue is not the job list (spec section 2.6), and no
 * row this function drops reaches any drawing.
 */
export function queueRuns(document: unknown): readonly CoordinatorRunRow[] {
  const rows = objectAt(document)?.['runs']
  if (!Array.isArray(rows)) return []
  const waiting: CoordinatorRunRow[] = []
  for (const row of rows) {
    const record = objectAt(row)
    if (record === null) continue
    const halted = objectAt(record['halt']) !== null
    const status = textAt(record['status']) ?? ''
    if (!halted && !(record['terminal'] === true && status !== 'succeeded')) continue
    const run = textAt(record['run']) ?? textAt(record['run_id']) ?? ''
    if (run === '') continue
    waiting.push({
      repo: textAt(record['repo']) ?? '',
      run,
      status,
      title: textAt(record['title']) ?? '',
      planTask: textAt(record['plan_task']) ?? '',
      halted,
    })
  }
  return waiting
}

/**
 * jobsDocument reads one page of `runs ls --json --since 24h --limit=50` into the
 * console's own shape: the rows, the window's own `totals`, and the cursor the
 * next page is asked with.
 *
 * **It is the second reader of the same document** and it reads it differently
 * from `queueRuns`, on purpose. The queue asks "which of these is waiting for a
 * person" and drops everything else; the job list asks "what does this page
 * hold" and keeps every row the CLI printed, in the CLI's order. The two are
 * different questions about one document, which is why the queue's reader is not
 * widened to answer both: a queue that stopped dropping succeeded runs would
 * draw a hundred jobs nobody is waiting on.
 *
 * The cursor is **the last row's own id**, which is what `--after` takes: the CLI
 * pages on `run`/`run_id`, so the console reads the id back out of the row it
 * drew rather than computing one. A page whose last row names no id has no
 * cursor, and the Older control is not offered for it -- paging without one would
 * ask for the same page again.
 *
 * `totals` are the window's, not the page's: since #1545 the CLI cuts what it
 * prints and sums what it read, so `runs` here may be far above the rows beside
 * it. That is the figure the head draws, and the one a screen that summed its own
 * rows would get wrong.
 */
export function jobsDocument(document: unknown): CoordinatorJobs {
  const root = objectAt(document)
  const raw = root?.['runs']
  const rows: CoordinatorJobRow[] = []
  if (Array.isArray(raw)) {
    for (const entry of raw) {
      const record = objectAt(entry)
      if (record === null) continue
      const run = textAt(record['run']) ?? textAt(record['run_id']) ?? ''
      // A row that names no run is dropped for the queue's reason: there is no id
      // to draw, to expand or to page from.
      if (run === '') continue
      const files = objectAt(record['changed_files'])
      const changed = files?.['paths']
      const cost = objectAt(record['cost'])?.['total_usd']
      rows.push({
        run,
        repo: textAt(record['repo']) ?? '',
        status: textAt(record['status']) ?? '',
        stage: textAt(record['stage']) ?? '',
        outcome: textAt(record['outcome']) ?? '',
        title: textAt(record['title']) ?? '',
        branch: textAt(record['branch']) ?? '',
        reason: textAt(record['reason']) ?? '',
        created: textAt(record['created']) ?? '',
        planTask: textAt(record['plan_task']) ?? '',
        launchedBy: textAt(record['launched_by']) ?? '',
        pr: typeof record['pr'] === 'number' ? record['pr'] : null,
        cost: typeof cost === 'number' && Number.isFinite(cost) ? measured(cost) : null,
        // `changed_files` is `runstore.ChangedFiles`, already bounded by the
        // store: what the row draws is the set it was given, and the figure
        // beside it is the document's own `total` -- a count this screen
        // computed would be a second opinion about how many paths a run changed.
        changed: Array.isArray(changed)
          ? changed.filter((one): one is string => typeof one === 'string')
          : [],
        changedTotal:
          files !== null && typeof files['total'] === 'number' ? files['total'] : null,
        changedTruncated: files !== null && files['truncated'] === true,
      })
    }
  }
  const totals = objectAt(root?.['totals'])
  const runs = totals === null ? null : totals['runs']
  const spent = totals === null ? null : totals['total_usd']
  const window =
    typeof runs === 'number' && Number.isFinite(runs) && typeof spent === 'number' && Number.isFinite(spent)
      ? { runs: measured(runs), spent: measured(spent) }
      : null
  const last = rows[rows.length - 1]
  return { rows, window, cursor: last === undefined ? '' : last.run }
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

/**
 * PULL_PROVIDERS is the hosts this console has a pull request route for, in
 * `src/main/window.ts`'s own order (`PULL_REQUEST_ROUTES`, D815).
 *
 * The list is written here as well because the renderer cannot ask main before
 * it draws, and what it decides is only **whether a number is a button**: the
 * destination is still built by main, from the triple, in main's own alphabet.
 * The two lists agreeing is therefore not a security property -- a provider this
 * screen lists and main does not is a press that opens nothing -- but a
 * provider this screen refuses is a number drawn as text, which is the safe
 * direction to be wrong in.
 *
 * It is exported for the one case that holds it equal to main's
 * (`coordinator.test.ts`, K2): a copy is only safe while it is still a copy, and
 * drift here is silent in both directions -- text nobody can press, or a button
 * that opens nothing.
 */
export const PULL_PROVIDERS: readonly string[] = Object.freeze(['github', 'bitbucket'])

/** PULL_REPO is main's own `PULL_REQUEST_REPO_RE`, character for character:
 * exactly the two segments a repository has, each beginning with a letter or a
 * digit. It is stricter than the `repository` kind the IPC schema accepts, and
 * the strictness is the point -- a value that is already a path or one that
 * walks is refused here rather than pasted into a URL later.
 *
 * Main does not export its pattern, so the claim above is held by a test rather
 * than by a reader's eye: `coordinator.test.ts` reads both literals out of the
 * two sources and compares them (K2). */
const PULL_REPO = /^[A-Za-z0-9][A-Za-z0-9._-]*\/[A-Za-z0-9][A-Za-z0-9._-]*$/

/**
 * asciiLower folds A–Z and nothing else, which is the fold the CLI compares
 * repository names with (`task.ASCIILower`, behind `CanonicalRepo`).
 * `toLowerCase` is not it: it turns a Kelvin sign into an ASCII "k" and "İ" into
 * "i" plus a combining dot, so a console that folded with it would call two
 * repositories the same that Fugaro keeps apart.
 */
export function asciiLower(text: string): string {
  let folded = ''
  for (let index = 0; index < text.length; index += 1) {
    const code = text.charCodeAt(index)
    folded += code >= 65 && code <= 90 ? String.fromCharCode(code + 32) : text[index]
  }
  return folded
}

/**
 * pullLink is the triple a number is opened with, or `null` when this console
 * will not build one -- and a `null` is drawn as text, never as a button that
 * would do nothing.
 *
 * Four things have to hold, and the first three are main's own builder's: a
 * provider it has a route for, a repository that is `owner/name` in the host's
 * alphabet, and a whole positive number. The fourth is this screen's, and it is
 * what keeps the button and the row saying the same thing: the repository must
 * be one the **`repository` text class draws**, because a number whose cell
 * shows `n/a` must not be a control that opens something -- the press would act
 * on a string the reader was never shown. `sanitizeText` is the class itself
 * rather than a second copy of its rules, so a value it refuses (`..` inside a
 * segment, or one past `MAX_REPOSITORY_LENGTH`) is text here too.
 */
function pullLink(provider: string, repo: string, number: number): CoordinatorPullLink | null {
  if (!PULL_PROVIDERS.includes(provider)) return null
  if (!PULL_REPO.test(repo)) return null
  if (!Number.isSafeInteger(number) || number < 1) return null
  if (!sanitizeText(repo, 'repository', 'repos[].repo').ok) return null
  return { provider, repo: asciiLower(repo), number }
}

/** pullName is the button's accessible name: the verb, the number as the row
 * draws it, and the repository it is in. The repository has already been through
 * `pullLink`'s own check, which is the same class the cell beside it draws. */
function pullName(label: string, repo: string): string {
  return `${COORDINATOR_OPEN_PR_NAME_WORD} ${label} in ${repo}`
}

/** pullButton finishes a number's button: a real button -- so it is in the tab
 * order and a press is a press, with no key handler of its own -- carrying the
 * name above and **no destination of any kind**, because the entry point sends
 * the triple to the operation and main builds the URL. */
function pullButton(element: ScreenElement, link: CoordinatorPullLink, label: string): void {
  element.setAttribute('type', 'button')
  element.setAttribute('aria-label', pullName(label, link.repo))
}

/** cut is one list with its first `COORDINATOR_LIST_CAP` members and how many
 * were left off, so a row can say what it did not draw. */
function cut<T>(items: readonly T[]): { readonly drawn: readonly T[]; readonly dropped: number } {
  return {
    drawn: items.slice(0, COORDINATOR_LIST_CAP),
    dropped: Math.max(0, items.length - COORDINATOR_LIST_CAP),
  }
}

function recordPanel(record: CoordinatorRecord, provider: string, repo: string): CoordinatorRecordPanel {
  const pr = record.pr
  const runs = cut(record.runs)
  return {
    label: recordLabel(record),
    // The record's own number, and never the label: a label that fell back to a
    // run's id names no pull request, so there is no triple to build and the
    // cell stays text.
    link: pr === null ? null : pullLink(provider, repo, pr.number),
    title: pr === null ? '' : pr.title,
    state: pr === null ? '' : pr.state,
    draft: pr !== null && pr.draft,
    branch: record.branch,
    release: record.release,
    inFlight: record.inFlight,
    runs: runs.drawn.map((run) => ({
      id: run.id,
      status: run.status,
      outcome: run.outcome,
      // The owner's ruling of 2026-10-31: the phrase is drawn only when there
      // is somebody to name. `nameableText` is that rule -- empty, a redaction
      // the CLI made, an address with nothing before the `@` and a pseudonym
      // are not names -- and an unnameable value becomes `''` here so `runCell`
      // draws no words at all rather than `declared by @x`.
      declaredBy: nameableText(run.declaredBy) ? run.declaredBy : '',
      inFlight: !run.terminal,
      filesTruncated: run.filesTruncated,
    })),
    runsDropped: runs.dropped,
  }
}

function repoPanel(repo: CoordinatorRepoView): CoordinatorRepoPanel {
  const view = repo.view
  return {
    repo: repo.repo,
    stored: repo.stored,
    stale: repo.stale === null ? null : { since: repo.stale.since, reason: repo.stale.reason },
    records: view.records.map((record) => recordPanel(record, repo.provider, repo.repo)),
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
    hasCollisions: view.collisions.length > 0,
    omitted: view.omitted > 0 ? measured(view.omitted) : null,
    omittedCollisions: view.omittedCollisions > 0 ? measured(view.omittedCollisions) : null,
  }
}

/**
 * documentQueuePart reads the pull requests that are waiting for a person.
 *
 * **All three conditions are the document's own fields** (spec section 2.2):
 * the host says the pull request is open and not a draft, no run behind it is
 * still going, and there is at least one run behind it at all -- a pull request
 * the store holds no run for is one somebody opened by hand, and the queue has
 * no run to offer it. Nothing here guesses at a review, a CI result or a moved
 * base: no document the console reads carries them, so the queue says the pull
 * request is open and stops there.
 */
function documentQueuePart(document: CoordinatorStatusView): CoordinatorQueuePart {
  const items: CoordinatorQueueItem[] = []
  for (const repo of document.repos) {
    for (const record of repo.view.records) {
      const pr = record.pr
      if (pr === null || pr.state !== 'open' || pr.draft) continue
      if (record.inFlight) continue
      const newest = record.runs[0]
      if (newest === undefined) continue
      items.push({
        kind: 'pullRequest',
        subject: recordLabel(record),
        link: pullLink(repo.provider, repo.repo, pr.number),
        scope: record.branch,
        repo: repo.repo,
        title: pr.title,
        ground: null,
        action: { kind: 'openRun', repo: repo.repo, run: newest.id },
      })
    }
  }
  const capped = cut(items)
  return { items: capped.drawn, dropped: capped.dropped, note: null }
}

/** runsQueuePart is the jobs: one item per row `queueRuns` kept, in the CLI's
 * own order. Retry offers the run's own repository and the plan task the launch
 * recorded for it, both as stored -- the console rewrites neither, and a plan
 * task the CLI would refuse is refused by main's own check rather than by a
 * second copy of that rule here. */
function runsQueuePart(rows: readonly CoordinatorRunRow[]): CoordinatorQueuePart {
  const items = rows.map(
    (row): CoordinatorQueueItem => ({
      kind: 'job',
      subject: row.run,
      link: null,
      scope: row.planTask,
      repo: row.repo,
      title: row.title,
      ground: null,
      action: { kind: 'retry', repo: row.repo, planTask: row.planTask },
    }),
  )
  const capped = cut(items)
  return { items: capped.drawn, dropped: capped.dropped, note: null }
}

/**
 * plansQueuePart is the tasks that are ready to launch: `plan status`' own
 * `state == "ready"`, which is the CLI's answer to "no run has joined it and
 * everything it needs is done" -- the console applies no rule of its own here,
 * because a screen that recomputed readiness from the edges could disagree with
 * the CLI about the same task.
 *
 * The plan task a launch is given is the pair the CLI stores, `<plan>/<task>`
 * (`run --plan-task`'s own help, and `planIDFromPath` for the plan's half): the
 * item id alone is a value main refuses, and a queue whose Launch could never
 * open a window would be a button that only looked like one. Both halves are the
 * document's -- the plan's own `id` and the task's own `id` -- and the join is
 * the flag's spelling and not a new fact.
 */
function plansQueuePart(document: PlanStatusView): CoordinatorQueuePart {
  const items: CoordinatorQueueItem[] = []
  for (const plan of document.plans) {
    for (const node of plan.nodes) {
      if (node.state !== 'ready') continue
      items.push({
        kind: 'plan',
        subject: node.id,
        link: null,
        scope: plan.id,
        repo: document.repo,
        title: node.title,
        ground: null,
        action: { kind: 'launch', repo: document.repo, planTask: `${plan.id}/${node.id}` },
      })
    }
  }
  const capped = cut(items)
  return { items: capped.drawn, dropped: capped.dropped, note: null }
}

/**
 * budgetQueuePart is the alerts `budget show --json` carries. The fleet's kill
 * switch is one item and stands for the fleet: with it on, every repository's
 * own state is a fact nobody can act on until the fleet is resumed, and a queue
 * that listed a repository at its cap underneath a fleet that refuses
 * everything would be drawing two cases of one blockage.
 *
 * Without it, each repository the document reports as killed, or as at its cap,
 * is an item of its own. `capsAtCap` is Spend's own reading of that row -- the
 * headroom the document reports, or the counted dollars against the cap -- so
 * the two screens cannot disagree about the same repository.
 *
 * The one act is a navigation to Spend (D812). Nothing here calls `budget kill`
 * or `budget resume`: those are the CLI's, behind a typed confirmation, and they
 * are on the never-reachable list.
 */
function budgetQueuePart(caps: SpendCaps): CoordinatorQueuePart {
  const global = caps.global
  // A document with no `global` block is a read that answered with no fleet
  // figures -- not a read that is still out: the part is drawn, and it holds
  // nothing rather than wearing a wait it is not in.
  if (global === null) return { items: [], dropped: 0, note: null }
  if (global.kill !== null) {
    return {
      items: [
        {
          kind: 'budget',
          subject: '',
          link: null,
          scope: '',
          repo: '',
          title: '',
          ground: 'kill',
          action: { kind: 'spend' },
        },
      ],
      dropped: 0,
      note: null,
    }
  }
  const items: CoordinatorQueueItem[] = []
  for (const row of caps.repos) {
    const killed = row.kill !== null
    if (!killed && !capsAtCap(row)) continue
    items.push({
      kind: 'budget',
      subject: row.repo,
      link: null,
      scope: '',
      repo: '',
      title: '',
      ground: killed ? 'repoKill' : 'cap',
      action: { kind: 'spend' },
    })
  }
  const capped = cut(items)
  return { items: capped.drawn, dropped: capped.dropped, note: null }
}

/** blindSpotOf counts the two things this page cannot reach, off the stored
 * view: the repositories the sweep marked stale, and the pull requests a sweep
 * could not read. It is `null` when there is nothing to say -- with no document,
 * and with both counts at zero, the line would be a line about nothing. */
function blindSpotOf(document: CoordinatorStatusView): CoordinatorBlindSpot | null {
  let stale = 0
  let unread = 0
  for (const repo of document.repos) {
    if (repo.stale !== null) stale += 1
    unread += repo.view.unread.length
  }
  if (stale === 0 && unread === 0) return null
  return {
    stale: measured(stale),
    unread: measured(unread),
  }
}

/**
 * buildCoordinatorModel turns the decoded documents into the panels the screen
 * draws. Every string it composes itself goes to the screen through `literal`
 * (module constants, and the record labels and numbers the console made); every
 * string the *document* carried goes through `putText`, with the class its wire
 * key declares.
 *
 * **The queue's count is the console's own.** It counts the items this page drew
 * -- not what the documents hold: the queue is what a person can act on here and
 * now -- and it is `null` unless the three reads the tab always makes have
 * answered, because a count over two parts of a queue is a figure the operator
 * has no way to check. Where the plans or the caps are not read at all
 * (`checkout`, `capsHidden`), the head stays empty and the part says why.
 *
 * The jobs are the fourth read and the one that is **not** made on entry (rule 5
 * of §6.2's opening), so the count stands over the parts the console has read
 * until the operator asks for them: it is a floor, and the jobs control in the
 * queue's own body names the read it leaves out. `queueWhole` says whether the
 * count covers everything, and it is what the head's "nothing is waiting" is
 * held to -- a queue whose jobs have not been asked for may not be called empty.
 */
export function buildCoordinatorModel(inputs: CoordinatorInputs): CoordinatorModel {
  const document = inputs.view
  const plans = inputs.plans
  const caps = inputs.caps
  const queue: CoordinatorQueueParts = {
    document: document === null ? null : documentQueuePart(document),
    runs: inputs.runs === null ? null : runsQueuePart(inputs.runs),
    plans:
      plans === null
        ? inputs.checkout
          ? null
          : { items: [], dropped: 0, note: 'noPlans' }
        : plansQueuePart(plans),
    budget:
      caps === null
        ? inputs.capsHidden
          ? { items: [], dropped: 0, note: 'capsHidden' }
          : null
        : budgetQueuePart(caps),
  }
  const counted = document !== null && plans !== null && caps !== null
  const whole = counted && inputs.runs !== null
  const drawn = (['document', 'runs', 'plans', 'budget'] as const).reduce(
    (total, source) => total + (queue[source]?.items.length ?? 0),
    0,
  )
  return {
    source: COORDINATOR_SOURCE_LINE,
    queue,
    queueCount: counted ? measured(drawn) : null,
    queueWhole: whole,
    blind: document === null ? null : blindSpotOf(document),
    totals:
      document === null
        ? null
        : {
            repos: measured(document.repos.length),
            inFlight: measured(
              document.repos.reduce(
                (total, repo) => total + repo.view.records.filter((record) => record.inFlight).length,
                0,
              ),
            ),
            collisions: measured(
              document.repos.reduce((total, repo) => total + repo.view.collisions.length, 0),
            ),
            plans: plans === null ? null : plans.totals.plans,
            counted: caps?.global?.counted ?? null,
            cap: caps?.global?.dailyCap ?? null,
          },
    repos: document === null ? null : document.repos.map((repo) => repoPanel(repo)),
  }
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
 * goes through `literal`. `unit` is the reading's, and it is a parameter rather
 * than a second helper because the cell is the same cell: the fleet strip draws
 * a count and a sum of dollars through this one shape, and a `usdCell` beside it
 * would be a second place this markup is written -- and two cells that can drift
 * apart about what a figure looks like. */
function numberCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  word: string,
  reading: Reading,
  unit: Unit,
  ...classes: string[]
): ScreenElement {
  const element = cell(parent, factory, 'span', 'coordinator-count', ...classes)
  literal(cell(element, factory, 'span', 'coordinator-count-word'), word)
  putReading(cell(element, factory, 'span', 'coordinator-count-value'), reading, unit)
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

// ---------------------------------------------------------------------------
// The action queue and the fleet totals (K1).
// ---------------------------------------------------------------------------

/** QUEUE_SOURCES is the queue's four reads, in the order the queue draws their
 * items: the stored view's pull requests, `runs ls`'s jobs, `plan status`'s
 * ready tasks and `budget show`'s alerts. The entry iterates it, and the model
 * keys its parts by it. */
export const QUEUE_SOURCES: readonly CoordinatorQueueSource[] = Object.freeze([
  'document',
  'runs',
  'plans',
  'budget',
])

/** TOTALS_SOURCES is the three reads the fleet strip's figures come from. The
 * run list is not one of them: **this page draws no figure from `runs ls`**
 * (D819), and a strip that waited for a read it does not use would be waiting
 * for nothing. */
export const TOTALS_SOURCES: readonly CoordinatorQueueSource[] = Object.freeze([
  'document',
  'plans',
  'budget',
])

/** QueueText is one cell of an item: the class the sanitizer draws its value in
 * (`TEXT_CLASSES`) and the wire path a refusal about it would name. Neither is a
 * value -- the values go through `putText` -- so the table holds no string a
 * document chose. */
interface QueueText {
  readonly textClass: TextClass
  readonly field: string
}

/** QueueCells is one kind of item's four document cells, in the order the item
 * draws them. */
interface QueueCells {
  readonly subject: QueueText
  readonly scope: QueueText
  readonly repo: QueueText
  readonly title: QueueText
}

/**
 * QUEUE_CELLS says how each kind's document values are drawn, one row per kind.
 *
 * The subject names the thing the item is about, the scope where it sits, the
 * repository it is in and the title whoever wrote it wrote -- and each is the
 * wire key it came from, so the class and the field are the document's own shape
 * rather than this screen's opinion about it. A pull request's title is the
 * host's text (`records[].pr.title`), a job's is the launch's (`runs[].title`)
 * and a plan task's is the planner's (`plans[].nodes[].title`), which is why the
 * table is per kind and not one class for all four.
 */
const QUEUE_CELLS: Readonly<Record<CoordinatorQueueKind, QueueCells>> = Object.freeze({
  pullRequest: {
    subject: { textClass: 'label', field: 'records[].pr.number' },
    scope: { textClass: 'branch', field: 'records[].branch' },
    repo: { textClass: 'repository', field: 'repos[].repo' },
    title: { textClass: 'sentence', field: 'records[].pr.title' },
  },
  job: {
    subject: { textClass: 'runId', field: 'runs[].run' },
    scope: { textClass: 'label', field: 'runs[].plan_task' },
    repo: { textClass: 'repository', field: 'runs[].repo' },
    title: { textClass: 'sentence', field: 'runs[].title' },
  },
  plan: {
    subject: { textClass: 'label', field: 'plans[].nodes[].id' },
    scope: { textClass: 'label', field: 'plans[].id' },
    repo: { textClass: 'repository', field: 'repo' },
    title: { textClass: 'sentence', field: 'plans[].nodes[].title' },
  },
  budget: {
    subject: { textClass: 'repository', field: 'repos[].repo' },
    scope: { textClass: 'label', field: 'repos[].repo' },
    repo: { textClass: 'repository', field: 'repos[].repo' },
    title: { textClass: 'sentence', field: 'repos[].repo' },
  },
})

/**
 * queueItemCell draws one item: what it is, what it is about, where that sits,
 * the repository it is in, the title whoever wrote it wrote, why it is waiting,
 * and **one** button.
 *
 * The kind's word, the reason it is waiting and the button's word are this
 * console's own and go through `literal`; every value a document carried goes
 * through `putText` in the class its wire key declares. **The reason is drawn
 * from the item's own `kind` and `ground`** rather than carried as a sentence:
 * the words are then written beside the constants they belong to, and no string
 * the model composed can reach the text sink. A budget alert's ground is one of
 * three -- the fleet's kill switch, a repository's own kill, or a cap reached --
 * because the two are different facts about different things and a reader who is
 * meant to go and resume something is owed the difference (D812).
 */
function queueItemCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  item: CoordinatorQueueItem,
): CoordinatorPullButton | null {
  const row = cell(parent, factory, 'li', 'coordinator-queue-item')
  const kind = cell(row, factory, 'span', 'coordinator-queue-kind')
  if (item.kind === 'pullRequest') literal(kind, COORDINATOR_QUEUE_PR_WORD)
  else if (item.kind === 'job') literal(kind, COORDINATOR_QUEUE_JOB_WORD)
  else if (item.kind === 'plan') literal(kind, COORDINATOR_QUEUE_PLAN_WORD)
  else literal(kind, COORDINATOR_QUEUE_BUDGET_WORD)
  const cells = QUEUE_CELLS[item.kind]
  // The tag is written out here, in the open, rather than hidden behind a
  // helper: `theme-selectors.test.ts` reads the renderer's own emitter calls for
  // the `tag.class` pairs a stylesheet rule may select, and the reset the button
  // needs is selected as `button.coordinator-queue-subject` (§2's licence).
  const subject = cell(row, factory, item.link === null ? 'span' : 'button', 'coordinator-queue-subject')
  // An item with nothing as its subject draws the cell empty and says why in
  // its own words: the fleet's alert is about every repository at once, so it
  // has none -- and `putText` on an empty string would draw the console's
  // not-available mark, which claims a figure could not be read. That is a
  // different fact from "there is no such figure", and a false one.
  if (item.subject === '') literal(subject, '')
  else putText(subject, item.subject, cells.subject.textClass, cells.subject.field)
  // A number the console can form a triple for is a button, and the triple is
  // what the entry point binds -- never a value the entry reads back out of the
  // element. A number it cannot form one for is the text K1 drew.
  const pull = item.link === null ? null : { element: subject, link: item.link }
  if (pull !== null) pullButton(subject, pull.link, item.subject)
  if (item.scope !== '') {
    textCell(row, factory, 'span', item.scope, cells.scope.textClass, cells.scope.field, 'coordinator-queue-scope')
  }
  if (item.repo !== '') {
    textCell(row, factory, 'span', item.repo, cells.repo.textClass, cells.repo.field, 'coordinator-queue-repo')
  }
  if (item.title !== '') {
    textCell(row, factory, 'span', item.title, cells.title.textClass, cells.title.field, 'coordinator-queue-title')
  }
  const why = cell(row, factory, 'span', 'coordinator-queue-why', 'sentence')
  if (item.kind === 'pullRequest') literal(why, COORDINATOR_QUEUE_PR_WHY_WORD)
  else if (item.kind === 'job') literal(why, COORDINATOR_QUEUE_JOB_WHY_WORD)
  else if (item.kind === 'plan') literal(why, COORDINATOR_QUEUE_PLAN_WHY_WORD)
  else if (item.ground === 'kill') literal(why, COORDINATOR_QUEUE_KILL_WORD)
  else if (item.ground === 'repoKill') literal(why, COORDINATOR_QUEUE_REPO_KILL_WORD)
  else literal(why, COORDINATOR_QUEUE_CAP_WORD)
  // The item's own button -- Open run, Retry, Launch or Open Spend -- which is
  // the act the item is *for*. The item's number is a second thing a person can
  // press (K2) and is drawn above as the number itself: this button acts on the
  // item, and that one opens the thing the item is about.
  const button = cell(row, factory, 'button', 'coordinator-queue-button')
  button.setAttribute('type', 'button')
  if (item.action.kind === 'openRun') literal(button, COORDINATOR_OPEN_RUN_WORD)
  else if (item.action.kind === 'retry') literal(button, COORDINATOR_RETRY_WORD)
  else if (item.action.kind === 'launch') literal(button, COORDINATOR_LAUNCH_WORD)
  else literal(button, COORDINATOR_OPEN_SPEND_WORD)
  return pull
}

/**
 * fillCoordinatorQueuePart draws one source's part of the queue into the host
 * the frame drew for it: its items, how many the console's cap left off, or the
 * note saying the read behind it was not made at all.
 *
 * It is the function the entry calls **when that part's own read answers**, and
 * it decides nothing about waiting: a part whose read is still out is `null` in
 * the model and is drawn as nothing at all, under the section's own busy mark.
 *
 * It answers the numbers it drew as buttons, in the order it drew them, because
 * the press is the entry's to bind: the screen holds no bridge, and a pair is
 * what keeps the element and the triple together without a class lookup or a
 * value written into the DOM.
 */
export function fillCoordinatorQueuePart(
  factory: ScreenFactory,
  model: CoordinatorModel,
  source: CoordinatorQueueSource,
  host: ScreenElement,
): readonly CoordinatorPullButton[] {
  const pulls: CoordinatorPullButton[] = []
  const part = model.queue[source]
  if (part === null) return pulls
  if (part.note === 'noPlans') {
    literal(cell(host, factory, 'p', 'coordinator-queue-note'), COORDINATOR_QUEUE_NO_PLANS_WORD)
    return pulls
  }
  if (part.note === 'capsHidden') {
    literal(cell(host, factory, 'p', 'coordinator-queue-note'), COORDINATOR_QUEUE_CAPS_HIDDEN_WORD)
    return pulls
  }
  if (part.items.length === 0) return pulls
  const list = cell(host, factory, 'ul', 'coordinator-queue-items')
  for (const item of part.items) {
    const pull = queueItemCell(list, factory, item)
    if (pull !== null) pulls.push(pull)
  }
  if (part.dropped > 0) {
    // A list holds list items, so the count is one of them: the console's own
    // figure for what it did not draw, said rather than dropped.
    putReading(cell(list, factory, 'li', 'coordinator-more'), measured(part.dropped), 'count')
  }
  return pulls
}

/**
 * fillCoordinatorHead writes the queue's own count: **the console's own count of
 * the items it drew**, with the words beside it.
 *
 * A count of `0` is drawn as nothing rather than as a figure, because the
 * sentence for an empty queue (`nothing is waiting for you`) is what says it: a
 * `0` and an empty sentence say the same thing twice, and the `0` would be the
 * one a reader mistakes for a figure off a document. The count is absent
 * altogether while the reads the tab makes are still out or refused.
 *
 * **The sentence is drawn over a whole queue and never over a partial one**:
 * `nothing is waiting for you` under a part that says `runs.list` refused -- or
 * under the jobs control, whose read the console has deliberately not made --
 * would be the page claiming a queue it has not read is a queue that holds
 * nothing, and the two are different facts. The count is the looser one, because
 * it is a count of what the console has drawn and the control beside it names
 * what it has not.
 */
export function fillCoordinatorHead(model: CoordinatorModel, frame: DrawnCoordinatorFrame): void {
  const count = model.queueCount
  const zero = count !== null && (count.value ?? 0) <= 0
  if (count === null || zero) literal(frame.queueCount, '')
  else putReading(frame.queueCount, count, 'count')
  if (zero && model.queueWhole) literal(frame.queueEmpty, COORDINATOR_QUEUE_EMPTY_WORD)
  else literal(frame.queueEmpty, '')
}

/**
 * coordinatorJobsControl draws the queue's jobs control: the one press that
 * makes the fourth read. It is drawn **in the part the jobs fill**, so the
 * control and the items it brings are in the same place, and it is drawn by the
 * entry and not by the frame because whether it is there at all is a fact about
 * this visit -- the jobs are read when the operator asks, and a frame drawn
 * again after a Refresh that already asked draws the part and not the control.
 *
 * It is a button and not a link, and it carries no `aria-controls`: the part it
 * fills is the element it sits in, and an `id` for the relationship would be an
 * id on a host the frame draws four of. `expanded` is the state the entry knows
 * and this does not: the control is drawn while the jobs have no answer, which is
 * both before the press and while the read the press made is out, and the two
 * are told apart by the attribute rather than by a control that disappears.
 */
export function coordinatorJobsControl(factory: ScreenFactory, expanded: boolean): ScreenElement {
  const button = make(factory, 'button', COORDINATOR_QUEUE_JOBS_CLASS)
  button.setAttribute('type', 'button')
  button.setAttribute('aria-expanded', expanded ? 'true' : 'false')
  literal(button, COORDINATOR_QUEUE_JOBS_WORD)
  return button
}

/**
 * fillCoordinatorBlind draws the line under the queue: what this page cannot
 * reach, with **no button** (D813). The sweep is a scheduled job and the
 * unread pull requests are a fact about the sweep, so neither is something this
 * console may act on -- and where there is no such fact the line is not drawn at
 * all, because a line of two zeroes is a line about nothing.
 */
export function fillCoordinatorBlind(
  factory: ScreenFactory,
  model: CoordinatorModel,
  host: ScreenElement,
): void {
  const blind = model.blind
  if (blind === null) return
  const line = cell(host, factory, 'p', 'coordinator-blind')
  literal(cell(line, factory, 'span', 'coordinator-blind-word'), COORDINATOR_BLIND_WORD)
  numberCell(line, factory, COORDINATOR_BLIND_STALE_WORD, blind.stale, 'count', 'coordinator-blind-stale')
  numberCell(line, factory, COORDINATOR_BLIND_UNREAD_WORD, blind.unread, 'count', 'coordinator-blind-unread')
}

/**
 * fillCoordinatorTotals draws the fleet's strip: what the four documents the
 * page read say about the fleet as a whole.
 *
 * Every figure is a count the console computed from an array a document carried
 * or a reading that document held, and **the money is `budget show`'s own** --
 * the same readings the Spend screen draws, so the two screens cannot disagree
 * about a fleet (D819). The strip is drawn only when the stored view answered:
 * the three counts at its head are that document's, and a strip of money without
 * them would be dollars about a fleet the console cannot count.
 */
export function fillCoordinatorTotals(
  factory: ScreenFactory,
  model: CoordinatorModel,
  host: ScreenElement,
): void {
  const totals = model.totals
  if (totals === null) return
  numberCell(host, factory, COORDINATOR_TOTAL_REPOS_WORD, totals.repos, 'count', 'coordinator-total-repos')
  numberCell(
    host,
    factory,
    COORDINATOR_TOTAL_IN_FLIGHT_WORD,
    totals.inFlight,
    'count',
    'coordinator-total-inflight',
  )
  numberCell(
    host,
    factory,
    COORDINATOR_TOTAL_COLLISIONS_WORD,
    totals.collisions,
    'count',
    'coordinator-total-collisions',
  )
  if (totals.plans !== null) {
    numberCell(host, factory, COORDINATOR_TOTAL_PLANS_WORD, totals.plans, 'count', 'coordinator-total-plans')
  }
  if (totals.counted !== null) {
    numberCell(host, factory, COORDINATOR_TOTAL_COUNTED_WORD, totals.counted, 'usd', 'coordinator-total-counted')
  }
  if (totals.cap !== null) {
    numberCell(host, factory, COORDINATOR_TOTAL_CAP_WORD, totals.cap, 'usd', 'coordinator-total-cap')
  }
}

// ---------------------------------------------------------------------------
// The job list (K4).
// ---------------------------------------------------------------------------

/**
 * coordinatorJobsControl is the section's own control, in one of its two words:
 * the button that asks for the list at all, and the button that asks for the page
 * after it. Both carry `aria-expanded` for what the press fills, the way the
 * queue's jobs control and the launch control do -- the two states are "the list
 * is on screen" and "the page after the one on screen has been asked for", and
 * `expanded` is whichever the caller means.
 *
 * They are buttons and never anchors, and neither carries a value from a
 * document: the cursor the Older control pages with is held by the entry as the
 * last row it drew, so nothing a document wrote reaches an attribute.
 */
export function coordinatorLoadJobsControl(factory: ScreenFactory, expanded: boolean): ScreenElement {
  return jobsControl(factory, COORDINATOR_JOBS_LOAD_CLASS, COORDINATOR_JOBS_LOAD_WORD, expanded)
}

/**
 * coordinatorOlderJobsControl is the other one: the press that asks for the page
 * after the one on screen, with `--after` the last row the console drew. Its
 * `expanded` is true once that page has landed, so a press that is still out says
 * so and a control that has been answered says so too.
 */
export function coordinatorOlderJobsControl(factory: ScreenFactory, expanded: boolean): ScreenElement {
  return jobsControl(factory, COORDINATOR_JOBS_OLDER_CLASS, COORDINATOR_JOBS_OLDER_WORD, expanded)
}

function jobsControl(
  factory: ScreenFactory,
  className: string,
  label: string,
  expanded: boolean,
): ScreenElement {
  const button = make(factory, 'button', className)
  button.setAttribute('type', 'button')
  button.setAttribute('aria-expanded', expanded ? 'true' : 'false')
  literal(button, label)
  return button
}

/**
 * fillCoordinatorJobsHead draws the window's two figures. It is drawn from the
 * **first** page's `totals` and written once, which is why it is a function of
 * the page rather than of a count the entry keeps: the figure belongs to the
 * read, and a head rewritten on every page would be a second chance to draw the
 * page's number under a word that says the window.
 *
 * Nothing is drawn when the document carried no `totals`: a window the console
 * has not read is not a window of zero runs.
 */
export function fillCoordinatorJobsHead(
  factory: ScreenFactory,
  window: CoordinatorJobsWindow | null,
  head: ScreenElement,
): void {
  if (window === null) return
  literal(cell(head, factory, 'span', 'coordinator-jobs-window-word'), COORDINATOR_JOBS_WINDOW_WORD)
  numberCell(head, factory, COORDINATOR_JOBS_RUNS_WORD, window.runs, 'count', 'coordinator-jobs-window-runs')
  numberCell(head, factory, COORDINATOR_JOBS_SPENT_WORD, window.spent, 'usd', 'coordinator-jobs-window-spent')
}

/**
 * jobCell draws one row: the collapsed line -- the run's id, the repository, the
 * status and the untrusted title -- and, under it, the body the operator expands.
 *
 * **The body is drawn here and hidden, rather than drawn on the press.** Every
 * string in it is a document's, so drawing it late would be a second place this
 * screen reads a document -- and the row's own press would then be a rendering
 * that could fail. It is `hidden` until the press, which is also what makes the
 * expanded state a fact about the element a test can read.
 *
 * The row draws **no button that opens anything**: the job list is a list, and
 * the console's one write is the launch confirmation (rule 11). A run's id is
 * text here, exactly as it is in the queue.
 *
 * The head itself is a **real `button`**, and not a `div` carrying `role`: it
 * is the disclosure the row's press opens, and a `div` would need a `tabindex`
 * and a key handler of its own before Enter and Space did what a click does --
 * a control that looks pressable to a mouse and is not to a keyboard. The
 * reset in `base.css` is what makes a button with a line of spans in it read
 * as the line does.
 */
function jobCell(parent: ScreenElement, factory: ScreenFactory, row: CoordinatorJobRow): ScreenElement {
  const item = cell(parent, factory, 'li', 'coordinator-job')
  const head = cell(item, factory, 'button', 'coordinator-job-head')
  head.setAttribute('type', 'button')
  textCell(head, factory, 'span', row.run, 'runId', 'runs[].run_id', 'coordinator-job-id')
  if (row.repo !== '') {
    textCell(head, factory, 'span', row.repo, 'repository', 'runs[].repo', 'coordinator-job-repo')
  }
  if (row.status !== '') {
    textCell(head, factory, 'span', row.status, 'label', 'runs[].status', 'coordinator-job-status')
  }
  if (row.outcome !== '') {
    textCell(head, factory, 'span', row.outcome, 'label', 'runs[].outcome', 'coordinator-job-outcome')
  }
  if (row.title !== '') {
    textCell(head, factory, 'span', row.title, 'sentence', 'runs[].title', 'coordinator-job-title')
  }
  const body = cell(item, factory, 'div', COORDINATOR_JOB_DETAIL_CLASS)
  body.setAttribute('hidden', '')
  if (row.stage !== '') {
    textCell(body, factory, 'span', row.stage, 'label', 'runs[].stage', 'coordinator-job-stage')
  }
  if (row.branch !== '') {
    textCell(body, factory, 'span', row.branch, 'branch', 'runs[].branch', 'coordinator-job-branch')
  }
  if (row.planTask !== '') {
    textCell(body, factory, 'span', row.planTask, 'label', 'runs[].plan_task', 'coordinator-job-plan')
  }
  if (row.launchedBy !== '') {
    // The declaration and not an identity, drawn in the person class its wire key
    // declares -- the same rule the queue's runs follow.
    textCell(body, factory, 'span', row.launchedBy, 'person', 'runs[].launched_by', 'coordinator-job-by')
  }
  if (row.created !== '') {
    textCell(body, factory, 'span', row.created, 'sentence', 'runs[].created', 'coordinator-job-created')
  }
  if (row.pr !== null) {
    textCell(body, factory, 'span', String(row.pr), 'label', 'runs[].pr', 'coordinator-job-pr')
  }
  if (row.cost !== null) {
    numberCell(body, factory, COORDINATOR_JOBS_SPENT_WORD, row.cost, 'usd', 'coordinator-job-cost')
  }
  if (row.reason !== '') {
    textCell(body, factory, 'span', row.reason, 'sentence', 'runs[].reason', 'coordinator-job-reason')
  }
  if (row.changed.length > 0 || row.changedTruncated) {
    const paths = row.changed.slice(0, COORDINATOR_LIST_CAP)
    for (const path of paths) {
      textCell(body, factory, 'span', path, 'path', 'changed_files.paths', 'coordinator-job-file')
    }
    // The count left off is the set's own `total`, which the store wrote: the
    // screen does not count a set it did not read. `truncated` without a total
    // draws the console's word for a short set and no figure at all.
    const dropped = row.changedTotal === null ? null : Math.max(0, row.changedTotal - paths.length)
    if (dropped !== null && dropped > 0) {
      countCell(body, factory, measured(dropped), 'coordinator-job-more')
    } else if (row.changedTruncated) {
      literal(cell(body, factory, 'span', 'coordinator-job-short'), COORDINATOR_TRUNCATED_WORD)
    }
  }
  return item
}

/**
 * fillCoordinatorJobs appends one page's rows to the list. **It appends and never
 * replaces**, which is what keeps a row the operator expanded open when the page
 * after it lands: the rows already drawn are the operator's own view, and the
 * press that asked for more is answered under them.
 */
export function fillCoordinatorJobs(
  factory: ScreenFactory,
  rows: readonly CoordinatorJobRow[],
  list: ScreenElement,
): readonly ScreenElement[] {
  return rows.map((row) => jobCell(list, factory, row))
}

/**
 * fillCoordinatorJobsEmpty draws the console's sentence for a window that holds
 * no run. It is the only thing the list ever says in its own words: every other
 * word in this section is a document's or a control's.
 */
export function fillCoordinatorJobsEmpty(factory: ScreenFactory, list: ScreenElement): void {
  literal(cell(list, factory, 'p', 'coordinator-jobs-empty'), COORDINATOR_JOBS_EMPTY_WORD)
}

/**
 * coordinatorJobsFloorNote draws the console's sentence for a CLI that is too old
 * to page, and names the version the CLI is. The version line is **main's**, read
 * by the handshake, and it is drawn through `putText` in the class a version is:
 * it is text wherever it came from, and a hostile version line is a version line
 * drawn as text rather than an element or an attribute.
 */
export function coordinatorJobsFloorNote(
  factory: ScreenFactory,
  version: string,
): ScreenElement {
  const note = make(factory, 'p', 'coordinator-jobs-note')
  literal(cell(note, factory, 'span', 'coordinator-jobs-floor-word'), COORDINATOR_JOBS_FLOOR_WORD)
  textCell(note, factory, 'span', version, 'label', 'cliVersion', 'coordinator-jobs-floor-version')
  return note
}

/** DrawnCoordinatorJobs is the job list's own section: the section the entry
 * marks, the head the window's figures are written into, the slot its busy mark
 * goes in, and the two hosts the rows and the controls are filled into. */
export interface DrawnCoordinatorJobs {
  readonly section: ScreenElement
  readonly head: ScreenElement
  readonly slot: ScreenElement
  readonly list: ScreenElement
  readonly controls: ScreenElement
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

/** recordCell draws one pull request as a row: a head line carrying the number,
 * the title and the state of the pull request, and the runs behind it nested
 * under that line. A record with no run keeps the console's own sentence where
 * the list would be, so the row says which of the two it is.
 *
 * The number is the row's **one** control where the console can form a triple
 * for it (K2), and text where it cannot -- the same rule the queue's subject
 * follows, drawn by the other half of this screen. It answers the pair it drew,
 * for the reason `fillCoordinatorQueuePart` does. */
function recordCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  record: CoordinatorRecordPanel,
): CoordinatorPullButton | null {
  const row = cell(parent, factory, 'li', 'coordinator-record')
  const head = cell(row, factory, 'div', 'coordinator-record-head')
  // The tag is written out at the call site for `theme-selectors.test.ts`'s
  // reason, above.
  const label = cell(head, factory, record.link === null ? 'span' : 'button', 'coordinator-record-label')
  putText(label, record.label, 'label', 'records[].pr.number')
  const pull = record.link === null ? null : { element: label, link: record.link }
  if (pull !== null) pullButton(label, pull.link, record.label)
  if (record.title !== '') {
    // The pull request's own words, written by whoever opened it: text, in the
    // class the wire key declares, and never a link (design section 5.1 rule 2).
    textCell(head, factory, 'span', record.title, 'sentence', 'records[].pr.title', 'coordinator-record-title')
  }
  if (record.branch !== '') {
    textCell(head, factory, 'span', record.branch, 'branch', 'records[].branch', 'coordinator-record-branch')
  }
  if (record.release !== '') {
    textCell(head, factory, 'span', record.release, 'label', 'records[].release', 'coordinator-record-release')
  }
  // One chip for what the host says the pull request is: its state, and the
  // console's own word beside it when the host files it as a draft. Both are
  // words, so the chip's tone is never the only thing that says `draft`.
  if (record.state !== '' || record.draft) {
    const chip = cell(head, factory, 'span', 'coordinator-record-chip')
    if (record.state !== '') {
      textCell(chip, factory, 'span', record.state, 'label', 'records[].pr.state', 'coordinator-record-state')
    }
    if (record.draft) {
      literal(cell(chip, factory, 'span', 'coordinator-record-draft'), COORDINATOR_DRAFT_WORD)
    }
  }
  const flight = cell(head, factory, 'span', 'coordinator-record-flight')
  if (record.inFlight) literal(flight, COORDINATOR_IN_FLIGHT_WORD)
  else literal(flight, COORDINATOR_FINISHED_WORD)
  if (record.runs.length === 0) {
    literal(cell(row, factory, 'span', 'coordinator-record-noruns'), COORDINATOR_NO_RUNS_WORD)
    return pull
  }
  const runs = cell(row, factory, 'ul', 'coordinator-runs')
  for (const run of record.runs) runCell(runs, factory, run)
  if (record.runsDropped > 0) {
    // A list holds list items, so this count is one rather than the span
    // `countCell` makes: the figure is a reading like every other here.
    putReading(cell(runs, factory, 'li', 'coordinator-more'), measured(record.runsDropped), 'count')
  }
  return pull
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

/** repoSummary draws one repository's head -- its name and which view this node
 * is -- then the strip of counts, the stale marker and the omissions --
 * everything the document answers about a repository without listing what is in
 * it -- and answers the element its detail belongs under. The head is drawn
 * first, and its two facts are drawn together, so a reader meets the name and
 * the source line before any figure: the figures of an unstored node are the
 * runs' own counts and mean something different from a stored node's. */
function repoSummary(
  parent: ScreenElement,
  factory: ScreenFactory,
  panel: CoordinatorRepoPanel,
): ScreenElement {
  const section = cell(parent, factory, 'section', 'coordinator-repo')
  const head = cell(section, factory, 'div', 'coordinator-repo-head')
  // The repository's own name, which the CLI read out of the local config: text
  // in the class the wire key declares, and never a link or a path.
  textCell(head, factory, 'h2', panel.repo, 'repository', 'repos[].repo', 'coordinator-repo-name')
  const state = cell(head, factory, 'p', 'coordinator-repo-state')
  if (panel.stored) literal(state, COORDINATOR_STORED_WORD)
  else literal(state, COORDINATOR_RUNS_WORD)
  if (panel.stale !== null) {
    // A block rather than the line of prose it was: it carries three facts (the
    // word, the stamp and the reason) and the omissions line stands with it.
    const stale = cell(section, factory, 'div', 'coordinator-repo-stale')
    literal(cell(stale, factory, 'span', 'coordinator-stale-word'), COORDINATOR_STALE_WORD)
    textCell(stale, factory, 'span', panel.stale.since, 'sentence', 'stale.since', 'coordinator-stale-since')
    textCell(stale, factory, 'span', panel.stale.reason, 'sentence', 'stale.reason', 'coordinator-stale-reason')
  }
  const counts = cell(section, factory, 'div', 'coordinator-counts')
  numberCell(counts, factory, COORDINATOR_RECORDS_WORD, panel.recordCount, 'count', 'coordinator-count-records')
  numberCell(
    counts,
    factory,
    COORDINATOR_IN_FLIGHT_COUNT_WORD,
    panel.inFlightCount,
    'count',
    'coordinator-count-inflight',
  )
  // The cell's own word is what carries the state; the class only says which
  // cell it is, so a reader who loses the tone still reads `collisions 2`.
  const countClasses = ['coordinator-count-collisions']
  if (panel.hasCollisions) countClasses.push('coordinator-count-alert')
  numberCell(counts, factory, COORDINATOR_COLLISIONS_WORD, panel.collisionCount, 'count', ...countClasses)
  if (panel.omitted !== null || panel.omittedCollisions !== null) {
    const omitted = cell(section, factory, 'p', 'coordinator-omitted')
    literal(cell(omitted, factory, 'span', 'coordinator-omitted-word'), COORDINATOR_OMITTED_WORD)
    if (panel.omitted !== null) countCell(omitted, factory, panel.omitted, 'coordinator-omitted-records')
    if (panel.omittedCollisions !== null) {
      countCell(omitted, factory, panel.omittedCollisions, 'coordinator-omitted-collisions')
    }
  }
  return cell(section, factory, 'div', COORDINATOR_DETAIL_CLASS)
}

/** repoDetail draws the part of one repository the summary does not: a record
 * per pull request with the runs behind it, the collisions and the pulls a sweep
 * could not read. It is a drawing of its own so the entry can make it in a chunk
 * of its own (`entry.ts`), after the summary is on screen.
 *
 * It answers the numbers it drew as buttons, in the order it drew them, for the
 * reason `fillCoordinatorQueuePart` does. */
export function repoDetail(
  parent: ScreenElement,
  factory: ScreenFactory,
  panel: CoordinatorRepoPanel,
): readonly CoordinatorPullButton[] {
  const pulls: CoordinatorPullButton[] = []
  if (panel.records.length > 0) {
    const records = cell(parent, factory, 'ul', 'coordinator-records')
    for (const record of panel.records) {
      const pull = recordCell(records, factory, record)
      if (pull !== null) pulls.push(pull)
    }
  }
  if (panel.collisions.length > 0) {
    const collisions = cell(parent, factory, 'ul', 'coordinator-collisions')
    for (const collision of panel.collisions) collisionCell(collisions, factory, collision)
  }
  if (panel.unread.length > 0) {
    const unread = cell(parent, factory, 'ul', 'coordinator-unreads')
    for (const entry of panel.unread) unreadCell(unread, factory, entry)
  }
  return pulls
}

/** CoordinatorDetail is one repository's detail: the panel to draw and the host
 * the entry fills it into. */
export interface CoordinatorDetail {
  readonly panel: CoordinatorRepoPanel
  readonly host: ScreenElement
}

/** DrawnCoordinatorFrame is the screen's shell: the three sections the entry
 * fills as their reads land, and the hosts each one paints into.
 *
 * `queue`, `totals` and the body are the three **sections** -- each with its own
 * busies and its own sequence token in the entry -- and `queueSlot`, `totalsSlot`
 * and `slot` are where their marks go. The queue's head cells and its four part
 * hosts are drawn here rather than by the model, because they are the same
 * elements whatever the reads answer with: a part's contents change, the part
 * does not.
 */
export interface DrawnCoordinatorFrame {
  readonly root: ScreenElement
  readonly body: ScreenElement
  readonly slot: ScreenElement
  readonly queue: ScreenElement
  readonly queueSlot: ScreenElement
  readonly queueCount: ScreenElement
  readonly queueEmpty: ScreenElement
  readonly parts: Readonly<Record<CoordinatorQueueSource, ScreenElement>>
  readonly blindHost: ScreenElement
  readonly totals: ScreenElement
  readonly totalsSlot: ScreenElement
  /** the job list's section (K4): drawn empty in the frame, because nothing is
   * read for it until the operator presses its control, and filled by the entry
   * from then on. It is a section of the frame and not of the model for that
   * reason: the model is what a read answered with, and this section's read has
   * not been made. */
  readonly jobs: DrawnCoordinatorJobs
}

/**
 * renderCoordinatorFrame draws everything the screen can say before any read
 * answers: the section stamped with its theme, the read's own source line (which
 * is a constant of the console's), the console's sentence about the view's
 * missing age, and the **three empty sections** the reads fill -- the action
 * queue with its head and its four part hosts, the fleet strip, and the body.
 *
 * **It is drawn in the frame the tab was opened in.** The read behind it is
 * `coordinator status --from-store --all --json`, a document of about 724 KB on
 * the owner's installation and the longest read of any tab; beside it the screen
 * reads `runs ls`, `plan status` and `budget show`, and each of the four paints
 * its own part the moment it lands (the owner's rule of 2026-10-31). What it
 * draws is what is true before the answers: which commands the queue is reading,
 * and that the console claims no age for the view.
 *
 * The body is empty but for the slot the busy mark is put in, so the entry can
 * fill it -- summary first, each repository's detail after -- without taking
 * anything down.
 */
export function renderCoordinatorFrame(
  factory: ScreenFactory,
  theme: CoordinatorTheme,
): DrawnCoordinatorFrame {
  const root = make(factory, 'section', 'coordinator')
  root.setAttribute('data-theme', theme)
  literal(cell(root, factory, 'p', 'coordinator-source'), COORDINATOR_SOURCE_LINE)
  literal(cell(root, factory, 'p', 'coordinator-age'), COORDINATOR_NO_AGE_WORD)
  const queue = cell(root, factory, 'section', COORDINATOR_QUEUE_CLASS)
  const head = cell(queue, factory, 'div', COORDINATOR_QUEUE_HEAD_CLASS)
  const queueCount = cell(head, factory, 'span', 'coordinator-queue-count')
  literal(cell(head, factory, 'span', 'coordinator-queue-word'), COORDINATOR_QUEUE_WORD)
  const queueEmpty = cell(head, factory, 'span', 'coordinator-queue-empty')
  const queueSlot = cell(head, factory, 'div', BUSY_SLOT_CLASS)
  const parts = {} as Record<CoordinatorQueueSource, ScreenElement>
  for (const source of QUEUE_SOURCES) {
    parts[source] = cell(queue, factory, 'div', COORDINATOR_QUEUE_PART_CLASS)
  }
  const blindHost = cell(queue, factory, 'div', COORDINATOR_BLIND_HOST_CLASS)
  const totals = cell(root, factory, 'section', COORDINATOR_TOTALS_CLASS)
  const totalsSlot = cell(totals, factory, 'div', BUSY_SLOT_CLASS)
  const jobsSection = cell(root, factory, 'section', COORDINATOR_JOBS_CLASS)
  const jobsHead = cell(jobsSection, factory, 'div', COORDINATOR_JOBS_HEAD_CLASS)
  const jobsSlot = cell(jobsHead, factory, 'div', BUSY_SLOT_CLASS)
  const jobsList = cell(jobsSection, factory, 'div', COORDINATOR_JOBS_LIST_CLASS)
  const jobsControls = cell(jobsSection, factory, 'div', COORDINATOR_JOBS_CONTROLS_CLASS)
  const jobs: DrawnCoordinatorJobs = {
    section: jobsSection,
    head: jobsHead,
    slot: jobsSlot,
    list: jobsList,
    controls: jobsControls,
  }
  const body = cell(root, factory, 'div', COORDINATOR_BODY_CLASS)
  const slot = cell(body, factory, 'div', BUSY_SLOT_CLASS)
  return { root, body, slot, queue, queueSlot, queueCount, queueEmpty, parts, blindHost, totals, totalsSlot, jobs }
}

/**
 * fillCoordinatorSummary draws one repository section per repository: the
 * repository's name, what the document says about its state, and its counts.
 *
 * **It answers one detail host per repository rather than drawing the detail
 * itself**, because the two halves are drawn at different times: this one the
 * moment the document lands (it is a handful of lines per repository), and each
 * repository's records and collisions after it, in a chunk of their own -- a
 * document that lists hundreds of pull requests is half a megabyte of drawing,
 * and a screen that made all of it in one turn would freeze the window it is
 * being drawn into.
 *
 * The guard for a document that names no repository is drawn here, in the
 * console's own words: `--all` on a config that lists none is refused by the CLI
 * (`coordinatorRepos`' `userErr`), so what this branch draws is a state the
 * console cannot read rather than one it can, and a screen that drew nothing for
 * it would be indistinguishable from a read that failed.
 *
 * **`repos: null` is not that state**: it is the document not having answered
 * yet, and a body that said "the installation lists no repository" while the
 * read is still out would be the screen inventing a fact about the owner's
 * installation. Nothing is drawn, and the section's own busy mark -- which the
 * entry put up when the read started -- is what says why.
 */
export function fillCoordinatorSummary(
  factory: ScreenFactory,
  model: CoordinatorModel,
  host: ScreenElement,
): readonly CoordinatorDetail[] {
  if (model.repos === null) return []
  if (model.repos.length === 0) {
    literal(cell(host, factory, 'p', 'coordinator-empty'), COORDINATOR_EMPTY_WORD)
    return []
  }
  return model.repos.map((panel) => ({ panel, host: repoSummary(host, factory, panel) }))
}

/**
 * renderCoordinator draws the whole screen at once: the frame, the queue, the
 * blind-spot line, the fleet's totals, the job list's own resting state, the
 * summary and every repository's detail.
 *
 * It is what a caller that has every document in hand draws -- the goldens'
 * mount, and the cases that assert on the tree -- and the entry does **not** use
 * it: the entry draws the frame first and fills each section as its own read
 * lands, which is the owner's rule of 2026-10-31. Both paths draw the same
 * elements in the same order, so what a case asserts about this function is true
 * of the screen on an operator's display.
 */
export function renderCoordinator(
  factory: ScreenFactory,
  model: CoordinatorModel,
  theme: CoordinatorTheme,
): ScreenElement {
  const frame = renderCoordinatorFrame(factory, theme)
  fillCoordinatorHead(model, frame)
  for (const source of QUEUE_SOURCES) {
    fillCoordinatorQueuePart(factory, model, source, frame.parts[source])
  }
  fillCoordinatorBlind(factory, model, frame.blindHost)
  fillCoordinatorTotals(factory, model, frame.totals)
  // The job list is drawn in the state a caller that holds every *other*
  // document is in: the jobs are the one read rule 5 keeps out of a first
  // paint, so what stands in the section is the control that asks for them --
  // the same shape the queue's own jobs part draws for the same reason. The
  // entry fills the section itself when an operator presses it.
  frame.jobs.controls.append(coordinatorLoadJobsControl(factory, false))
  for (const detail of fillCoordinatorSummary(factory, model, frame.body)) {
    repoDetail(detail.host, factory, detail.panel)
  }
  return frame.root
}
