// Regenerates vectors.json, the shared test vectors for the front agent's turn
// (mobile plan, Task P5.4.4).
//
//   node shared/front-agent/vectors.generate.mjs
//
// The vectors are the CONTRACT between the two implementations of the turn: this
// package, which the phone runs, and the Go consumer, which P5.4.5 reimplements
// because it cannot call into JavaScript. Neither end can test the other, so
// both test against this file: every case pins a conversation, the clock it is
// judged at, and the exact answer -- the action record the go authorises, the
// phase, the read-back, or the refusal code and the field at fault. A
// disagreement about when a go expires, which turn binds the repository, or
// which utterance counts as the go fails on the side that is wrong, rather than
// three months later on a phone.
//
// This file is not a test and is not run by `npm test` (which matches
// `**/*.test.js`); it is the recipe for the artifact, kept beside it so the
// artifact can be re-derived rather than hand-edited. `vectors.test.js` holds
// the artifact to this module and to the plan's claims, and the Go side holds
// itself to the artifact.
//
// The values are readable placeholders: no secret, no credential and nothing
// secret-shaped appears here, and the ids are ordinary words.

import { writeFileSync } from 'node:fs';
import {
  DEFAULT_BIOMETRIC_ON_COSTLY,
  DEFAULT_COSTLY_MODES,
  DEFAULT_MAX_BRIEF,
  DEFAULT_MAX_TURNS,
  DEFAULT_TTL_MS,
  ID_MAX,
  MAX_TTL_MS,
  MIN_TTL_MS,
  MODE_MAX,
  REPO_MAX,
  SPOKEN_TITLE_MAX,
  TEXT_MAX,
  TIME_MAX,
  turn,
} from './index.js';

const T0 = 1_700_000_000_000;
const MINUTE = 60 * 1000;
const REPO = { repo: 'dimipaun/fugaro' };
const BRIEF = '# Task: docs refactor\nMove the guide.';

const said = (id, text, at, extra = {}) => ({ id, role: 'person', text, at, ...extra });
const replied = (id, at, text, brief = null) => ({ id, role: 'agent', text, at, brief });
const ready = () => [said('utt_1', 'plan the docs refactor', T0, { target: REPO }), replied('rep_1', T0 + MINUTE, 'here is a draft', BRIEF)];
const shaping = () => [said('utt_1', 'plan the docs refactor', T0, { target: REPO }), replied('rep_1', T0 + MINUTE, 'what must it not touch?', null)];
const go = (at, over = {}) => said('utt_go', 'Fugaro, confirm', at, { actionId: 'act_1', confirmationId: 'cnf_1', ...over });

