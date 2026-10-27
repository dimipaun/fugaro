// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The sample data's own claims (owner ruling of 2026-10-19).
//
// The demo is only honest if a reviewer can tell it is a demo, and only stable
// if what it shows does not move: every value here is fixed, obviously
// fictional, and consistent with the rules the app applies to the same kind of
// value. That is what this file asserts -- and it asserts it HERE, over the
// data, rather than through a run of the demo, because these are properties of
// the data and a run would prove them only by accident.
//
// The four claims:
//
//   1. **Fixed.** Every string and number is frozen and the clock does not
//      move, so two runs of the demo are the same run and every assertion in
//      `index.test.js` is about the demo rather than about when it ran.
//   2. **Fictional, and seen to be.** Every name a reviewer reads is the
//      demo's own: installations, repos, uids, projects. D114's tuple is a
//      shape here, not an installation.
//   3. **Not a credential.** No string in the data is secret-shaped by the
//      app's own detector (`accounts/secret.js`), and no record carries a tag
//      -- tags are made at run time by the app's MAC code, which is what makes
//      the client's verification in `index.test.js` mean something.
//   4. **Legal where it is used.** The heartbeat interval is inside P1.4's
//      band, the destructive command's verb is one of P6.2's, its target is a
//      run, the question's safe answer is one of the answers it offers, and the
//      job states are the CLI's three.

import assert from 'node:assert/strict';
import test from 'node:test';

import { DESTRUCTIVE_VERBS, TARGET_KIND } from '../../../shared/confirm/index.js';
import { MAX_INTERVAL_MS, MIN_INTERVAL_MS } from '../../../shared/presence/index.js';
import { secretShaped } from '../accounts/index.js';
import {
  DEMO_ACCOUNT,
  DEMO_DESTRUCTIVE,
  DEMO_HEARTBEAT,
  DEMO_INSTALLATIONS,
  DEMO_INTERVAL_MS,
  DEMO_JOBS,
  DEMO_NOW,
  DEMO_PROMPT,
  DEMO_PROMPT_SAFE_ANSWER,
  DEMO_REPLIES,
  DEMO_SECOND_ACCOUNT,
  DEMO_UTTERANCES,
} from './sample.js';
import { DEMO_PAIRING_KEY } from './keys.js';
import * as KEYS from './keys.js';
import * as SAMPLE from './sample.js';

/** Every string in the sample data, with a path saying where it is. */
function stringsIn(value, path = 'sample', found = []) {
  if (typeof value === 'string') found.push([path, value]);
  else if (Array.isArray(value)) value.forEach((entry, i) => stringsIn(entry, `${path}[${i}]`, found));
  else if (value !== null && typeof value === 'object') {
    for (const [key, entry] of Object.entries(value)) stringsIn(entry, `${path}.${key}`, found);
  }
  return found;
}

test('the clock is fixed, and the data is frozen', () => {
  // A demo that read a clock would show different messages, tags and presence
  // every time it was entered, and nothing about it could be asserted.
  assert.equal(DEMO_NOW, 1_766_000_000_000);
  for (const value of [DEMO_ACCOUNT, DEMO_SECOND_ACCOUNT, DEMO_DESTRUCTIVE, DEMO_PROMPT, DEMO_HEARTBEAT]) {
    assert.ok(Object.isFrozen(value));
  }
  assert.ok(Object.isFrozen(DEMO_INSTALLATIONS));
  assert.ok(Object.isFrozen(DEMO_JOBS));
  assert.ok(Object.isFrozen(DEMO_REPLIES));
  assert.ok(Object.isFrozen(DEMO_UTTERANCES));
  assert.ok(Object.isFrozen(DEMO_DESTRUCTIVE.target));
  assert.ok(Object.isFrozen(DEMO_PROMPT.options));
  assert.ok(Object.isFrozen(DEMO_HEARTBEAT.agents));
  // Everything the demo shows is at or before its clock: a message from the
  // future is a message the client drops as expired (or a presence that never
  // happened).
  for (const [path, at] of [
    ['reply', DEMO_REPLIES[0].at],
    ['reply', DEMO_REPLIES[1].at],
    ['prompt', DEMO_PROMPT.at],
    ['heartbeat', DEMO_HEARTBEAT.at],
  ]) {
    assert.ok(Number.isSafeInteger(at) && at <= DEMO_NOW, `${path} ${String(at)}`);
  }
});

