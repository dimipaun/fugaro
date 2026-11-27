// The spec-shaping conversation's tests (mobile plan, Task P5.4.4).
//
// Everything here is a function over values: no clock, no file, no network, no
// database. `now` is always a number this file passes in, so every timeout below
// is reproducible and none of it can pass by accident at 3 a.m.
//
// The claims the plan names first are separate tests, because each is a
// different kind of guarantee:
//
//   * the function is PURE with an injected clock -- the same inputs answer the
//     same thing, nothing handed in is mutated, and the module reads no clock of
//     its own (the source scan below, and the `invalid-now` refusal);
//   * the "go" is recognised ONLY as its own utterance -- P6.2's fixed phrase
//     through P6.1's router and nothing else, and an utterance that is a command
//     beside it is refused rather than read as either;
//   * the transitions are TOTAL: a table written by hand here, never imported,
//     and every cell of it driven through literal transcripts.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import * as frontAgent from './index.js';
import { EVENT, OUTCOME, PHASE, ROLE, TRANSITIONS, conversation, turn } from './index.js';

const T0 = 1_700_000_000_000;
const MINUTE = 60 * 1000;
const HOUR = 60 * MINUTE;
const REPO = Object.freeze({ repo: 'dimipaun/fugaro' });
const BRIEF = '# Task: docs refactor\nMove the guide.';
const GO = Object.freeze({ actionId: 'act_1', confirmationId: 'cnf_1' });

/** A person's turn. */
function said(id, text, at, extra = {}) {
  return { id, role: ROLE.PERSON, text, at, ...extra };
}

/** The agent's reply to the turn before it. */
function replied(id, at, text, brief = null) {
  return { id, role: ROLE.AGENT, text, at, brief };
}

/** A ready conversation: the person named a repo, the agent drafted the brief. */
function ready() {
  return [said('utt_1', 'plan the docs refactor', T0, { target: REPO }), replied('rep_1', T0 + MINUTE, 'here is a draft', BRIEF)];
}

/** A shaping conversation: the repo is known and no brief has arrived. */
function shaping() {
  return [said('utt_1', 'plan the docs refactor', T0, { target: REPO }), replied('rep_1', T0 + MINUTE, 'what must it not touch?', null)];
}

/** The go: P6.2's fixed phrase, as P6.1 routes it. */
function go(at, ids = GO) {
  return said('utt_go', 'Fugaro, confirm', at, ids);
}

function refused(input) {
  const result = turn(input);
  assert.equal(result.ok, false, `expected a refusal, got ${JSON.stringify(result)}`);
  return result;
}

// ---------------------------------------------------------------------------
// The module's surface
// ---------------------------------------------------------------------------

test('the exported surface is an exact set: a new export is a decision', () => {
  assert.deepEqual(Object.keys(frontAgent).sort(), [
    'CONFIRM_CUE',
    'DEFAULT_BIOMETRIC_ON_COSTLY',
    'DEFAULT_COSTLY_MODES',
    'DEFAULT_MAX_BRIEF',
    'DEFAULT_MAX_TURNS',
    'DEFAULT_TTL_MS',
    'EVENT',
    'ID_MAX',
    'INPUT_REFUSALS',
    'MAX_MAX_BRIEF',
    'MAX_MAX_TURNS',
    'MAX_TTL_MS',
    'MIN_MAX_BRIEF',
    'MIN_MAX_TURNS',
    'MIN_TTL_MS',
    'MODE_MAX',
    'OUTCOME',
    'PHASE',
    'REFUSAL',
    'REPO_MAX',
    'ROLE',
    'TEXT_MAX',
    'TIME_MAX',
    'TRANSITIONS',
    'VERB',
    'conversation',
    'costlyStart',
    'readBack',
    'turn',
  ]);
});

// ---------------------------------------------------------------------------
// The brief is shaped
// ---------------------------------------------------------------------------

test('an utterance that is not an app command is agent text, and the reply becomes the next turn', () => {
  const result = turn({
    transcript: [said('utt_1', 'plan the docs refactor', T0, { target: REPO })],
    reply: { id: 'rep_1', text: 'what must it not touch?', brief: null },
    now: T0 + MINUTE,
  });
  assert.equal(result.ok, true);
  assert.equal(result.outcome, OUTCOME.SHAPING);
  assert.equal(result.phase, PHASE.SHAPING);
  assert.equal(result.target.repo, 'dimipaun/fugaro');
  assert.equal(result.brief, null);
  assert.deepEqual(result.transcript.map((one) => one.role), [ROLE.PERSON, ROLE.AGENT]);
  assert.equal(result.transcript[1].at, T0 + MINUTE);
});

