// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The ten never-send rules, each with the test the plan asks for (mobile plan,
// Task P5.3.7 -- SECURITY-SENSITIVE; D118, D111, D123, D124; the design is
// docs/mobile/design/voice-evaluation-switch.md §5, §4.5 and §6).
//
// The plan's sentence this file is here for: "**the design's §5 is a list of ten
// rules and each one is a test**". The test names carry the rule's own words,
// `EGRESS_RULES` is pinned to the ten, and the cases are the ones the design
// calls out by name -- a rule about a state rather than a phrase, a destructive
// route served entirely locally, the residual stated honestly, a redactor that
// substitutes and never drops and is not claimed for the audio path, no queue or
// spool, no credential, nothing when no command is intended, only the project's
// own speech, and every failure answering locally with the indicator saying so.
//
// **Every secret-shaped value below is built at run time by concatenation**, the
// way `shared/digest/redact.test.js` builds its own: a literal of that shape in a
// test file is a leak the scanners cannot tell from the real thing.

import assert from 'node:assert/strict';
import test from 'node:test';

import { MemorySettingsStore, SettingsStore } from '../accounts/index.js';
import { ROUTE } from '../../../shared/command/index.js';
import { initialState, pending, readBack, step } from '../../../shared/confirm/index.js';
import { REDACTED } from '../../../shared/digest/index.js';
import { CALL_FAILURE, ENGINE, REASON, STATE } from '../../../shared/voice-switch/index.js';
import { EGRESS_CODE, EGRESS_OFFER, EGRESS_RULES, createVoiceEgress, permit, readBackFor } from './egress.js';
import { createVoiceSwitch } from './switch.js';

const AT = 1700000000000;
const ACCOUNT = 'laptop';
const PROJECT = 'acme-0123456789abcdef';
const OTHER = 'other-0123456789abcdef';
const NAME = 'deepgram';
const AUDIO = Object.freeze({ captured: 1 });

/** The pairing material's shape, built at run time. */
const SECRET = ['pair', 'ing-', 'material-', '0123456789abcdef'].join('');
const SECRET_TEXT = `the run paired with ${SECRET} at the gate`;
const REDACTED_TEXT = `the run paired with ${REDACTED} at the gate`;

/** The two directions and this account's switches. */
const ADMITS = Object.freeze({
  ok: true,
  providers: Object.freeze([NAME]),
  direction: Object.freeze({ stt: true, tts: true }),
  reason: null,
});

/** A switch with each named direction turned on and the provider agreed to. */
async function switchedOn(...directions) {
  const store = new SettingsStore(new MemorySettingsStore());
  const voice = createVoiceSwitch({ settings: store, accountId: ACCOUNT });
  for (const direction of directions) await voice.set(direction, ENGINE.REMOTE);
  await voice.recordConsent(NAME, AT);
  return voice;
}

/** A switch nobody has touched: both directions local, no consent. */
function untouched() {
  return createVoiceSwitch({ settings: new SettingsStore(new MemorySettingsStore()), accountId: ACCOUNT });
}

/** A hop that answers everything with the answer it was built with. */
function fakeHop(answers = {}) {
  const calls = [];
  const answer = () =>
    answers.failure ?? { ok: true, engine: NAME, contentType: 'application/json', body: new Uint8Array([1]) };
  return {
    calls,
    async transcribe(input) {
      calls.push({ kind: 'transcribe', ...input });
      return answer();
    },
    async speak(input) {
      calls.push({ kind: 'speak', ...input });
      return answer();
    },
  };
}

/** An egress over one switch and one hop, for one project list. */
function egress({ voice = untouched(), hop = fakeHop(), projects = [PROJECT], secrets = [SECRET], onCall } = {}) {
  return {
    voice,
    hop,
    egress: createVoiceEgress({ hop, voiceSwitch: voice, projects, secrets, onCall }),
  };
}

