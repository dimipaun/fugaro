// Tests for the spoken-confirmation state machine (mobile plan, Task P6.2).
//
// SECURITY-SENSITIVE. The plan's verification list is the spine of this file,
// and each of its items is a test named after it:
//
//   * a misrecognised "confirm" ("conform", "confirmed it", "yes") does not confirm
//   * a replayed confirm (the same confirmation id or utterance id twice)
//     authorises nothing the second time
//   * a confirm for a different target (the pending action changed, or a second
//     pending action) is refused
//   * a confirm after the timeout is refused
//   * a command and "confirm" in one utterance is refused
//   * the read-back for "cancel the docs refactor" names the resolved run id
//   * the state machine's transitions are total (an exhaustive table test)
//
// The plan's red step -- "the replayed-confirm test against a machine without an
// action-id check" -- is the first mutation in mutation-proof.sh, and the test it
// turns red is "one confirmation authorises exactly one action, and a replay
// authorises nothing".
//
// Everything here is injected: a fixed NOW far from the day this test runs, so a
// rule that quietly read a clock of its own would fail rather than pass.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import {
  ABORT_FIELDS,
  COMMAND_FIELDS,
  CONFIRM_CUE,
  CONFIRM_FIELDS,
  CONFIRM_PHRASES,
  DEFAULT_CONFIRM_TIMEOUT_MS,
  DEFAULT_MAX_LEDGER,
  DEFAULT_MAX_PENDING,
  DESTRUCTIVE_VERBS,
  EVENT,
  ID_MAX,
  INPUT_REFUSALS,
  LEDGER_KIND,
  MAX_CONFIRM_TIMEOUT_MS,
  MAX_FORWARD_JUMP_MS,
  MAX_MAX_LEDGER,
  MAX_MAX_PENDING,
  MIN_CONFIRM_TIMEOUT_MS,
  MIN_MAX_LEDGER,
  MIN_MAX_PENDING,
  OUTCOME,
  PASS_THROUGH_VERBS,
  PHASE,
  REFUSAL,
  REFUSED_VERBS,
  STATE_FIELDS,
  TARGET_KIND,
  TEXT_MAX,
  TIME_MAX,
  TRANSITIONS,
  VERB,
  initialState,
  isConfirmPhrase,
  pending,
  readBack,
  step,
} from './index.js';
import {
  CONFIRMATION_PHRASES as COMMAND_CONFIRMATION_PHRASES,
  isConfirmation,
  routeUtterance,
} from '../command/index.js';
import { ACTIONS as SCHEMA_ACTIONS, validateMessage } from '../mailbox-schema/index.js';

// The module's `pending` (P6.12's screen view) under a name that does not read
// like the state's own `pending` array.
const pendingOf = pending;

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// A fixed now, deliberately far from the day this test runs.
const NOW = 1_700_000_000_000;

// Small, countable numbers rather than the module's defaults, so the timeout
// boundary and the caps are things a reader can check by hand.
const TIMEOUT = 60_000;
const MAX_PENDING = 2;
const MAX_LEDGER = 8;

const REPO = 'dimipaun/fugaro';
const PR = 568;
const SHA = 'a'.repeat(40);
const SHA_64 = 'b'.repeat(64);
const SHA_MOVED = 'c'.repeat(40);

const RUN = Object.freeze({ kind: TARGET_KIND.RUN, runId: 'run_9f3c' });
const RUN_OTHER = Object.freeze({ kind: TARGET_KIND.RUN, runId: 'run_0a11' });
const MERGE = Object.freeze({ kind: TARGET_KIND.MERGE, repo: REPO, pr: PR, headSha: SHA });
// What "confirming an action" confirms (P6.3: `confirm` takes a run or a
// repository and PR, and carries no head SHA).
const PR_TARGET = Object.freeze({ kind: TARGET_KIND.PR, repo: REPO, pr: PR });

// The fixed phrase as P6.1's router recognises it: an app command, so it
// carries the wake word. A bare "confirm" is agent text (D164).
const CONFIRM_TEXT = 'Fugaro, confirm';

// The phrase the plan names, and the utterance that resolves it to a run id.
const CANCEL_TEXT = 'cancel the docs refactor';

function cancelEvent(overrides = {}) {
  return {
    type: EVENT.COMMAND,
    now: NOW + 1_000,
    utteranceId: 'utt_a',
    text: CANCEL_TEXT,
    verb: VERB.CANCEL,
    target: RUN,
    actionId: 'act_a',
    ...overrides,
  };
}

function mergeEvent(overrides = {}) {
  return {
    type: EVENT.COMMAND,
    now: NOW + 1_000,
    utteranceId: 'utt_m',
    text: 'merge it',
    verb: VERB.MERGE,
    target: MERGE,
    actionId: 'act_m',
    ...overrides,
  };
}

// Confirming an action (D128's third destructive verb) is phrased without the
// confirming phrase: a command utterance that carries the phrase is refused as
// `command-and-confirm`, which is the whole point of that rule.
function confirmActionEvent(overrides = {}) {
  return {
    type: EVENT.COMMAND,
    now: NOW + 1_000,
    utteranceId: 'utt_c',
    text: 'approve the deploy',
    verb: VERB.CONFIRM_ACTION,
    target: PR_TARGET,
    actionId: 'act_c',
    ...overrides,
  };
}

function launchEvent(overrides = {}) {
  return {
    type: EVENT.COMMAND,
    now: NOW + 1_000,
    utteranceId: 'utt_l',
    text: 'kick off the docs job',
    verb: VERB.LAUNCH,
    ...overrides,
  };
}

function confirmEvent(overrides = {}) {
  return {
    type: EVENT.CONFIRM,
    now: NOW + 2_000,
    utteranceId: 'utt_b',
    text: CONFIRM_TEXT,
    actionId: 'act_a',
    confirmationId: 'cnf_1',
    target: RUN,
    ...overrides,
  };
}

function abortEvent(overrides = {}) {
  return {
    type: EVENT.ABORT,
    now: NOW + 1_000,
    utteranceId: 'utt_z',
    text: 'cancel that',
    ...overrides,
  };
}

/** A fresh machine with the test fixtures' knobs. */
function newMachine(overrides = {}) {
  const result = initialState({
    now: NOW,
    timeoutMs: TIMEOUT,
    maxPending: MAX_PENDING,
    maxLedger: MAX_LEDGER,
    ...overrides,
  });
  assert.equal(result.ok, true, 'the fixtures must be a machine');
  return result.state;
}

/** A machine holding exactly the read-back the given command opened. */
function machineWith(event = cancelEvent(), overrides = {}) {
  const result = step(newMachine(overrides), event);
  assert.equal(result.ok, true, `the fixture command must be read back, not ${result.code}`);
  assert.equal(result.outcome, OUTCOME.READ_BACK);
  return result;
}

function refused(result, code, field) {
  assert.equal(result.ok, false, `expected a refusal, got ${result.outcome}`);
  assert.equal(result.code, code, `expected ${code}, got ${result.code}`);
  if (field !== undefined) assert.equal(result.field, field);
  return result;
}

function accepted(result, outcome) {
  assert.equal(result.ok, true, `expected ${outcome}, got ${result.code}`);
  assert.equal(result.outcome, outcome);
  return result;
}

// ---------------------------------------------------------------------------
// The closed sets
// ---------------------------------------------------------------------------

test('the verb sets are closed, disjoint, and frozen', () => {
  const all = [...DESTRUCTIVE_VERBS, ...PASS_THROUGH_VERBS, ...REFUSED_VERBS];
  assert.equal(new Set(all).size, all.length, 'a verb is in two sets');
  assert.deepEqual([...DESTRUCTIVE_VERBS], [VERB.CANCEL, VERB.CONFIRM_ACTION, VERB.MERGE]);

  // The destructive verbs are exactly P6.3's action-record verbs, spelled the
  // same: the record this machine authorises is the record the schema admits.
  assert.deepEqual([...DESTRUCTIVE_VERBS].sort(), [...SCHEMA_ACTIONS].sort());
  // And D162's launch is not one of them: it travels as a plain message.
  assert.equal(SCHEMA_ACTIONS.includes(VERB.LAUNCH), false);

  // D162's answer: a launch is the only verb that passes through.
  assert.deepEqual([...PASS_THROUGH_VERBS], [VERB.LAUNCH]);

  // The ruling's own list: the kill switch, the budget writes, the deletes.
  for (const verb of ['kill', 'kill-switch', 'budget', 'budget-kill', 'budget-resume', 'budget-set', 'delete', 'delete-account', 'destroy', 'purge', 'reset', 'revoke']) {
    assert.ok(REFUSED_VERBS.includes(verb), `${verb} is not refused outright`);
  }

  for (const set of [DESTRUCTIVE_VERBS, PASS_THROUGH_VERBS, REFUSED_VERBS, CONFIRM_PHRASES, DESTRUCTIVE_VERBS]) {
    assert.ok(Object.isFrozen(set));
  }
});

test('the fixed phrase is P6.1\'s confirmation command, and the words the ruling excludes are not phrases', () => {
  assert.deepEqual([...CONFIRM_PHRASES], ['confirm']);
  // One vocabulary: this module's phrase is P6.1's, imported, not restated.
  assert.deepEqual([...CONFIRM_PHRASES], [...COMMAND_CONFIRMATION_PHRASES]);

  // What the phrase is: whatever P6.1's router reads as the `confirm` app
  // command -- the wake word (or its alias), then "confirm", and nothing else.
  for (const text of [
    'Fugaro, confirm',
    'fugaro confirm',
    'FUGARO, CONFIRM',
    'Figaro, confirm',
    'Fugaro, confirm.',
    'Fugaro: confirm',
    '  Fugaro,   confirm  ',
  ]) {
    assert.equal(isConfirmPhrase(text), true, `${JSON.stringify(text)} is the phrase`);
    assert.equal(isConfirmation(routeUtterance({ utterance: text })), true, `P6.1 disagrees about ${JSON.stringify(text)}`);
  }

  // What it is not: D47's Rule asks for a fixed, short, low-confusability
  // phrase, and G10's own example ("yes, merge it") is not one. A bare
  // "confirm" is not one either: P6.1 routes it to the agent as text (D164), so
  // a machine that accepted it would confirm on words the agent also received.
  for (const text of [
    'confirm',
    'Confirm',
    'confirm.',
    'conform',
    'confirmed',
    'confirmed it',
    'yes',
    'yes, merge it',
    'sure',
    'ok',
    'yeah',
    'yep',
    'correct',
    'affirmative',
    'do it',
    'go ahead',
    'confirmation',
    'please confirm',
    'say confirm',
    'can you confirm',
    'confirm cancel the docs refactor',
    'konfirm',
    'Fugaro, conform',
    'Fugaro, confirmed',
    'Fugaro, confirmed it',
    'Fugaro, confirm it',
    'Fugaro, yes',
    'Fugaro, please confirm',
    'Fugaro, confirm, cancel the docs refactor',
    'Um, Fugaro, confirm',
    'Fugaro, con​firm',
    'Fugaro​, confirm',
    'Fugaro, confirm​',
    '​Fugaro, confirm',
    'Fugaro, confirm⁠',
    'Fugaro, ｃｏｎｆｉｒｍ',
    'Fugaro, confırm',
    'Fugaro, ' + 'confirm '.repeat(600),
    '',
    '   ',
  ]) {
    assert.equal(isConfirmPhrase(text), false, `${JSON.stringify(text)} is not the phrase`);
  }

  // And nothing that is not a string is a phrase.
  for (const value of [undefined, null, 1, true, {}, [], ['Fugaro, confirm']]) {
    assert.equal(isConfirmPhrase(value), false);
  }
});

test('a bare "confirm", which P6.1 forwards to the agent, never confirms a pending action', () => {
  const withPending = machineWith();
  for (const text of ['confirm', 'Confirm', 'CONFIRM', 'confirm.']) {
    refused(step(withPending.state, confirmEvent({ text })), REFUSAL.NOT_A_CONFIRMATION, 'text');
  }
  accepted(step(withPending.state, confirmEvent({ text: 'Figaro, confirm.' })), OUTCOME.CONFIRMED);
});

test('an invisible format character is refused in every text, so what is shown is what is judged', () => {
  const withPending = machineWith();
  for (const text of ['Fugaro, con​firm', 'Fugaro,​ confirm', 'Fugaro, confirm‍', '⁦Fugaro, confirm⁩', 'Fugaro, confirm﻿']) {
    refused(step(withPending.state, confirmEvent({ text })), REFUSAL.BAD_CHARACTER, 'text');
  }
  // A command whose text hides the confirming word behind an invisible
  // character is refused too: otherwise "con​firm" slips past the
  // command-and-confirm rule on a screen that shows "confirm".
  refused(step(newMachine(), cancelEvent({ text: 'cancel it con​firm' })), REFUSAL.BAD_CHARACTER, 'text');
  refused(step(newMachine(), abortEvent({ text: 'cancel that​' })), REFUSAL.BAD_CHARACTER, 'text');
});

