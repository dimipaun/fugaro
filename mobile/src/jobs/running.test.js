// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The running-jobs view (mobile identity-pairing plan, task J2; F30, D2797, O3).
//
// Two halves, as `list.test.js` has them. The first is `runningJobs`' own
// judgement of what it is handed -- a reading that is not one is refused, a run
// with an outcome entry is not a running job, and the two bounds (the
// repository's total timeout and the day the id names) make a stale one. The
// second is the wiring on the session port: the projects are the paired
// sessions, a project the phone cannot read becomes a row rather than a gap,
// and a session that is not one is refused with the port's own code.

import assert from 'node:assert/strict';
import test from 'node:test';

import { stubSession } from '../../../shared/mobile-session/stub.js';
import { measured, unavailable } from '../feed/index.js';
import { JobList } from './list.js';
import { RUNNING_CODES, RUN_STATES, runningJobs } from './running.js';

const NOW = Date.parse('2026-11-24T12:00:00Z');
/** Six hours: `timeouts.total`'s default shape in the fixtures below. */
const TIMEOUT = 6 * 3_600_000;

/** A run id as `internal/task.NewRunID` mints one: the UTC start it began at,
 * then four hex characters. */
function runId(atMs, suffix = 'ab12') {
  const iso = new Date(atMs).toISOString();
  return `${iso.slice(0, 4)}${iso.slice(5, 7)}${iso.slice(8, 10)}-${iso.slice(11, 13)}${iso.slice(14, 16)}${iso.slice(17, 19)}-${suffix}`;
}

/** One run entry of a project's reading: no outcome (still going, as far as the
 * record says), a minute old, and a measured half a dollar. */
function run(over = {}) {
  return { run: runId(NOW - 60_000), spend: measured(0.5), outcome: null, ...over };
}

/** One paired project's reading. */
function project(over = {}) {
  return { project: 'p-1', reachable: true, timeoutMs: TIMEOUT, runs: [run()], ...over };
}

/** The view's answer for one set of readings, as the rows a screen would draw. */
function rowsOf(projects, nowMs = NOW) {
  const answer = runningJobs({ projects, nowMs });
  assert.equal(answer.ok, true, answer.ok === false ? `${answer.code} at ${answer.field}` : '');
  return answer.rows;
}

/** The refusal of one set of readings. */
function refusalOf(projects, nowMs = NOW) {
  const answer = runningJobs({ projects, nowMs });
  assert.equal(answer.ok, false, 'expected a refusal');
  return answer;
}

/** The `JobList`'s own wiring: one fake feed reader and the session port. */
function wired({ sessions = [], readRuns = null, now = () => NOW } = {}) {
  const reader = { start() {}, refresh() {}, stop() {}, snapshot() { return null; } };
  return new JobList({ reader, sessions, readRuns, now });
}

test('a project with an outcome must not list first', () => {
  // The task's own red step. The run of `p-1` has an outcome entry -- the record
  // says it finished -- so it is not a running job and `p-1` draws no row at
  // all; the first row belongs to `p-2`, whose run has no outcome. An
  // implementation that listed every `outcomes`-less run without checking, or
  // that listed a finished one first, fails here.
  const rows = rowsOf([
    project({ project: 'p-1', runs: [run({ outcome: { status: 'succeeded' } })] }),
    project({ project: 'p-2' }),
  ]);
  assert.deepEqual(rows.map((row) => row.project), ['p-2']);
});

test('a running row carries its project, its age and its spend', () => {
  const spend = measured(1.25);
  const [row] = rowsOf([project({ project: 'p-7', runs: [run({ run: runId(NOW - 90_000), spend })] })]);
  assert.deepEqual(row, {
    project: 'p-7',
    run: runId(NOW - 90_000),
    state: 'running',
    ageMs: 90_000,
    spend,
  });
});

test('a run with no spend read carries null, never zero', () => {
  // A missing field is `unavailable` and never `0` (the list's own rule): a
  // spend the read did not carry is a figure the view does not have, and the
  // screen prints `n/a` for it rather than a measurement of nothing.
  const [row] = rowsOf([project({ runs: [run({ spend: null })] })]);
  assert.equal(row.spend, null);
  const [unread] = rowsOf([project({ runs: [run({ spend: unavailable() })] })]);
  assert.equal(unread.spend.state, unavailable().state);
});

test('a run past the repository total timeout is stale, not running', () => {
  const [row] = rowsOf([project({ runs: [run({ run: runId(NOW - TIMEOUT - 1_000) })] })]);
  assert.equal(row.state, 'stale');
  // ... and one inside the bound is running: the bound is what the state turns
  // on, not the age alone.
  const [running] = rowsOf([project({ runs: [run({ run: runId(NOW - TIMEOUT + 1_000) })] })]);
  assert.equal(running.state, 'running');
});

