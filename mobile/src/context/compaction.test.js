// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The compact button and the idle-only automatic compaction (mobile identity
// and pairing plan, task Z2; D2789).
//
// The red step is first: a send while the agent is busy is refused, for both
// triggers -- Z0 (Check 732) found a slash command injected into a working
// session queued into the turn that is running, which is the interruption the
// spec forbids. The rest pins the two triggers' own rules: the button compacts
// for an idle agent whatever the reading and the project's threshold say, and
// the automatic one fires only from a reading that reached the session policy's
// threshold, holds when the project set none or nothing was read, and never
// fires on the gauge's rounded percentage.
//
// What the tests deliberately do not cover: the send itself. The app's one
// write path is the mailbox client (D113), and this module answers the command
// a screen sends with -- a test that delivered it would be testing the
// screen's wiring, not the decision.

import assert from 'node:assert/strict';
import test from 'node:test';

import { PORT_CODES, SessionPortError } from '../../../shared/mobile-session/port.js';
import { STUB_COMPACTION_THRESHOLD, STUB_PROJECT_ID, stubSession } from '../../../shared/mobile-session/stub.js';
import {
  AGENT_STATES,
  COMPACT_COMMAND,
  COMPACTION_CODES,
  COMPACTION_REASONS,
  COMPACTION_STATES,
  GAUGE_STATES,
  autoCompaction,
  compactButton,
  compactionCommand,
  contextGauge,
  isCompaction,
} from './index.js';

/** Z1's gauge over a reading, which is what the automatic trigger reads. */
function gaugeOf(used, total, over = {}) {
  return contextGauge({ session: stubSession(over), reading: { used, total } }).gauge;
}

/** An idle agent, which is the only state that permits a send. */
const IDLE = { agent: AGENT_STATES.IDLE };

test('a send while the agent is busy is refused, for both triggers', () => {
  const busy = { agent: AGENT_STATES.BUSY };
  for (const [name, answer] of [
    ['the button', compactButton({ session: stubSession(), ...busy })],
    ['the automatic trigger', autoCompaction({ session: stubSession(), gauge: gaugeOf(190_000, 200_000), ...busy })],
  ]) {
    assert.equal(answer.ok, false, `${name} sent to a busy agent`);
    assert.equal(answer.code, COMPACTION_CODES.AGENT_BUSY);
    assert.equal(answer.field, 'agent');
    // No command at all: a refusal is never a half decision a screen could
    // send, and it is not a `hold` either -- silence would leave the tap
    // looking as if it had compacted.
    assert.equal(answer.compaction, undefined);
  }
});

test('the agent\'s state must be read, and an unread one is refused', () => {
  for (const agent of [undefined, null, 'IDLE', 'working', '', true, 0]) {
    const answer = compactButton({ session: stubSession(), agent });
    assert.equal(answer.ok, false, `${JSON.stringify(agent)} was read as a state`);
    assert.equal(answer.code, COMPACTION_CODES.BAD_AGENT);
    assert.equal(answer.field, 'agent');
  }
  // Absence is never idle: a caller with no reading sends nothing.
  assert.equal(autoCompaction({ session: stubSession(), gauge: gaugeOf(190_000, 200_000) }).code, COMPACTION_CODES.BAD_AGENT);
});

test('the button compacts for an idle agent, whatever the reading says', () => {
  const { ok, compaction } = compactButton({ session: stubSession(), ...IDLE });
  assert.equal(ok, true);
  assert.equal(compaction.state, COMPACTION_STATES.COMPACT);
  assert.equal(compaction.command, COMPACT_COMMAND);
  assert.equal(compaction.reason, null);
  assert.equal(compaction.project_id, STUB_PROJECT_ID);
  // The threshold is the project's, and the button does not consult it: a
  // session at 5% compacts when the person taps, and a project that set no
  // threshold compacts when they tap too.
  assert.equal(compactButton({ session: stubSession({ policy: { compaction_threshold: 0.9 } }), ...IDLE }).compaction.command, COMPACT_COMMAND);
  assert.equal(compactButton({ session: stubSession({ policy: { compaction_threshold: null } }), ...IDLE }).compaction.command, COMPACT_COMMAND);
});