// ---------------------------------------------------------------------------
// The machine and its knobs
// ---------------------------------------------------------------------------

test('initialState requires an injected now, and refuses the knobs rather than clamping them', () => {
  const good = initialState({ now: NOW });
  assert.equal(good.ok, true);
  assert.deepEqual(
    [good.state.phase, good.state.now, good.state.pending.length, good.state.ledger.length],
    [PHASE.IDLE, NOW, 0, 0],
  );
  assert.equal(good.state.timeoutMs, DEFAULT_CONFIRM_TIMEOUT_MS);
  assert.equal(good.state.maxPending, DEFAULT_MAX_PENDING);
  assert.equal(good.state.maxLedger, DEFAULT_MAX_LEDGER);
  assert.ok(Object.isFrozen(good.state));
  assert.ok(Object.isFrozen(good.state.pending));
  assert.ok(Object.isFrozen(good.state.ledger));

  refused(initialState({}), REFUSAL.MISSING_FIELD, 'now');
  refused(initialState({ now: undefined }), REFUSAL.MISSING_FIELD, 'now');
  refused(initialState({ now: 0 }), REFUSAL.INVALID_NOW, 'now');
  refused(initialState({ now: -0 }), REFUSAL.INVALID_NOW, 'now');
  refused(initialState({ now: 1.5 }), REFUSAL.INVALID_NOW, 'now');
  refused(initialState({ now: TIME_MAX + 1 }), REFUSAL.INVALID_NOW, 'now');
  refused(initialState({ now: '1700000000000' }), REFUSAL.INVALID_NOW, 'now');
  refused(initialState({ now: NOW, timeoutMs: MIN_CONFIRM_TIMEOUT_MS - 1 }), REFUSAL.INVALID_TIMEOUT, 'timeoutMs');
  refused(initialState({ now: NOW, timeoutMs: MAX_CONFIRM_TIMEOUT_MS + 1 }), REFUSAL.INVALID_TIMEOUT, 'timeoutMs');
  refused(initialState({ now: NOW, maxPending: MIN_MAX_PENDING - 1 }), REFUSAL.INVALID_MAX_PENDING, 'maxPending');
  refused(initialState({ now: NOW, maxPending: MAX_MAX_PENDING + 1 }), REFUSAL.INVALID_MAX_PENDING, 'maxPending');
  refused(initialState({ now: NOW, maxLedger: MIN_MAX_LEDGER - 1 }), REFUSAL.INVALID_MAX_LEDGER, 'maxLedger');
  refused(initialState({ now: NOW, maxLedger: MAX_MAX_LEDGER + 1 }), REFUSAL.INVALID_MAX_LEDGER, 'maxLedger');
  refused(initialState({ now: NOW, window: 1 }), REFUSAL.UNKNOWN_FIELD, 'options');
  refused(initialState(null), REFUSAL.NOT_AN_OBJECT, 'options');

  // The bounds themselves are inside, and are not clamped to something else.
  assert.equal(initialState({ now: NOW, timeoutMs: MIN_CONFIRM_TIMEOUT_MS }).state.timeoutMs, MIN_CONFIRM_TIMEOUT_MS);
  assert.equal(initialState({ now: NOW, timeoutMs: MAX_CONFIRM_TIMEOUT_MS }).state.timeoutMs, MAX_CONFIRM_TIMEOUT_MS);
});

// ---------------------------------------------------------------------------
// The read-back (P6.2: built from the resolved target, never the transcript)
// ---------------------------------------------------------------------------

test('the read-back for "cancel the docs refactor" names the resolved run id', () => {
  const withPending = machineWith(cancelEvent());

  // The speaker called it "the docs refactor"; the read-back does not.
  assert.equal(withPending.readBack, `Cancel run ${RUN.runId}. ${CONFIRM_CUE}`);
  assert.ok(withPending.readBack.includes(RUN.runId));
  assert.ok(!withPending.readBack.includes('docs refactor'), 'the transcript leaked into the read-back');
  assert.ok(!withPending.readBack.includes(CANCEL_TEXT));

  // The pending record holds the resolved target, and no read-back at all: the
  // text a human trusts is rebuilt from the target every time it is asked for.
  assert.deepEqual(withPending.state.pending, [
    {
      actionId: 'act_a',
      verb: VERB.CANCEL,
      target: RUN,
      at: NOW + 1_000,
      deadline: NOW + 1_000 + TIMEOUT,
      by: 'utt_a',
    },
  ]);
  assert.equal(Object.hasOwn(withPending.state.pending[0], 'readBack'), false);
  assert.equal(Object.hasOwn(withPending.state, 'readBack'), false);

  // And the screen's view (P6.12) recomputes the same text.
  const listed = pendingOf(withPending.state, NOW + 1_500);
  assert.equal(listed.ok, true);
  assert.equal(listed.actions.length, 1);
  assert.equal(listed.actions[0].readBack, withPending.readBack);
});

test('the read-back is built from the resolved target alone, for every destructive verb', () => {
  // A transcript that names something else entirely cannot change them.
  assert.equal(
    machineWith(cancelEvent({ text: 'cancel the thing Sam mentioned, not the other one' })).readBack,
    `Cancel run ${RUN.runId}. ${CONFIRM_CUE}`,
  );
  assert.equal(
    machineWith(mergeEvent()).readBack,
    `Merge ${REPO}#${PR} at head ${SHA}. ${CONFIRM_CUE}`,
  );
  assert.equal(
    machineWith(confirmActionEvent()).readBack,
    `Confirm ${REPO}#${PR}. ${CONFIRM_CUE}`,
  );
  assert.equal(
    machineWith(confirmActionEvent({ target: RUN })).readBack,
    `Confirm run ${RUN.runId}. ${CONFIRM_CUE}`,
  );
  // The cue names the phrase P6.1 recognises, wake word and all.
  assert.equal(CONFIRM_CUE, 'Say "Fugaro, confirm" to confirm.');
  // A 64-character SHA is carried as it is, not shortened.
  assert.equal(
    machineWith(mergeEvent({ target: { ...MERGE, headSha: SHA_64 } })).readBack,
    `Merge ${REPO}#${PR} at head ${SHA_64}. ${CONFIRM_CUE}`,
  );

  // readBack is exported for the consumers (P6.12's screen, P6.9's fallback),
  // and it refuses a pair the machine would refuse rather than composing words
  // for it.
  assert.equal(readBack(VERB.CANCEL, RUN), `Cancel run ${RUN.runId}. ${CONFIRM_CUE}`);
  assert.equal(readBack(VERB.CANCEL, RUN_OTHER), `Cancel run ${RUN_OTHER.runId}. ${CONFIRM_CUE}`);
  assert.equal(readBack(VERB.CANCEL, MERGE), null, 'a verb and a target of the wrong kind');
  assert.equal(readBack(VERB.MERGE, { ...MERGE, headSha: 'HEAD' }), null);
  assert.equal(readBack(VERB.LAUNCH, RUN), null, 'a launch is never read back');
  assert.equal(readBack(VERB.CANCEL, 'run_9f3c'), null);
  assert.equal(readBack('defenestrate', RUN), null);
});

test('an ambiguous or unresolved target is refused, so it can never be read back', () => {
  // The router resolves a name against the configured lists; when it cannot, it
  // hands in nothing, and nothing is what can be confirmed.
  for (const target of [undefined, null]) {
    refused(step(newMachine(), cancelEvent({ target })), REFUSAL.UNRESOLVED_TARGET, 'target');
    refused(step(newMachine(), mergeEvent({ target })), REFUSAL.UNRESOLVED_TARGET, 'target');
    refused(step(newMachine(), confirmActionEvent({ target })), REFUSAL.UNRESOLVED_TARGET, 'target');
  }

  // A target that is not a target at all is refused by its own code, not by
  // `unresolved-target`: the two are different facts.
  for (const target of ['run_9f3c', 7, [], true]) {
    refused(step(newMachine(), cancelEvent({ target })), REFUSAL.WRONG_TYPE, 'target');
  }

  // Nothing entered `pending`, so nothing can be confirmed afterwards.
  const after = step(newMachine(), cancelEvent({ target: null }));
  refused(step(after.ok ? after.state : newMachine(), confirmEvent()), REFUSAL.NO_PENDING_ACTION);
});

test('a verb handed an argument of the wrong kind is refused', () => {
  // cancel takes a run, not a merge or an action.
  refused(step(newMachine(), cancelEvent({ target: MERGE })), REFUSAL.VERB_TARGET_MISMATCH, 'target.kind');
  refused(step(newMachine(), cancelEvent({ target: PR_TARGET })), REFUSAL.VERB_TARGET_MISMATCH, 'target.kind');
  // merge takes a repository, PR and head -- never a bare PR, which would be a
  // merge with no head bound to it.
  refused(step(newMachine(), mergeEvent({ target: RUN })), REFUSAL.VERB_TARGET_MISMATCH, 'target.kind');
  refused(step(newMachine(), mergeEvent({ target: PR_TARGET })), REFUSAL.VERB_TARGET_MISMATCH, 'target.kind');
  // confirm takes a run or a repository and PR (P6.3's `confirm`), never a
  // head SHA: P6.3 refuses a head SHA on anything but a merge.
  accepted(step(newMachine(), confirmActionEvent({ target: RUN })), OUTCOME.READ_BACK);
  accepted(step(newMachine(), confirmActionEvent({ target: PR_TARGET })), OUTCOME.READ_BACK);
  refused(step(newMachine(), confirmActionEvent({ target: MERGE })), REFUSAL.VERB_TARGET_MISMATCH, 'target.kind');
  refused(step(newMachine(), confirmActionEvent({ target: { kind: 'action', actionId: 'act_x' } })), REFUSAL.VERB_TARGET_MISMATCH, 'target.kind');

  // And a launch takes neither a target nor an action id: D162's answer is that
  // it is not read back, so an argument beside it is a caller that thinks it is.
  refused(step(newMachine(), launchEvent({ target: RUN })), REFUSAL.VERB_TARGET_MISMATCH, 'target');
  refused(step(newMachine(), launchEvent({ actionId: 'act_l' })), REFUSAL.VERB_TARGET_MISMATCH, 'actionId');

  // A target carrying a field of another kind is refused, not read with it
  // dropped: the field set is the version.
  refused(step(newMachine(), cancelEvent({ target: { ...RUN, repo: REPO } })), REFUSAL.UNKNOWN_FIELD, 'target');
  // A target of the right kind with no id at all is missing a field, which is
  // its own refusal: nothing can be resolved from it, so nothing is read back.
  refused(step(newMachine(), cancelEvent({ target: { kind: TARGET_KIND.RUN } })), REFUSAL.MISSING_FIELD, 'target.runId');
  // And so is a target with no kind at all -- named as the kind's own field,
  // not folded into the refusal for a target of the wrong kind.
  refused(step(newMachine(), cancelEvent({ target: { runId: RUN.runId } })), REFUSAL.MISSING_FIELD, 'target.kind');
});