/** A turn's route, in the router's own shape. */
const agentText = { ok: true, route: ROUTE.AGENT_TEXT, forwarded: true, command: null, text: 'cancel the docs refactor' };
const heard = (command) => ({ ok: true, route: ROUTE.APP_COMMAND, forwarded: false, command });
const notUnderstood = { ok: true, route: ROUTE.NOT_UNDERSTOOD, forwarded: false, command: null, reason: 'unmatched' };
const refusedRoute = { ok: false, code: 'utterance-blank', field: 'utterance' };

/** A confirmation machine with one cancel read back and still inside its window. */
function cancelPending() {
  const built = initialState({ now: AT });
  const stepped = step(built.state, {
    type: 'command',
    now: AT,
    utteranceId: 'u1',
    text: 'Fugaro, cancel run run_9f3c',
    verb: 'cancel',
    target: { kind: 'run', runId: 'run_9f3c' },
    actionId: 'a1',
  });
  assert.equal(stepped.ok, true, 'the fixture state must be one shared/confirm really builds');
  return { state: stepped.state, target: { kind: 'run', runId: 'run_9f3c' } };
}

test('the ten rules of the design are ten, and they are the ones §5 lists', () => {
  assert.deepEqual(
    [...EGRESS_RULES],
    [
      'confirmation-audio',
      'confirmation-pending',
      'destructive-route',
      'redaction-substitutes',
      'no-command',
      'foreign-project',
      'other-installation',
      'console-traffic',
      'no-credential',
      'no-spool',
    ],
  );
});

test('rule 1 and rule 2: nothing at all while a confirmation is pending, and it is a rule about a state', async () => {
  const { state } = cancelPending();
  const { voice, hop, egress: gate } = egress({ voice: await switchedOn('stt') });

  // Everything else is in the turn's favour: the switch is on, the consent is
  // recorded, the project admits the provider, and the utterance is speech for
  // the agent. The pending action alone holds it, and it holds **both**
  // directions -- the confirmation is not a phrase this module could recognise.
  for (const direction of ['stt', 'tts']) {
    const held = await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: agentText, state, now: AT, direction });
    assert.equal(held.ok, true, direction);
    assert.equal(held.send, false, direction);
    assert.equal(held.engine, ENGINE.LOCAL, direction);
    assert.equal(held.reason, EGRESS_CODE.PENDING, direction);
  }
  const spoken = await gate.speak({ text: SECRET_TEXT, project: PROJECT, route: agentText, state, now: AT });
  assert.equal(spoken.send, false);
  assert.equal(spoken.reason, EGRESS_CODE.PENDING);
  assert.equal(hop.calls.length, 0, 'nothing goes, and nothing is even composed');

  // The same turn, one millisecond past the confirmation's own deadline, goes:
  // the rule is `pending(state, now)` and not a sentence in a transcript, which
  // is why an expired action releases the freeze and a phrase never does.
  const after = pending(state, AT).actions[0].deadline + 1;
  const released = await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: agentText, state, now: after, status: ADMITS });
  assert.equal(released.send, true);
  assert.equal(hop.calls.length, 1);

  // And a state this app cannot read is not a state that says "nothing is
  // pending": the turn is local rather than sent on a guess.
  const unreadable = await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: agentText, state: { phase: 'idle' }, now: AT });
  assert.equal(unreadable.send, false);
  assert.equal(unreadable.reason, EGRESS_CODE.STATE_UNREADABLE);
});

test('rule 1: the read-back and the confirming utterance are local, and the read-back is rebuilt', async () => {
  const { state, target } = cancelPending();
  const { hop, egress: gate } = egress({ voice: await switchedOn('tts') });

  // The read-back is `shared/confirm`'s own, rebuilt from the resolved target
  // and never from a transcript: it carries the target and the fixed cue, and
  // the transcript's own words are nowhere in it (§6 point 1).
  const spoken = readBackFor('cancel', target);
  assert.equal(spoken, readBack('cancel', target));
  assert.ok(spoken.includes('run_9f3c'));
  assert.ok(!spoken.includes('the docs refactor'));

  // The confirming utterance is an utterance like any other, and while the
  // read-back stands it does not go -- so a remote engine cannot be the source
  // of the confirmation, whatever phrase it returns (§6 point 6).
  const confirming = await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: heard('confirm'), state, now: AT });
  assert.equal(confirming.send, false);
  assert.equal(confirming.reason, EGRESS_CODE.PENDING);
  assert.equal(hop.calls.length, 0);
});