test('the automatic trigger compacts a reading that reached the project\'s threshold', () => {
  const at = autoCompaction({ session: stubSession(), gauge: gaugeOf(80_000, 200_000), ...IDLE });
  assert.equal(at.ok, true);
  assert.equal(at.compaction.state, COMPACTION_STATES.COMPACT);
  assert.equal(at.compaction.command, COMPACT_COMMAND);
  assert.equal(at.compaction.reason, null);
  // Over the threshold is over it, and the project's own value is what is
  // compared: the stub's is 0.4.
  assert.equal(STUB_COMPACTION_THRESHOLD, 0.4);
  assert.equal(autoCompaction({ session: stubSession(), gauge: gaugeOf(200_000, 200_000), ...IDLE }).compaction.state, COMPACTION_STATES.COMPACT);
  assert.equal(autoCompaction({ session: stubSession({ policy: { compaction_threshold: 0.01 } }), gauge: gaugeOf(2_000, 200_000), ...IDLE }).compaction.state, COMPACTION_STATES.COMPACT);
});

test('a reading under the threshold holds, and the rounded percentage is not the reading', () => {
  const under = autoCompaction({ session: stubSession(), gauge: gaugeOf(79_200, 200_000), ...IDLE });
  assert.equal(under.ok, true);
  assert.equal(under.compaction.state, COMPACTION_STATES.HOLD);
  assert.equal(under.compaction.command, null);
  assert.equal(under.compaction.reason, COMPACTION_REASONS.BELOW_THRESHOLD);
  // 79200 of 200000 is 39.6%, which the gauge draws as 40%: the comparison is
  // on the reading's own tokens, so a threshold of 40% does not fire there.
  assert.equal(gaugeOf(79_200, 200_000).percent, 40);
  assert.equal(under.compaction.state, COMPACTION_STATES.HOLD);
  // One token under, and exactly at it.
  assert.equal(autoCompaction({ session: stubSession(), gauge: gaugeOf(79_999, 200_000), ...IDLE }).compaction.reason, COMPACTION_REASONS.BELOW_THRESHOLD);
  assert.equal(autoCompaction({ session: stubSession(), gauge: gaugeOf(80_000, 200_000), ...IDLE }).compaction.state, COMPACTION_STATES.COMPACT);
  // An empty window is a reading and not a hole: nothing to compact.
  assert.equal(autoCompaction({ session: stubSession(), gauge: gaugeOf(0, 200_000), ...IDLE }).compaction.reason, COMPACTION_REASONS.BELOW_THRESHOLD);
});

test('a project that set no threshold never compacts by itself', () => {
  const session = stubSession({ policy: { compaction_threshold: null } });
  const full = autoCompaction({ session, gauge: gaugeOf(200_000, 200_000), ...IDLE });
  assert.equal(full.ok, true);
  assert.equal(full.compaction.state, COMPACTION_STATES.HOLD);
  assert.equal(full.compaction.reason, COMPACTION_REASONS.NO_THRESHOLD);
  assert.equal(full.compaction.command, null);
  // The threshold is asked before the reading is: nothing was measured here
  // either, and "this project compacts nothing automatically" is the reason a
  // screen should give.
  assert.equal(autoCompaction({ session, gauge: null, ...IDLE }).compaction.reason, COMPACTION_REASONS.NO_THRESHOLD);
  assert.equal(autoCompaction({ session, ...IDLE }).compaction.reason, COMPACTION_REASONS.NO_THRESHOLD);
});