test('every name a reviewer reads is the demo\'s own', () => {
  const names = [
    DEMO_ACCOUNT.id, DEMO_ACCOUNT.firebaseProjectId, DEMO_ACCOUNT.authUid, DEMO_ACCOUNT.fugaroProject,
    DEMO_SECOND_ACCOUNT.id, DEMO_SECOND_ACCOUNT.firebaseProjectId, DEMO_SECOND_ACCOUNT.authUid, DEMO_SECOND_ACCOUNT.fugaroProject,
  ];
  for (const name of names) assert.ok(name.startsWith('demo'), name);
  for (const installation of DEMO_INSTALLATIONS) {
    assert.ok(installation.label.startsWith('Demo'), installation.label);
    assert.ok(installation.account.id.startsWith('demo'));
  }
  for (const job of DEMO_JOBS) {
    assert.ok(job.runId.startsWith('demo-run-'), job.runId);
    assert.ok(job.repo.startsWith('demo'), job.repo);
    assert.ok(job.title.length > 0);
  }
  assert.ok(DEMO_PROMPT.id.startsWith('demo-'));
  for (const reply of DEMO_REPLIES) assert.ok(reply.id.startsWith('demo-reply-'));
  for (const utterance of DEMO_UTTERANCES) assert.ok(utterance.startsWith('Demo: '), utterance);
  // The two installations are different installations: a list of two rows that
  // were the same account would be a list of one.
  assert.notEqual(DEMO_ACCOUNT.authUid, DEMO_SECOND_ACCOUNT.authUid);
  assert.notEqual(DEMO_ACCOUNT.id, DEMO_SECOND_ACCOUNT.id);
});

test('nothing in the sample data is a credential, and nothing carries a tag', () => {
  // The app's own detector: `accounts/secret.js` decides what a token or a key
  // LOOKS like, and it is the same judgement the app's other configured
  // strings are held to (`app-config.test.js`). A demo whose sample data
  // tripped it would be a demo carrying something a reviewer must not see.
  // The whole MODULE and not a list of the exports a reviewer thought of: a
  // new export carrying a key-shaped value would be invisible to a hand-written
  // list, and this file exists to make that visible.
  for (const [path, value] of stringsIn({ sample: SAMPLE, keys: KEYS })) {
    assert.equal(secretShaped(value), null, `${path} is ${secretShaped(value)}`);
  }

  // The demo's key is a placeholder and is DETECTED as one by that same
  // detector -- so a scan over the app cannot mistake it for a real key, and a
  // reviewer who finds it knows what it is.
  assert.ok(DEMO_PAIRING_KEY.startsWith('demo-pairing-key-'));
  assert.ok(secretShaped(DEMO_PAIRING_KEY) === null, 'the placeholder must not look like a key');
  assert.ok(!/^[0-9a-f]{32,}$/.test(DEMO_PAIRING_KEY));

  // No record carries a `mac`: every tag in the demo is made at run time by
  // `transport.js`, with the app's own code, so the client's verification is
  // about the app rather than about a string written by hand.
  for (const record of [DEMO_PROMPT, ...DEMO_REPLIES, DEMO_HEARTBEAT]) {
    assert.equal(Object.hasOwn(record, 'mac'), false);
  }
});

test('the sample data is legal where the app will use it', () => {
  // D117's interval, inside P1.4's band -- the client refuses one outside it at
  // construction, so a demo interval outside it would be a demo that cannot be
  // built.
  assert.ok(DEMO_INTERVAL_MS >= MIN_INTERVAL_MS && DEMO_INTERVAL_MS <= MAX_INTERVAL_MS);

  // P6.2's vocabulary: the verb is one of the destructive ones (a launch is the
  // pass-through case and has no read-back), and a cancel's target is a run.
  assert.ok(DESTRUCTIVE_VERBS.includes(DEMO_DESTRUCTIVE.verb), DEMO_DESTRUCTIVE.verb);
  assert.equal(DEMO_DESTRUCTIVE.target.kind, TARGET_KIND.RUN);
  assert.ok(DEMO_DESTRUCTIVE.target.runId.startsWith('demo-run-'));
  assert.ok(DEMO_DESTRUCTIVE.confirmationId.startsWith('demo-'));

  // The question offers the answers, and the safe one is among them: D168/D173
  // make the safe answer phone-local, and one that was never offered would be
  // an answer the reviewer cannot give.
  assert.equal(DEMO_PROMPT.kind, 'prompt');
  assert.ok(DEMO_PROMPT.options.length >= 2);
  assert.ok(DEMO_PROMPT.options.includes(DEMO_PROMPT_SAFE_ANSWER), DEMO_PROMPT_SAFE_ANSWER);
  assert.equal(new Set(DEMO_PROMPT.options).size, DEMO_PROMPT.options.length, 'no answer is offered twice');

  // The job states are the CLI's own three, because a state nothing else in the
  // product uses would be a row about a product that does not exist.
  assert.deepEqual([...new Set(DEMO_JOBS.map((job) => job.state))].sort(), ['failed', 'running', 'succeeded']);

  // Three utterances are sent and each reply answers one of them: two replies
  // for two questions, and the destructive command has no answer (the read-back
  // is the app's, not a reply).
  assert.equal(DEMO_UTTERANCES.length, 2);
  assert.deepEqual(DEMO_REPLIES.map((reply) => reply.replyTo), ['demo-utterance-1', 'demo-utterance-2']);
  assert.equal(new Set([...DEMO_REPLIES.map((reply) => reply.id), DEMO_PROMPT.id]).size, 3, 'ids are distinct');
});