test('rule 3: a destructive route is served entirely locally', async () => {
  const { voice, hop, egress: gate } = egress({ voice: await switchedOn('stt') });
  assert.equal((await voice.read()).ok, true);

  for (const command of ['confirm', 'cancel-that']) {
    const held = await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: heard(command), now: AT });
    assert.equal(held.send, false, command);
    assert.equal(held.engine, ENGINE.LOCAL, command);
    assert.equal(held.reason, EGRESS_CODE.DESTRUCTIVE, command);
  }
  // A route the router refused is not acted on either, and neither is one the
  // app understands as its own command: both are the app's own business.
  for (const route of [refusedRoute, heard('list'), heard('where-am-i'), notUnderstood]) {
    const held = await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route, now: AT });
    assert.equal(held.send, false, JSON.stringify(route));
    assert.equal(held.engine, ENGINE.LOCAL, JSON.stringify(route));
  }
  assert.equal(hop.calls.length, 0);
});

test('rule 3: the residual, and the one branch where a turn may go unclassified', async () => {
  const { hop, egress: gate } = egress({ voice: await switchedOn('stt') });

  // A spoken turn on a device whose local engine could not hear the user: there
  // is no transcript, so nothing has been routed, and the audio is the only way
  // to find out what was said. It goes -- and the design says so in §5 rule 3
  // ("the first utterance ... was transcribed remotely before anything knew it
  // was destructive"). A module that answered `local` here would be claiming
  // remote STT can never be used at all.
  const residual = await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: null, now: AT, status: ADMITS });
  assert.equal(residual.send, true);
  assert.equal(residual.engine, ENGINE.REMOTE);
  assert.equal(hop.calls.length, 1);

  // The reply's text is the app's own, so there is always a route for it: a
  // `tts` turn with none is a defect, and it is served locally.
  const reply = await gate.speak({ text: SECRET_TEXT, project: PROJECT, route: null, now: AT, direction: 'tts' });
  assert.equal(reply.send, false);
  assert.equal(reply.reason, EGRESS_CODE.ROUTE_UNREADABLE);
  assert.equal(hop.calls.length, 1);

  // And rule 2 still holds on the residual's path: a confirmation pending
  // freezes the unclassified turn too.
  const { state } = cancelPending();
  const frozen = await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: null, state, now: AT });
  assert.equal(frozen.send, false);
  assert.equal(frozen.reason, EGRESS_CODE.PENDING);
  assert.equal(hop.calls.length, 1);
});

test('rule 4: on the text path the redactor substitutes and never drops', async () => {
  const { hop, egress: gate } = egress({ voice: await switchedOn('tts') });
  const told = [];

  const answer = await gate.speak({ text: SECRET_TEXT, project: PROJECT, route: agentText, now: AT, direction: 'tts', status: ADMITS });
  assert.equal(answer.ok, true);
  assert.equal(answer.send, true, 'the message still goes: the rule masks, it does not block');
  assert.equal(answer.redacted, true);
  assert.equal(answer.text, REDACTED_TEXT);
  assert.equal(hop.calls.length, 1);
  assert.equal(hop.calls[0].text, REDACTED_TEXT);
  assert.equal(hop.calls[0].text.includes(SECRET), false);
  assert.ok(hop.calls[0].text.includes(REDACTED));

  // A redactor that refused is a turn served locally: text whose redaction
  // failed is text whose redaction did not happen, and nothing goes. Here it is
  // `redact`'s own refusal (the text it was handed is not text) rather than a
  // defect in the fixture: whatever makes the redactor refuse, the payload does
  // not leave.
  const held = await gate.speak({ text: undefined, project: PROJECT, route: agentText, now: AT, direction: 'tts', status: ADMITS });
  assert.equal(held.send, false);
  assert.equal(held.reason, EGRESS_CODE.REDACTOR_FAILED);
  assert.equal(hop.calls.length, 1, 'and the unredacted text never left');
});