test('a brief shaped by the reply makes the conversation ready, and the read-back names the repo', () => {
  const result = turn({ transcript: ready(), reply: null, now: T0 + 2 * MINUTE, options: { mode: 'pro' } });
  assert.equal(result.ok, true);
  assert.equal(result.outcome, OUTCOME.SHAPING);
  assert.equal(result.phase, PHASE.READY);
  assert.match(result.readBack, /dimipaun\/fugaro/u);
  assert.match(result.readBack, /docs refactor/u);
  assert.match(result.readBack, /Fugaro, confirm/u);
});

test('a person turn that named no repo leaves the target unresolved, and there is no read-back', () => {
  const result = turn({
    transcript: [said('utt_1', 'plan the docs refactor', T0)],
    reply: { id: 'rep_1', text: 'which repo?', brief: BRIEF },
    now: T0 + MINUTE,
    options: { mode: 'pro' },
  });
  assert.equal(result.ok, true);
  assert.equal(result.phase, PHASE.SHAPING);
  assert.equal(result.target, null);
  assert.equal(result.readBack, null);
});

test('a later turn that names no repo keeps the one already named: the target is sticky', () => {
  const transcript = [...ready(), said('utt_2', 'make it shorter', T0 + 2 * MINUTE)];
  const result = turn({
    transcript,
    reply: { id: 'rep_2', text: 'shorter', brief: '# Task: docs refactor\nShorter.' },
    now: T0 + 3 * MINUTE,
  });
  assert.equal(result.ok, true);
  assert.equal(result.phase, PHASE.READY);
  assert.equal(result.target.repo, 'dimipaun/fugaro');
});

test('a person turn may clear a target it could not resolve, and then the conversation is not ready', () => {
  const transcript = [...ready(), said('utt_2', 'use the other repo', T0 + 2 * MINUTE, { target: null })];
  const result = turn({
    transcript,
    reply: { id: 'rep_2', text: 'which one?', brief: BRIEF },
    now: T0 + 3 * MINUTE,
  });
  assert.equal(result.ok, true);
  assert.equal(result.phase, PHASE.SHAPING);
  assert.equal(result.target, null);
});

test('a reply that holds no brief puts a ready conversation back to shaping', () => {
  const transcript = [...ready(), said('utt_2', 'hold on', T0 + 2 * MINUTE)];
  const result = turn({ transcript, reply: { id: 'rep_2', text: 'sure', brief: null }, now: T0 + 3 * MINUTE });
  assert.equal(result.ok, true);
  assert.equal(result.phase, PHASE.SHAPING);
  assert.equal(result.brief, null);
});

test('a person turn with no reply yet is refused: the agent answers every turn', () => {
  const result = refused({ transcript: [said('utt_1', 'plan the docs refactor', T0)], now: T0 + MINUTE });
  assert.equal(result.code, 'missing-reply');
  assert.equal(result.field, 'reply');
});

test('a reply beside a go is refused: nothing is answered once the person said go', () => {
  const result = refused({
    transcript: [...ready(), go(T0 + 2 * MINUTE)],
    reply: { id: 'rep_2', text: 'too late', brief: null },
    now: T0 + 2 * MINUTE,
    options: { mode: 'pro' },
  });
  assert.equal(result.code, 'unexpected-reply');
});

test('a brief whose first line is not a task line is refused where it is written', () => {
  const result = refused({
    transcript: [said('utt_1', 'plan it', T0, { target: REPO })],
    reply: { id: 'rep_1', text: 'draft', brief: 'Move the guide.\n# Task: too late' },
    now: T0 + MINUTE,
  });
  assert.equal(result.code, 'bad-brief');
  assert.equal(result.field, 'reply.brief');
});

test('a brief over its bound is refused where it is written', () => {
  const result = refused({
    transcript: [said('utt_1', 'plan it', T0, { target: REPO })],
    reply: { id: 'rep_1', text: 'draft', brief: 'x'.repeat(4097) },
    now: T0 + MINUTE,
  });
  assert.equal(result.code, 'too-long');
  assert.equal(result.field, 'reply.brief');
});

// ---------------------------------------------------------------------------
// The "go"
// ---------------------------------------------------------------------------