test('a repository that is not owner/name, a PR that is not a number, and a SHA that is not a SHA are refused', () => {
  const cases = [
    ['', REFUSAL.EMPTY_STRING, 'target.repo'],
    ['fugaro', REFUSAL.BAD_REPO, 'target.repo'],
    ['dimipaun/', REFUSAL.BAD_REPO, 'target.repo'],
    ['/fugaro', REFUSAL.BAD_REPO, 'target.repo'],
    ['a/b/c', REFUSAL.BAD_REPO, 'target.repo'],
    ['dimipaun/fugaro.git', null],
    ['-leading/fugaro', REFUSAL.BAD_REPO, 'target.repo'],
    ['dimipaun/fug;aro', REFUSAL.BAD_REPO, 'target.repo'],
    ['dimipaun/../fugaro', REFUSAL.BAD_REPO, 'target.repo'],
    ['x'.repeat(101) + '/fugaro', REFUSAL.BAD_REPO, 'target.repo'],
    ['x'.repeat(202), REFUSAL.TOO_LONG, 'target.repo'],
    [7, REFUSAL.WRONG_TYPE, 'target.repo'],
  ];
  for (const [repo, code, field] of cases) {
    const result = step(newMachine(), mergeEvent({ target: { ...MERGE, repo } }));
    if (code === null) {
      assert.equal(result.ok, true, `${repo} must be a repository`);
    } else {
      refused(result, code, field);
    }
  }

  for (const pr of [0, -1, -0, 1.5, 1e9, '568', null, undefined]) {
    refused(step(newMachine(), mergeEvent({ target: { ...MERGE, pr } })), REFUSAL.BAD_PR, 'target.pr');
  }

  refused(step(newMachine(), mergeEvent({ target: { ...MERGE, headSha: '' } })), REFUSAL.EMPTY_STRING, 'target.headSha');
  for (const headSha of ['abc', SHA.toUpperCase(), SHA.slice(0, 39), SHA + 'a', 'g'.repeat(40)]) {
    refused(step(newMachine(), mergeEvent({ target: { ...MERGE, headSha } })), REFUSAL.BAD_HEAD_SHA, 'target.headSha');
  }
  // A SHA that is not a string at all is the wrong type, not a bad SHA.
  refused(step(newMachine(), mergeEvent({ target: { ...MERGE, headSha: 7 } })), REFUSAL.WRONG_TYPE, 'target.headSha');
  // Both shapes git and GitHub produce are accepted.
  assert.equal(step(newMachine(), mergeEvent({ target: { ...MERGE, headSha: SHA_64 } })).ok, true);
});

// ---------------------------------------------------------------------------
// D162: the launch
// ---------------------------------------------------------------------------

test('a launch passes through with no read-back and no record, and its utterance is still spent', () => {
  const result = accepted(step(newMachine(), launchEvent()), OUTCOME.PASS_THROUGH);
  assert.equal(result.readBack, null, 'a launch is never read back');
  assert.equal(result.phase, PHASE.IDLE);
  assert.equal(result.state.pending.length, 0, 'a launch opens no pending action');
  assert.equal(result.record, undefined, 'a launch writes no action record');

  // "one utterance, one event": the launch's utterance is spent like any other,
  // so the id is not a second utterance later.
  assert.deepEqual(result.state.ledger, [{ kind: LEDGER_KIND.UTTERANCE, id: 'utt_l', at: NOW + 1_000 }]);
  refused(step(result.state, launchEvent()), REFUSAL.REPLAYED_UTTERANCE, 'utteranceId');

  // A launch from a machine with a read-back outstanding leaves it alone: D162's
  // answer does not depend on the machine's state, so it does not disturb it.
  const withPending = machineWith();
  const after = accepted(step(withPending.state, launchEvent()), OUTCOME.PASS_THROUGH);
  assert.equal(after.phase, PHASE.PENDING);
  assert.deepEqual(after.state.pending, withPending.state.pending);
});

test('the kill switch, the budget writes and the deletes are refused outright', () => {
  for (const verb of REFUSED_VERBS) {
    refused(step(newMachine(), cancelEvent({ verb })), REFUSAL.REFUSED_VERB, 'verb');
    // With a plausible target beside it, too: a refused verb's target is never
    // read, so nothing about it is echoed and nothing about it is trusted.
    refused(step(newMachine(), cancelEvent({ verb, target: null })), REFUSAL.REFUSED_VERB, 'verb');
    refused(step(newMachine(), cancelEvent({ verb, target: { kind: 'run', runId: 'x'.repeat(5000) } })), REFUSAL.REFUSED_VERB, 'verb');
  }

  // An unknown verb is refused too, and by its own code: the verb set is closed,
  // so a new destructive verb is not silently unprotected.
  for (const verb of ['defenestrate', 'Cancel', 'CANCEL', 'cancell', 'ls', '*']) {
    refused(step(newMachine(), cancelEvent({ verb })), REFUSAL.UNKNOWN_VERB, 'verb');
  }
});

// ---------------------------------------------------------------------------
// The phrase, and the separate utterance
// ---------------------------------------------------------------------------

test('a misrecognised "confirm" does not confirm', () => {
  const withPending = machineWith();
  for (const text of ['conform', 'confirmed it', 'confirmed', 'yes', 'sure', 'ok', 'yeah', 'confirm.', 'please confirm', 'yes, cancel it']) {
    assert.equal(isConfirmPhrase(text), false, `${text} must not be the phrase`);
    const result = refused(step(withPending.state, confirmEvent({ text })), REFUSAL.NOT_A_CONFIRMATION, 'text');
    assert.equal(result.state, undefined);
  }

  // The refusal spends nothing: the read-back is still outstanding, and the
  // utterance that misheard is still usable.
  const still = pendingOf(withPending.state, NOW + 2_000);
  assert.equal(still.actions.length, 1);
  // And the utterance that misheard is still usable: a misrecognition is a
  // refusal, not a spent utterance, so the same utterance can say the phrase.
  accepted(step(withPending.state, confirmEvent()), OUTCOME.CONFIRMED);

  // A confirm with no pending action is refused as well, so "yes" cannot
  // conjure one.
  refused(step(newMachine(), confirmEvent()), REFUSAL.NO_PENDING_ACTION, 'actionId');
});

test('the fixed phrase confirms exactly once, and the action record is what it authorises', () => {
  const withPending = machineWith(cancelEvent({ now: NOW + 1_000 }));
  const result = accepted(step(withPending.state, confirmEvent({ now: NOW + 2_000 })), OUTCOME.CONFIRMED);

  assert.equal(result.phase, PHASE.IDLE);
  assert.equal(result.actionId, 'act_a');
  assert.equal(result.confirmationId, 'cnf_1');
  assert.equal(result.confirmedAt, NOW + 2_000);
  assert.deepEqual(result.target, RUN);

  // The record is P6.3's action record, field for field: the action id, the
  // verb, the resolved target, and the confirmation's id and time -- and
  // nothing else. It is what goes into `message.action` unchanged.
  assert.deepEqual(result.record, {
    id: 'act_a',
    verb: VERB.CANCEL,
    target: { run: RUN.runId },
    confirmation: { id: 'cnf_1', at: NOW + 2_000 },
  });
  assert.deepEqual(Object.keys(result.record).sort(), ['confirmation', 'id', 'target', 'verb']);
  assert.ok(Object.isFrozen(result.record));
  assert.ok(Object.isFrozen(result.record.target));
  assert.ok(Object.isFrozen(result.record.confirmation));

  // The pending action is consumed, and every id the confirmation spent is in
  // the ledger: this is what makes a replay visible.
  assert.equal(result.state.pending.length, 0);
  assert.deepEqual(result.state.ledger, [
    { kind: LEDGER_KIND.ACTION, id: 'act_a', at: NOW + 1_000 },
    { kind: LEDGER_KIND.UTTERANCE, id: 'utt_a', at: NOW + 1_000 },
    { kind: LEDGER_KIND.CONFIRMATION, id: 'cnf_1', at: NOW + 2_000 },
    { kind: LEDGER_KIND.UTTERANCE, id: 'utt_b', at: NOW + 2_000 },
  ]);

  // The read-back spoken at confirmation time is the target that will happen.
  assert.equal(result.readBack, `Cancel run ${RUN.runId}. ${CONFIRM_CUE}`);
});

test('every record the machine authorises is an action record P6.3 accepts, and binds what was read back', () => {
  // A message wrapping the record exactly as the app will send it. The mac is a
  // placeholder of the right shape: its value is P6.3's and P6.7's business.
  const wrap = (record) => ({
    id: 'msg_1',
    sender: 'phone',
    text: 'action',
    at: NOW,
    mac: '0'.repeat(64),
    kind: 'action',
    action: record,
  });
  const cases = [
    [cancelEvent(), RUN, { id: 'act_a', verb: 'cancel', target: { run: RUN.runId } }],
    [mergeEvent(), MERGE, { id: 'act_m', verb: 'merge', target: { repo: REPO, pr: PR }, head_sha: SHA }],
    [mergeEvent({ target: { ...MERGE, headSha: SHA_64 } }), { ...MERGE, headSha: SHA_64 }, { id: 'act_m', verb: 'merge', target: { repo: REPO, pr: PR }, head_sha: SHA_64 }],
    [confirmActionEvent({ target: RUN }), RUN, { id: 'act_c', verb: 'confirm', target: { run: RUN.runId } }],
    [confirmActionEvent(), PR_TARGET, { id: 'act_c', verb: 'confirm', target: { repo: REPO, pr: PR } }],
  ];
  for (const [command, target, expected] of cases) {
    const opened = machineWith(command);
    const confirmed = accepted(
      step(opened.state, confirmEvent({ actionId: command.actionId, target, now: NOW + 2_000 })),
      OUTCOME.CONFIRMED,
    );
    assert.deepEqual(confirmed.record, { ...expected, confirmation: { id: 'cnf_1', at: NOW + 2_000 } });
    const validated = validateMessage(wrap(confirmed.record));
    assert.equal(validated.ok, true, `P6.3 refuses the ${command.verb} record: ${validated.code} ${validated.field}`);
    assert.deepEqual(validated.message.action, confirmed.record);
  }

  // A merge record without its head is not a record P6.3 admits, so the head
  // the machine binds is load-bearing, not decoration.
  const merge = machineWith(mergeEvent());
  const record = accepted(step(merge.state, confirmEvent({ actionId: 'act_m', target: MERGE })), OUTCOME.CONFIRMED).record;
  const { head_sha: _dropped, ...headless } = record;
  assert.equal(validateMessage(wrap(headless)).ok, false);
});

test('a target P6.3 would refuse is refused before it is read back, so it can never be confirmed', () => {
  // A run id the consumer would pass to `fugaro cancel` as a flag or a path,
  // or one carrying a character outside P6.3's run alphabet.
  for (const runId of ['-rf', '--help', '.hidden', 'run 1', 'run@1', 'run,1', 'ünï']) {
    refused(step(newMachine(), cancelEvent({ target: { kind: TARGET_KIND.RUN, runId } })), REFUSAL.BAD_KEY, 'target.runId');
    refused(step(newMachine(), confirmActionEvent({ target: { kind: TARGET_KIND.RUN, runId } })), REFUSAL.BAD_KEY, 'target.runId');
  }
  // P6.3's run alphabet admits these, so this module does too.
  for (const runId of ['20261018-125859-d3c8', 'run_9f3c', 'a:b', '_x']) {
    accepted(step(newMachine(), cancelEvent({ target: { kind: TARGET_KIND.RUN, runId } })), OUTCOME.READ_BACK);
  }
  // A repository P6.3 caps at 200 code units, both segments full.
  const long = `${'o'.repeat(100)}/${'n'.repeat(100)}`;
  refused(step(newMachine(), mergeEvent({ target: { ...MERGE, repo: long } })), REFUSAL.TOO_LONG, 'target.repo');
  refused(step(newMachine(), confirmActionEvent({ target: { ...PR_TARGET, repo: long } })), REFUSAL.TOO_LONG, 'target.repo');
  // And a PR number with nothing in front of it is P6.3's too.
  refused(step(newMachine(), confirmActionEvent({ target: { ...PR_TARGET, pr: 0 } })), REFUSAL.BAD_PR, 'target.pr');
});

test('a command and "confirm" in one utterance is refused', () => {
  for (const text of [
    'cancel the docs refactor, confirm',
    'confirm cancel the docs refactor',
    'cancel run 42 confirm please',
    'merge it — confirm',
    'Confirm the docs refactor',
    // Punctuation glued to the word is still the word: P6.1 strips it, so a
    // command that ends "confirm." carries the phrase as much as one ending
    // "confirm" does.
    'cancel the docs refactor confirm.',
    'cancel the docs refactor,confirm',
    'cancel the docs refactor (confirm)',
    'cancel the docs refactor -- "confirm"',
    'Fugaro, cancel the docs refactor; Fugaro, confirm!',
  ]) {
    refused(step(newMachine(), cancelEvent({ text })), REFUSAL.COMMAND_AND_CONFIRM, 'text');
  }
  // "cancel that" is a command too, and carries one.
  refused(step(newMachine(), abortEvent({ text: 'cancel that, confirm' })), REFUSAL.COMMAND_AND_CONFIRM, 'text');

  // A command whose text merely contains the letters is not carrying the phrase:
  // the token is a word.
  assert.equal(step(newMachine(), cancelEvent({ text: 'cancel the confirmation job' })).ok, true);
  assert.equal(step(newMachine(), cancelEvent({ text: 'cancel the reconfirm job' })).ok, true);
});