test('rule 4: the redactor is not claimed for the audio path, which is not a string', async () => {
  const { hop, egress: gate } = egress({ voice: await switchedOn('stt') });
  const audio = { captured: SECRET };
  const answer = await gate.transcribe({ audio, contentType: 'audio/wav', project: PROJECT, route: agentText, now: AT, status: ADMITS });
  assert.equal(answer.send, true);
  // The payload is handed through **identically**: no redaction is applied and
  // none is claimed (§3's table: "redaction is a function of a string, and audio
  // is not a string"). What protects this path is the rest of the list.
  assert.equal(hop.calls[0].audio, audio);
  assert.equal(answer.redacted, undefined);
});

test('rule 5: nothing goes when no command is intended', async () => {
  const { hop, egress: gate } = egress({ voice: await switchedOn('stt') });

  // The wake word was heard and nothing after it was recognised: the utterance
  // does not leave the app (`shared/command`'s own branch).
  const nothing = await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: notUnderstood, now: AT });
  assert.equal(nothing.send, false);
  assert.equal(nothing.reason, EGRESS_CODE.NO_COMMAND);

  // The app's own command is the app's own business.
  const listing = await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: heard('list'), now: AT });
  assert.equal(listing.send, false);
  assert.equal(listing.reason, EGRESS_CODE.NO_COMMAND);

  // Speech for the agent is the one route that goes.
  const speech = await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: agentText, now: AT, status: ADMITS });
  assert.equal(speech.send, true);
  assert.equal(hop.calls.length, 1);
});

test('rule 5: the capture is one turn, so there is no ambient path for speech to take', async () => {
  const { egress: gate } = egress({ voice: await switchedOn('stt') });
  // One call is one turn and the module holds nothing between them: the only
  // entry points are the two directions, and neither is a stream. What is
  // asserted here is the surface; `boundary.test.js` holds the absence of a
  // timer, a file and a store, which is the same rule from the other side.
  assert.deepEqual(Object.keys(gate).sort(), ['decide', 'speak', 'transcribe']);
  for (const name of Object.keys(gate)) {
    assert.equal(typeof gate[name], 'function', name);
  }
  assert.equal('start' in gate || 'listen' in gate || 'stream' in gate, false);
});

test('rule 6: only this account’s own project goes', async () => {
  const { hop, egress: gate } = egress({ voice: await switchedOn('stt'), projects: [PROJECT] });
  const held = await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: OTHER, route: agentText, now: AT });
  assert.equal(held.send, false);
  assert.equal(held.reason, EGRESS_CODE.FOREIGN_PROJECT);

  const nameless = await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: null, route: agentText, now: AT });
  assert.equal(nameless.send, false);
  assert.equal(nameless.reason, EGRESS_CODE.FOREIGN_PROJECT);
  assert.equal(hop.calls.length, 0);
});

test('rule 7: belong is never a target, whatever the list says', async () => {
  const { hop, egress: gate } = egress({ voice: await switchedOn('stt'), projects: [PROJECT, 'belong'] });
  const held = await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: 'belong', route: agentText, now: AT });
  assert.equal(held.send, false);
  assert.equal(held.reason, EGRESS_CODE.OTHER_INSTALLATION);
  assert.equal(hop.calls.length, 0);
});

test('rule 8: the hop is the only transport this module can send through', () => {
  const voice = untouched();
  assert.throws(() => createVoiceEgress({ voiceSwitch: voice, projects: [PROJECT] }), TypeError);
  assert.throws(() => createVoiceEgress({ hop: { speak: async () => ({ ok: true }) }, voiceSwitch: voice, projects: [PROJECT] }), TypeError);
  assert.throws(() => createVoiceEgress({ hop: fakeHop(), projects: [PROJECT] }), TypeError);
  assert.throws(() => createVoiceEgress({ hop: fakeHop(), voiceSwitch: voice, projects: 'all' }), TypeError);
  assert.throws(() => createVoiceEgress({ hop: fakeHop(), voiceSwitch: voice, projects: [PROJECT], secrets: [7] }), TypeError);
  assert.throws(() => createVoiceEgress({ hop: fakeHop(), voiceSwitch: voice, projects: [PROJECT], onCall: 'log' }), TypeError);
});

