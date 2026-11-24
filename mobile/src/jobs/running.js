// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The running-jobs view: which runs are going, across the projects the phone is
// paired with (mobile identity-pairing plan, task J2; F30, D2797, O3).
//
// **What a running run is, and what this file is not.** A run's own record is
// `runs/$slug/$run` and its registry entry `agents/$slug/$run`; a paired device
// reads neither. What J1 opened is one node of the outcomes family --
// `outcomes/$day/$slug/$run` -- beside the run's share of the day the app
// already read (`spend/$day/runs/$slug/$run`, D112's grant). So a run the phone
// knows about either has an outcome entry, and the record says it finished, or
// has none, and it may still be going; that is the whole of what this view
// decides, and it decides it from what it is **handed**.
//
// **The two bounds, and why the view needs both.** "Has no outcome" is not
// "running": a run whose machine died leaves an entry nobody ever finished, and
// a phone that listed it forever would be listing a ghost. A no-outcome run is
// therefore running only while two things hold, and it is **stale** when either
// fails:
//
//   * **inside its repository's total timeout** (`timeouts.total`). This is the
//     **run's** bound and not the project's: one project serves many
//     repositories, each with its own `timeouts.total`, so a single bound for
//     the whole reading would mark a long-running repository's run stale before
//     its time and let a short one read as running past its own. The run's
//     repository is the `$slug` of the path the phone read its day share from
//     (`spend/$day/runs/$slug/$run`), and its `fugaro.yaml` is not readable --
//     no grant covers it -- so the bound arrives with the **run entry**, set by
//     whoever built the reading.
//   * **on the day its id names.** `internal/task.NewRunID` mints an id as
//     `YYYYMMDD-HHMMSS-xxxx` in UTC, so the id the phone holds carries the run's
//     own start: the **age** of a run is `now - that instant` and the day the
//     run started in is the day the id names, with no second read for either. A
//     run does not outlive its day (the sweeper's `runs last at most a day`), so
//     an id from an earlier day is stale however young the clock makes it -- a
//     phone whose clock is a few minutes off, or a run that began at 23:59,
//     would otherwise read as running past midnight.
//
// **A project the phone cannot read is a row, not a gap.** Dropping it would
// leave a list that says "nothing is running here" about a project whose state
// nobody checked, which is the one thing this view must not say. So an
// unreachable project draws its own row, and the reader's failure is carried as
// that row rather than raised: one project's outage must not blank the jobs of
// the others, and the row is the feedback a screen shows.
//
// **What a row cannot carry.** No repository: `config/caps/repos/$slug` and
// `spend/$day/repos/$slug` are granted and D2797 amended the ban on **run**
// rows, not that one -- the resolution of 2026-10-17 stands for repositories,
// and `scope.test.js` still refuses the token everywhere. No run title either:
// titles live in `agents/$slug/$run`, which no user claim reads, so a row is the
// project, the run's id, its age, its spend and its state. And the spend is a
// **figure** (`src/feed/index.js`), never a bare number: a spend the read did
// not carry is `unavailable` and prints `n/a`, which is `lines.js`'s rule
// applied to this view's own rows.
//
// **Every role sees this view.** A viewer reads `outcomes` and the run's day
// share exactly as an operator does, and the owner accepted that read for all
// three roles (O3): what a role may not do is start or cancel a run, which is
// J3's and J4's, and nothing here gates on the session's role.
//
// The view's input is one **reading** per paired project, and this file judges
// every field of it rather than trusting the app that built it:
//
// ```
// { project: '<opaque project id>', reachable: true,
//   runs: [ { run: '20261124-101530-ab12', timeoutMs: 21600000,
//             spend: <figure|null>, outcome: null } ] }
// ```
//
// `outcome` is the entry at `outcomes/$day/$slug/$run`, or `null` when there is
// none -- its contents are not this view's business (which outcome it is belongs
// to the runs view the console has), so any entry means the run is not drawn.

import { dayKey, isFigure } from '../feed/index.js';

/**
 * The refusals. Every way this view declines to draw a list, as a value a caller
 * can branch on, in the app's convention (`fields.js`'s `LIST_CODES`).
 */
export const RUNNING_CODES = Object.freeze({
  /** The clock is not an instant this view can measure an age against. */
  BAD_NOW: 'bad-now',
  /** The readings are not a list of projects. */
  BAD_PROJECTS: 'bad-projects',
  /** A project's reading is not one: no project, no `reachable`, or not an object. */
  BAD_PROJECT: 'bad-project',
  /** A run's repository total timeout is missing, zero or not whole milliseconds. */
  BAD_TIMEOUT: 'bad-timeout',
  /** A reachable project's runs are not a list of entries. */
  BAD_RUNS: 'bad-runs',
  /** A run entry is not one: no run id, an unknown outcome, or a spend that is not a figure. */
  BAD_RUN: 'bad-run',
});

/**
 * What a row says, in the order the screen draws it: what is going, what the
 * record cannot still be going, and a project the phone could not read at all.
 * `unreachable` is not a run's state -- there is no run on that row -- but it is
 * the state of that row, and a screen switching on one word is what keeps the
 * three from being drawn differently by two screens.
 */
export const RUN_STATES = Object.freeze(['running', 'stale', 'unreachable']);

/** The state's rank, and with it the drawing order above. */
const RANK = Object.freeze(Object.fromEntries(RUN_STATES.map((state, rank) => [state, rank])));

/** `internal/task.NewRunID`'s own form, as the six fields it names. */
const RUN_ID = /^(\d{4})(\d{2})(\d{2})-(\d{2})(\d{2})(\d{2})-[0-9a-f]{4}$/;

const DAY_MS = 86_400_000;