test('the utterance that issued the command can never confirm it', () => {
  const withPending = machineWith();
  refused(
    step(withPending.state, confirmEvent({ utteranceId: 'utt_a' })),
    REFUSAL.SAME_UTTERANCE,
    'utteranceId',
  );

  // Even with a fresh confirmation id and the exact target: the utterance is the
  // thing that is refused.
  refused(
    step(withPending.state, confirmEvent({ utteranceId: 'utt_a', confirmationId: 'cnf_2' })),
    REFUSAL.SAME_UTTERANCE,
    'utteranceId',
  );

  // A different utterance confirms it.
  assert.equal(step(withPending.state, confirmEvent({ utteranceId: 'utt_b' })).ok, true);
});

// ---------------------------------------------------------------------------
// One confirmation authorises exactly one action
// ---------------------------------------------------------------------------

test('one confirmation authorises exactly one action, and a replay authorises nothing', () => {
  const withPending = machineWith();
  const first = accepted(step(withPending.state, confirmEvent()), OUTCOME.CONFIRMED);

  // The same confirmation again, byte for byte: the pending action it named is
  // gone, so there is nothing left for it to authorise.
  const replay = step(first.state, confirmEvent());
  refused(replay, REFUSAL.NO_PENDING_ACTION, 'actionId');
  assert.equal(replay.state, undefined, 'a refusal carries no state');

  // The same utterance id, a fresh confirmation id and a fresh action: refused
  // as a replayed utterance.
  const second = accepted(
    step(first.state, cancelEvent({ utteranceId: 'utt_d', actionId: 'act_d', now: NOW + 3_000 })),
    OUTCOME.READ_BACK,
  );
  refused(
    step(second.state, confirmEvent({ utteranceId: 'utt_b', actionId: 'act_d', confirmationId: 'cnf_2', now: NOW + 4_000 })),
    REFUSAL.REPLAYED_UTTERANCE,
    'utteranceId',
  );

  // The same confirmation id, a fresh utterance, against a *different* action:
  // refused as a replayed confirmation. This is the red step's subject -- a
  // machine without the action-id check would authorise the second action here.
  refused(
    step(second.state, confirmEvent({ utteranceId: 'utt_e', actionId: 'act_d', confirmationId: 'cnf_1', now: NOW + 4_000 })),
    REFUSAL.REPLAYED_CONFIRMATION,
    'confirmationId',
  );

  // And with a fresh confirmation id the same pending action *is* confirmable:
  // the refusal above is about the ids, not about the action.
  const third = accepted(
    step(second.state, confirmEvent({ utteranceId: 'utt_e', actionId: 'act_d', confirmationId: 'cnf_3', now: NOW + 4_000 })),
    OUTCOME.CONFIRMED,
  );
  assert.equal(third.actionId, 'act_d');
});

test('a replayed utterance id is refused whatever it carries', () => {
  const first = machineWith();
  refused(step(first.state, cancelEvent()), REFUSAL.REPLAYED_UTTERANCE, 'utteranceId');
  refused(step(first.state, cancelEvent({ now: NOW + 5_000 })), REFUSAL.REPLAYED_UTTERANCE, 'utteranceId');
  refused(step(first.state, mergeEvent({ utteranceId: 'utt_a' })), REFUSAL.REPLAYED_UTTERANCE, 'utteranceId');
  refused(step(first.state, launchEvent({ utteranceId: 'utt_a' })), REFUSAL.REPLAYED_UTTERANCE, 'utteranceId');
  refused(step(first.state, abortEvent({ utteranceId: 'utt_a' })), REFUSAL.REPLAYED_UTTERANCE, 'utteranceId');
});

test('a reused action id is refused, so one action id is one action', () => {
  const first = machineWith();
  refused(
    step(first.state, cancelEvent({ utteranceId: 'utt_f', now: NOW + 2_000 })),
    REFUSAL.REPLAYED_ACTION,
    'actionId',
  );
  // Even after the first action is confirmed and gone: the ledger remembers it.
  const confirmed = accepted(step(first.state, confirmEvent()), OUTCOME.CONFIRMED);
  refused(
    step(confirmed.state, cancelEvent({ utteranceId: 'utt_f', now: NOW + 2_000 })),
    REFUSAL.REPLAYED_ACTION,
    'actionId',
  );
});

// ---------------------------------------------------------------------------
// The target, the second pending action, and the timeout
// ---------------------------------------------------------------------------

test('a confirm for a different target is refused', () => {
  // The pending action changed: the speaker confirms a run that is not the one
  // read back.
  const withPending = machineWith();
  refused(step(withPending.state, confirmEvent({ target: RUN_OTHER })), REFUSAL.DIFFERENT_TARGET, 'target');

  // The read-back the speaker heard was a paraphrase of nothing: the target it
  // named is the only one this confirmation may assert.
  refused(
    step(withPending.state, confirmEvent({ target: 'run_9f3c' })),
    REFUSAL.WRONG_TYPE,
    'target',
  );

  // For a merge, the head moving is exactly this: the read-back is stale, and
  // confirming it would merge a commit the speaker never heard named (D169).
  const merge = machineWith(mergeEvent());
  accepted(step(merge.state, confirmEvent({ target: MERGE, actionId: 'act_m', now: NOW + 2_000 })), OUTCOME.CONFIRMED);
  const moved = step(merge.state, confirmEvent({ target: { ...MERGE, headSha: SHA_MOVED }, actionId: 'act_m' }));
  refused(moved, REFUSAL.DIFFERENT_TARGET, 'target');

  // A confirmation that names the same PR but another repository is refused too.
  refused(
    step(merge.state, confirmEvent({ target: { ...MERGE, repo: 'someone/else' }, actionId: 'act_m' })),
    REFUSAL.DIFFERENT_TARGET,
    'target',
  );

  // The merge record carries the head the speaker was read back, bound as
  // P6.3's `head_sha`, so the consumer can refuse a merge whose head moved.
  const mergeConfirm = step(merge.state, confirmEvent({ target: MERGE, actionId: 'act_m', now: NOW + 2_000 }));
  assert.deepEqual(mergeConfirm.record.target, { repo: REPO, pr: PR });
  assert.equal(mergeConfirm.record.head_sha, SHA);
});

test('a second pending action makes a confirm ambiguous, and abort clears them all', () => {
  const first = machineWith(cancelEvent({ now: NOW + 1_000 }));
  const second = accepted(
    step(first.state, cancelEvent({ utteranceId: 'utt_2', actionId: 'act_2', target: RUN_OTHER, now: NOW + 1_500 })),
    OUTCOME.READ_BACK,
  );
  assert.equal(second.phase, PHASE.CROWDED);

  // "a confirm for a different target (the pending action changed, or a second
  // pending action) is refused": with two read-backs outstanding, a bare
  // "confirm" has not said which one, so it authorises neither.
  refused(step(second.state, confirmEvent({ now: NOW + 2_000 })), REFUSAL.AMBIGUOUS_PENDING, 'pending');
  refused(
    step(second.state, confirmEvent({ actionId: 'act_2', target: RUN_OTHER, confirmationId: 'cnf_2', now: NOW + 2_000 })),
    REFUSAL.AMBIGUOUS_PENDING,
    'pending',
  );
  // Even a replayed confirmation is refused here, and by the ambiguity: nothing
  // is authorised, which is the point.
  refused(step(second.state, confirmEvent({ confirmationId: 'cnf_1', now: NOW + 2_000 })), REFUSAL.AMBIGUOUS_PENDING, 'pending');

  // "cancel that" forgets both, and authorises nothing.
  const aborted = accepted(step(second.state, abortEvent({ now: NOW + 2_000 })), OUTCOME.DISCARDED);
  assert.equal(aborted.phase, PHASE.IDLE);
  assert.deepEqual([...aborted.actionIds].sort(), ['act_2', 'act_a']);
  assert.equal(aborted.state.pending.length, 0);
  refused(step(aborted.state, confirmEvent({ now: NOW + 3_000 })), REFUSAL.NO_PENDING_ACTION, 'actionId');

  // "cancel that" with nothing outstanding is a fact, not a refusal.
  const nothing = accepted(step(newMachine(), abortEvent()), OUTCOME.NOTHING_PENDING);
  assert.deepEqual([...nothing.actionIds], []);
  assert.equal(nothing.phase, PHASE.IDLE);
});

test('a confirm after the timeout is refused, and at the deadline exactly it is not', () => {
  const withPending = machineWith(cancelEvent({ now: NOW + 1_000 }));
  const deadline = NOW + 1_000 + TIMEOUT;

  // One millisecond past: refused.
  refused(step(withPending.state, confirmEvent({ now: deadline + 1 })), REFUSAL.CONFIRMATION_EXPIRED, 'now');
  refused(step(withPending.state, confirmEvent({ now: deadline + TIMEOUT })), REFUSAL.CONFIRMATION_EXPIRED, 'now');

  // At the deadline exactly: inside. The bound is inclusive on purpose, and the
  // test states it so a reader can check the arithmetic by hand.
  accepted(step(withPending.state, confirmEvent({ now: deadline })), OUTCOME.CONFIRMED);

  // A hand-built state whose deadline is inside the timeout bounds is read the
  // same way: the timeout is a state field, and the caller may move it.
  const short = machineWith(cancelEvent({ now: NOW + 1_000 }), { timeoutMs: MIN_CONFIRM_TIMEOUT_MS });
  refused(step(short.state, confirmEvent({ now: NOW + 1_000 + MIN_CONFIRM_TIMEOUT_MS + 1 })), REFUSAL.CONFIRMATION_EXPIRED, 'now');
  accepted(step(short.state, confirmEvent({ now: NOW + 1_000 + MIN_CONFIRM_TIMEOUT_MS })), OUTCOME.CONFIRMED);
});

test('an expired read-back is swept by the next command, so the pending set stays bounded', () => {
  const withPending = machineWith(cancelEvent({ now: NOW + 1_000 }));
  // A command long after the read-back: the stale one is dropped, not carried.
  const later = accepted(
    step(withPending.state, mergeEvent({ now: NOW + 1_000 + TIMEOUT + 1 })),
    OUTCOME.READ_BACK,
  );
  assert.equal(later.phase, PHASE.PENDING, 'the stale read-back was carried');
  assert.deepEqual(later.state.pending.map((one) => one.actionId), ['act_m']);

  // A confirm is not a sweep: the timeout is *named* rather than answered with
  // "nothing is pending".
  const stale = machineWith(cancelEvent({ now: NOW + 1_000 }), { maxPending: 1 });
  refused(step(stale.state, confirmEvent({ now: NOW + 1_000 + TIMEOUT + 1 })), REFUSAL.CONFIRMATION_EXPIRED, 'now');

  // And the cap refuses rather than evicting a live read-back.
  const full = machineWith(cancelEvent({ now: NOW + 1_000 }), { maxPending: 1 });
  refused(
    step(full.state, cancelEvent({ utteranceId: 'utt_2', actionId: 'act_2', target: RUN_OTHER, now: NOW + 1_500 })),
    REFUSAL.PENDING_FULL,
    'pending',
  );
});

test('the id ledger refuses when it is full, and never forgets to make room', () => {
  // maxLedger 2 is exactly one command (an utterance and an action).
  const machine = newMachine({ maxLedger: 2 });
  const first = accepted(step(machine, cancelEvent({ now: NOW + 1_000 })), OUTCOME.READ_BACK);
  assert.equal(first.state.ledger.length, 2);

  refused(step(first.state, cancelEvent({ utteranceId: 'utt_2', actionId: 'act_2', now: NOW + 1_500 })), REFUSAL.LEDGER_FULL, 'ledger');
  refused(step(first.state, launchEvent({ now: NOW + 1_500 })), REFUSAL.LEDGER_FULL, 'ledger');
  refused(step(first.state, abortEvent({ now: NOW + 1_500 })), REFUSAL.LEDGER_FULL, 'ledger');
  // A confirmation needs two records of its own, so a full ledger refuses it.
  refused(step(first.state, confirmEvent({ now: NOW + 2_000 })), REFUSAL.LEDGER_FULL, 'ledger');

  // Nothing was forgotten: the ledger still holds exactly what it held.
  assert.equal(first.state.ledger.length, 2);

  // A destructive command and a confirmation each spend two ids (an utterance
  // and an action, or an utterance and a confirmation); a launch and "cancel
  // that" spend one. With maxLedger 3 and two spent, the two-id events are
  // refused and the one-id events are not.
  const roomy = machineWith(cancelEvent({ now: NOW + 1_000 }), { maxLedger: 3 });
  assert.equal(roomy.state.ledger.length, 2);
  refused(step(roomy.state, cancelEvent({ utteranceId: 'utt_2', actionId: 'act_2', now: NOW + 1_500 })), REFUSAL.LEDGER_FULL, 'ledger');
  refused(step(roomy.state, confirmEvent({ now: NOW + 2_000 })), REFUSAL.LEDGER_FULL, 'ledger');
  accepted(step(roomy.state, launchEvent({ now: NOW + 1_500 })), OUTCOME.PASS_THROUGH);
  accepted(step(roomy.state, abortEvent({ now: NOW + 1_500 })), OUTCOME.DISCARDED);
});

