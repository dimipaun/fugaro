// The front-agent turn's refusals, and the claims about them that hold whatever
// the input is (mobile plan, Task P5.4.4).
//
// Two kinds of test here, because they answer different questions.
//
//   * **The sweep.** Every code in `REFUSAL` is *reached* by a probe below, and
//     the table plus the transition table together account for exactly the codes
//     the module declares. A refusal code no input can reach is a code nobody
//     can act on; a code the module answers with that is in neither table is one
//     the caller has never been told about.
//   * **The shapes.** What each refusal is *for*: a field read through a getter
//     is never called, an id that is not a database key cannot be stored, an
//     utterance with an invisible format character is refused because what is
//     shown must be what is judged, and a `now` outside its window is refused
//     rather than clamped.

import assert from 'node:assert/strict';
import test from 'node:test';

import { INPUT_REFUSALS, MIN_TTL_MS, REFUSAL, ROLE, TRANSITIONS, conversation, turn } from './index.js';

const T0 = 1_700_000_000_000;
const MINUTE = 60 * 1000;
const REPO = Object.freeze({ repo: 'dimipaun/fugaro' });
const BRIEF = '# Task: docs refactor\nMove the guide.';
const GO = Object.freeze({ actionId: 'act_1', confirmationId: 'cnf_1' });

function said(id, text, at, extra = {}) {
  return { id, role: ROLE.PERSON, text, at, ...extra };
}

function replied(id, at, text, brief = null) {
  return { id, role: ROLE.AGENT, text, at, brief };
}

function ready() {
  return [said('utt_1', 'plan it', T0, { target: REPO }), replied('rep_1', T0 + MINUTE, 'draft', BRIEF)];
}

function go(at = T0 + 2 * MINUTE) {
  return said('utt_go', 'Fugaro, confirm', at, GO);
}

/** A transcript of `count` person turns, each with its own id and stamp. */
function many(count) {
  const turns = [];
  for (let index = 0; index < count; index += 1) {
    turns.push(said(`utt_${index}`, 'one more', T0 + index));
  }
  return turns;
}

function revoked() {
  const { proxy, revoke } = Proxy.revocable({}, {});
  revoke();
  return proxy;
}