test('the go is P6.2’s fixed phrase as its own utterance, and it authorises one launch', () => {
  const result = turn({
    transcript: [...ready(), go(T0 + 2 * MINUTE)],
    reply: null,
    now: T0 + 2 * MINUTE,
    options: { mode: 'pro' },
  });
  assert.equal(result.ok, true);
  assert.equal(result.outcome, OUTCOME.GO);
  assert.equal(result.phase, PHASE.SPENT);
  assert.deepEqual(result.action, {
    id: 'act_1',
    verb: 'launch',
    mode: 'pro',
    target: { repo: 'dimipaun/fugaro' },
    confirmation: { id: 'cnf_1', at: T0 + 2 * MINUTE },
  });
  assert.equal(result.brief, BRIEF);
  assert.equal(result.transcript.length, 3);
});

test('the go binds the target that was read back, never one the go asserted', () => {
  const result = refused({
    transcript: [...ready(), said('utt_go', 'Fugaro, confirm', T0 + 2 * MINUTE, { ...GO, target: { repo: 'someone/else' } })],
    reply: null,
    now: T0 + 2 * MINUTE,
    options: { mode: 'pro' },
  });
  assert.equal(result.code, 'unexpected-field');
  assert.equal(result.field, 'target');
});

test('a bare "confirm" is agent text: P6.1 forwards it, so it is a turn and not a go', () => {
  const transcript = [...ready(), said('utt_2', 'confirm', T0 + 2 * MINUTE)];
  const result = turn({ transcript, reply: { id: 'rep_2', text: 'did you mean to go?', brief: null }, now: T0 + 3 * MINUTE });
  assert.equal(result.ok, true);
  assert.equal(result.outcome, OUTCOME.SHAPING);
});

test('a misrecognised go is not a go: "go ahead", "yes", "confirmed it" and "conform" are agent text', () => {
  for (const text of ['go ahead', 'yes', 'confirmed it', 'conform']) {
    const transcript = [...ready(), said('utt_2', text, T0 + 2 * MINUTE)];
    const result = turn({ transcript, reply: { id: 'rep_2', text: 'ok', brief: null }, now: T0 + 3 * MINUTE });
    assert.equal(result.ok, true, text);
    assert.equal(result.outcome, OUTCOME.SHAPING, text);
  }
});

test('an utterance that is both a command and a go is refused', () => {
  const result = refused({
    transcript: [...ready(), said('utt_2', 'Fugaro, confirm it', T0 + 2 * MINUTE)],
    reply: null,
    now: T0 + 2 * MINUTE,
    options: { mode: 'pro' },
  });
  assert.equal(result.code, 'command-and-go');
});

test('an app command is not the front agent’s to read', () => {
  const result = refused({
    transcript: [...ready(), said('utt_2', 'Fugaro, list projects', T0 + 2 * MINUTE)],
    reply: null,
    now: T0 + 2 * MINUTE,
  });
  assert.equal(result.code, 'not-agent-text');
});

test('an unfinished wake-word utterance is refused: P6.1 never forwards it', () => {
  for (const text of ['Fugaro, twiddle the widgets', 'Fugaro, conform']) {
    const result = refused({
      transcript: [...ready(), said('utt_2', text, T0 + 2 * MINUTE)],
      reply: null,
      now: T0 + 2 * MINUTE,
    });
    assert.equal(result.code, 'not-understood', text);
  }
});

// ---------------------------------------------------------------------------
// The go's refusals, which are D128's amendment reused
// ---------------------------------------------------------------------------

test('a go whose conversation still holds an unresolved target is refused', () => {
  const transcript = [said('utt_1', 'plan the docs refactor', T0), replied('rep_1', T0 + MINUTE, 'draft', BRIEF), go(T0 + 2 * MINUTE)];
  const result = refused({ transcript, reply: null, now: T0 + 2 * MINUTE, options: { mode: 'pro' } });
  assert.equal(result.code, 'unresolved-target');
});

test('a go with no brief shaped is refused', () => {
  const transcript = [...shaping(), go(T0 + 2 * MINUTE)];
  const result = refused({ transcript, reply: null, now: T0 + 2 * MINUTE, options: { mode: 'pro' } });
  assert.equal(result.code, 'no-brief');
});