// ---------------------------------------------------------------------------
// The `now` window
// ---------------------------------------------------------------------------

test('a now that goes backwards, or leaps forward, is refused', () => {
  const machine = newMachine();
  refused(step(machine, cancelEvent({ now: NOW - 1 })), REFUSAL.NOW_WENT_BACKWARDS, 'now');
  refused(step(machine, cancelEvent({ now: 1 })), REFUSAL.NOW_WENT_BACKWARDS, 'now');

  // One far-future now would otherwise refuse every later honest one for ever.
  refused(step(machine, cancelEvent({ now: NOW + MAX_FORWARD_JUMP_MS + 1 })), REFUSAL.NOW_JUMPED, 'now');
  accepted(step(machine, cancelEvent({ now: NOW + MAX_FORWARD_JUMP_MS })), OUTCOME.READ_BACK);

  // A now that is not a time at all is refused before the window is even asked.
  refused(step(machine, cancelEvent({ now: -0 })), REFUSAL.INVALID_NOW, 'now');
  refused(step(machine, cancelEvent({ now: 1.5 })), REFUSAL.INVALID_NOW, 'now');
  refused(step(machine, cancelEvent({ now: TIME_MAX + 1 })), REFUSAL.INVALID_NOW, 'now');
  refused(step(machine, cancelEvent({ now: '1700000000000' })), REFUSAL.INVALID_NOW, 'now');
  refused(step(machine, cancelEvent({ now: undefined })), REFUSAL.MISSING_FIELD, 'now');
  refused(step(machine, cancelEvent({ now: Date.now() })), REFUSAL.NOW_JUMPED, 'now');
});

// ---------------------------------------------------------------------------
// Input hygiene
// ---------------------------------------------------------------------------

test('an event that is not an event, and a field it may not carry, are refused', () => {
  const machine = newMachine();

  for (const event of [undefined, null, 42, 'cancel', [], true]) {
    refused(step(machine, event), REFUSAL.NOT_AN_OBJECT, 'event');
  }
  refused(step(machine, new (class Event {})()), REFUSAL.NOT_AN_OBJECT, 'event');

  refused(step(machine, { now: NOW }), REFUSAL.MISSING_FIELD, 'type');
  refused(step(machine, { type: EVENT.COMMAND, now: NOW, utteranceId: 'u', text: 'cancel it', verb: 'cancel', target: RUN }), REFUSAL.MISSING_FIELD, 'actionId');
  refused(step(machine, { type: EVENT.CONFIRM, now: NOW, text: CONFIRM_TEXT, actionId: 'a', confirmationId: 'c', target: RUN }), REFUSAL.MISSING_FIELD, 'utteranceId');
  refused(step(machine, { type: EVENT.ABORT, now: NOW, utteranceId: 'u' }), REFUSAL.MISSING_FIELD, 'text');
  refused(step(machine, { type: EVENT.CONFIRM, now: NOW, utteranceId: 'u', text: CONFIRM_TEXT, confirmationId: 'c', target: RUN }), REFUSAL.MISSING_FIELD, 'actionId');

  // The field set is the version: a field of another event, or one this module
  // does not name, is refused rather than read with it dropped.
  refused(step(machine, { ...confirmEvent(), verb: 'cancel' }), REFUSAL.UNKNOWN_FIELD, 'event');
  refused(step(machine, { ...abortEvent(), target: RUN }), REFUSAL.UNKNOWN_FIELD, 'event');
  refused(step(machine, { ...cancelEvent(), confirmationId: 'cnf_9' }), REFUSAL.UNKNOWN_FIELD, 'event');
  refused(step(machine, { ...cancelEvent(), admin: true }), REFUSAL.UNKNOWN_FIELD, 'event');
  for (const field of ['runId', 'action', 'command', 'utterance', 'action_id', 'ActionId']) {
    refused(step(machine, { ...cancelEvent(), [field]: 'x' }), REFUSAL.UNKNOWN_FIELD, 'event');
  }
  assert.deepEqual([...COMMAND_FIELDS].sort(), ['actionId', 'now', 'target', 'text', 'type', 'utteranceId', 'verb']);
  assert.deepEqual([...CONFIRM_FIELDS].sort(), ['actionId', 'confirmationId', 'now', 'target', 'text', 'type', 'utteranceId']);
  assert.deepEqual([...ABORT_FIELDS].sort(), ['now', 'text', 'type', 'utteranceId']);
});

test('a phase that disagrees with the pending read-backs is refused rather than trusted', () => {
  const withPending = machineWith();
  refused(step({ ...withPending.state, phase: PHASE.IDLE }, confirmEvent()), REFUSAL.INVALID_STATE, 'phase');
  refused(step({ ...withPending.state, phase: PHASE.CROWDED }, confirmEvent()), REFUSAL.INVALID_STATE, 'phase');
  refused(step({ ...newMachine(), phase: PHASE.PENDING }, abortEvent()), REFUSAL.INVALID_STATE, 'phase');

  refused(step({ ...newMachine(), phase: 'waiting' }, abortEvent()), REFUSAL.UNKNOWN_STATE, 'phase');
  refused(step({ ...newMachine(), phase: null }, abortEvent()), REFUSAL.UNKNOWN_STATE, 'phase');
  refused(step({ ...newMachine(), phase: 'IDLE' }, abortEvent()), REFUSAL.UNKNOWN_STATE, 'phase');
});

test('a state that is not a state is refused, each way with its own code', () => {
  const state = newMachine();
  const pending = machineWith().state.pending[0];
  const entry = { kind: LEDGER_KIND.UTTERANCE, id: 'utt_x', at: NOW - 1_000 };

  refused(step({ ...state, phase: PHASE.IDLE, extra: 1 }, abortEvent()), REFUSAL.UNKNOWN_FIELD, 'state');
  refused(step(null, abortEvent()), REFUSAL.NOT_AN_OBJECT, 'state');
  refused(step(state, { ...abortEvent(), now: undefined }), REFUSAL.MISSING_FIELD, 'now');

  const missing = { ...state };
  delete missing.ledger;
  refused(step(missing, abortEvent()), REFUSAL.MISSING_FIELD, 'ledger');
  refused(step({ ...state, timeoutMs: MAX_CONFIRM_TIMEOUT_MS + 1 }, abortEvent()), REFUSAL.INVALID_TIMEOUT, 'timeoutMs');
  refused(step({ ...state, maxPending: 0 }, abortEvent()), REFUSAL.INVALID_MAX_PENDING, 'maxPending');
  refused(step({ ...state, maxLedger: 0 }, abortEvent()), REFUSAL.INVALID_MAX_LEDGER, 'maxLedger');
  refused(step({ ...state, now: 0 }, abortEvent()), REFUSAL.INVALID_NOW, 'now');

  // The pending set.
  refused(step({ ...state, pending: 'none' }, abortEvent()), REFUSAL.INVALID_PENDING, 'pending');
  // Two records for one action id, before the phase is even asked about.
  refused(step({ ...state, phase: PHASE.PENDING, pending: [{ ...pending }, { ...pending }] }, abortEvent()), REFUSAL.DUPLICATE_PENDING_ENTRY, 'pending');
  refused(step({ ...state, phase: PHASE.PENDING, pending: [{ ...pending, verb: VERB.LAUNCH }] }, abortEvent()), REFUSAL.INVALID_PENDING_ENTRY, 'pending');
  refused(step({ ...state, phase: PHASE.PENDING, pending: [{ ...pending, extra: 1 }] }, abortEvent()), REFUSAL.INVALID_PENDING_ENTRY, 'pending');
  refused(step({ ...state, phase: PHASE.PENDING, pending: [{ ...pending, deadline: pending.at }] }, abortEvent()), REFUSAL.INVALID_PENDING_ENTRY, 'pending');
  // A deadline further out than the longest timeout this module accepts could
  // never have been minted by it, so it is refused.
  refused(
    step({ ...state, phase: PHASE.PENDING, pending: [{ ...pending, deadline: pending.at + MAX_CONFIRM_TIMEOUT_MS + 1 }] }, abortEvent()),
    REFUSAL.INVALID_PENDING_ENTRY,
    'pending',
  );
  refused(step({ ...state, phase: PHASE.PENDING, pending: [{ ...pending, at: -0 }] }, abortEvent()), REFUSAL.INVALID_PENDING_ENTRY, 'pending');
  refused(step({ ...state, phase: PHASE.PENDING, pending: [{ ...pending, by: '' }] }, abortEvent()), REFUSAL.INVALID_PENDING_ENTRY, 'pending');
  refused(step({ ...state, phase: PHASE.PENDING, pending: [{ ...pending, actionId: 'a/b' }] }, abortEvent()), REFUSAL.INVALID_PENDING_ENTRY, 'pending');
  refused(step({ ...state, phase: PHASE.PENDING, pending: [{ ...pending, target: { ...RUN, kind: TARGET_KIND.MERGE } }] }, abortEvent()), REFUSAL.INVALID_PENDING_ENTRY, 'pending');
  // Two records for one action id, and a set out of canonical order.
  const two = { ...pending, actionId: 'act_b', at: pending.at + 1, deadline: pending.deadline + 1 };
  const later = { ...pending, actionId: 'act_c', at: pending.at + 2, deadline: pending.deadline + 2 };
  refused(
    step({ ...state, phase: PHASE.CROWDED, maxPending: 4, pending: [pending, { ...pending }] }, abortEvent()),
    REFUSAL.DUPLICATE_PENDING_ENTRY,
    'pending',
  );
  refused(
    step({ ...state, phase: PHASE.CROWDED, maxPending: 4, pending: [later, two, pending] }, abortEvent()),
    REFUSAL.UNSORTED_PENDING,
    'pending',
  );
  // Longer than the state's own cap is not a state either.
  refused(
    step({ ...state, phase: PHASE.CROWDED, maxPending: 1, pending: [two, later] }, abortEvent()),
    REFUSAL.INVALID_PENDING,
    'pending',
  );

  // The ledger.
  refused(step({ ...state, ledger: 'none' }, abortEvent()), REFUSAL.INVALID_LEDGER, 'ledger');
  refused(step({ ...state, ledger: [{ ...entry, kind: 'prompt' }] }, abortEvent()), REFUSAL.INVALID_LEDGER_ENTRY, 'ledger');
  refused(step({ ...state, ledger: [{ ...entry, extra: 1 }] }, abortEvent()), REFUSAL.INVALID_LEDGER_ENTRY, 'ledger');
  refused(step({ ...state, ledger: [{ ...entry, id: '' }] }, abortEvent()), REFUSAL.INVALID_LEDGER_ENTRY, 'ledger');
  refused(step({ ...state, ledger: [{ ...entry, id: 'i'.repeat(ID_MAX + 1) }] }, abortEvent()), REFUSAL.INVALID_LEDGER_ENTRY, 'ledger');
  refused(step({ ...state, ledger: [{ ...entry, at: 0 }] }, abortEvent()), REFUSAL.INVALID_LEDGER_ENTRY, 'ledger');
  refused(step({ ...state, ledger: [entry, { ...entry }] }, abortEvent()), REFUSAL.DUPLICATE_LEDGER_ENTRY, 'ledger');
  refused(
    step({ ...state, ledger: [entry, { ...entry, id: 'utt_y', at: NOW - 2_000 }] }, abortEvent()),
    REFUSAL.UNSORTED_LEDGER,
    'ledger',
  );
  refused(step({ ...state, maxLedger: 1, ledger: [entry, { ...entry, id: 'utt_y' }] }, abortEvent()), REFUSAL.INVALID_LEDGER, 'ledger');

  // A state the module wrote is read back by the module, every field of it.
  const good = machineWith().state;
  assert.equal(step(good, abortEvent({ utteranceId: 'utt_q' })).ok, true);
  assert.deepEqual([...STATE_FIELDS].sort(), ['ledger', 'maxLedger', 'maxPending', 'now', 'pending', 'phase', 'timeoutMs']);
});