test('no reading is a hold, and a gauge this module did not build is refused', () => {
  for (const none of [undefined, null]) {
    const held = autoCompaction({ session: stubSession(), gauge: none, ...IDLE });
    assert.equal(held.ok, true, 'a caller with no reading is the state Z1 calls unavailable, not a defect');
    assert.equal(held.compaction.state, COMPACTION_STATES.HOLD);
    assert.equal(held.compaction.reason, COMPACTION_REASONS.NO_READING);
    assert.equal(held.compaction.command, null);
  }
  // Z1's own `unavailable` gauge is the same answer as no gauge at all.
  const unavailable = contextGauge({ session: stubSession() }).gauge;
  assert.equal(unavailable.state, GAUGE_STATES.UNAVAILABLE);
  assert.equal(autoCompaction({ session: stubSession(), gauge: unavailable, ...IDLE }).compaction.reason, COMPACTION_REASONS.NO_READING);
  // Present and unusable is a defect in the wiring: refused, never repaired.
  for (const bad of ['40%', { used: 1 }, 40, [], { ...gaugeOf(1, 2), percent: 99 }]) {
    const answer = autoCompaction({ session: stubSession(), gauge: bad, ...IDLE });
    assert.equal(answer.ok, false, `${JSON.stringify(bad)} was read as a gauge`);
    assert.equal(answer.code, COMPACTION_CODES.BAD_GAUGE);
    assert.equal(answer.field, 'gauge');
  }
});

test('a session that is not a port is refused with the port\'s own code', () => {
  for (const notASession of ['stub-project', null, []]) {
    assert.throws(() => compactButton({ session: notASession, ...IDLE }), (error) => {
      assert.ok(error instanceof SessionPortError);
      assert.equal(error.code, PORT_CODES.NOT_A_SESSION);
      return true;
    });
  }
  assert.throws(() => autoCompaction({ session: { project_id: STUB_PROJECT_ID }, ...IDLE }), (error) => {
    assert.equal(error.code, PORT_CODES.MISSING_MEMBER);
    return true;
  });
});

test('the decision names the session it is about', () => {
  const other = autoCompaction({
    session: stubSession({ project_id: 'other-project' }),
    gauge: gaugeOf(160_000, 200_000),
    ...IDLE,
  });
  assert.equal(other.compaction.project_id, 'other-project');
  assert.equal(other.compaction.state, COMPACTION_STATES.COMPACT);
  assert.equal(compactButton({ session: stubSession({ project_id: 'other-project' }), ...IDLE }).compaction.project_id, 'other-project');
});

test('a decision is frozen, and its key set is the whole shape', () => {
  const { compaction } = compactButton({ session: stubSession(), ...IDLE });
  assert.ok(Object.isFrozen(compaction));
  assert.deepEqual(Object.keys(compaction).sort(), ['command', 'project_id', 'reason', 'state']);
  assert.ok(isCompaction(compaction));
  assert.ok(isCompaction(autoCompaction({ session: stubSession(), gauge: gaugeOf(1, 2), ...IDLE }).compaction));
});

test('compactionCommand refuses what this module did not build', () => {
  const { compaction } = compactButton({ session: stubSession(), ...IDLE });
  const held = autoCompaction({ session: stubSession(), gauge: gaugeOf(1, 10), ...IDLE }).compaction;
  assert.equal(compactionCommand(compaction), COMPACT_COMMAND);
  assert.equal(compactionCommand(held), null);
  const cases = [
    {},
    null,
    COMPACT_COMMAND,
    { ...compaction, command: null },
    { ...compaction, reason: COMPACTION_REASONS.NO_READING },
    { ...compaction, state: COMPACTION_STATES.HOLD },
    { ...held, command: COMPACT_COMMAND },
    { ...held, reason: null },
    { ...held, reason: 'somewhere-else' },
    { ...compaction, [Symbol('extra')]: true },
    { ...compaction, extra: true },
    { ...compaction, project_id: '' },
  ];
  for (const bad of cases) {
    assert.equal(isCompaction(bad), false, `${JSON.stringify(bad)} was accepted as a decision`);
    assert.throws(() => compactionCommand(bad), TypeError);
  }
});