test('a replayed go authorises nothing the second time', () => {
  const first = turn({ transcript: [...ready(), go(T0 + 2 * MINUTE)], reply: null, now: T0 + 2 * MINUTE, options: { mode: 'pro' } });
  assert.equal(first.ok, true);
  const second = refused({
    transcript: [...first.transcript, said('utt_go2', 'Fugaro, confirm', T0 + 3 * MINUTE, { actionId: 'act_2', confirmationId: 'cnf_2' })],
    reply: null,
    now: T0 + 3 * MINUTE,
    options: { mode: 'pro' },
  });
  assert.equal(second.code, 'replayed-go');
});

test('a second go carrying the same action id is refused too: one go, one launch', () => {
  const spent = [...ready(), go(T0 + 2 * MINUTE)];
  const result = refused({
    transcript: [...spent, said('utt_go2', 'Fugaro, confirm', T0 + 3 * MINUTE, { actionId: 'act_1', confirmationId: 'cnf_2' })],
    reply: null,
    now: T0 + 3 * MINUTE,
    options: { mode: 'pro' },
  });
  assert.equal(result.code, 'replayed-go');
});

test('the same turn twice is a replayed turn, and it authorises nothing either', () => {
  const goTurn = go(T0 + 2 * MINUTE);
  const result = refused({ transcript: [...ready(), goTurn, goTurn], reply: null, now: T0 + 3 * MINUTE, options: { mode: 'pro' } });
  assert.equal(result.code, 'replayed-turn');
});

test('a go after the conversation’s timeout is refused', () => {
  const result = refused({
    transcript: [...ready(), go(T0 + 11 * MINUTE)],
    reply: null,
    now: T0 + 11 * MINUTE,
    options: { mode: 'pro', ttlMs: 10 * MINUTE },
  });
  assert.equal(result.code, 'go-expired');
});

test('a go at the deadline is still live, and one millisecond later is not', () => {
  const deadline = T0 + 10 * MINUTE;
  const live = turn({ transcript: [...ready(), go(deadline)], reply: null, now: deadline, options: { mode: 'pro', ttlMs: 10 * MINUTE } });
  assert.equal(live.ok, true);
  const stale = refused({
    transcript: [...ready(), go(deadline)],
    reply: null,
    now: deadline + 1,
    options: { mode: 'pro', ttlMs: 10 * MINUTE },
  });
  assert.equal(stale.code, 'go-expired');
});

test('a go inside the conversation’s timeout brands the run with the mode it was given', () => {
  const result = turn({
    transcript: [...ready(), go(T0 + 9 * MINUTE)],
    reply: null,
    now: T0 + 9 * MINUTE,
    options: { mode: 'critical', ttlMs: 10 * MINUTE },
  });
  assert.equal(result.ok, true);
  assert.equal(result.action.mode, 'critical');
  assert.equal(result.costly, true);
});

test('a start in a costly mode is reported costly, and the biometric follows the session policy', () => {
  const transcript = [...ready(), go(T0 + 2 * MINUTE)];
  const plain = turn({ transcript, reply: null, now: T0 + 2 * MINUTE, options: { mode: 'quick' } });
  assert.equal(plain.ok, true);
  assert.equal(plain.costly, false);
  assert.equal(plain.biometric, false);
  const costly = turn({ transcript, reply: null, now: T0 + 2 * MINUTE, options: { mode: 'pro', biometricOnCostly: true } });
  assert.equal(costly.ok, true);
  assert.equal(costly.costly, true);
  assert.equal(costly.biometric, true);
});

test('a go with no mode names the field that is missing rather than minting one', () => {
  const result = refused({ transcript: [...ready(), go(T0 + 2 * MINUTE)], reply: null, now: T0 + 2 * MINUTE });
  assert.equal(result.code, 'missing-field');
  assert.equal(result.field, 'mode');
});

test('a turn after the go is refused: the conversation is spent', () => {
  const first = turn({ transcript: [...ready(), go(T0 + 2 * MINUTE)], reply: null, now: T0 + 2 * MINUTE, options: { mode: 'pro' } });
  const later = refused({
    transcript: [...first.transcript, said('utt_3', 'one more thing', T0 + 3 * MINUTE)],
    reply: { id: 'rep_3', text: 'too late', brief: null },
    now: T0 + 4 * MINUTE,
    options: { mode: 'pro' },
  });
  assert.equal(later.code, 'conversation-closed');
});

// ---------------------------------------------------------------------------
// Purity, and the clock
// ---------------------------------------------------------------------------

test('the same inputs answer the same thing, and nothing handed in is mutated', () => {
  const transcript = ready();
  const frozen = structuredClone(transcript);
  const a = turn({ transcript, reply: null, now: T0 + 2 * MINUTE });
  const b = turn({ transcript, reply: null, now: T0 + 2 * MINUTE });
  assert.deepEqual(a, b);
  assert.deepEqual(transcript, frozen);
  assert.ok(Object.isFrozen(a));
});