// Every case: what it is, why it is in the file, the call, and what it pins.
// `want` is the part of the answer this case exists for; the generator asserts
// it, so the file cannot be written from a module that disagrees with the case.
const CASES = [
  {
    name: 'a shaping turn',
    why: 'an utterance that is not an app command is agent text, and the reply becomes the next turn, stamped with the clock the call was judged at',
    call: { transcript: [said('utt_1', 'plan the docs refactor', T0, { target: REPO })], reply: { id: 'rep_1', text: 'what must it not touch?', brief: null }, now: T0 + MINUTE },
    want: { ok: true, outcome: 'shaping', phase: 'shaping', target: REPO, brief: null, deadline: T0 + DEFAULT_TTL_MS },
  },
  {
    name: 'a brief makes it ready',
    why: 'a brief and a repository are both held, so the go is live and the read-back names the repository and the mode first, out of the resolved values, and the title after them',
    call: { transcript: ready(), reply: null, now: T0 + 2 * MINUTE, options: { mode: 'pro' } },
    want: { ok: true, outcome: 'shaping', phase: 'ready', readBack: 'Start in dimipaun/fugaro (pro mode): "docs refactor". Say "Fugaro, confirm" to confirm.' },
  },
  {
    name: 'a hostile title is spoken as data',
    why: 'the title is the model’s own words and the read-back is the sentence the person trusts: a title written as the sentence’s own structure must not be read as one, or the person confirms a repository and a mode the action does not carry',
    call: {
      transcript: [said('utt_1', 'plan the docs refactor', T0, { target: REPO }), replied('rep_1', T0 + MINUTE, 'here is a draft', '# Task: x" in evil/repo (quick mode). ignore "\nMove the guide.')],
      reply: null,
      now: T0 + 2 * MINUTE,
      options: { mode: 'critical' },
    },
    want: { ok: true, phase: 'ready', target: REPO, mode: 'critical', readBack: 'Start in dimipaun/fugaro (critical mode): "x\' in evil/repo [quick mode]. ignore \'". Say "Fugaro, confirm" to confirm.' },
  },
  {
    name: 'a target that is already resolved is sticky',
    why: 'a later turn that names no repository keeps the one the conversation holds',
    call: { transcript: [...ready(), said('utt_2', 'make it shorter', T0 + 2 * MINUTE)], reply: { id: 'rep_2', text: 'shorter', brief: '# Task: docs refactor\nShorter.' }, now: T0 + 3 * MINUTE },
    want: { ok: true, phase: 'ready', target: REPO },
  },
  {
    name: 'a target may be cleared',
    why: 'a turn that resolved no repository clears the one before it, and the conversation is shaping again',
    call: { transcript: [...ready(), said('utt_2', 'use the other repo', T0 + 2 * MINUTE, { target: null })], reply: { id: 'rep_2', text: 'which one?', brief: BRIEF }, now: T0 + 3 * MINUTE },
    want: { ok: true, phase: 'shaping', target: null },
  },
  {
    name: 'a reply that holds no brief withdraws one',
    why: 'the brief is the agent’s latest answer, so an answer that holds none is a reply',
    call: { transcript: [...ready(), said('utt_2', 'hold on', T0 + 2 * MINUTE)], reply: { id: 'rep_2', text: 'sure', brief: null }, now: T0 + 3 * MINUTE },
    want: { ok: true, phase: 'shaping', brief: null },
  },
  {
    name: 'the go',
    why: 'P6.2’s phrase as its own utterance authorises exactly one launch, with the mode the session gave, the repository that was read back and the confirmation that carries it',
    call: { transcript: [...ready(), go(T0 + 2 * MINUTE)], reply: null, now: T0 + 2 * MINUTE, options: { mode: 'pro' } },
    want: {
      ok: true,
      outcome: 'go',
      phase: 'spent',
      costly: true,
      biometric: false,
      action: { id: 'act_1', verb: 'launch', mode: 'pro', target: REPO, confirmation: { id: 'cnf_1', at: T0 + 2 * MINUTE } },
    },
  },
  {
    name: 'a go in a mode the session does not call costly',
    why: 'D2786 reads back a costly start and lets a plain one through, so what the go costs is the session policy’s answer',
    call: { transcript: [...ready(), go(T0 + 2 * MINUTE)], reply: null, now: T0 + 2 * MINUTE, options: { mode: 'poc' } },
    want: { ok: true, outcome: 'go', costly: false, biometric: false, action: { mode: 'poc' } },
  },
  {
    name: 'a costly start with the biometric switch on',
    why: 'the switch is the session’s, and it is asked for only on a costly start',
    call: { transcript: [...ready(), go(T0 + 2 * MINUTE)], reply: null, now: T0 + 2 * MINUTE, options: { mode: 'critical', biometricOnCostly: true } },
    want: { ok: true, costly: true, biometric: true },
  },
  {
    name: 'the session’s own costly modes',
    why: 'a session that calls another mode costly is read back for it, and the default list is not consulted',
    call: { transcript: [...ready(), go(T0 + 2 * MINUTE)], reply: null, now: T0 + 2 * MINUTE, options: { mode: 'poc', costlyModes: ['poc'] } },
    want: { ok: true, costly: true },
  },
  {
    name: 'the go at the deadline is still live',
    why: 'the timeout is inclusive at its own edge: at `startedAt + ttlMs` the go is taken, one millisecond later it is refused',
    call: { transcript: [...ready(), go(T0 + 10 * MINUTE)], reply: null, now: T0 + 10 * MINUTE, options: { mode: 'pro', ttlMs: 10 * MINUTE } },
    want: { ok: true, outcome: 'go' },
  },
  {
    name: 'a go past the deadline',
    why: 'a brief that has sat longer than the TTL is stale, and the conversation must be shaped again',
    call: { transcript: [...ready(), go(T0 + 10 * MINUTE + 1)], reply: null, now: T0 + 10 * MINUTE + 1, options: { mode: 'pro', ttlMs: 10 * MINUTE } },
    want: { ok: false, code: 'go-expired', field: null },
  },
  {
    name: 'a replayed go',
    why: 'one go authorises one launch: the conversation is spent and a second go is refused, whatever ids it carries',
    call: { transcript: [...ready(), go(T0 + 2 * MINUTE), go(T0 + 3 * MINUTE, { id: 'utt_go2', actionId: 'act_2', confirmationId: 'cnf_2' })], reply: null, now: T0 + 3 * MINUTE, options: { mode: 'pro' } },
    want: { ok: false, code: 'replayed-go' },
  },
  {
    name: 'a go binding a target it asserted itself',
    why: 'the go binds the repository that was read back, so a go carrying a target is refused rather than believed',
    call: { transcript: [...ready(), go(T0 + 2 * MINUTE, { target: { repo: 'someone/else' } })], reply: null, now: T0 + 2 * MINUTE, options: { mode: 'pro' } },
    want: { ok: false, code: 'unexpected-field', field: 'target' },
  },
  {
    name: 'a go with no resolved repository',
    why: 'D128’s amendment: nothing is read back without a resolved target, so nothing can be confirmed',
    call: { transcript: [said('utt_1', 'plan the docs refactor', T0), replied('rep_1', T0 + MINUTE, 'draft', BRIEF), go(T0 + 2 * MINUTE)], reply: null, now: T0 + 2 * MINUTE, options: { mode: 'pro' } },
    want: { ok: false, code: 'unresolved-target', field: 'target' },
  },
  {
    name: 'a go with no brief',
    why: 'the brief is the thing the go authorises, so a conversation that shaped none has nothing to launch',
    call: { transcript: [...shaping(), go(T0 + 2 * MINUTE)], reply: null, now: T0 + 2 * MINUTE, options: { mode: 'pro' } },
    want: { ok: false, code: 'no-brief', field: 'brief' },
  },
  {
    name: 'a go before the agent has answered the person’s newest turn',
    why: 'a person turn with no answer after it is a reply still owed, so the brief the conversation holds is not the one the person is confirming: the phase is shaping, and the go is refused rather than taken',
    call: { transcript: [...ready(), said('utt_2', 'actually, use the other repo', T0 + 2 * MINUTE), go(T0 + 3 * MINUTE)], reply: null, now: T0 + 3 * MINUTE, options: { mode: 'pro' } },
    want: { ok: false, code: 'missing-reply', field: null },
  },
  {
    name: 'a go the session gave no mode for',
    why: 'a launch runs in a mode, and this module does not mint one: the field is named rather than guessed',
    call: { transcript: [...ready(), go(T0 + 2 * MINUTE)], reply: null, now: T0 + 2 * MINUTE },
    want: { ok: false, code: 'missing-field', field: 'mode' },
  },
  {
    name: 'a bare "confirm"',
    why: 'P6.1 forwards "confirm" to the agent, so it is agent text: a machine that also read it as a go would act on words the agent received',
    call: { transcript: [...ready(), said('utt_2', 'confirm', T0 + 2 * MINUTE)], reply: { id: 'rep_2', text: 'did you mean to go?', brief: null }, now: T0 + 3 * MINUTE },
    want: { ok: true, outcome: 'shaping', phase: 'shaping' },
  },
  {
    name: 'a misrecognised go',
    why: 'the near misses are agent text and never a confirmation: "go ahead", "yes", "confirmed it" and "conform"',
    call: { transcript: [...ready(), said('utt_2', 'go ahead', T0 + 2 * MINUTE)], reply: { id: 'rep_2', text: 'ok', brief: null }, now: T0 + 3 * MINUTE },
    want: { ok: true, outcome: 'shaping' },
  },
  {
    name: 'a wake word the grammar does not know',
    why: 'P6.1 never forwards a wake-word utterance it cannot route, so neither does this: "Fugaro, conform" is not agent text',
    call: { transcript: [...ready(), said('utt_2', 'Fugaro, conform', T0 + 2 * MINUTE)], reply: null, now: T0 + 3 * MINUTE },
    want: { ok: false, code: 'not-understood' },
  },
  {
    name: 'an app command',
    why: 'an app command is another screen’s, not the front agent’s, and the transcript that holds one is not this conversation',
    call: { transcript: [...ready(), said('utt_2', 'Fugaro, list projects', T0 + 2 * MINUTE)], reply: null, now: T0 + 3 * MINUTE },
    want: { ok: false, code: 'not-agent-text' },
  },
  {
    name: 'an utterance carrying a command beside the go',
    why: 'P6.2’s command-and-confirm rule: one misrecognition must not satisfy both halves of D47’s Rule',
    call: { transcript: [...ready(), said('utt_2', 'Fugaro, confirm it', T0 + 2 * MINUTE)], reply: null, now: T0 + 3 * MINUTE },
    want: { ok: false, code: 'command-and-go' },
  },
  {
    name: 'a reply beside the go',
    why: 'nothing is answered once the person said go',
    call: { transcript: [...ready(), go(T0 + 2 * MINUTE)], reply: { id: 'rep_2', text: 'too late', brief: null }, now: T0 + 2 * MINUTE, options: { mode: 'pro' } },
    want: { ok: false, code: 'unexpected-reply', field: 'reply' },
  },
  {
    name: 'a turn after the go',
    why: 'the go spends the conversation, and a later turn is refused rather than shaped into a second launch',
    call: { transcript: [...ready(), go(T0 + 2 * MINUTE), said('utt_3', 'one more thing', T0 + 3 * MINUTE)], reply: { id: 'rep_3', text: 'too late', brief: null }, now: T0 + 4 * MINUTE, options: { mode: 'pro' } },
    want: { ok: false, code: 'conversation-closed' },
  },
  {
    name: 'a person turn with no reply yet',
    why: 'the agent answers every turn, so a turn with no reply is not a finished step',
    call: { transcript: [said('utt_1', 'plan the docs refactor', T0)], reply: null, now: T0 + MINUTE },
    want: { ok: false, code: 'missing-reply', field: 'reply' },
  },
  {
    name: 'the same turn twice',
    why: 'one utterance is one turn: a repeated turn id is refused while the transcript is read',
    call: { transcript: [said('utt_1', 'x', T0), said('utt_1', 'y', T0 + 1)], reply: null, now: T0 + MINUTE },
    want: { ok: false, code: 'replayed-turn', field: 'transcript[1].id' },
  },
  {
    name: 'a turn stamped after the clock judging it',
    why: 'a time that has not happened yet is a clock disagreement, refused rather than clamped',
    call: { transcript: ready(), reply: null, now: T0 },
    want: { ok: false, code: 'invalid-now' },
  },
  {
    name: 'a brief with no task line',
    why: 'the brief’s first line is the run’s title, and a run with no title is refused where it would be launched',
    call: { transcript: [said('utt_1', 'x', T0, { target: REPO })], reply: { id: 'rep_1', text: 'draft', brief: 'Move the guide.\n# Task: too late' }, now: T0 + MINUTE },
    want: { ok: false, code: 'bad-brief', field: 'reply.brief' },
  },
  {
    name: 'a brief with no title',
    why: 'the marker is there and the title is not: `fugaro watch` and the pull request title come from that line, and a run with no title is refused where it would be launched',
    call: { transcript: [said('utt_1', 'x', T0, { target: REPO })], reply: { id: 'rep_1', text: 'draft', brief: '# Task:   \nMove the guide.' }, now: T0 + MINUTE },
    want: { ok: false, code: 'bad-brief', field: 'reply.brief' },
  },
  {
    name: 'a repository that is not owner/name',
    why: 'the repository is passed to `gh` as an argument, so a shape that could be read as a flag is refused',
    call: { transcript: [said('utt_1', 'x', T0, { target: { repo: '-o/name' } })], reply: null, now: T0 + MINUTE },
    want: { ok: false, code: 'bad-repo', field: 'target.repo' },
  },
  {
    name: 'a transcript out of order',
    why: 'a turn stamped before the turn before it is refused rather than sorted: the order is the conversation',
    call: { transcript: [said('utt_1', 'x', T0 + 10), said('utt_2', 'y', T0)], reply: null, now: T0 + MINUTE },
    want: { ok: false, code: 'unsorted-transcript', field: 'transcript[1].at' },
  },
  {
    name: 'a transcript over its bound',
    why: 'a conversation longer than maxTurns is refused, and the bound is the caller’s',
    call: { transcript: [said('utt_1', 'x', T0), said('utt_2', 'y', T0 + 1), said('utt_3', 'z', T0 + 2)], reply: { id: 'rep_9', text: 'ok', brief: null }, now: T0 + MINUTE, options: { maxTurns: 2 } },
    want: { ok: false, code: 'too-many-turns' },
  },
  {
    name: 'a transcript of no turns',
    why: 'there is nothing to judge',
    call: { transcript: [], reply: { id: 'rep_1', text: 'x', brief: null }, now: T0 + MINUTE },
    want: { ok: false, code: 'empty-transcript', field: 'transcript' },
  },
  {
    name: 'an agent turn with nothing to reply to',
    why: 'the brief would be an answer to nobody',
    call: { transcript: [replied('rep_1', T0, 'x', BRIEF)], reply: null, now: T0 + MINUTE },
    want: { ok: false, code: 'invalid-transcript', field: 'transcript[0].role' },
  },
  {
    name: 'an utterance with no words in it',
    why: 'a transcript the recogniser heard as noise is refused, never forwarded',
    call: { transcript: [said('utt_1', '  ...  ', T0)], reply: null, now: T0 + MINUTE },
    want: { ok: false, code: 'not-understood' },
  },
  {
    name: 'a timeout outside its bounds',
    why: 'a timeout the caller may move is a timeout the caller can set wrong, and a phantom one is refused',
    call: { transcript: ready(), reply: null, now: T0 + MINUTE, options: { ttlMs: MIN_TTL_MS - 1 } },
    want: { ok: false, code: 'invalid-timeout', field: 'options.ttlMs' },
  },
];