test('a run from an earlier day is stale however young it is', () => {
  // Half past eleven last night, twelve hours ago: inside a day-long timeout and
  // still not running, because a run does not outlive the day it started in.
  const at = Date.parse('2026-11-23T23:30:00Z');
  const [row] = rowsOf(
    [project({ timeoutMs: 24 * 3_600_000, runs: [run({ run: runId(at) })] })],
  );
  assert.equal(row.state, 'stale');
  assert.equal(row.ageMs, NOW - at);
  // A run started later today is running even at the same age: it is the day the
  // id names that makes the difference, not how long ago it was.
  const [today] = rowsOf([
    project({ timeoutMs: 24 * 3_600_000, runs: [run({ run: runId(Date.parse('2026-11-24T00:30:00Z')) })] }),
  ]);
  assert.equal(today.state, 'running');
});

test('a run started in the future is running, and its age is not negative', () => {
  // A clock difference between the phone and the machine that minted the id is
  // not a run past its bound: the run is still going, and a screen showing
  // "-3m" would be showing a device's argument with a clock.
  const [row] = rowsOf([project({ runs: [run({ run: runId(NOW + 180_000) })] })]);
  assert.equal(row.state, 'running');
  assert.equal(row.ageMs, 0);
});

test('an unreachable project is a row that says so', () => {
  const rows = rowsOf([
    { project: 'p-1', reachable: false },
    project({ project: 'p-2' }),
  ]);
  assert.deepEqual(rows.map((row) => row.project).sort(), ['p-1', 'p-2']);
  const unreachable = rows.find((row) => row.project === 'p-1');
  assert.deepEqual(unreachable, {
    project: 'p-1',
    run: null,
    state: 'unreachable',
    ageMs: null,
    spend: null,
  });
  // The other project's jobs are drawn: one project the phone cannot read does
  // not blank the list.
  assert.equal(rows.filter((row) => row.state === 'running').length, 1);
});

test('the view lists nothing when no project has a run without an outcome', () => {
  assert.deepEqual(rowsOf([project({ runs: [] }), project({ project: 'p-2', runs: [run({ outcome: { status: 'failed' } })] })]), []);
});

test('running rows come first, then stale ones, then unreachable projects', () => {
  const rows = rowsOf([
    { project: 'p-3', reachable: false },
    project({ project: 'p-1', runs: [run({ run: runId(NOW - TIMEOUT - 60_000) })] }),
    project({ project: 'p-2', runs: [run({ run: runId(NOW - 120_000) })] }),
  ]);
  assert.deepEqual(rows.map((row) => [row.project, row.state]), [
    ['p-2', 'running'],
    ['p-1', 'stale'],
    ['p-3', 'unreachable'],
  ]);
});

test('within a state the oldest run is first, and ties are broken deterministically', () => {
  const young = runId(NOW - 60_000);
  const old = runId(NOW - 600_000);
  const rows = rowsOf([
    project({ project: 'p-2', runs: [{ run: young, spend: null }, { run: old, spend: null }] }),
    project({ project: 'p-1', runs: [{ run: young, spend: null }] }),
  ]);
  assert.deepEqual(rows.map((row) => [row.project, row.run]), [
    ['p-2', old],
    ['p-1', young],
    ['p-2', young],
  ]);
});

test('RUN_STATES is the closed set of the words a row carries, in drawing order', () => {
  assert.deepEqual([...RUN_STATES], ['running', 'stale', 'unreachable']);
  const rows = rowsOf([
    { project: 'p-3', reachable: false },
    project({ project: 'p-1' }),
  ]);
  for (const row of rows) assert.ok(RUN_STATES.includes(row.state), row.state);
});

test('a reading the app did not build is refused, and the code names what', () => {
  const cases = [
    ['the clock is not an instant', [project()], 'not a number', RUNNING_CODES.BAD_NOW],
    ['the projects are not a list', { not: 'a list' }, NOW, RUNNING_CODES.BAD_PROJECTS],
    ['a reading is not an object', ['p-1'], NOW, RUNNING_CODES.BAD_PROJECT],
    ['the project is not named', [project({ project: '' })], NOW, RUNNING_CODES.BAD_PROJECT],
    ['reachable is not a boolean', [project({ reachable: 'yes' })], NOW, RUNNING_CODES.BAD_PROJECT],
    ['the timeout is missing', [project({ timeoutMs: undefined })], NOW, RUNNING_CODES.BAD_TIMEOUT],
    ['the timeout is zero', [project({ timeoutMs: 0 })], NOW, RUNNING_CODES.BAD_TIMEOUT],
    ['the timeout is not whole', [project({ timeoutMs: 1.5 })], NOW, RUNNING_CODES.BAD_TIMEOUT],
    ['the runs are not a list', [project({ runs: 'a run' })], NOW, RUNNING_CODES.BAD_RUNS],
    ['the run entry is not an object', [project({ runs: ['20261124-120000-ab12'] })], NOW, RUNNING_CODES.BAD_RUN],
    ['the run is not a run id', [project({ runs: [run({ run: 'demo-run-7' })] })], NOW, RUNNING_CODES.BAD_RUN],
    ['the run id names a day that does not exist', [project({ runs: [run({ run: '20261332-120000-ab12' })] })], NOW, RUNNING_CODES.BAD_RUN],
    ['the spend is not a figure', [project({ runs: [run({ spend: 1.25 })] })], NOW, RUNNING_CODES.BAD_RUN],
    ['the outcome is not an entry', [project({ runs: [run({ outcome: true })] })], NOW, RUNNING_CODES.BAD_RUN],
  ];
  for (const [name, projects, nowMs, code] of cases) {
    const answer = runningJobs({ projects, nowMs });
    assert.equal(answer.ok, false, name);
    assert.equal(answer.code, code, name);
  }
});