test('the module reads no clock of its own, and no random source', () => {
  const source = readFileSync(new URL('./turn.js', import.meta.url), 'utf8');
  const code = source.replace(/\/\/.*$/gmu, '');
  assert.equal(/\bDate\b/u.test(code), false, 'no Date');
  assert.equal(/\bMath\.random\b/u.test(code), false, 'no randomness');
  assert.equal(/\bprocess\./u.test(code), false, 'no process');
});

test('a turn with no now is refused rather than judged at an invented time', () => {
  const result = refused({ transcript: ready(), reply: null });
  assert.equal(result.code, 'invalid-now');
});

test('a turn stamped after the clock it is judged at is refused', () => {
  const result = refused({ transcript: ready(), reply: null, now: T0 });
  assert.equal(result.code, 'invalid-now');
});

// ---------------------------------------------------------------------------
// The transitions, driven cell by cell
// ---------------------------------------------------------------------------

// The table, written by hand and never imported: a test that read the module's
// own declaration back would pin nothing. The codes are spelled out rather than
// taken from REFUSAL, so a code that changed value would fail here.
const EXPECTED = {
  [PHASE.IDLE]: {
    [EVENT.TURN]: {
      ok: ['shaping'],
      next: ['shaping', 'ready'],
      no: ['not-agent-text', 'not-understood', 'command-and-go', 'missing-reply'],
    },
    [EVENT.GO]: {
      ok: [],
      next: ['idle'],
      no: ['not-agent-text', 'not-understood', 'command-and-go', 'unexpected-reply', 'unresolved-target'],
    },
  },
  [PHASE.SHAPING]: {
    [EVENT.TURN]: {
      ok: ['shaping'],
      next: ['shaping', 'ready'],
      no: ['not-agent-text', 'not-understood', 'command-and-go', 'missing-reply'],
    },
    [EVENT.GO]: {
      ok: [],
      next: ['shaping'],
      no: ['not-agent-text', 'not-understood', 'command-and-go', 'unexpected-reply', 'unresolved-target', 'no-brief', 'missing-field', 'go-expired'],
    },
  },
  [PHASE.READY]: {
    [EVENT.TURN]: {
      ok: ['shaping'],
      next: ['shaping', 'ready'],
      no: ['not-agent-text', 'not-understood', 'command-and-go', 'missing-reply'],
    },
    [EVENT.GO]: {
      ok: ['go'],
      next: ['spent'],
      no: ['not-agent-text', 'not-understood', 'command-and-go', 'unexpected-reply', 'missing-field', 'go-expired'],
    },
  },
  [PHASE.SPENT]: {
    [EVENT.TURN]: {
      ok: [],
      next: ['spent'],
      no: ['not-agent-text', 'not-understood', 'command-and-go', 'missing-reply', 'conversation-closed'],
    },
    [EVENT.GO]: {
      ok: [],
      next: ['spent'],
      no: ['not-agent-text', 'not-understood', 'command-and-go', 'unexpected-reply', 'replayed-go'],
    },
  },
};

test('the declared transition table is the one written by hand here', () => {
  for (const phase of Object.values(PHASE)) {
    for (const event of Object.values(EVENT)) {
      const declared = TRANSITIONS[phase][event];
      const expected = EXPECTED[phase][event];
      assert.deepEqual([...declared.ok].sort(), [...expected.ok].sort(), `${phase}/${event} outcomes`);
      assert.deepEqual([...declared.next].sort(), [...expected.next].sort(), `${phase}/${event} next`);
      assert.deepEqual([...declared.no].sort(), [...expected.no].sort(), `${phase}/${event} refusals`);
    }
  }
});

/** A conversation that has taken its go. */
function spent() {
  const answer = turn({ transcript: [...ready(), go(T0 + 2 * MINUTE)], reply: null, now: T0 + 2 * MINUTE, options: { mode: 'pro' } });
  assert.equal(answer.ok, true);
  return answer.transcript;
}

const BY_PHASE = {
  [PHASE.IDLE]: [],
  [PHASE.SHAPING]: shaping(),
  [PHASE.READY]: ready(),
  [PHASE.SPENT]: null,
};

