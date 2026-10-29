// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The offline gate's decision (mobile plan, Task P6.10; D167, G21).
//
// The whole of this file is one pure function over two observations, and the
// plan's verification is written as three things the client must not do. This is
// the first half of it: what the refusal IS, and which reason it gives. The
// second half -- that nothing is written, that an action is never queued and
// that no write is ever handed to the SDK's offline queue -- is in
// `client.test.js`, where there is a store to inspect.
//
// The cases below are the edges D167 leaves to the code rather than to the
// ruling: an answer that is not `true`, a presence nobody marked verified, a
// state P1.4 does not have. Every one of them refuses, because a gate that
// guesses "probably fine" is the gate G21 describes.

import assert from 'node:assert/strict';
import test from 'node:test';

import { STATE } from '../../../shared/presence/index.js';
import {
  GATE_CODES,
  OFFLINE_SPEECH,
  evaluateConnection,
  evaluateGate,
  offlineSpeech,
} from './gate.js';

/** A presence object in the shape `client.js`'s `presenceOf` returns. */
function presence(state, verified = true) {
  return { state, verified, reason: 'fresh', ageMs: 1_000, agents: null };
}

test('a connected phone that has observed nothing refuses, and says which nothing it is', () => {
  // **The review's M2.** `null` is the absence of an observation, and the gate
  // fails closed on it: no observation = offline, with its own code so that the
  // owner is told "I have not heard from it yet" rather than a claim that it is
  // down. A connected phone with a live heartbeat is the only thing that sends.
  for (const presence of [null, undefined]) {
    assert.deepEqual(evaluateGate({ online: true, presence }), {
      ok: false,
      code: GATE_CODES.CONSUMER_UNOBSERVED,
      field: 'presence',
    });
  }
  // `presence` left out entirely is the same input as `null`, not a licence:
  // the default is a refusal, the way the connection's is.
  assert.deepEqual(evaluateGate({ online: true }), {
    ok: false,
    code: GATE_CODES.CONSUMER_UNOBSERVED,
    field: 'presence',
  });
  assert.deepEqual(evaluateGate(), { ok: false, code: GATE_CODES.NO_CONNECTION, field: 'connection' });
  // And the observation is enough on its own: the connection is still asked
  // first, so an offline phone with a live heartbeat refuses as a tunnel.
  assert.deepEqual(evaluateGate({ online: true, presence: presence(STATE.LIVE) }), { ok: true });
});

test('the connection half can be asked on its own: it is what flush and poll ask', () => {
  // `flush` and `poll` re-send and read; neither has an observation question to
  // ask (presence is what a poll MAKES), and asking it would refuse every pass
  // on a phone that had not polled -- the circle. So the two halves are two
  // functions, and this one answers about the connection alone: no presence, no
  // refusal it did not come for.
  for (const online of [true]) {
    assert.deepEqual(evaluateConnection(online), { ok: true });
  }
  for (const online of [false, undefined, null, 0, 1, 'online', {}, [], Number.NaN]) {
    assert.deepEqual(
      evaluateConnection(online),
      { ok: false, code: GATE_CODES.NO_CONNECTION, field: 'connection' },
      String(online),
    );
  }
  assert.deepEqual(evaluateConnection(), { ok: false, code: GATE_CODES.NO_CONNECTION, field: 'connection' });
  // The two agree about the connection, which is the one thing they share.
  for (const online of [true, false, undefined]) {
    const whole = evaluateGate({ online, presence: presence(STATE.LIVE) });
    const half = evaluateConnection(online);
    assert.equal(whole.ok, half.ok, String(online));
    if (!half.ok) assert.deepEqual(whole, half, String(online));
  }
  assert.equal(Object.isFrozen(evaluateConnection(true)), true);
});

test('a live, verified consumer may be sent to, and nothing else may', () => {
  assert.deepEqual(evaluateGate({ online: true, presence: presence(STATE.LIVE) }), { ok: true });
  assert.deepEqual(
    evaluateGate({ online: true, presence: presence(STATE.STALE) }),
    { ok: false, code: GATE_CODES.CONSUMER_STALE, field: 'presence' },
  );
  assert.deepEqual(
    evaluateGate({ online: true, presence: presence(STATE.OFFLINE) }),
    { ok: false, code: GATE_CODES.CONSUMER_OFFLINE, field: 'presence' },
  );
  // P1.4's fourth state has its OWN code rather than being folded into
  // `consumer-offline`: a phone whose clock disagrees with the server's is not
  // a phone whose orchestrator is down, and the consumer refuses a record
  // stamped ahead of its clock (P6.7), so the owner is told the true thing.
  assert.deepEqual(
    evaluateGate({ online: true, presence: presence(STATE.CLOCK_DIFFERS) }),
    { ok: false, code: GATE_CODES.CONSUMER_CLOCK_DIFFERS, field: 'presence' },
  );
});