test('an accessor property is refused, and the getter is never called', () => {
  const machine = newMachine();
  let reads = 0;
  const getter = () => {
    reads++;
    return NOW;
  };

  const eventWithGetter = { ...cancelEvent() };
  delete eventWithGetter.now;
  Object.defineProperty(eventWithGetter, 'now', { get: getter, enumerable: true, configurable: true });
  refused(step(machine, eventWithGetter), REFUSAL.ACCESSOR_PROPERTY, 'event');
  assert.equal(reads, 0, 'the getter must never be called');

  const stateWithGetter = { ...newMachine() };
  delete stateWithGetter.now;
  Object.defineProperty(stateWithGetter, 'now', { get: getter, enumerable: true, configurable: true });
  refused(step(stateWithGetter, abortEvent()), REFUSAL.ACCESSOR_PROPERTY, 'state');
  assert.equal(reads, 0, 'the getter must never be called');

  // A getter inside the ledger is refused as a ledger entry, and never called:
  // a ledger that answered differently each read could hide a spent id.
  const ledgerWithGetter = [{ kind: LEDGER_KIND.UTTERANCE, id: 'utt_x' }];
  Object.defineProperty(ledgerWithGetter[0], 'at', { get: getter, enumerable: true, configurable: true });
  refused(step({ ...machine, ledger: ledgerWithGetter }, abortEvent()), REFUSAL.INVALID_LEDGER_ENTRY, 'ledger');
  assert.equal(reads, 0, 'the getter must never be called');
});

test('a ledger that is not a plain array, or a pending set that is not one, is refused', () => {
  const machine = newMachine();

  // An own iterator would decide which spent ids this module sees, so the array
  // is read by index and a non-plain array is not an array.
  const iterated = [{ kind: LEDGER_KIND.UTTERANCE, id: 'utt_x', at: NOW - 1 }];
  iterated[Symbol.iterator] = function* empty() {};
  refused(step({ ...machine, ledger: iterated }, abortEvent()), REFUSAL.INVALID_LEDGER, 'ledger');

  const sparse = [];
  sparse[1] = { kind: LEDGER_KIND.UTTERANCE, id: 'utt_x', at: NOW - 1 };
  refused(step({ ...machine, ledger: sparse }, abortEvent()), REFUSAL.INVALID_LEDGER, 'ledger');

  const subclassed = new (class extends Array {})();
  subclassed.push({ kind: LEDGER_KIND.UTTERANCE, id: 'utt_x', at: NOW - 1 });
  refused(step({ ...machine, ledger: subclassed }, abortEvent()), REFUSAL.INVALID_LEDGER, 'ledger');

  refused(step({ ...machine, pending: {} }, abortEvent()), REFUSAL.INVALID_PENDING, 'pending');
});

test('an exception from a hostile input is a refusal, and what was thrown is never echoed', () => {
  const hostile = new Proxy(
    {},
    {
      ownKeys() {
        throw new Error('a name this module must not repeat');
      },
    },
  );
  const result = refused(step(newMachine(), hostile), REFUSAL.INTERNAL_ERROR, null);
  assert.equal(JSON.stringify(result).includes('repeat'), false);

  const revoked = Proxy.revocable({}, {});
  revoked.revoke();
  refused(step(newMachine(), revoked.proxy), REFUSAL.INTERNAL_ERROR, null);
  refused(step(revoked.proxy, abortEvent()), REFUSAL.INTERNAL_ERROR, null);
});

test('step never mutates its inputs, and a refusal carries no state', () => {
  const machine = newMachine();
  const before = JSON.parse(JSON.stringify(machine));
  const event = cancelEvent();
  const eventBefore = JSON.parse(JSON.stringify(event));

  const result = accepted(step(machine, event), OUTCOME.READ_BACK);
  assert.deepEqual(JSON.parse(JSON.stringify(machine)), before, 'the state was mutated');
  assert.deepEqual(JSON.parse(JSON.stringify(event)), eventBefore, 'the event was mutated');
  assert.notEqual(result.state, machine, 'the answer must be a new state');

  // Every state the module hands back is frozen, so a caller cannot edit one
  // into a machine the module would never have built.
  assert.ok(Object.isFrozen(result.state));
  assert.ok(Object.isFrozen(result.state.pending));
  assert.ok(Object.isFrozen(result.state.pending[0]));
  assert.ok(Object.isFrozen(result.state.pending[0].target));
  assert.ok(Object.isFrozen(result.state.ledger));
  assert.ok(Object.isFrozen(result.state.ledger[0]));
  assert.ok(Object.isFrozen(result));
  assert.throws(() => {
    result.state.phase = PHASE.IDLE;
  }, TypeError);

  refused(step(machine, cancelEvent({ verb: 'kill-switch' })), REFUSAL.REFUSED_VERB, 'verb');
  const refusal = step(machine, cancelEvent({ verb: 'kill-switch' }));
  assert.equal(Object.hasOwn(refusal, 'state'), false, 'a refusal must carry no state');
  assert.deepEqual(Object.keys(refusal).sort(), ['code', 'field', 'ok']);
});

test('a refusal names the field by a fixed path, and never the value or a key the caller chose', () => {
  const machine = newMachine();
  const secret = 'ghp_this_must_not_be_echoed';

  // The utterance id is the caller's choice: a refusal reports the path.
  refused(step(machine, cancelEvent({ utteranceId: `${secret}/x` })), REFUSAL.BAD_KEY, 'utteranceId');
  refused(step(machine, cancelEvent({ text: secret, verb: 'kill-switch' })), REFUSAL.REFUSED_VERB, 'verb');
  // And an unknown key is reported by the container, never by the key: the key
  // name is the input's choice, and echoing it logs whatever a caller put there.
  const withUnknown = { ...cancelEvent(), [secret]: 1 };
  const result = refused(step(machine, withUnknown), REFUSAL.UNKNOWN_FIELD, 'event');
  assert.equal(result.field, 'event');
  assert.equal(JSON.stringify(result).includes(secret), false);
  assert.equal(JSON.stringify(step(machine, cancelEvent({ text: secret, verb: 'kill-switch' }))).includes(secret), false);
});

test('an id that is not a database key, and a text that is not a text, are refused', () => {
  const machine = newMachine();
  refused(step(machine, cancelEvent({ utteranceId: '' })), REFUSAL.EMPTY_STRING, 'utteranceId');
  refused(step(machine, cancelEvent({ utteranceId: 7 })), REFUSAL.WRONG_TYPE, 'utteranceId');
  refused(step(machine, cancelEvent({ utteranceId: 'u'.repeat(ID_MAX + 1) })), REFUSAL.TOO_LONG, 'utteranceId');
  for (const id of ['a/b', 'a.b', 'a#b', 'a$b', 'a[b', 'a]b', 'a%b', 'a\tb', 'a\nb', 'a\u0000b', 'a\ud800b']) {
    refused(step(machine, cancelEvent({ utteranceId: id })), REFUSAL.BAD_KEY, 'utteranceId');
  }
  refused(step(machine, cancelEvent({ text: '' })), REFUSAL.EMPTY_STRING, 'text');
  refused(step(machine, cancelEvent({ text: 'x'.repeat(TEXT_MAX + 1) })), REFUSAL.TOO_LONG, 'text');
  refused(step(machine, cancelEvent({ text: 7 })), REFUSAL.WRONG_TYPE, 'text');
  refused(step(machine, cancelEvent({ text: 'cancel\u0000it' })), REFUSAL.BAD_CHARACTER, 'text');

  // The same, on a confirmation and an abort.
  refused(step(machine, confirmEvent({ actionId: 'a/b' })), REFUSAL.BAD_KEY, 'actionId');
  refused(step(machine, confirmEvent({ confirmationId: '' })), REFUSAL.EMPTY_STRING, 'confirmationId');
  refused(step(machine, abortEvent({ utteranceId: 'a/b' })), REFUSAL.BAD_KEY, 'utteranceId');

  // A multi-byte character is one character, not two: the bound is code units
  // and a lone surrogate is not a character at all.
  assert.equal(step(machine, cancelEvent({ text: 'café 中文' })).ok, true);
  refused(step(machine, cancelEvent({ text: '\ud83d' })), REFUSAL.BAD_CHARACTER, 'text');
});

// ---------------------------------------------------------------------------
// The screen's view
// ---------------------------------------------------------------------------

test('pending() recomputes the read-back rather than trusting a stored one', () => {
  const withPending = machineWith();

  // There is nowhere for a stored read-back to live: a state whose pending
  // record carries one is not a state this module reads, so a state that has
  // been edited into saying something else is refused rather than spoken.
  const edited = {
    ...withPending.state,
    pending: [{ ...withPending.state.pending[0], readBack: 'Cancel run something else.' }],
  };
  refused(step(edited, confirmEvent()), REFUSAL.INVALID_PENDING_ENTRY, 'pending');
  assert.equal(Object.hasOwn(withPending.state.pending[0], 'readBack'), false);

  const listed = pendingOf(withPending.state, NOW + 1_500);
  assert.equal(listed.ok, true);
  assert.equal(listed.actions[0].readBack, `Cancel run ${RUN.runId}. ${CONFIRM_CUE}`);

  // Expired read-backs are not listed, and the deadline exactly still is.
  assert.equal(pendingOf(withPending.state, NOW + 1_000 + TIMEOUT).actions.length, 1);
  assert.equal(pendingOf(withPending.state, NOW + 1_000 + TIMEOUT + 1).actions.length, 0);

  refused(pendingOf(withPending.state, 0), REFUSAL.INVALID_NOW, 'now');
  refused(pendingOf('not a state', NOW), REFUSAL.NOT_AN_OBJECT, 'state');
});

// ---------------------------------------------------------------------------
// The vectors file (P6.2: the accept/refuse cases as data, for P6.7's parity test)
// ---------------------------------------------------------------------------

test('the vectors file replays exactly: every accept and every refusal', () => {
  const url = new URL('./vectors.json', import.meta.url);
  const vectors = JSON.parse(readFileSync(url, 'utf8'));

  assert.ok(Array.isArray(vectors.cases) && vectors.cases.length >= 15, 'the vectors are the cases');

  let accepts = 0;
  let refusals = 0;
  for (const testCase of vectors.cases) {
    assert.equal(typeof testCase.name, 'string', 'a case is named');

    const opened = initialState({
      now: vectors.now,
      timeoutMs: vectors.timeoutMs,
      maxPending: vectors.maxPending,
      maxLedger: vectors.maxLedger,
    });
    assert.equal(opened.ok, true, `${testCase.name}: the vectors' machine`);

    let state = opened.state;
    assert.equal(testCase.events.length, testCase.expect.length, `${testCase.name}: an expectation per event`);

    for (let index = 0; index < testCase.events.length; index += 1) {
      const event = testCase.events[index];
      const expected = testCase.expect[index];
      const where = `${testCase.name} #${index} (${event.type})`;
      const result = step(state, event);

      if (expected.ok) {
        accepts++;
        assert.equal(result.ok, true, `${where}: expected ${expected.outcome}, got ${result.code}`);
        assert.equal(result.outcome, expected.outcome, `${where}: outcome`);
        if (expected.phase !== undefined) assert.equal(result.phase, expected.phase, `${where}: phase`);
        if (expected.readBack !== undefined) assert.equal(result.readBack, expected.readBack, `${where}: read-back`);
        if (expected.actionId !== undefined) assert.equal(result.actionId, expected.actionId, `${where}: action id`);
        if (expected.confirmationId !== undefined) assert.equal(result.confirmationId, expected.confirmationId, `${where}: confirmation id`);
        if (expected.record !== undefined) assert.deepEqual(result.record, expected.record, `${where}: record`);
        state = result.state;
      } else {
        refusals++;
        assert.equal(result.ok, false, `${where}: expected the refusal ${expected.code}`);
        assert.equal(result.code, expected.code, `${where}: refusal code`);
        assert.equal(result.field, expected.field, `${where}: refusal field`);
        // A refusal keeps the machine where it was: the next event is judged
        // against the same state.
      }
    }
  }

  assert.ok(accepts >= 15, `only ${accepts} accepts`);
  assert.ok(refusals >= 15, `only ${refusals} refusals`);
});

// ---------------------------------------------------------------------------
// The total table
// ---------------------------------------------------------------------------