// Every code, and one input that reaches it. The keys are checked against
// REFUSAL below in both directions, so a code that is added to the module
// without a probe -- or a probe left behind by a code that was removed -- fails.
const PROBES = {
  'not-an-object': () => turn([]),
  'unknown-field': () => turn({ transcript: [], now: T0, mode: 'pro' }),
  'unexpected-field': () => turn({
    transcript: [said('utt_1', 'x', T0), { id: 'rep_1', role: ROLE.AGENT, text: 'x', at: T0 + 1, brief: null, target: REPO }],
    reply: null,
    now: T0 + MINUTE,
  }),
  'missing-field': () => turn({ transcript: [...ready(), go()], reply: null, now: T0 + 3 * MINUTE }),
  'accessor-property': () => {
    const input = { transcript: [] };
    Object.defineProperty(input, 'now', { get: () => T0, enumerable: true });
    return turn(input);
  },
  'internal-error': () => turn(revoked()),
  'wrong-type': () => turn({ transcript: [said(5, 'x', T0)], reply: null, now: T0 + MINUTE }),
  'empty-string': () => turn({ transcript: [said('', 'x', T0)], reply: null, now: T0 + MINUTE }),
  'too-long': () => turn({ transcript: [said('x'.repeat(101), 'x', T0)], reply: null, now: T0 + MINUTE }),
  'bad-key': () => turn({ transcript: [said('a/b', 'x', T0)], reply: null, now: T0 + MINUTE }),
  'bad-character': () => turn({ transcript: [said('utt_1', 'con​firm', T0)], reply: null, now: T0 + MINUTE }),
  'invalid-now': () => turn({ transcript: ready(), reply: null, now: 'yesterday' }),
  'unknown-role': () => turn({ transcript: [{ id: 'utt_1', role: 'robot', text: 'x', at: T0 }], reply: null, now: T0 + MINUTE }),
  'invalid-transcript': () => turn({ transcript: 'one two three', reply: null, now: T0 + MINUTE }),
  'empty-transcript': () => turn({ transcript: [], reply: { id: 'rep_1', text: 'x', brief: null }, now: T0 + MINUTE }),
  'too-many-turns': () => turn({ transcript: many(65), reply: null, now: T0 + MINUTE }),
  'replayed-turn': () => turn({ transcript: [said('utt_1', 'x', T0), said('utt_1', 'y', T0 + 1)], reply: null, now: T0 + MINUTE }),
  'unsorted-transcript': () => turn({ transcript: [said('utt_1', 'x', T0 + 10), said('utt_2', 'y', T0)], reply: null, now: T0 + MINUTE }),
  'invalid-timeout': () => turn({ transcript: ready(), reply: null, now: T0 + MINUTE, options: { ttlMs: 0 } }),
  'invalid-max-turns': () => turn({ transcript: ready(), reply: null, now: T0 + MINUTE, options: { maxTurns: 1000 } }),
  'invalid-max-brief': () => turn({ transcript: ready(), reply: null, now: T0 + MINUTE, options: { maxBrief: 0 } }),
  'invalid-mode': () => turn({ transcript: ready(), reply: null, now: T0 + MINUTE, options: { mode: '' } }),
  'invalid-costly-modes': () => turn({ transcript: ready(), reply: null, now: T0 + MINUTE, options: { costlyModes: 'pro' } }),
  'bad-repo': () => turn({ transcript: [said('utt_1', 'x', T0, { target: { repo: 'nodash' } })], reply: null, now: T0 + MINUTE }),
  'bad-brief': () => turn({ transcript: [said('utt_1', 'x', T0, { target: REPO })], reply: { id: 'rep_1', text: 'x', brief: 'no marker' }, now: T0 + MINUTE }),
  'not-agent-text': () => turn({ transcript: [...ready(), said('utt_2', 'Fugaro, list agents', T0 + 2 * MINUTE)], reply: null, now: T0 + 3 * MINUTE }),
  'not-understood': () => turn({ transcript: [...ready(), said('utt_2', 'Fugaro, twiddle', T0 + 2 * MINUTE)], reply: null, now: T0 + 3 * MINUTE }),
  'command-and-go': () => turn({ transcript: [...ready(), said('utt_2', 'Fugaro, confirm it', T0 + 2 * MINUTE)], reply: null, now: T0 + 3 * MINUTE }),
  'missing-reply': () => turn({ transcript: [said('utt_1', 'x', T0)], reply: null, now: T0 + MINUTE }),
  'unexpected-reply': () => turn({ transcript: [...ready(), go()], reply: { id: 'rep_2', text: 'x', brief: null }, now: T0 + 3 * MINUTE, options: { mode: 'pro' } }),
  'conversation-closed': () => turn({ transcript: [...ready(), go(), said('utt_3', 'more', T0 + 3 * MINUTE)], reply: { id: 'rep_3', text: 'x', brief: null }, now: T0 + 4 * MINUTE, options: { mode: 'pro' } }),
  'replayed-go': () => turn({ transcript: [...ready(), go(), said('utt_go2', 'Fugaro, confirm', T0 + 3 * MINUTE, { actionId: 'act_2', confirmationId: 'cnf_2' })], reply: null, now: T0 + 4 * MINUTE, options: { mode: 'pro' } }),
  'unresolved-target': () => turn({ transcript: [said('utt_1', 'x', T0), replied('rep_1', T0 + 1, 'x', BRIEF), go()], reply: null, now: T0 + 3 * MINUTE, options: { mode: 'pro' } }),
  'no-brief': () => turn({ transcript: [said('utt_1', 'x', T0, { target: REPO }), replied('rep_1', T0 + 1, 'x', null), go()], reply: null, now: T0 + 3 * MINUTE, options: { mode: 'pro' } }),
  'go-expired': () => turn({ transcript: [...ready(), go(T0 + 11 * MINUTE)], reply: null, now: T0 + 11 * MINUTE, options: { mode: 'pro', ttlMs: 10 * MINUTE } }),
};

test('every code the module declares has a probe, and every probe is reached', () => {
  assert.deepEqual(Object.keys(REFUSAL).map((name) => REFUSAL[name]).sort(), Object.keys(PROBES).sort());
  for (const [code, probe] of Object.entries(PROBES)) {
    const result = probe();
    assert.equal(result.ok, false, `${code} was not refused`);
    assert.equal(result.code, code);
  }
});