test('rule 9: the turn hands the hop the payload and nothing else', async () => {
  const { hop, egress: gate } = egress({ voice: await switchedOn('stt', 'tts') });
  await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: agentText, now: AT, status: ADMITS });
  await gate.speak({ text: SECRET_TEXT, project: PROJECT, route: agentText, now: AT, direction: 'tts', status: ADMITS });

  // Exactly what the hop's own contract takes: the project, the engine's name
  // and the payload. A credential added here would be a credential in the
  // request, and there is no branch that writes one -- the ID token is `hop.js`'s
  // and comes from the app's own auth, not from a voice turn.
  assert.deepEqual(Object.keys(hop.calls[0]).sort(), ['audio', 'contentType', 'engine', 'kind', 'project']);
  assert.deepEqual(Object.keys(hop.calls[1]).sort(), ['engine', 'kind', 'project', 'text']);
  assert.equal(hop.calls[0].engine, NAME);
  assert.equal(hop.calls[0].project, PROJECT);
});

test('rule 9: the consent a call is made under is the record’s, never the caller’s', async () => {
  // The switch is off and nothing is consented: an admitting status answer is
  // not an agreement, and the turn is local with the device's own reason.
  const off = egress({ voice: untouched() });
  const held = await off.egress.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: agentText, now: AT, status: ADMITS });
  assert.equal(held.send, false);
  assert.equal(held.reason, REASON.SWITCH_LOCAL);
  assert.equal(off.hop.calls.length, 0);

  // Switched on with the consent recorded, and no project-status answer yet:
  // the badge says `local — checking`, and the turn waits for the hop's answer
  // rather than guessing in either direction (§4.2).
  const on = egress({ voice: await switchedOn('stt') });
  const checking = await on.egress.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: agentText, now: AT });
  assert.equal(checking.send, false);
  assert.equal(checking.reason, REASON.NO_ANSWER);
  const admitted = await on.egress.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: agentText, now: AT, status: ADMITS });
  assert.equal(admitted.send, true);
  assert.equal(on.hop.calls.length, 1);
});

test('a switch that answered what is not a badge is a local turn, and not a refusal', async () => {
  // The badge is the one input that comes from the app's own record rather than
  // from the caller, so an answer this module cannot read is still a turn it
  // owes an answer about: `local`, the badge's own `badge-unreadable`, and no
  // call. Handing the caller a refusal instead would leave the turn unanswered
  // and the owner with no sentence about their own speech.
  // `null`, a primitive, a badge-shaped string: none of them is an object this
  // module can read a `send` off. (An object with no provider is a different
  // answer -- a route to nobody -- and rule 8's own test is where it lives.)
  for (const answer of [null, 'local', 7, undefined]) {
    const gate = createVoiceEgress({
      hop: fakeHop(),
      voiceSwitch: { indicator: async () => answer },
      projects: [PROJECT],
      secrets: [SECRET],
    });
    const turn = await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: agentText, now: AT });
    assert.equal(turn.ok, true, String(answer));
    assert.equal(turn.send, false, String(answer));
    assert.equal(turn.engine, ENGINE.LOCAL, String(answer));
    assert.equal(turn.reason, EGRESS_CODE.BADGE_UNREADABLE, String(answer));
  }

  // A switch that threw is the same answer, and it carries the text-entry offer
  // when the local engine is the one that failed: §4.5's last row is about what
  // the owner can do next, and it does not depend on which half broke.
  const threw = createVoiceEgress({
    hop: fakeHop(),
    voiceSwitch: {
      indicator: async () => {
        throw new Error('the record could not be read');
      },
    },
    projects: [PROJECT],
    secrets: [SECRET],
  });
  const turn = await threw.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: agentText, now: AT, localFailed: true });
  assert.equal(turn.reason, EGRESS_CODE.BADGE_UNREADABLE);
  assert.equal(turn.offer, EGRESS_OFFER.TEXT_ENTRY);
});

