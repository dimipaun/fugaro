// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The receipt's checks (mobile plan, Task P5.2.9; the design is
// docs/mobile/design/push-notifications.md §3.1, §3.9 and §4 rules 1, 3, 8, 10,
// 11 and 12).
//
// **What this file is for, in the design's own words.** The task's verification
// names five things, and each is a test below rather than a paragraph: a push
// the app cannot resolve against a new, **MAC-verified** mailbox change produces
// **no alert at all** (rule 11 -- under Q6 (i) the payload carries no id, so
// "ignore an unknown id" protects nothing and this rule is the one that does the
// work); a hand-crafted push whose title carries a **lure** causes **no action
// and no navigation**, and the lure is never rendered as state or as a link
// (rules 3 and 8); the app **writes nothing** on receipt (§3.9); the alert's
// text comes from the app's own **closed set of phrases** and names no
// repository, run or amount (§3.1); and the handler reaches **no store the app
// already holds and no secret**.
//
// **The last two are pinned twice, and on purpose.** A source scan holds the
// shape a later change would have to break (a write verb, an import of
// `src/accounts/`, a route), and a behavioural test holds what the receipt
// actually does with a real store in front of it. Neither is sufficient alone:
// the scan cannot see a store reached through a port, and a test can only
// observe the paths it thought to watch.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import { validateMessage } from '../../../shared/mailbox-schema/index.js';
import { macOf, verifyMac } from '../mailbox/mac.js';
import { MemoryMailboxStore } from '../mailbox/store.js';
import { CONTROL_NAMES } from './controls.js';
import {
  ALERT_CATEGORIES,
  ALERT_PHRASES,
  ALERT_PUBLIC,
  ALERT_VISIBILITY,
  CATEGORY_FIELD,
  MemoryMailboxChange,
  PUSH_CHANNEL,
  RECEIPT_CODES,
  createPushReceipt,
} from './receipt.js';

/** A mailbox id shaped like an Auth uid, and never a real one. */
const UID = 'AbCdEf0123456789ghIJkl';
/** D171's per-pairing key, synthetic: what the store holds and this pairing signs with. */
const MAC_KEY = 'synthetic-mac-key-0123456789abcdef';
/** Another pairing's key, for the "this tag is not ours" case. */
const OTHER_KEY = 'synthetic-mac-key-fedcba9876543210';
const T0 = 1_700_000_000_000;
const OUTBOX = `mailbox/${UID}/outbox`;

/** A push as the platform hands it to the app: FCM's message, `data` and all. */
function push(data) {
  return { data, messageId: 'not-a-real-message-id', from: 'not-a-real-sender' };
}

/** A message as this pairing signs it, for the phone to read out of its outbox. */
function signed(record, key = MAC_KEY) {
  return { ...record, mac: macOf(record, key, 'outbox') };
}

/** The receipt over a port that answers whatever the test told it to. */
function receiptOver(mailbox) {
  const receipt = createPushReceipt({ mailbox });
  assert.equal(receipt.ok, true);
  return receipt;
}

/**
 * A model of the read-only, MAC-verified mailbox read the port's real adapter
 * will be (`receipt.js`'s header: the client's own verified read is `poll()`,
 * which writes, so the adapter is a later task's and this is what stands in for
 * it here).
 *
 * It verifies the tag with D171's key over P6.3's canonical bytes and answers
 * `fresh` for a message the app has not acked -- and it is deliberately a
 * **model**: the point of the tests below is that the receipt posts an alert on
 * `fresh` and on nothing else, and that a forged or already-seen message is not
 * `fresh`. The mailbox has a source for the `prompt` category and none for the
 * budget ones, which is the design's own position (§1: the phone cannot see a
 * run's outcome) and is pinned here rather than assumed.
 */
function verifiedMailbox(db, key = MAC_KEY) {
  return {
    async newChange(category) {
      if (category !== 'prompt') return Object.freeze({ ok: true, fresh: false });
      const read = await db.read(OUTBOX);
      if (!read.ok) return Object.freeze({ ok: false, code: read.code, field: 'outbox' });
      for (const [id, record] of Object.entries(read.value ?? {})) {
        if (record === null || typeof record !== 'object' || Array.isArray(record)) continue;
        if (record.id !== id) continue;
        if (!validateMessage(record).ok) continue;
        if (record.sender !== UID) continue;
        if (!verifyMac(record, key, 'outbox')) continue;
        if (record.acked === true) continue;
        if (record.kind === 'prompt') return Object.freeze({ ok: true, fresh: true });
      }
      return Object.freeze({ ok: true, fresh: false });
    },
  };
}