test('every code is either an input refusal or declared in a transition cell', () => {
  const declared = new Set(INPUT_REFUSALS);
  for (const phase of Object.values(TRANSITIONS)) {
    for (const event of Object.values(phase)) {
      for (const code of event.no) declared.add(code);
    }
  }
  for (const name of Object.keys(REFUSAL)) {
    assert.ok(declared.has(REFUSAL[name]), `${name} is in neither table`);
  }
  for (const code of INPUT_REFUSALS) {
    assert.ok(Object.values(REFUSAL).includes(code), `${code} is not a code`);
  }
});

test('a refusal is frozen, and carries no transcript', () => {
  for (const probe of Object.values(PROBES)) {
    const result = probe();
    assert.ok(Object.isFrozen(result));
    assert.equal(result.transcript, undefined);
    assert.deepEqual(Object.keys(result).sort(), ['code', 'field', 'ok']);
  }
});

test('a field read through a getter is refused without ever being called', () => {
  let called = false;
  const input = { transcript: [] };
  Object.defineProperty(input, 'now', {
    get() {
      called = true;
      return T0;
    },
  });
  const result = turn(input);
  assert.equal(result.code, 'accessor-property');
  assert.equal(called, false);
});

test('an utterance with an invisible format character is refused, not routed', () => {
  const result = turn({ transcript: [said('utt_1', 'Fugaro, confir​m', T0)], reply: null, now: T0 + MINUTE });
  assert.equal(result.code, 'bad-character');
});

test('an utterance with no words in it is refused rather than forwarded', () => {
  const result = turn({ transcript: [said('utt_1', '  ...  ', T0)], reply: null, now: T0 + MINUTE });
  assert.equal(result.code, 'not-understood');
});

test('an agent reply with nothing to reply to is not a conversation', () => {
  const result = turn({ transcript: [replied('rep_1', T0, 'x', BRIEF)], reply: null, now: T0 + MINUTE });
  assert.equal(result.code, 'invalid-transcript');
  assert.equal(result.field, 'transcript[0].role');
});

test('a repository that is not owner/name is refused, including one that could be read as a flag', () => {
  for (const repo of ['nodash', 'a/b/c', '', '-o/name', '.o/name', 'o/-name', `${'x'.repeat(101)}/y`, 'o/na me']) {
    const result = turn({ transcript: [said('utt_1', 'x', T0, { target: { repo } })], reply: null, now: T0 + MINUTE });
    assert.equal(result.code, 'bad-repo', repo);
    assert.equal(result.field, 'target.repo', repo);
  }
});

test('a target that is not the target shape is refused rather than read', () => {
  for (const target of ['dimipaun/fugaro', { repo: 'dimipaun/fugaro', extra: 1 }, { name: 'x' }, []]) {
    const result = turn({ transcript: [said('utt_1', 'x', T0, { target })], reply: null, now: T0 + MINUTE });
    assert.equal(result.code, 'bad-repo', JSON.stringify(target));
  }
});

test('the repository is bounded, and its bound is P6.3’s target bound', () => {
  const reply = { id: 'rep_1', text: 'draft', brief: BRIEF };
  const repo = `${'o'.repeat(100)}/${'n'.repeat(99)}`;
  assert.equal(turn({ transcript: [said('utt_1', 'x', T0, { target: { repo } })], reply, now: T0 + MINUTE }).ok, true);
  const over = `${'o'.repeat(100)}/${'n'.repeat(100)}`;
  assert.equal(turn({ transcript: [said('utt_1', 'x', T0, { target: { repo: over } })], reply, now: T0 + MINUTE }).code, 'bad-repo');
});

test('the transcript is bounded by maxTurns, and the caller may move the bound', () => {
  const reply = { id: 'rep_9', text: 'ok', brief: null };
  assert.equal(turn({ transcript: many(3), reply, now: T0 + MINUTE }).ok, true);
  assert.equal(turn({ transcript: many(3), reply, now: T0 + MINUTE, options: { maxTurns: 3 } }).ok, true);
  assert.equal(turn({ transcript: many(4), reply, now: T0 + MINUTE, options: { maxTurns: 3 } }).code, 'too-many-turns');
});