test('the transition table is total: every phase x every event has a defined outcome', () => {
  const phases = Object.values(PHASE);
  const events = Object.values(EVENT);

  assert.deepEqual(sorted(Object.keys(TRANSITIONS)), sorted(phases), 'a phase has no row');

  const outcomes = new Set();
  const vocabulary = new Set(Object.values(REFUSAL));
  for (const phase of phases) {
    assert.deepEqual(sorted(Object.keys(TRANSITIONS[phase])), sorted(events), `${phase} has no full row`);
    for (const event of events) {
      const cell = TRANSITIONS[phase][event];
      assert.ok(cell.ok.length + cell.no.length > 0, `${phase} x ${event} has neither an outcome nor a refusal`);
      assert.ok(cell.next.length > 0, `${phase} x ${event} names no next phase`);
      assert.equal(new Set(cell.no).size, cell.no.length, `${phase} x ${event} lists a refusal twice`);
      for (const next of cell.next) assert.ok(phases.includes(next), `${phase} x ${event} names ${next}`);
      for (const outcome of cell.ok) outcomes.add(outcome);
      for (const code of cell.no) assert.ok(vocabulary.has(code), `${phase} x ${event}: ${code} is not a refusal code`);
    }
  }

  // Every outcome the module can answer with is declared in some cell, so the
  // table is not a summary that quietly covers part of the machine.
  assert.deepEqual(sorted([...outcomes]), sorted(Object.values(OUTCOME)));

  // And every refusal code is either an input refusal or declared in a cell:
  // nothing this module can answer with is unaccounted for.
  const declared = new Set(INPUT_REFUSALS);
  for (const row of Object.values(TRANSITIONS)) {
    for (const cell of Object.values(row)) for (const code of cell.no) declared.add(code);
  }
  for (const [name, code] of Object.entries(REFUSAL)) {
    assert.ok(declared.has(code), `REFUSAL.${name} (${code}) is declared nowhere`);
  }
  // The input refusals are about the state and the event, not about a cell, so
  // they may not be a way of excusing a code that belongs in one.
  for (const code of INPUT_REFUSALS) {
    assert.ok(
      [REFUSAL.NOT_AN_OBJECT, REFUSAL.UNKNOWN_FIELD, REFUSAL.MISSING_FIELD, REFUSAL.ACCESSOR_PROPERTY, REFUSAL.INTERNAL_ERROR, REFUSAL.INVALID_NOW, REFUSAL.NOW_WENT_BACKWARDS, REFUSAL.NOW_JUMPED, REFUSAL.UNKNOWN_STATE, REFUSAL.INVALID_STATE, REFUSAL.UNKNOWN_EVENT, REFUSAL.INVALID_TIMEOUT, REFUSAL.INVALID_MAX_PENDING, REFUSAL.INVALID_MAX_LEDGER, REFUSAL.INVALID_PENDING, REFUSAL.INVALID_PENDING_ENTRY, REFUSAL.DUPLICATE_PENDING_ENTRY, REFUSAL.UNSORTED_PENDING, REFUSAL.INVALID_LEDGER, REFUSAL.INVALID_LEDGER_ENTRY, REFUSAL.DUPLICATE_LEDGER_ENTRY, REFUSAL.UNSORTED_LEDGER].includes(code),
      `${code} is not an input refusal`,
    );
  }
});

// The EXPECTED table, written here by hand and NOT imported from the module: a
// test that read the module's own TRANSITIONS could only ever agree with it.
// Each cell names the outcomes it may answer with, the phases it may leave the
// machine in, and the refusals it may answer with. The `next` of a cell that
// can refuse includes the phase a refusal leaves behind, because a refusal
// carries no state: the machine stays exactly where it was.
const ID_TEXT_REFUSALS = Object.freeze([
  'bad-character',
  'bad-key',
  'empty-string',
  'missing-field',
  'too-long',
  'unknown-field',
  'wrong-type',
]);
const COMMAND_REFUSALS = Object.freeze([
  ...ID_TEXT_REFUSALS,
  'bad-head-sha',
  'bad-pr',
  'bad-repo',
  'command-and-confirm',
  'ledger-full',
  'refused-verb',
  'replayed-action',
  'replayed-utterance',
  'unknown-verb',
  'unresolved-target',
  'verb-target-mismatch',
]);
const COMMAND_REFUSALS_FULL = Object.freeze([...COMMAND_REFUSALS, 'pending-full']);
const ABORT_REFUSALS = Object.freeze([
  ...ID_TEXT_REFUSALS,
  'command-and-confirm',
  'ledger-full',
  'replayed-utterance',
]);
const IDLE_CONFIRM_REFUSALS = Object.freeze([
  ...ID_TEXT_REFUSALS,
  'no-pending-action',
  'not-a-confirmation',
]);
const PENDING_CONFIRM_REFUSALS = Object.freeze([
  ...ID_TEXT_REFUSALS,
  'bad-head-sha',
  'bad-pr',
  'bad-repo',
  'confirmation-expired',
  'different-target',
  'ledger-full',
  'no-pending-action',
  'not-a-confirmation',
  'replayed-confirmation',
  'replayed-utterance',
  'same-utterance',
  'unresolved-target',
  'verb-target-mismatch',
]);
const CROWDED_CONFIRM_REFUSALS = Object.freeze([
  ...ID_TEXT_REFUSALS,
  'ambiguous-pending',
  'not-a-confirmation',
]);

const EXPECTED = {
  [PHASE.IDLE]: {
    [EVENT.COMMAND]: { ok: ['pass-through', 'read-back'], next: ['idle', 'pending'], no: COMMAND_REFUSALS },
    [EVENT.CONFIRM]: { ok: [], next: ['idle'], no: IDLE_CONFIRM_REFUSALS },
    [EVENT.ABORT]: { ok: ['nothing-pending'], next: ['idle'], no: ABORT_REFUSALS },
  },
  [PHASE.PENDING]: {
    // A second read-back is allowed, and makes the machine crowded; a launch
    // leaves the outstanding read-back exactly as it was.
    [EVENT.COMMAND]: { ok: ['pass-through', 'read-back'], next: ['pending', 'crowded'], no: COMMAND_REFUSALS_FULL },
    [EVENT.CONFIRM]: { ok: ['confirmed'], next: ['idle', 'pending'], no: PENDING_CONFIRM_REFUSALS },
    [EVENT.ABORT]: { ok: ['discarded', 'nothing-pending'], next: ['idle', 'pending'], no: ABORT_REFUSALS },
  },
  [PHASE.CROWDED]: {
    [EVENT.COMMAND]: { ok: ['pass-through', 'read-back'], next: ['pending', 'crowded'], no: COMMAND_REFUSALS_FULL },
    // A crowded machine refuses every confirm: `ambiguous-pending` is checked
    // before the replay ledger, so the replay codes are unreachable here.
    [EVENT.CONFIRM]: { ok: [], next: ['crowded'], no: CROWDED_CONFIRM_REFUSALS },
    [EVENT.ABORT]: { ok: ['discarded', 'nothing-pending'], next: ['idle', 'crowded'], no: ABORT_REFUSALS },
  },
};

const sorted = (values) => [...new Set(values)].sort();

/** A ledger record, as a literal. */
function spent(kind, id, at) {
  return { kind, id, at };
}

/** A pending read-back, as a literal. */
function spawn(actionId, overrides = {}) {
  const at = overrides.at ?? NOW - 1_000;
  return {
    actionId,
    verb: overrides.verb ?? VERB.CANCEL,
    target: overrides.target ?? RUN,
    at,
    deadline: at + TIMEOUT,
    by: overrides.by ?? 'utt_1',
  };
}

const EXPIRED_LONG = NOW - TIMEOUT - 200;
const EXPIRED_SOON = NOW - TIMEOUT - 100;

// Ids every composition has already spent, so a replay is reachable in every
// phase rather than only in the composition that happened to use them.
const SPENT_LEDGER = Object.freeze([
  spent(LEDGER_KIND.ACTION, 'act_spent', NOW - 40_000),
  spent(LEDGER_KIND.CONFIRMATION, 'cnf_spent', NOW - 30_000),
  spent(LEDGER_KIND.UTTERANCE, 'utt_spent', NOW - 20_000),
]);

/** The phase a pending set implies, restated independently of the module. */
function phaseFrom(pendings) {
  if (pendings.length === 0) return PHASE.IDLE;
  if (pendings.length === 1) return PHASE.PENDING;
  return PHASE.CROWDED;
}

function stateWith(pendings, overrides = {}) {
  return {
    phase: phaseFrom(pendings),
    now: NOW,
    timeoutMs: TIMEOUT,
    maxPending: Math.max(MAX_PENDING, pendings.length),
    maxLedger: MAX_LEDGER,
    pending: pendings,
    ledger: SPENT_LEDGER,
    ...overrides,
  };
}

const ONE_UTTERANCE = spent(LEDGER_KIND.UTTERANCE, 'utt_1', NOW - 1_000);
const ONE_ACTION = spent(LEDGER_KIND.ACTION, 'act_1', NOW - 1_000);
const TWO_UTTERANCE = spent(LEDGER_KIND.UTTERANCE, 'utt_2', NOW - 900);

/**
 * Every composition a phase can have.
 *
 * Each is a state the module reads, and between them they reach every cell of
 * the table in both directions: `E` is the ordinary pending machine, `G` the one
 * whose read-back has timed out, `H` the one whose ledger is full, `I` the one
 * with no room for another read-back, `L` a crowded machine whose read-backs
 * have both timed out (so a command can leave it pending again).
 */
const COMPOSITIONS = {
  'idle': { pendings: [] },
  'idle, one id spent': { pendings: [] },
  'idle, ledger full': { pendings: [], maxLedger: SPENT_LEDGER.length },
  'idle, room for one read-back': { pendings: [], maxPending: MIN_MAX_PENDING },
  'one live cancel': { pendings: [spawn('act_1')], ledger: [...SPENT_LEDGER, ONE_ACTION, ONE_UTTERANCE] },
  'one live cancel, ledger full': {
    pendings: [spawn('act_1')],
    ledger: [...SPENT_LEDGER, ONE_ACTION, ONE_UTTERANCE],
    maxLedger: SPENT_LEDGER.length + 2,
  },
  'one live cancel, no room for another': { pendings: [spawn('act_1')], maxPending: MIN_MAX_PENDING },
  'one live merge': { pendings: [spawn('act_m', { verb: VERB.MERGE, target: MERGE, by: 'utt_m' })] },
  'one live action': { pendings: [spawn('act_c', { verb: VERB.CONFIRM_ACTION, target: PR_TARGET, by: 'utt_c' })] },
  'one expired cancel': { pendings: [spawn('act_1', { at: EXPIRED_LONG })] },
  'two live cancels': {
    pendings: [spawn('act_1', { at: NOW - 200 }), spawn('act_2', { at: NOW - 100, target: RUN_OTHER, by: 'utt_2' })],
  },
  'two live cancels, ledger full': {
    pendings: [spawn('act_1', { at: NOW - 200 }), spawn('act_2', { at: NOW - 100, target: RUN_OTHER, by: 'utt_2' })],
    ledger: [...SPENT_LEDGER, ONE_ACTION, ONE_UTTERANCE, TWO_UTTERANCE],
    maxLedger: SPENT_LEDGER.length + 3,
  },
  'two live cancels, no room for another': {
    pendings: [spawn('act_1', { at: NOW - 200 }), spawn('act_2', { at: NOW - 100, target: RUN_OTHER, by: 'utt_2' })],
    maxPending: 2,
  },
  'two expired cancels': {
    pendings: [spawn('act_1', { at: EXPIRED_LONG }), spawn('act_2', { at: EXPIRED_SOON, target: RUN_OTHER, by: 'utt_2' })],
  },
  'three live cancels': {
    pendings: [
      spawn('act_1', { at: NOW - 300 }),
      spawn('act_2', { at: NOW - 200, target: RUN_OTHER, by: 'utt_2' }),
      spawn('act_3', { at: NOW - 100, target: RUN, by: 'utt_3' }),
    ],
  },
};