test('the door is a mailbox read and a call, and a port it cannot use is refused at construction', () => {
  const receipt = receiptOver(new MemoryMailboxChange());
  assert.deepEqual(Object.keys(receipt).sort(), ['ok', 'receive']);
  assert.equal(typeof receipt.receive, 'function');
  // A receipt that could exist without a mailbox read would answer "no alert"
  // for a reason nobody chose, at the one moment a push arrived -- so it is a
  // wiring bug and it throws, like every other door in this directory.
  assert.throws(() => createPushReceipt(), TypeError, 'no options at all');
  assert.throws(() => createPushReceipt({}), TypeError, 'no port at all');
  assert.throws(() => createPushReceipt({ mailbox: null }), TypeError);
  assert.throws(() => createPushReceipt({ mailbox: {} }), TypeError, 'a port without newChange()');
  assert.throws(() => createPushReceipt({ mailbox: { newChange: 'no' } }), TypeError);
  // The double refuses the same class of mistake: `new MemoryMailboxChange(true)`
  // would destructure to `{fresh: []}` and be a read that is never fresh, which
  // is a receipt that never posts for a reason no test would have chosen.
  assert.throws(() => new MemoryMailboxChange(true), TypeError);
});

test('the category set is the schema’s five, and every one of them has a phrase', () => {
  // One vocabulary: `controls.js` reads the same names for §3.8's switches, and
  // the sender reads them out of the registration message
  // (`internal/cli/push.go`). Pinned as the design's own list so that a sixth
  // category arrives as a failure here rather than as a blank notification.
  assert.deepEqual([...ALERT_CATEGORIES], [...CONTROL_NAMES]);
  assert.deepEqual([...ALERT_CATEGORIES], [
    'kill_switch', 'cap_reached', 'consumer_offline', 'cap_near', 'prompt',
  ]);
  assert.deepEqual(Object.keys(ALERT_PHRASES).sort(), [...ALERT_CATEGORIES].sort());
  assert.deepEqual(ALERT_PHRASES, {
    kill_switch: 'Fugaro: a kill switch is on.',
    cap_reached: 'Fugaro: a spending cap has been reached.',
    consumer_offline: 'Fugaro: the orchestrator has gone offline.',
    cap_near: 'Fugaro: a spending cap is nearly reached.',
    prompt: 'Fugaro: a question is waiting for your answer.',
  });
  assert.equal(CATEGORY_FIELD, 'category');
});