test('a brief is bounded by maxBrief, and the caller may lower the bound', () => {
  const reply = { id: 'rep_1', text: 'draft', brief: BRIEF };
  const transcript = [said('utt_1', 'x', T0, { target: REPO })];
  assert.equal(turn({ transcript, reply, now: T0 + MINUTE, options: { maxBrief: 5 } }).code, 'too-long');
  assert.equal(turn({ transcript, reply, now: T0 + MINUTE, options: { maxBrief: BRIEF.length } }).ok, true);
});

test('the timeout is bounded on both sides, and honoured inside them', () => {
  const transcript = ready();
  assert.equal(turn({ transcript, reply: null, now: T0 + MINUTE, options: { ttlMs: MIN_TTL_MS } }).ok, true);
  assert.equal(turn({ transcript, reply: null, now: T0 + MINUTE, options: { ttlMs: 60 * 1000 } }).ok, true);
  assert.equal(turn({ transcript, reply: null, now: T0 + MINUTE, options: { ttlMs: MIN_TTL_MS - 1 } }).code, 'invalid-timeout');
  assert.equal(turn({ transcript, reply: null, now: T0 + MINUTE, options: { ttlMs: 1.5 } }).code, 'invalid-timeout');
  assert.equal(turn({ transcript, reply: null, now: T0 + MINUTE, options: { ttlMs: -0 } }).code, 'invalid-timeout');
});

test('the session’s costly modes are read, not assumed, and a list that cannot be read is refused', () => {
  const transcript = [...ready(), go()];
  const now = T0 + 3 * MINUTE;
  const costly = turn({ transcript, reply: null, now, options: { mode: 'poc', costlyModes: ['poc'] } });
  assert.equal(costly.ok, true);
  assert.equal(costly.costly, true, 'the session named poc costly, so it is');
  const plain = turn({ transcript, reply: null, now, options: { mode: 'poc' } });
  assert.equal(plain.costly, false, 'the default list does not name poc');
  for (const costlyModes of [['pro', 'pro'], [''], [5], 'pro']) {
    assert.equal(turn({ transcript, reply: null, now, options: { mode: 'pro', costlyModes } }).code, 'invalid-costly-modes');
  }
});

test('a go whose mode is longer than a mode may be is refused', () => {
  const result = turn({ transcript: [...ready(), go()], reply: null, now: T0 + 3 * MINUTE, options: { mode: 'm'.repeat(65) } });
  assert.equal(result.code, 'invalid-mode');
});

test('a start in a costly mode asks for the biometric only when the session says so', () => {
  const transcript = [...ready(), go()];
  const readings = [
    { biometricOnCostly: true, biometric: true },
    { biometricOnCostly: false, biometric: false },
    { biometricOnCostly: 'yes', biometric: null },
    { biometricOnCostly: undefined, biometric: false },
  ];
  for (const reading of readings) {
    const options = { mode: 'pro' };
    if (reading.biometricOnCostly !== undefined) options.biometricOnCostly = reading.biometricOnCostly;
    const result = turn({ transcript, reply: null, now: T0 + 3 * MINUTE, options });
    if (reading.biometric === null) {
      assert.equal(result.code, 'wrong-type', String(reading.biometricOnCostly));
      continue;
    }
    assert.equal(result.biometric, reading.biometric, String(reading.biometricOnCostly));
  }
});

test('the conversation view refuses the same inputs as the turn, and names a reply it does not read', () => {
  assert.equal(conversation({ transcript: ready(), now: T0 + MINUTE }).ok, true);
  assert.equal(conversation({ transcript: ready(), now: T0 + MINUTE, reply: { id: 'rep_2', text: 'x', brief: null } }).code, 'unknown-field');
  assert.equal(conversation({ transcript: [], now: T0 }).ok, true);
  assert.equal(conversation({ transcript: ready(), now: T0 }).code, 'invalid-now');
});

test('a conversation with no turns has no deadline and cannot be expired', () => {
  const view = conversation({ transcript: [], now: T0 });
  assert.equal(view.phase, 'idle');
  assert.equal(view.deadline, null);
  assert.equal(view.expired, false);
  assert.equal(view.readBack, null);
  assert.equal(view.target, null);
  assert.equal(view.brief, null);
});

test('the deadline is the first turn’s stamp, not the last', () => {
  const view = conversation({ transcript: [said('utt_1', 'x', T0, { target: REPO }), replied('rep_1', T0 + 5 * MINUTE, 'x', BRIEF)], now: T0 + 6 * MINUTE });
  assert.equal(view.deadline, T0 + view.ttlMs);
});