/** Every event worth sending to a composition. */
function probesFor(pendings, now) {
  const fresh = (n) => ({ now: now + 1, utteranceId: `utt_p${n}`, actionId: `act_p${n}` });
  const events = [
    ['command', 'a destructive command with a resolved target', { ...cancelEvent(), ...fresh(1) }],
    ['command', 'a launch', { ...launchEvent(), now: now + 1, utteranceId: 'utt_pl' }],
    ['command', 'the kill switch', { ...cancelEvent(), ...fresh(2), verb: 'kill-switch' }],
    ['command', 'a budget write', { ...launchEvent(), now: now + 1, utteranceId: 'utt_pb', verb: 'budget-set' }],
    ['command', 'a delete', { ...cancelEvent(), ...fresh(3), verb: 'delete' }],
    ['command', 'a verb outside the closed set', { ...cancelEvent(), ...fresh(4), verb: 'defenestrate' }],
    ['command', 'a command carrying a confirm token', { ...cancelEvent(), ...fresh(5), text: 'cancel it, confirm' }],
    ['command', 'a merge with a repository that is not owner/name', { ...mergeEvent(), ...fresh(6), target: { ...MERGE, repo: 'nope' } }],
    ['command', 'a merge with a PR that is not a number', { ...mergeEvent(), ...fresh(7), target: { ...MERGE, pr: 0 } }],
    ['command', 'a merge with a head that is not a SHA', { ...mergeEvent(), ...fresh(8), target: { ...MERGE, headSha: 'HEAD' } }],
    ['command', 'a target of the wrong kind', { ...cancelEvent(), ...fresh(9), target: MERGE }],
    ['command', 'an unresolved target', { ...cancelEvent(), ...fresh(10), target: null }],
    ['command', 'a launch carrying a target', { ...launchEvent(), now: now + 1, utteranceId: 'utt_plt', target: RUN }],
    ['command', 'a replayed utterance', { ...cancelEvent(), ...fresh(11), utteranceId: 'utt_spent' }],
    ['command', 'a reused action id', { ...cancelEvent(), ...fresh(12), actionId: 'act_spent' }],
    ['command', 'an unknown field', { ...cancelEvent(), ...fresh(13), extra: true }],
    ['command', 'a missing utterance id', (() => {
      const event = { ...cancelEvent(), ...fresh(14) };
      delete event.utteranceId;
      return event;
    })()],
    ['command', 'an utterance id of the wrong type', { ...cancelEvent(), ...fresh(15), utteranceId: 7 }],
    ['command', 'an empty text', { ...cancelEvent(), ...fresh(16), text: '' }],
    ['command', 'a text past its bound', { ...cancelEvent(), ...fresh(17), text: 'x'.repeat(TEXT_MAX + 1) }],
    ['command', 'an utterance id with a key character', { ...cancelEvent(), ...fresh(18), utteranceId: 'a/b' }],
    ['command', 'a text with a control character', { ...cancelEvent(), ...fresh(19), text: 'a\u0000b' }],

    ['confirm', 'a misrecognised confirm', { ...confirmEvent(), now: now + 1, utteranceId: 'utt_py', confirmationId: 'cnf_py', text: 'yes' }],
    ['confirm', 'a confirm naming no outstanding action', { ...confirmEvent(), now: now + 1, utteranceId: 'utt_px', actionId: 'act_none', confirmationId: 'cnf_px' }],
    ['confirm', 'a confirm with an unknown field', { ...confirmEvent(), now: now + 1, utteranceId: 'utt_pe', actionId: 'act_1', confirmationId: 'cnf_pe', extra: true }],
    ['confirm', 'a confirm with a missing action id', (() => {
      const event = { ...confirmEvent(), now: now + 1, utteranceId: 'utt_pq', confirmationId: 'cnf_pq' };
      delete event.actionId;
      return event;
    })()],
    ['confirm', 'a confirm from an utterance id of the wrong type', { ...confirmEvent(), now: now + 1, utteranceId: 7, confirmationId: 'cnf_pw' }],
    ['confirm', 'a confirm with an empty utterance id', { ...confirmEvent(), now: now + 1, utteranceId: '', confirmationId: 'cnf_pv' }],
    ['confirm', 'a confirm with a text past its bound', { ...confirmEvent(), now: now + 1, utteranceId: 'utt_pu', confirmationId: 'cnf_pu', text: 'x'.repeat(TEXT_MAX + 1) }],
    ['confirm', 'a confirm whose confirmation id is not a key', { ...confirmEvent(), now: now + 1, utteranceId: 'utt_pf', confirmationId: 'a/b' }],
    ['confirm', 'a confirm whose text carries a control character', { ...confirmEvent(), now: now + 1, utteranceId: 'utt_pg', confirmationId: 'cnf_pg', text: 'a\u0000b' }],
    ['confirm', 'a confirm whose utterance id is a spent one', { ...confirmEvent(), now: now + 1, utteranceId: 'utt_spent', actionId: 'act_1', confirmationId: 'cnf_replay' }],
    ['confirm', 'a confirm whose confirmation id is a spent one', { ...confirmEvent(), now: now + 1, utteranceId: 'utt_pt', actionId: 'act_1', confirmationId: 'cnf_spent' }],
    ['confirm', 'a confirm with no target', { ...confirmEvent(), now: now + 1, utteranceId: 'utt_ps', actionId: 'act_1', confirmationId: 'cnf_ps', target: null }],
    ['confirm', 'a confirm whose target is of the wrong kind', { ...confirmEvent(), now: now + 1, utteranceId: 'utt_pr', actionId: 'act_1', confirmationId: 'cnf_pr', target: MERGE }],

    ['abort', '"cancel that"', { ...abortEvent(), now: now + 1, utteranceId: 'utt_pz' }],
    ['abort', 'a "cancel that" from a spent utterance', { ...abortEvent(), now: now + 1, utteranceId: 'utt_spent' }],
    ['abort', 'a "cancel that" carrying a confirm token', { ...abortEvent(), now: now + 1, utteranceId: 'utt_paa', text: 'cancel that, confirm' }],
    ['abort', 'a "cancel that" with an unknown field', { ...abortEvent(), now: now + 1, utteranceId: 'utt_pab', extra: true }],
    ['abort', 'a "cancel that" with a missing text', (() => {
      const event = { ...abortEvent(), now: now + 1, utteranceId: 'utt_pac' };
      delete event.text;
      return event;
    })()],
    ['abort', 'a "cancel that" with an empty text', { ...abortEvent(), now: now + 1, utteranceId: 'utt_pad', text: '' }],
    ['abort', 'a "cancel that" with an utterance id of the wrong type', { ...abortEvent(), now: now + 1, utteranceId: 7 }],
    ['abort', 'a "cancel that" with a key character in its id', { ...abortEvent(), now: now + 1, utteranceId: 'a/b' }],
    ['abort', 'a "cancel that" with an id past its bound', { ...abortEvent(), now: now + 1, utteranceId: 'i'.repeat(ID_MAX + 1) }],
    ['abort', 'a "cancel that" with a control character in its text', { ...abortEvent(), now: now + 1, utteranceId: 'utt_pae', text: 'a\u0000b' }],
  ];

  for (const entry of pendings) {
    // A confirmation that names this read-back, from a fresh utterance with the
    // exact target: it confirms a live one, and is refused as expired when the
    // read-back has timed out.
    events.push([
      'confirm',
      `a confirm for ${entry.actionId}`,
      {
        type: EVENT.CONFIRM,
        now: now + 1,
        utteranceId: `utt_pc_${entry.actionId}`,
        text: CONFIRM_TEXT,
        actionId: entry.actionId,
        confirmationId: `cnf_pc_${entry.actionId}`,
        target: entry.target,
      },
    ]);
    // The utterance that issued it can never confirm it.
    events.push([
      'confirm',
      `a confirm for ${entry.actionId} from the utterance that issued it`,
      {
        type: EVENT.CONFIRM,
        now: now + 1,
        utteranceId: entry.by,
        text: CONFIRM_TEXT,
        actionId: entry.actionId,
        confirmationId: `cnf_self_${entry.actionId}`,
        target: entry.target,
      },
    ]);
    // A different target: the pending action changed.
    const other =
      entry.verb === VERB.MERGE
        ? { ...MERGE, headSha: SHA_MOVED }
        : entry.verb === VERB.CONFIRM_ACTION
          ? { ...PR_TARGET, pr: PR + 1 }
          : RUN_OTHER;
    events.push([
      'confirm',
      `a confirm for ${entry.actionId} asserting another target`,
      {
        type: EVENT.CONFIRM,
        now: now + 1,
        utteranceId: `utt_po_${entry.actionId}`,
        text: CONFIRM_TEXT,
        actionId: entry.actionId,
        confirmationId: `cnf_po_${entry.actionId}`,
        target: other,
      },
    ]);
    if (entry.verb === VERB.MERGE) {
      for (const [label, target] of [
        ['a repository that is not owner/name', { ...MERGE, repo: 'nope' }],
        ['a PR that is not a number', { ...MERGE, pr: 0 }],
        ['a head that is not a SHA', { ...MERGE, headSha: 'HEAD' }],
      ]) {
        events.push([
          'confirm',
          `a confirm for ${entry.actionId} with ${label}`,
          {
            type: EVENT.CONFIRM,
            now: now + 1,
            utteranceId: `utt_pm_${entry.actionId}`,
            text: CONFIRM_TEXT,
            actionId: entry.actionId,
            confirmationId: `cnf_pm_${entry.actionId}`,
            target,
          },
        ]);
      }
    }
  }
  return events;
}

test('the declared table is the expected one, written independently of the module', () => {
  assert.deepEqual(sorted(Object.keys(TRANSITIONS)), sorted(Object.keys(EXPECTED)));
  for (const [phase, row] of Object.entries(EXPECTED)) {
    assert.deepEqual(sorted(Object.keys(TRANSITIONS[phase])), sorted(Object.keys(row)), `${phase}'s row`);
    for (const [event, cell] of Object.entries(row)) {
      const declared = TRANSITIONS[phase][event];
      assert.deepEqual(sorted(declared.ok), sorted(cell.ok), `${phase} x ${event}: outcomes`);
      assert.deepEqual(sorted(declared.next), sorted(cell.next), `${phase} x ${event}: next phases`);
      assert.deepEqual(sorted(declared.no), sorted(cell.no), `${phase} x ${event}: refusals`);
    }
  }
});

test('every composition x every event agrees with the expected table, and every cell is reached', () => {
  const observed = {};
  for (const phase of Object.keys(EXPECTED)) {
    observed[phase] = {};
    for (const event of Object.keys(EXPECTED[phase])) observed[phase][event] = { ok: [], next: [], no: [] };
  }

  let probes = 0;
  for (const [name, composition] of Object.entries(COMPOSITIONS)) {
    const { pendings, ...overrides } = composition;
    const open = stateWith(pendings, overrides);

    // The composition must be a state the module reads, or this test proves
    // nothing about it.
    // A confirm that is not the phrase is refused before the machine's state is
    // consulted at all, so this probe answers "the state was read" in every
    // composition, including the ones whose ledger is full.
    const readable = step(open, {
      type: EVENT.CONFIRM,
      now: NOW,
      utteranceId: 'utt_probe',
      text: 'yes',
      actionId: 'act_probe',
      confirmationId: 'cnf_probe',
      target: RUN,
    });
    assert.equal(readable.code, REFUSAL.NOT_A_CONFIRMATION, `${name} is not a readable state: ${readable.code ?? readable.outcome}`);

    for (const [event, label, probe] of probesFor(pendings, NOW)) {
      probes++;
      const cell = EXPECTED[open.phase][event];
      const seen = observed[open.phase][event];
      const result = step(open, probe);
      const where = `${name} (${open.phase}) x ${event}: ${label}`;
      if (result.ok) {
        assert.ok(cell.ok.includes(result.outcome), `${where} answered ${result.outcome}`);
        assert.ok(cell.next.includes(result.phase), `${where} left the machine ${result.phase}`);
        assert.equal(result.state.phase, result.phase, `${where}: the answer and its state disagree`);
        // And what it answers with is a state the module reads back.
        const back = step(result.state, abortEvent({ now: result.state.now, utteranceId: 'utt_probe' }));
        assert.equal(back.ok, true, `${where} returned an unreadable state: ${back.code}`);
        seen.ok.push(result.outcome);
        seen.next.push(result.phase);
      } else {
        assert.ok(cell.no.includes(result.code), `${where} refused ${result.code}`);
        // A refusal has no state: the machine stays in the phase it was in,
        // which the cell must therefore declare as a next phase.
        assert.ok(cell.next.includes(open.phase), `${where}: a refusal must leave ${open.phase}`);
        assert.equal(result.state, undefined, `${where}: a refusal must carry no state`);
        seen.no.push(result.code);
        seen.next.push(open.phase);
      }
    }
  }
  assert.ok(probes > 500, `only ${probes} probes ran`);

  // The other direction: every outcome, next phase and refusal the table
  // declares is actually produced, so the table cannot over-declare a cell
  // either. A code nothing raises would be a rule nothing enforces.
  for (const [phase, row] of Object.entries(EXPECTED)) {
    for (const [event, cell] of Object.entries(row)) {
      const seen = observed[phase][event];
      assert.deepEqual(sorted(seen.ok), sorted(cell.ok), `${phase} x ${event}: outcomes reached`);
      assert.deepEqual(sorted(seen.next), sorted(cell.next), `${phase} x ${event}: phases reached`);
      assert.deepEqual(sorted(seen.no), sorted(cell.no), `${phase} x ${event}: refusals reached`);
    }
  }
});