test('a refusal names a field and never a value', () => {
  const answer = refusalOf([project({ runs: [run({ run: 'not-a-run-id-but-a-secret-looking-string' })] })]);
  assert.deepEqual(Object.keys(answer).sort(), ['code', 'field', 'ok']);
  assert.equal(JSON.stringify(answer).includes('secret-looking'), false);
});

test('the view is wired on the session port, and reads every paired project once', async () => {
  const first = stubSession();
  const second = stubSession({ project_id: 'stub-project-two' });
  const asked = [];
  const reader = wired({
    sessions: [first, second],
    readRuns: async (session) => {
      asked.push(session);
      return { project: session.project_id, reachable: true, timeoutMs: TIMEOUT, runs: [run()] };
    },
  });
  const answer = await reader.running();
  assert.equal(answer.ok, true);
  assert.equal(answer.read, true);
  assert.deepEqual(asked, [first, second]);
  assert.deepEqual(answer.rows.map((row) => row.project).sort(), ['stub-project', 'stub-project-two']);
});

test('with no reader there is nothing to read, and the answer says so', async () => {
  // The shape `poll()` uses for a list built without a mailbox client:
  // emptiness that would otherwise read as "nothing is running" is marked.
  // A phone with no paired project has nothing to read at all.
  assert.deepEqual(await wired().running(), { ok: true, read: false, rows: [] });
  // ... and a list with a reader but no paired project *did* look: it read
  // every project it has, which is none, and found nothing running. The two
  // empty lists are not the same value, which is the point of `read`.
  const answer = await wired({ readRuns: async () => project() }).running();
  assert.deepEqual(answer, { ok: true, read: true, rows: [] });
});

test('a project the phone cannot read is unreachable, and the others still draw', async () => {
  const reader = wired({
    sessions: [stubSession(), stubSession({ project_id: 'stub-project-two' })],
    readRuns: async (session) => {
      if (session.project_id === 'stub-project') throw new Error('the read failed');
      return { project: session.project_id, reachable: true, timeoutMs: TIMEOUT, runs: [run()] };
    },
  });
  const answer = await reader.running();
  assert.equal(answer.ok, true);
  const unreachable = answer.rows.find((row) => row.state === 'unreachable');
  assert.equal(unreachable.project, 'stub-project');
  assert.equal(answer.rows.filter((row) => row.state === 'running').length, 1);
});

test('a read refused in the app’s own shape is an unreachable project too', async () => {
  const reader = wired({
    sessions: [stubSession()],
    readRuns: async () => ({ ok: false, code: 'offline', field: 'runs' }),
  });
  const answer = await reader.running();
  assert.equal(answer.ok, true);
  assert.deepEqual(answer.rows.map((row) => row.state), ['unreachable']);
});

test('a session that is not a port is refused with the port’s own code', async () => {
  // The port's refusal, carried unchanged (`list.js` passes another module's
  // decision through rather than re-labelling it): `not-a-session` for a value
  // that is not a session at all, and `missing-member` for one that is shaped
  // like a session with a member gone -- both `PORT_CODES`, both naming what
  // the port's own `member` field names.
  for (const [session, code] of [['a session', 'not-a-session'], [{ project_id: 'p-1' }, 'missing-member']]) {
    const reader = wired({ sessions: [session], readRuns: async () => project() });
    await assert.rejects(reader.running(), (error) => {
      assert.equal(error.name, 'SessionPortError', code);
      assert.equal(error.code, code);
      return true;
    });
  }
  // ... and the read is not made for a session the port refused: the refusal is
  // the answer, not something to work around with a half-built session.
  let reads = 0;
  const reader = wired({ sessions: [{ project_id: 'p-1' }], readRuns: async () => { reads += 1; return project(); } });
  await assert.rejects(reader.running());
  assert.equal(reads, 0);
});

test('the wiring refuses a reader that is not one, and sessions with no reader', () => {
  assert.throws(() => wired({ readRuns: 'a reader' }), TypeError);
  assert.throws(() => wired({ sessions: 'the sessions', readRuns: async () => project() }), TypeError);
  assert.throws(
    () => wired({ sessions: [stubSession()] }),
    TypeError,
    'sessions were handed in with nothing to read them with',
  );
});