test('rule 10: a failure is terminal, and nothing is queued, spooled or retried', async () => {
  const failure = { ok: false, code: 'unreachable', field: 'hop', detail: null };
  const hop = fakeHop({ failure });
  const { egress: gate } = egress({ voice: await switchedOn('stt'), hop });

  const first = await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: agentText, now: AT, status: ADMITS });
  assert.equal(first.ok, true);
  assert.equal(first.send, false);
  assert.equal(first.engine, ENGINE.LOCAL);
  assert.equal(first.reason, CALL_FAILURE.UNREACHABLE);
  assert.equal(hop.calls.length, 1, 'one turn is one request, and a failure is not a second one');
  assert.equal('retry' in first, false);
  assert.equal('queue' in first, false);

  // The next turn is a new turn and makes its own call: nothing was remembered,
  // nothing was held for later, and the failure was not cached into a local-only
  // mode either.
  await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: agentText, now: AT + 1, status: ADMITS });
  assert.equal(hop.calls.length, 2);
});

test('every failure answers locally, and the indicator says so', async () => {
  const rows = [
    [{ ok: false, code: 'unreachable', field: 'hop' }, CALL_FAILURE.UNREACHABLE],
    [{ ok: false, code: 'bad-url', field: 'baseUrl', detail: 'allow-list-empty' }, CALL_FAILURE.UNREACHABLE],
    [{ ok: false, code: 'no-token', field: 'token' }, CALL_FAILURE.EXPIRED_TOKEN],
    [{ ok: false, code: 'refused', field: 'hop', detail: 'token_unverified' }, CALL_FAILURE.EXPIRED_TOKEN],
    [{ ok: false, code: 'refused', field: 'hop', detail: 'device_unknown' }, CALL_FAILURE.REVOKED_DEVICE],
    [{ ok: false, code: 'refused', field: 'hop', detail: 'provider_failed' }, CALL_FAILURE.PROVIDER_ERROR],
    [{ ok: false, code: 'refused', field: 'hop', detail: 'force_local' }, CALL_FAILURE.PROVIDER_ERROR],
    [{ ok: false, code: 'refused', field: 'hop', detail: null }, CALL_FAILURE.PROVIDER_ERROR],
    [{ ok: false, code: 'bad-answer', field: 'answer' }, CALL_FAILURE.PROVIDER_ERROR],
  ];

  for (const [failure, wanted] of rows) {
    const voice = await switchedOn('stt');
    const hop = fakeHop({ failure });
    const { egress: gate } = egress({ voice, hop });
    const answer = await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: agentText, now: AT, status: ADMITS });

    assert.equal(answer.engine, ENGINE.LOCAL, wanted);
    assert.equal(answer.reason, wanted, wanted);
    assert.equal(hop.calls.length, 1, wanted);

    // The record the badge reads is the call's own, and the badge shows local:
    // §4.5's "the app answers from the local engine; the indicator shows local".
    const badge = await voice.indicator({ direction: 'stt', status: ADMITS, lastCallInDirection: answer.call });
    assert.equal(badge.engine, ENGINE.LOCAL, wanted);
    assert.equal(badge.state, STATE.LOCAL, wanted);
    assert.equal(badge.reason, wanted, wanted);
    assert.equal(badge.send, false, wanted);
  }
});

test('a failed local engine offers text entry, and a successful one does not', async () => {
  const { egress: gate } = egress({ voice: untouched() });
  const failed = await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: agentText, now: AT, localFailed: true });
  assert.equal(failed.send, false);
  assert.equal(failed.engine, ENGINE.LOCAL);
  assert.equal(failed.offer, EGRESS_OFFER.TEXT_ENTRY);

  const fine = await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: agentText, now: AT });
  assert.equal(fine.offer, null);

  // And a remote call that failed into the local engine, on a device whose local
  // engine is the one that just failed, owes the user text entry too.
  const hop = fakeHop({ failure: { ok: false, code: 'unreachable', field: 'hop' } });
  const { egress: other } = egress({ voice: await switchedOn('stt'), hop });
  const both = await other.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: agentText, now: AT, status: ADMITS, localFailed: true });
  assert.equal(both.send, false);
  assert.equal(both.offer, EGRESS_OFFER.TEXT_ENTRY);
});

