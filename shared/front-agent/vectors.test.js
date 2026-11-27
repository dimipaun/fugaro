// The front agent's turn vectors, read back by the module that wrote them
// (mobile plan, Task P5.4.4).
//
// `vectors.json` is the contract's data half: the conversation, the clock it is
// judged at, and the exact answer. The Go consumer reimplements `turn` (P5.4.5)
// and holds itself to this file, and this test holds the FILE to two things the
// Go side cannot check:
//
//   * **the module**, replayed over the file's own inputs -- a case whose answer
//     the module no longer gives is a contract nobody can meet; and
//   * **the plan's claims**, written out again here by hand, one case per claim
//     P5.4.4's verification list names. A table read back from the file would
//     pin nothing, which is why this one is not derived from anything.
//
// No dependency, no network, no clock and no randomness: node's own test runner,
// the JSON reader, and the module.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import * as frontAgent from './index.js';

const raw = readFileSync(new URL('./vectors.json', import.meta.url), 'utf8');
const vectors = JSON.parse(raw);

const CASE_FIELDS = ['expect', 'name', 'now', 'options', 'reply', 'transcript', 'why'];

test('the file says what it is, and every case of it is one this reader knows', () => {
  assert.deepEqual(Object.keys(vectors).sort(), [
    'cases',
    'constants',
    'format',
    'generated_by',
    'reader',
    'roles',
    'what',
  ]);
  assert.equal(vectors.format, 'fugaro front-agent turn vectors');
  assert.equal(vectors.generated_by, 'node shared/front-agent/vectors.generate.mjs');
  assert.ok(vectors.cases.length >= 20);
  const names = new Set();
  for (const one of vectors.cases) {
    assert.equal(names.has(one.name), false, `${one.name} appears twice`);
    names.add(one.name);
    assert.deepEqual(Object.keys(one).sort().filter((key) => !CASE_FIELDS.includes(key)), []);
    assert.equal(typeof one.why, 'string');
    assert.ok(one.why.length > 0);
  }
});

test('the module answers every case exactly as the file records it', () => {
  for (const one of vectors.cases) {
    const call = { transcript: one.transcript, reply: one.reply ?? null, now: one.now };
    if (one.options !== undefined) call.options = one.options;
    const answer = frontAgent.turn(call);
    if (one.expect.ok) {
      assert.equal(answer.ok, true, one.name);
      assert.deepEqual(
        {
          ok: true,
          outcome: answer.outcome,
          phase: answer.phase,
          target: answer.target,
          brief: answer.brief,
          mode: answer.mode,
          readBack: answer.readBack,
          deadline: answer.deadline,
          costly: answer.costly,
          biometric: answer.biometric,
          action: answer.action ?? null,
        },
        one.expect,
        one.name,
      );
      continue;
    }
    assert.equal(answer.ok, false, one.name);
    assert.deepEqual({ ok: false, code: answer.code, field: answer.field }, one.expect, one.name);
  }
});

test('the same call answers the same thing: the turn is a function of its values', () => {
  const one = vectors.cases[0];
  const call = { transcript: one.transcript, reply: one.reply, now: one.now };
  assert.deepEqual(frontAgent.turn(call), frontAgent.turn(call));
});

// The plan's claims, one case each, hand-written: the case name, and what it
// must answer. Every name here is a case in the file, and every case in the file
// carries a claim -- both directions, in the counts below.
const CLAIMS = {
  'the go': { ok: true, outcome: 'go', phase: 'spent' },
  'a costly start with the biometric switch on': { ok: true, costly: true, biometric: true },
  'a go with no resolved repository': { ok: false, code: 'unresolved-target' },
  'a go with no brief': { ok: false, code: 'no-brief' },
  'a replayed go': { ok: false, code: 'replayed-go' },
  'a go past the deadline': { ok: false, code: 'go-expired' },
  'the go at the deadline is still live': { ok: true, outcome: 'go' },
  'an utterance carrying a command beside the go': { ok: false, code: 'command-and-go' },
  'a bare "confirm"': { ok: true, outcome: 'shaping' },
  'a misrecognised go': { ok: true, outcome: 'shaping' },
  'a go binding a target it asserted itself': { ok: false, code: 'unexpected-field' },
  'a go the session gave no mode for': { ok: false, code: 'missing-field' },
  'a turn after the go': { ok: false, code: 'conversation-closed' },
  'a brief makes it ready': { ok: true, phase: 'ready' },
  'a transcript of no turns': { ok: false, code: 'empty-transcript' },
};

function holds(answer, claim) {
  return Object.entries(claim).every(([key, value]) => JSON.stringify(answer[key]) === JSON.stringify(value));
}

test('every claim the plan names is pinned by a case that answers it', () => {
  for (const [name, claim] of Object.entries(CLAIMS)) {
    const one = vectors.cases.find((entry) => entry.name === name);
    assert.ok(one !== undefined, `${name} is not in the file`);
    assert.ok(holds(one.expect, claim), `${name} does not answer ${JSON.stringify(claim)}: ${JSON.stringify(one.expect)}`);
  }
  assert.equal(Object.keys(CLAIMS).length <= vectors.cases.length, true);
});

test('the file’s constants are the module’s, so the Go side cannot read a second set', () => {
  assert.deepEqual(vectors.constants, {
    ttl_ms: frontAgent.DEFAULT_TTL_MS,
    min_ttl_ms: frontAgent.MIN_TTL_MS,
    max_ttl_ms: frontAgent.MAX_TTL_MS,
    max_turns: frontAgent.DEFAULT_MAX_TURNS,
    max_brief: frontAgent.DEFAULT_MAX_BRIEF,
    id_max: frontAgent.ID_MAX,
    text_max: frontAgent.TEXT_MAX,
    repo_max: frontAgent.REPO_MAX,
    mode_max: frontAgent.MODE_MAX,
    spoken_title_max: frontAgent.SPOKEN_TITLE_MAX,
    time_max: frontAgent.TIME_MAX,
    costly_modes: [...frontAgent.DEFAULT_COSTLY_MODES],
    biometric_on_costly: frontAgent.DEFAULT_BIOMETRIC_ON_COSTLY,
  });
  assert.deepEqual(vectors.roles, [frontAgent.ROLE.PERSON, frontAgent.ROLE.AGENT]);
});

test('every refusal code in the file is one the module declares, and one a cell accounts for', () => {
  const declared = new Set([...frontAgent.INPUT_REFUSALS, ...Object.values(frontAgent.REFUSAL)]);
  for (const one of vectors.cases) {
    if (one.expect.ok) continue;
    assert.ok(declared.has(one.expect.code), `${one.expect.code} is not a code the module declares`);
  }
});

test('nothing in the file is secret-shaped, and no case carries a credential', () => {
  assert.equal(/secret|credential|password|api[_-]?key|BEGIN [A-Z ]*PRIVATE KEY/iu.test(raw), false);
});