function expectation(answer) {
  if (!answer.ok) return { ok: false, code: answer.code, field: answer.field };
  return {
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
  };
}

// matches holds one value to another, field by field: a case names only the part
// of the answer it exists for, and everything it does not name is the module's
// business rather than the case's.
function matches(got, want) {
  if (want === null || typeof want !== 'object' || Array.isArray(want)) return JSON.stringify(got) === JSON.stringify(want);
  if (got === null || typeof got !== 'object') return false;
  return Object.entries(want).every(([key, value]) => matches(got[key], value));
}

const cases = CASES.map((one) => {
  const answer = turn(one.call);
  const expect = expectation(answer);
  for (const [key, value] of Object.entries(one.want)) {
    if (!matches(expect[key], value)) {
      throw new Error(`${one.name}: ${key} is ${JSON.stringify(expect[key])}, the case wants ${JSON.stringify(value)}`);
    }
  }
  const record = { name: one.name, why: one.why, transcript: one.call.transcript, reply: one.call.reply, now: one.call.now };
  if (one.call.options !== undefined) record.options = one.call.options;
  record.expect = expect;
  return record;
});

const document = {
  format: 'fugaro front-agent turn vectors',
  what: 'one call of the spec-shaping turn (mobile plan, Task P5.4.4): the conversation, the clock it is judged at, and the exact answer -- the action record the go authorises, the phase, the read-back, or the refusal code and the field at fault',
  reader: 'the Go consumer reimplements `turn` (P5.4.5) and holds itself to these cases; `vectors.test.js` holds them to the JavaScript module and to the plan’s claims',
  generated_by: 'node shared/front-agent/vectors.generate.mjs',
  roles: ['person', 'agent'],
  constants: {
    ttl_ms: DEFAULT_TTL_MS,
    min_ttl_ms: MIN_TTL_MS,
    max_ttl_ms: MAX_TTL_MS,
    max_turns: DEFAULT_MAX_TURNS,
    max_brief: DEFAULT_MAX_BRIEF,
    id_max: ID_MAX,
    text_max: TEXT_MAX,
    repo_max: REPO_MAX,
    mode_max: MODE_MAX,
    spoken_title_max: SPOKEN_TITLE_MAX,
    time_max: TIME_MAX,
    costly_modes: DEFAULT_COSTLY_MODES,
    biometric_on_costly: DEFAULT_BIOMETRIC_ON_COSTLY,
  },
  cases,
};

writeFileSync(new URL('./vectors.json', import.meta.url), `${JSON.stringify(document, null, 2)}\n`);
console.log(`wrote ${cases.length} cases`);