test('a caller is told when a call starts and how it ended, so it can put the screen back', async () => {
  const seen = [];
  const { egress: gate } = egress({ voice: await switchedOn('stt'), onCall: (record) => seen.push({ ...record }) });
  const answer = await gate.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: agentText, now: AT, status: ADMITS });
  assert.equal(answer.send, true);
  assert.deepEqual(seen, [
    { direction: 'stt', engine: ENGINE.REMOTE, provider: NAME, state: 'in-flight', reason: null },
    { direction: 'stt', engine: ENGINE.REMOTE, provider: NAME, state: 'done', reason: null },
  ]);
  // `shared/voice-switch` reads these records and refuses a field it does not
  // name, so what `onCall` is told is exactly what a badge takes.
  const badge = await switchedOn('stt').then((voice) => voice.indicator({ direction: 'stt', status: ADMITS, lastCallInDirection: seen[0] }));
  assert.equal(badge.inFlight, true);
  assert.equal(badge.state, STATE.REMOTE);

  // A local turn tells it nothing at all: the local engine is the app's own and
  // this module never sees it.
  const quiet = [];
  const { egress: local } = egress({ voice: untouched(), onCall: (record) => quiet.push(record) });
  await local.transcribe({ audio: AUDIO, contentType: 'audio/wav', project: PROJECT, route: agentText, now: AT });
  assert.deepEqual(quiet, []);
});

test('decide is the gate without the send, and it is a pure function of the turn', () => {
  const { state } = cancelPending();
  const badge = { ok: true, send: true, provider: NAME, reason: null };
  const allow = { direction: 'stt', route: agentText, state: null, now: AT, project: PROJECT, projects: [PROJECT], badge };
  assert.equal(permit(allow).send, true);

  assert.equal(permit({ ...allow, state }).reason, EGRESS_CODE.PENDING);
  assert.equal(permit({ ...allow, route: heard('confirm') }).reason, EGRESS_CODE.DESTRUCTIVE);
  assert.equal(permit({ ...allow, route: notUnderstood }).reason, EGRESS_CODE.NO_COMMAND);
  assert.equal(permit({ ...allow, route: refusedRoute }).reason, EGRESS_CODE.ROUTE_UNREADABLE);
  assert.equal(permit({ ...allow, project: OTHER }).reason, EGRESS_CODE.FOREIGN_PROJECT);
  assert.equal(permit({ ...allow, direction: 'tts', route: null }).reason, EGRESS_CODE.ROUTE_UNREADABLE);
  assert.equal(permit({ ...allow, badge: { ok: true, send: false, provider: null, reason: REASON.NO_CONSENT } }).reason, REASON.NO_CONSENT);
  assert.equal(permit({ ...allow, badge: { ok: true, send: true, provider: null, reason: null } }).reason, EGRESS_CODE.NO_PROVIDER);
  assert.equal(permit({ ...allow, direction: 'up' }).ok, false);
  assert.equal(permit({ ...allow, projects: 'all' }).ok, false);
  assert.equal(permit({ ...allow, badge: null }).ok, false);
  // The switch's own fail-closed badge (a record it could not read) is still a
  // badge, and it says local: `send: false` with the refusal beside it.
  assert.equal(permit({ ...allow, badge: { send: false, provider: null, reason: REASON.NO_ANSWER } }).send, false);

  // Nothing in the input is mutated, and the same turn answers the same way
  // twice -- which is what makes it a rule rather than a piece of state.
  const once = permit(allow);
  assert.deepEqual(once, permit(allow));
  assert.deepEqual(Object.keys(allow).sort(), ['badge', 'direction', 'now', 'projects', 'project', 'route', 'state'].sort());
});