test('§3.1 and rule 10: a phrase names the app and nothing about the owner’s work', () => {
  for (const [category, text] of Object.entries(ALERT_PHRASES)) {
    assert.equal(typeof text, 'string', category);
    assert.match(text, /^Fugaro: /, category);
    // No repository, no run, no branch, no amount, no currency and no code: a
    // digit, a slash or a `#` is the shape every one of those has, and none of
    // them belongs in a sentence that is read off a lock screen.
    assert.doesNotMatch(text, /[0-9/#$]/, category);
    assert.equal(text.includes('owner/'), false, category);
  }
  // The public version is a sentence of its own and discloses less than any
  // phrase, which is the whole point of a redacted one.
  assert.equal(ALERT_PUBLIC, 'Fugaro: open the app to see what changed.');
  assert.doesNotMatch(ALERT_PUBLIC, /[0-9/#$]/);
  for (const text of Object.values(ALERT_PHRASES)) {
    assert.notEqual(text, ALERT_PUBLIC);
  }
  assert.equal(ALERT_VISIBILITY, 'private');
});

test('§3.1: the text is the app’s own table, never a sentence a message carries', async () => {
  // Q6 left this choice to this task -- the sentence travels in the payload, or
  // the phone composes it from its own bundle -- and the answer is the phone's
  // for the alert the APP composes (iOS's visible sentence is the sender's
  // payload text, which the header states and this module does not read). Said
  // from the outside: a payload carrying a *plausible* sentence, another
  // category's phrase or the redacted line changes nothing about what is posted.
  const receipt = receiptOver(new MemoryMailboxChange({ fresh: true }));
  for (const [category, text] of Object.entries(ALERT_PHRASES)) {
    const decision = await receipt.receive(push({
      category,
      title: ALERT_PHRASES.kill_switch,
      body: ALERT_PUBLIC,
      text: 'Fugaro: everything is fine.',
      public: 'ignored',
      visibility: 'public',
    }));
    assert.equal(decision.alert.text, text, category);
    // The redacted line and the visibility are the app's constants too: a
    // payload that could set either would decide what a lock screen shows.
    assert.equal(decision.alert.public, ALERT_PUBLIC, category);
    assert.equal(decision.alert.visibility, ALERT_VISIBILITY, category);
  }
});

test('the receipt is the same function on both platforms, and takes no platform', async () => {
  // §4 rule 11's iOS exception is about what the SYSTEM shows before the app
  // runs, and not about what the app does when it does: on iOS the alert has
  // already appeared and the receipt's answer is still "nothing to do". So there
  // is no platform here to branch on -- the door ignores one, and the answer to
  // the same message is the same answer.
  const message = push({ category: 'prompt' });
  const here = await receiptOver(new MemoryMailboxChange({ fresh: ['prompt'] })).receive(message);
  const there = await createPushReceipt({
    mailbox: new MemoryMailboxChange({ fresh: ['prompt'] }),
    platform: 'ios',
  }).receive(message);
  assert.deepEqual(there, here);
  assert.deepEqual(Object.keys(there).sort(), ['alert', 'category', 'code', 'ok', 'post']);
});

test('the channel’s own description says the one thing that works against a forged push', () => {
  // §3.9: the user's habit of opening the app rather than acting on the banner
  // is the only control there is, and the channel's description is one of the
  // places the app can say so.
  assert.deepEqual(PUSH_CHANNEL, {
    id: 'fugaro-doorbell',
    name: 'Fugaro',
    description: 'Fugaro notifications only ring the doorbell. Open the app to see what changed.',
  });
  assert.match(PUSH_CHANNEL.description, /doorbell/);
  assert.doesNotMatch(PUSH_CHANNEL.description, /[0-9/#$]/);
});

test('rule 11: a category the mailbox cannot resolve posts no alert at all', async () => {
  const mailbox = new MemoryMailboxChange({ fresh: [] });
  const receipt = receiptOver(mailbox);
  const decision = await receipt.receive(push({ category: 'prompt' }));
  assert.deepEqual(decision, {
    ok: true,
    post: false,
    code: RECEIPT_CODES.NOT_RESOLVED,
    category: 'prompt',
    alert: null,
  });
  // The port was asked, and asked once: "no alert" is an answer about the
  // mailbox and not a failure to look.
  assert.deepEqual([...mailbox.calls()], ['prompt']);
});

test('rule 11: a new, verified change of the category posts the app’s own phrase', async () => {
  const mailbox = new MemoryMailboxChange({ fresh: ['prompt'] });
  const receipt = receiptOver(mailbox);
  const decision = await receipt.receive(push({ category: 'prompt' }));
  assert.equal(decision.ok, true);
  assert.equal(decision.post, true);
  assert.equal(decision.code, RECEIPT_CODES.POSTED);
  assert.equal(decision.category, 'prompt');
  // The alert is the app's closed set, and its keys are the whole of it: a
  // field added here would be a field a screen could render.
  assert.deepEqual(Object.keys(decision.alert).sort(), [
    'category', 'channel', 'public', 'text', 'visibility',
  ]);
  assert.equal(decision.alert.text, ALERT_PHRASES.prompt);
  assert.equal(decision.alert.public, ALERT_PUBLIC);
  assert.equal(decision.alert.visibility, ALERT_VISIBILITY);
  assert.equal(decision.alert.channel, PUSH_CHANNEL.id);
  assert.deepEqual([...mailbox.calls()], ['prompt']);
});

test('every category of the set resolves the same way, one phrase each', async () => {
  for (const category of ALERT_CATEGORIES) {
    const mailbox = new MemoryMailboxChange({ fresh: [category] });
    const decision = await receiptOver(mailbox).receive(push({ category }));
    assert.equal(decision.post, true, category);
    assert.equal(decision.alert.text, ALERT_PHRASES[category], category);
  }
});

test('a category the app does not have is never asked about and never posted', async () => {
  const mailbox = new MemoryMailboxChange({ fresh: true });
  const receipt = receiptOver(mailbox);
  for (const data of [
    { category: 'finished' },      // §1's excluded pair: a category the app cannot resolve
    { category: 'cap-reached' },   // the same word with the wrong punctuation
    { category: 'CAP_REACHED' },
    { category: '' },
    { category: 7 },
    { category: { toString: () => 'prompt' } },
    { category: ['prompt'] },
    {},                            // no category at all
  ]) {
    const decision = await receipt.receive(push(data));
    assert.equal(decision.post, false, JSON.stringify(data));
    assert.equal(decision.alert, null, JSON.stringify(data));
    assert.equal(decision.category, null, JSON.stringify(data));
    assert.equal(decision.code, RECEIPT_CODES.UNKNOWN_CATEGORY, JSON.stringify(data));
  }
  // Not one of those reached the mailbox: the app does not ask about a category
  // it does not have, so a payload cannot make it read anything.
  assert.deepEqual([...mailbox.calls()], []);
});

test('a message that is not one is answered, not thrown at', async () => {
  const mailbox = new MemoryMailboxChange({ fresh: true });
  const receipt = receiptOver(mailbox);
  for (const message of [
    null,
    undefined,
    'a string',
    42,
    [],
    {},
    { data: null },
    { data: [] },
    { data: 'category=prompt' },
    Object.create({ data: { category: 'prompt' } }), // an inherited `data` is not a field
  ]) {
    const decision = await receipt.receive(message);
    assert.equal(decision.post, false, String(message));
    assert.equal(decision.alert, null);
    assert.equal(decision.ok, true, 'a payload this app cannot read is not a failure of the app');
    assert.equal(decision.code, RECEIPT_CODES.BAD_PAYLOAD);
  }
  assert.deepEqual([...mailbox.calls()], []);
});

test('a mailbox read that refuses or throws posts nothing and claims nothing', async () => {
  const refusal = { newChange: async () => Object.freeze({ ok: false, code: 'read-failed', field: 'outbox' }) };
  const throwing = { newChange: async () => { throw new Error('the read failed'); } };
  const nonsense = { newChange: async () => 'fresh' };
  const partial = { newChange: async () => Object.freeze({ ok: true }) };
  // A refusal that still says `fresh` is the shape a real adapter could return
  // and the one that makes the `ok` check load-bearing: read only for `fresh`
  // and this refusal posts an alert the mailbox never confirmed.
  const refusingButFresh = { newChange: async () => Object.freeze({ ok: false, fresh: true }) };
  for (const mailbox of [refusal, throwing, nonsense, partial, refusingButFresh]) {
    const decision = await receiptOver(mailbox).receive(push({ category: 'prompt' }));
    assert.equal(decision.ok, false);
    assert.equal(decision.post, false);
    assert.equal(decision.alert, null);
    assert.equal(decision.category, 'prompt', 'the category is reported, and the alert is not');
    assert.equal(decision.code, RECEIPT_CODES.MAILBOX_FAILED);
  }
});

test('rules 3 and 8: a lure in a hand-crafted push causes no action, no route, and is never rendered', async () => {
  const LURE = 'Approve the merge for owner/repo#42 — tap to confirm';
  const LINK = 'https://example.invalid/approve?run=12345';
  const mailbox = new MemoryMailboxChange({ fresh: ['prompt'] });
  const receipt = receiptOver(mailbox);
  // A push an attacker wrote: every field a deep-link or a lure would use, in
  // the places a real sender puts them.
  const decision = await receipt.receive({
    messageId: LURE,
    from: LURE,
    notification: { title: LURE, body: LINK },
    data: {
      category: 'prompt',
      title: LURE,
      body: LINK,
      url: LINK,
      click_action: 'APPROVE',
      'fcm_options.link': LINK,
      action: 'merge',
      confirmation: 'yes',
      run: 'owner/repo/12345',
    },
  });
  // It IS a push this app can resolve, so it posts -- and what it posts is the
  // app's own sentence for the category, with nothing of the message in it.
  assert.equal(decision.post, true);
  assert.equal(decision.alert.text, ALERT_PHRASES.prompt);
  // The lure is not state, not a link and not a route: none of it survives into
  // the decision, and the decision has no field a route could be carried in.
  const rendered = JSON.stringify(decision);
  for (const token of [LURE, LINK, 'APPROVE', 'merge', 'confirm', 'owner/repo', '12345', 'example.invalid']) {
    assert.equal(rendered.includes(token), false, token);
  }
  assert.deepEqual(Object.keys(decision).sort(), ['alert', 'category', 'code', 'ok', 'post']);
  assert.deepEqual(Object.keys(decision.alert).sort(), [
    'category', 'channel', 'public', 'text', 'visibility',
  ]);
  // And the app was asked about the category and about nothing else: a message
  // cannot steer what the app reads, let alone what it does.
  assert.deepEqual([...mailbox.calls()], ['prompt']);
});

test('rule 11 over a real store: only a new, MAC-verified change of the category posts', async () => {
  const db = new MemoryMailboxStore();
  const receipt = receiptOver(verifiedMailbox(db));
  const prompt = (id, at) => ({
    id,
    sender: UID,
    text: 'may I proceed?',
    at,
    kind: 'prompt',
  });

  // Nothing in the mailbox at all.
  assert.equal((await receipt.receive(push({ category: 'prompt' }))).code, RECEIPT_CODES.NOT_RESOLVED);

  // A message whose tag is NOT this pairing's: the app can read it and must not
  // believe it, and an alert from it would be the forged-state alert rule 11
  // exists to stop.
  db.deliver(`${OUTBOX}/m-1`, signed(prompt('m-1', T0), OTHER_KEY));
  assert.equal((await receipt.receive(push({ category: 'prompt' }))).code, RECEIPT_CODES.NOT_RESOLVED);

  // The same message, signed by this pairing, and not yet seen: this is the one
  // shape that posts.
  db.deliver(`${OUTBOX}/m-2`, signed(prompt('m-2', T0 + 1_000)));
  const posted = await receipt.receive(push({ category: 'prompt' }));
  assert.equal(posted.post, true);
  assert.equal(posted.alert.text, ALERT_PHRASES.prompt);

  // ... and the app has now spoken and acked it (the client's own write, in its
  // own pass): the same push resolves to nothing, which is what makes the
  // doorbell mean something rather than buzz per delivery.
  db.deliver(`${OUTBOX}/m-2/acked`, true);
  assert.equal((await receipt.receive(push({ category: 'prompt' }))).code, RECEIPT_CODES.NOT_RESOLVED);

  // A category the mailbox has no source for resolves to nothing however many
  // pushes arrive: §1's position, and the reason "ignore an unknown id" would
  // have protected nothing under Q6 (i).
  assert.equal((await receipt.receive(push({ category: 'cap_reached' }))).code, RECEIPT_CODES.NOT_RESOLVED);
  assert.equal((await receipt.receive(push({ category: 'kill_switch' }))).code, RECEIPT_CODES.NOT_RESOLVED);

  // §3.9 rule 12: the app wrote nothing on receipt, in any of those cases. The
  // reads are the port's own, and every one of them is the outbox: nothing
  // reached for a node outside the one family a prompt can be resolved from.
  assert.deepEqual(db.writes(), []);
  assert.equal(db.reads().length > 0, true, 'the port really read the mailbox');
  for (const read of db.reads()) assert.equal(read.path, OUTBOX);
});

test('§3.9 rule 12: the receipt writes nothing, reaches no store and no secret', () => {
  const source = readFileSync(join(import.meta.dirname, 'receipt.js'), 'utf8');
  assert.equal(source.includes('createPushReceipt'), true, 'the scan read the file it means to');

  // The same idiom the module’s other scans use: the comments come out first,
  // so a rule quoted in prose is not read as code.
  const code = (text) => text.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');
  const RULES = [
    ['a write', /\.write\s*\(/],
    ['a registration', /\.register\s*\(/],
    ['a send', /\.send\s*\(/],
    ['a flush', /\.flush\s*\(/],
    ['a poll', /\.poll\s*\(/],
    ['a store the app already holds', /\b(SecretStore|SettingsStore|AccountStore|MemorySettingsStore|MemorySecureStore)\b/],
    ['a secret', /\b(reveal|macKey|idToken|SECRET_ITEMS)\b/],
    ['the accounts module', /accounts\//],
    ['a route or a link', /\b(Linking|openURL|navigate|deepLink|click_action|fcm_options)\b/],
    ['a platform branch', /\b(Platform|platform)\b/],
    ['a non-relative import', /(?:\bfrom|\bimport)\s*\(?\s*['"][^.'"]/],
  ];
  const offences = (text) => RULES.filter(([, pattern]) => pattern.test(code(text))).map(([name]) => name);

  // The positive control first: every rule flags a planted line, or a green run
  // over the real file below would mean nothing.
  const PLANTED = {
    'a write': "store.write('mailbox/u/outbox/m-1', record)",
    'a registration': 'client.register({ token: t })',
    'a send': "client.send('hello')",
    'a flush': 'client.flush()',
    'a poll': 'client.poll()',
    'a store the app already holds': 'new SecretStore(port)',
    'a secret': 'secret.reveal()',
    'the accounts module': "import { SettingsStore } from '../accounts/index.js';",
    'a route or a link': 'Linking.openURL(url)',
    'a platform branch': "if (Platform.OS === 'ios') { post(); }",
    'a non-relative import': "import assert from 'node:assert/strict';",
  };
  for (const [name, pattern] of RULES) {
    const planted = `function offence() { const x = ${PLANTED[name]}; }`;
    assert.equal(pattern.test(code(planted)), true, name);
  }
  // ... and then the file itself.
  assert.deepEqual(offences(source), [], 'receipt.js: see the rules above');
  // The control above is not the whole story: the same planted lines as PROSE
  // must not be flagged, which is what lets this tree's comments keep
  // explaining the rules it keeps.
  assert.deepEqual(offences('// client.poll() and store.write(p) are forbidden here\n'), []);
});