/** A frozen refusal, in the shape the app's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

function isNode(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

/**
 * The instant a run id names, in epoch milliseconds, or `null` when the string
 * is not a run id.
 *
 * The digits are a UTC calendar date and time, and a date the calendar does not
 * have (month 13, day 32) is not a run id: `Date.UTC` rolls such a date over
 * into the next one, so the instant is compared back against the digits it was
 * read from rather than trusted.
 */
function startedAt(run) {
  const parts = RUN_ID.exec(run);
  if (parts === null) return null;
  const [, year, month, day, hour, minute, second] = parts;
  const ms = Date.UTC(Number(year), Number(month) - 1, Number(day), Number(hour), Number(minute), Number(second));
  const spelled = `${year}-${month}-${day}T${hour}:${minute}:${second}`;
  return new Date(ms).toISOString().slice(0, 19) === spelled ? ms : null;
}

/** One row, frozen: the project, the run (or `null`), the state, the age and
 * the spend (a figure, or `null` when the read did not carry one). */
function row(state, project, run, ageMs, spend) {
  return Object.freeze({ project, run, state, ageMs, spend });
}

/**
 * The order the screen draws the rows in.
 *
 * What is running comes first -- the phone is opened to see what is going -- and
 * within a state the **oldest** run is first, because the run nearest its bound
 * is the one an operator is looking for. A project nobody could read comes last:
 * it is a caveat about the list and not a job on it. A tie is broken by the
 * project and then the run, so the order is total and two draws of one reading
 * agree -- a screen that reordered equal rows between renders would be a screen
 * that flickers.
 */
function compareRows(a, b) {
  if (RANK[a.state] !== RANK[b.state]) return RANK[a.state] - RANK[b.state];
  if (a.ageMs !== b.ageMs) return (b.ageMs ?? 0) - (a.ageMs ?? 0);
  if (a.project !== b.project) return a.project < b.project ? -1 : 1;
  if (a.run === null || b.run === null || a.run === b.run) return 0;
  return a.run < b.run ? -1 : 1;
}

/** One project's rows, from its reading. `{ ok: true, rows }` or a refusal. */
function projectRows(reading, nowMs, today) {
  if (!isNode(reading)) return refuse(RUNNING_CODES.BAD_PROJECT, 'projects[]');
  const project = reading.project;
  if (typeof project !== 'string' || project.length === 0) return refuse(RUNNING_CODES.BAD_PROJECT, 'project');
  if (typeof reading.reachable !== 'boolean') return refuse(RUNNING_CODES.BAD_PROJECT, 'reachable');
  if (!reading.reachable) return Object.freeze({ ok: true, rows: [row('unreachable', project, null, null, null)] });
  if (!Array.isArray(reading.runs)) return refuse(RUNNING_CODES.BAD_RUNS, 'runs');
  const rows = [];
  for (const entry of reading.runs) {
    if (!isNode(entry)) return refuse(RUNNING_CODES.BAD_RUN, 'runs[]');
    if (typeof entry.run !== 'string' || startedAt(entry.run) === null) return refuse(RUNNING_CODES.BAD_RUN, 'run');
    // The bound is the run's own repository's (`timeouts.total`), which is why
    // it is read here and not from the reading: a project serves many
    // repositories and each sets its own.
    const timeoutMs = entry.timeoutMs;
    if (typeof timeoutMs !== 'number' || !Number.isInteger(timeoutMs) || timeoutMs <= 0) {
      return refuse(RUNNING_CODES.BAD_TIMEOUT, 'runs[].timeoutMs');
    }
    const outcome = entry.outcome;
    if (outcome !== null && outcome !== undefined && !isNode(outcome)) return refuse(RUNNING_CODES.BAD_RUN, 'outcome');
    const spend = entry.spend;
    if (spend !== null && spend !== undefined && !isFigure(spend)) return refuse(RUNNING_CODES.BAD_RUN, 'spend');
    // An entry at the run's outcome node is the record saying the run finished:
    // it is not a running job, and it is not this view's to draw.
    if (isNode(outcome)) continue;
    const start = startedAt(entry.run);
    const elapsed = nowMs - start;
    const stale = elapsed > timeoutMs || Math.floor(start / DAY_MS) < today;
    // A start in the future is a clock difference and not an age: the run is
    // still going, and the row reads `0` rather than a negative duration.
    rows.push(row(stale ? 'stale' : 'running', project, entry.run, Math.max(0, elapsed), spend ?? null));
  }
  return Object.freeze({ ok: true, rows });
}

/**
 * The running jobs across the paired projects, as one ordered list of rows.
 *
 * `projects` is one reading per paired project, in the shape above;
 * `nowMs` is the phone's clock, and it is the **only** clock this view reads --
 * the age of a run is measured against it and the day a run must have started in
 * is taken from it. Returns `{ ok: true, rows }` or a refusal. A refusal stops
 * the whole view rather than dropping one project: a reading the app did not
 * build is a defect in the wiring, and drawing the rest would present a list
 * whose gaps nobody can see.
 */
export function runningJobs({ projects, nowMs } = {}) {
  if (typeof nowMs !== 'number' || !Number.isFinite(nowMs)) return refuse(RUNNING_CODES.BAD_NOW, 'nowMs');
  if (!Array.isArray(projects)) return refuse(RUNNING_CODES.BAD_PROJECTS, 'projects');
  const today = dayKey(nowMs);
  const rows = [];
  for (const reading of projects) {
    const judged = projectRows(reading, nowMs, today);
    if (judged.ok === false) return judged;
    rows.push(...judged.rows);
  }
  rows.sort(compareRows);
  return Object.freeze({ ok: true, rows: Object.freeze(rows) });
}