test('every phase of the table is reached by its own transcript', () => {
  for (const phase of Object.values(PHASE)) {
    const prior = BY_PHASE[phase] === null ? spent() : BY_PHASE[phase];
    const view = conversation({ transcript: prior, now: T0 + 10 * MINUTE });
    assert.equal(view.ok, true, `${phase} was refused`);
    assert.equal(view.phase, phase, `the fixture for ${phase}`);
  }
});

for (const phase of Object.values(PHASE)) {
  for (const event of Object.values(EVENT)) {
    test(`the ${phase} x ${event} cell answers inside what it declares`, () => {
      const prior = BY_PHASE[phase] === null ? spent() : BY_PHASE[phase];
      const transcript =
        event === EVENT.TURN
          ? [...prior, said('utt_next', 'one more thing', T0 + 5 * MINUTE)]
          : [...prior, said('utt_next', 'Fugaro, confirm', T0 + 5 * MINUTE, { actionId: 'act_9', confirmationId: 'cnf_9' })];
      const reply = event === EVENT.TURN ? { id: 'rep_next', text: 'ok', brief: null } : null;
      const result = turn({ transcript, reply, now: T0 + 6 * MINUTE, options: { mode: 'pro' } });
      const declared = TRANSITIONS[phase][event];
      if (result.ok) {
        assert.ok(declared.ok.includes(result.outcome), `${phase}/${event} answered ${result.outcome}`);
        assert.ok(declared.next.includes(result.phase), `${phase}/${event} left ${result.phase}`);
      } else {
        assert.ok(declared.no.includes(result.code), `${phase}/${event} refused with ${result.code}`);
      }
    });
  }
}

// ---------------------------------------------------------------------------
// The conversation's own view
// ---------------------------------------------------------------------------

test('conversation reports the phase, the deadline and the read-back of a stored transcript', () => {
  const view = conversation({ transcript: ready(), now: T0 + 2 * MINUTE, options: { mode: 'pro' } });
  assert.equal(view.ok, true);
  assert.equal(view.phase, PHASE.READY);
  assert.equal(view.deadline, T0 + view.ttlMs);
  assert.equal(view.ttlMs, 72 * HOUR);
  assert.equal(view.expired, false);
  assert.equal(view.mode, 'pro');
  assert.equal(view.target.repo, 'dimipaun/fugaro');
  assert.match(view.readBack, /dimipaun\/fugaro/u);
});

test('conversation reports nothing to read back without a mode: a start has none to name', () => {
  const view = conversation({ transcript: ready(), now: T0 + 2 * MINUTE });
  assert.equal(view.ok, true);
  assert.equal(view.phase, PHASE.READY);
  assert.equal(view.mode, null);
  assert.equal(view.readBack, null);
});

test('conversation reports a conversation past its TTL as expired, on the same clock', () => {
  const view = conversation({ transcript: ready(), now: T0 + 72 * HOUR + 1 });
  assert.equal(view.ok, true);
  assert.equal(view.phase, PHASE.READY);
  assert.equal(view.expired, true);
  const inside = conversation({ transcript: ready(), now: T0 + 72 * HOUR });
  assert.equal(inside.expired, false);
});

test('conversation names nothing it cannot know: no read-back without a brief', () => {
  const view = conversation({ transcript: shaping(), now: T0 + 2 * MINUTE, options: { mode: 'pro' } });
  assert.equal(view.ok, true);
  assert.equal(view.phase, PHASE.SHAPING);
  assert.equal(view.readBack, null);
  assert.equal(view.brief, null);
});

test('a spent conversation has nothing left to read back', () => {
  const view = conversation({ transcript: spent(), now: T0 + 3 * MINUTE, options: { mode: 'pro' } });
  assert.equal(view.ok, true);
  assert.equal(view.phase, PHASE.SPENT);
  assert.equal(view.readBack, null);
});

// ---------------------------------------------------------------------------
// readBack as a value of its own
// ---------------------------------------------------------------------------

test('readBack recomputes the sentence, and answers null for what it cannot read', () => {
  const spoken = frontAgent.readBack(REPO, BRIEF, 'pro');
  assert.match(spoken, /Start "docs refactor" in dimipaun\/fugaro \(pro mode\)\./u);
  assert.ok(spoken.endsWith(frontAgent.CONFIRM_CUE));
  assert.equal(frontAgent.readBack(REPO, 'no marker here', 'pro'), null);
  assert.equal(frontAgent.readBack(REPO, BRIEF, null), null);
  assert.equal(frontAgent.readBack({ repo: 5 }, BRIEF, 'pro'), null);
});