test('no connection refuses whatever the consumer looks like', () => {
  // The connection is asked FIRST, and it is the SDK's own flag: a write with no
  // connection is one the SDK holds and sends on reconnect (G21). A live
  // consumer does not make an offline write sendable.
  for (const state of [null, presence(STATE.LIVE), presence(STATE.STALE), presence(STATE.OFFLINE)]) {
    assert.deepEqual(
      evaluateGate({ online: false, presence: state }),
      { ok: false, code: GATE_CODES.NO_CONNECTION, field: 'connection' },
      String(state),
    );
  }
});

test('an answer that is not `true` is offline, and a gate that guesses is not a gate', () => {
  // A port that is missing, wired wrong or throwing its state away must refuse
  // to send. `isOnline()` returning `undefined` -- the shape of a port whose
  // adapter was never filled in -- is the case that matters.
  for (const online of [undefined, null, 0, 1, 'online', {}, [], Number.NaN]) {
    const verdict = evaluateGate({ online, presence: presence(STATE.LIVE) });
    assert.equal(verdict.ok, false, String(online));
    assert.equal(verdict.code, GATE_CODES.NO_CONNECTION, String(online));
  }
});

test('a presence that is not evidence of a live consumer refuses', () => {
  // `verified` is P1.4's own silence: its rule says nothing about whether the
  // heartbeat was the consumer's, and the client reports an unverified heartbeat
  // as `offline`. A hand-built presence with the flag missing must not be read
  // as a live consumer, and neither must a state string nobody wrote.
  for (const bad of [
    { state: STATE.LIVE },
    { state: STATE.LIVE, verified: false },
    { state: STATE.LIVE, verified: 'true' },
    { state: 'something-else', verified: true },
    { state: null, verified: true },
    { verified: true },
  ]) {
    const verdict = evaluateGate({ online: true, presence: bad });
    assert.equal(verdict.ok, false, JSON.stringify(bad));
    assert.equal(verdict.code, GATE_CODES.CONSUMER_OFFLINE, JSON.stringify(bad));
  }
  // Not a presence object at all is the same answer: nothing about it is
  // evidence that anything is running.
  for (const bad of ['live', 42, [], true]) {
    const verdict = evaluateGate({ online: true, presence: bad });
    assert.equal(verdict.ok, false, String(bad));
    assert.equal(verdict.code, GATE_CODES.CONSUMER_OFFLINE, String(bad));
  }
});

test('every refusal is a code, and never a state', () => {
  // A caller switches on `code` over `GATE_CODES` and over P1.4's `STATE`, and
  // the two lists must not collide: `offline` is a state and `consumer-offline`
  // is a reason, and the same string for both would make a screen that renders
  // whichever it saw first.
  const states = Object.values(STATE);
  const codes = Object.values(GATE_CODES);
  assert.equal(new Set(codes).size, codes.length, 'the codes are distinct');
  for (const code of codes) {
    assert.equal(states.includes(code), false, code);
  }
  for (const code of codes) {
    assert.equal(typeof OFFLINE_SPEECH[code], 'string', code);
    assert.notEqual(OFFLINE_SPEECH[code], '', code);
    // Every sentence says the same two things D167 asks for: that nothing was
    // sent, and that the owner is being told.
    assert.equal(OFFLINE_SPEECH[code].includes('not sent'), true, code);
  }
});

test('a sentence is looked up, not invented', () => {
  assert.equal(offlineSpeech(GATE_CODES.NO_CONNECTION), OFFLINE_SPEECH[GATE_CODES.NO_CONNECTION]);
  // A code the table does not carry answers `null`, so a screen shows nothing
  // rather than a sentence nobody wrote.
  for (const unknown of ['nonsense', '', undefined, null, 42, STATE.OFFLINE]) {
    assert.equal(offlineSpeech(unknown), null, String(unknown));
  }
  assert.equal(Object.hasOwn(OFFLINE_SPEECH, 'toString'), false, 'the table has no inherited keys');
});

test('the verdict is frozen, like everything else the client hands out', () => {
  const allowed = evaluateGate({ online: true });
  const refused = evaluateGate({ online: false });
  assert.equal(Object.isFrozen(allowed), true);
  assert.equal(Object.isFrozen(refused), true);
  assert.equal(Object.isFrozen(OFFLINE_SPEECH), true);
  assert.equal(Object.isFrozen(GATE_CODES), true);
});
