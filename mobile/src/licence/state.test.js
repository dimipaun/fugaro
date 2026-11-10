// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The app's lapse state (subscriptions plan S6.3; D436, T12, D523; the design
// is docs/design/lapse-read-only.md, §1, §4.2 and §5).
//
// **What this file is about.** The verifier (`verify.js`) names three states of
// a KEY -- `valid`, `grace`, `expired` -- and refuses everything else. What the
// APP is in is a different question with a fifth answer, and this module is
// that question's answer:
//
//   | the product's state | what it is | what it allows |
//   | no key              | the store is empty | everything, with a banner |
//   | `valid`             | an installed key with `now <= expires` | everything |
//   | `grace`             | `expires < now <= expires + 30 days` | everything |
//   | `lapsed`            | past the grace | reads, cancel and the kill switch; not launch, not configuration |
//   | `unverified`        | a key this build cannot verify | the no-key state's |
//
// The rows are the design's table verbatim (§1), and the fifth is the one the
// verifier hands here rather than naming itself: a store can hold a key this
// build does not verify -- an id the keyring dropped once its grace passed, or
// a rollback -- and §5.4 reads that as the no-key state, because a refused key
// never had a state (the verifier refuses rather than naming one) and a refusal
// a product turned into read-only would be a product taking a capability away
// on a fact the verifier declined to judge.
//
// **The refusal, and what it is not.** A lapse refuses the launch and the
// configuration change (§4.2) and keeps everything T12 keeps: every read,
// cancel, and the kill switch. The kept set is a CLOSED, NAMED class and never
// "anything that writes" (§3.2's rule): the tests below pin both halves of it,
// and pin that a write this table does not name is refused once the key has
// lapsed -- so a later task that adds a write class cannot ship one that a
// lapse silently allows.
//
// **What the app does NOT do here:** it does not refuse a read. Nothing in this
// module is on a read path at all -- the mailbox client's `send` is the app's
// one write path and the only caller -- which is why "the kill switch stays" is
// a row of the table rather than a check somewhere: the app reads the kill
// state (`src/feed/`), a read is never gated, and the switch a person uses is
// the CLI's `fugaro budget kill`, which is core and never gated either.
//
// **The clock is an argument**, exactly as it is in `verify.js`: a state is a
// fact about a key and a moment, so this module reads no clock of its own and
// could not answer a question about "now" if it wanted to.
//
// The keys below are REAL Ed25519 signatures over real claim sets, verified by
// this module's own verifier with Node's Ed25519 as the primitive -- the same
// fixtures `store.test.js` builds, for the same reason: what a state is, is a
// fact about a key that verified, and a test that handed in a stub verifier
// would be testing its own stub. They are generated at run time and no key
// material of any installation is recorded here (Global Constraint 11).

import assert from 'node:assert/strict';
import { createPublicKey, generateKeyPairSync, sign as edSign, verify as edVerify } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';

import {
  LAPSE_KEPT_WRITES,
  LICENCE_STATE_CODES,
  LICENCE_STATES,
  UNENFORCED_LICENCE,
  WRITES,
  createLicenceState,
  lapseRefusalAt,
  licenceSpeech,
  licenceState,
  lapseRefusal,
  writeOf,
} from './state.js';
import { GRACE_DAYS, createLicenceVerifier } from './verify.js';

const DAY = 24 * 60 * 60;

/** ISSUED is the moment every key here is minted at: a fixed Unix second
 * rather than a clock, because a state is a fact about a key and a `now`. */
const ISSUED = 1_800_000_000;

/** ED25519_SPKI_PREFIX is the twelve bytes every Ed25519 public key's DER
 * spelling starts with, so a raw 32-byte key can be handed to Node. */
const ED25519_SPKI_PREFIX = Buffer.from('302a300506032b6570032100', 'hex');

/** nodeEd25519 is Node's own Ed25519 as the verifier's port. */
function nodeEd25519(signature, message, publicKey) {
  const key = createPublicKey({
    key: Buffer.concat([ED25519_SPKI_PREFIX, Buffer.from(publicKey)]),
    format: 'der',
    type: 'spki',
  });
  return edVerify(null, Buffer.from(message), key, Buffer.from(signature));
}

/** signer is one test key: a pair generated at run time, its raw public half as
 * a keyring carries it, and the id its keys are spelled under. */
function signer(id) {
  const { publicKey, privateKey } = generateKeyPairSync('ed25519');
  const raw = publicKey.export({ type: 'spki', format: 'der' });
  const half = raw.subarray(raw.length - 32);
  return { id, privateKey, keyring: { [id]: Buffer.from(half).toString('base64url') } };
}

/** mint signs one claim set as it is spelled: the payload's bytes, and a
 * signature over exactly those bytes. */
function mint(s, claims) {
  const payload = Buffer.from(JSON.stringify(claims), 'utf8');
  return [s.id, payload.toString('base64url'), edSign(null, payload, s.privateKey).toString('base64url')].join('.');
}

/** claimsFor is a claim set of the shape the design fixes. */
function claimsFor(overrides = {}) {
  return {
    tier: 'team',
    features: ['coordinator'],
    seats: 5,
    organisation: 'example.com',
    issued: ISSUED,
    expires: ISSUED + 365 * DAY,
    ...overrides,
  };
}

/** at is a `Date` from a Unix second: what every read here takes as its
 * moment, exactly as `verify.js` takes it. */
function at(seconds) {
  return new Date(seconds * 1000);
}

/** verifier is this build's verifier over one keyring of test keys. */
function verifier(signers) {
  const keyring = {};
  for (const one of signers) Object.assign(keyring, one.keyring);
  return createLicenceVerifier({ ed25519: nodeEd25519, keyring });
}

// --- the five states -------------------------------------------------------

test('a product with no key is in the no-key state, and it refuses nothing', () => {
  const production = signer('prod-2026');
  const read = licenceState({ keys: [], verifier: verifier([production]), now: at(ISSUED) });
  assert.deepEqual(read, { state: LICENCE_STATES.NO_KEY, claims: null }, 'the empty store is F43’s no-key state');

  // §5.3: an installation with no key keeps working. The reasons are the
  // design's -- 0.112.0 is upgraded into the release that enforces with no key
  // at all, a fresh install has none, and D523 defers every notice -- and the
  // hole (a lapsed user who deletes their key is back here) is stated there
  // rather than hidden by this module.
  assert.equal(lapseRefusal(LICENCE_STATES.NO_KEY, WRITES.LAUNCH), null);
  assert.equal(lapseRefusal(LICENCE_STATES.NO_KEY, WRITES.MERGE), null);
  assert.equal(lapseRefusal(LICENCE_STATES.NO_KEY, WRITES.CONFIG), null);
  assert.equal(lapseRefusal(LICENCE_STATES.NO_KEY, WRITES.MESSAGE), null);
});

test('the same key is valid, then in grace, then lapsed, as the clock moves', () => {
  const production = signer('prod-2026');
  const key = mint(production, claimsFor());
  const keys = [key];
  const build = verifier([production]);

  // Inside the term: everything works (§5.1).
  const valid = licenceState({ keys, verifier: build, now: at(ISSUED) });
  assert.equal(valid.state, LICENCE_STATES.VALID);
  assert.deepEqual(valid.claims, claimsFor(), 'the claims come back with the state');

  // The key's last second is `valid` and the one after it is `grace` (the
  // verifier's own boundary: `now <= expires` is valid), and everything still
  // works through the grace -- the renewal banner is the only difference
  // (§5.1).
  assert.equal(
    licenceState({ keys, verifier: build, now: at(ISSUED + 365 * DAY) }).state,
    LICENCE_STATES.VALID,
    'the expiry second itself is still inside the term',
  );
  const grace = licenceState({ keys, verifier: build, now: at(ISSUED + 365 * DAY + 1) });
  assert.equal(grace.state, LICENCE_STATES.GRACE);
  assert.equal(lapseRefusal(grace.state, WRITES.LAUNCH), null, 'nothing is refused for the length of the grace');

  const lastGraceSecond = licenceState({ keys, verifier: build, now: at(ISSUED + (365 + GRACE_DAYS) * DAY) });
  assert.equal(lastGraceSecond.state, LICENCE_STATES.GRACE, 'the grace runs thirty days from `expires`');

  // One second later the key has lapsed, and it is the only state that refuses.
  const lapsed = licenceState({ keys, verifier: build, now: at(ISSUED + (365 + GRACE_DAYS) * DAY + 1) });
  assert.equal(lapsed.state, LICENCE_STATES.LAPSED);
  assert.notEqual(lapseRefusal(lapsed.state, WRITES.LAUNCH), null);
});

test('a key this build cannot verify reads as no key, and not as a lapse', () => {
  const production = signer('prod-2026');
  const stranger = signer('prod-2027');
  // The store only ever held a key that verified when it was pasted; the
  // keyring then dropped the id it names (its grace passed), and the app reads
  // a text nothing verifies. It gets the no-key state's behaviour and its own
  // banner (§5.4) -- never a refusal, because a product that read a verifier's
  // refusal as read-only would be taking a capability away on a fact the
  // verifier declined to judge.
  const read = licenceState({ keys: [mint(stranger, claimsFor())], verifier: verifier([production]), now: at(ISSUED) });
  assert.deepEqual(read, { state: LICENCE_STATES.UNVERIFIED, claims: null });
  assert.equal(lapseRefusal(read.state, WRITES.LAUNCH), null, 'and it keeps the launch');
});

test('the renewal is the key that applies (T26), and its later expiry is the state', () => {
  const production = signer('prod-2026');
  const first = mint(production, claimsFor({ expires: ISSUED + 365 * DAY }));
  const renewal = mint(production, claimsFor({ issued: ISSUED + 335 * DAY, expires: ISSUED + 700 * DAY }));
  const build = verifier([production]);

  // Both keys are held, the first has expired, and the renewal has not: the
  // state is the LATEST key's, so an overlap installs a working licence
  // without a day of read-only.
  const read = licenceState({ keys: [first, renewal], verifier: build, now: at(ISSUED + 400 * DAY) });
  assert.equal(read.state, LICENCE_STATES.VALID, 'the renewal is the one that applies');
  assert.equal(read.claims.expires, ISSUED + 700 * DAY);

  // A key the verifier refuses is skipped rather than counted, so a dropped
  // key beside a good one changes nothing.
  const stranger = signer('prod-2027');
  const mixed = licenceState({ keys: [mint(stranger, claimsFor()), first], verifier: build, now: at(ISSUED) });
  assert.equal(mixed.state, LICENCE_STATES.VALID, 'the key that verified is the one read');
});

// --- what a lapse refuses, and what it keeps -------------------------------

test('a lapse keeps the message, the cancel and the kill switch, and refuses the launch, the merge and the configuration', () => {
  const lapsed = LICENCE_STATES.LAPSED;

  // T12's kept set, whole: ordinary chatter (and every read, which never
  // reaches this module), a cancel, and the kill switch.
  assert.equal(lapseRefusal(lapsed, WRITES.MESSAGE), null, 'chatter is kept');
  assert.equal(lapseRefusal(lapsed, WRITES.CANCEL), null, 'cancel is kept');
  assert.equal(lapseRefusal(lapsed, WRITES.KILL_SWITCH), null, 'the kill switch is kept');

  // And the refused set, whole: the launch, the configuration change and the
  // repository write -- which is T12's "cannot launch or change configuration",
  // with `merge` refused because a repository write is not among the three
  // things T12 keeps (§4.2's own call).
  for (const write of [WRITES.LAUNCH, WRITES.MERGE, WRITES.CONFIG]) {
    const refused = lapseRefusal(lapsed, write);
    assert.deepEqual(
      refused,
      { ok: false, code: LICENCE_STATE_CODES.LAPSED, field: 'licence' },
      `${write} is refused after the grace`,
    );
  }

  // The kept set is CLOSED and NAMED (§3.2): a write this table does not name
  // -- a class a later task adds and this one never heard of -- is refused
  // rather than allowed, so the refusal can never become "anything that
  // writes" in reverse. The alternative, a table of refused names, would allow
  // every write nobody had thought about yet.
  assert.deepEqual(lapseRefusal(lapsed, 'a-write-nobody-named'), refusedWrite());
  assert.deepEqual([...LAPSE_KEPT_WRITES], [WRITES.MESSAGE, WRITES.CANCEL, WRITES.KILL_SWITCH], 'the kept set, as written');

  // Every other state refuses nothing at all -- not only the two that allow
  // everything, but the two the design reads as the no-key state as well.
  for (const state of [LICENCE_STATES.NO_KEY, LICENCE_STATES.VALID, LICENCE_STATES.GRACE, LICENCE_STATES.UNVERIFIED]) {
    for (const write of [...Object.values(WRITES), 'a-write-nobody-named']) {
      assert.equal(lapseRefusal(state, write), null, `${state} refuses nothing (${write})`);
    }
  }
});

/** refusedWrite is the refusal every refused write gets. */
function refusedWrite() {
  return Object.freeze({ ok: false, code: LICENCE_STATE_CODES.LAPSED, field: 'licence' });
}

// --- the classification the write path hands in -----------------------------

test('the verb a message was classified as names the write it makes', () => {
  // The app's closed verb set, as §4.2's table reads it: the two a lapse
  // refuses, the three it keeps. A message with no classification at all is
  // chatter -- and every read the app speaks -- which is a kept write.
  assert.equal(writeOf({}), WRITES.MESSAGE, 'an unclassified utterance is a message');
  assert.equal(writeOf({ verb: 'launch' }), WRITES.LAUNCH, 'D162’s pass-through is the launch');
  assert.equal(writeOf({ verb: 'config' }), WRITES.CONFIG);
  assert.equal(writeOf({ verb: 'merge' }), WRITES.MERGE);
  assert.equal(writeOf({ verb: 'cancel' }), WRITES.CANCEL);
  assert.equal(writeOf({ verb: 'confirm' }), WRITES.CANCEL, 'a confirmation with no record names nothing else');
  assert.equal(writeOf({ verb: 'kill' }), WRITES.KILL_SWITCH);
  assert.equal(writeOf({ verb: 'kill-switch' }), WRITES.KILL_SWITCH, 'the switch is kept whichever spelling arrives');

  // A verb nobody wrote is a write this table cannot name, and that is refused
  // after a lapse rather than guessed at: the classification is the CALLER's,
  // so an unrecognised one is a caller the table cannot vouch for.
  assert.equal(writeOf({ verb: 'a-verb-nobody-wrote' }), null);
  assert.equal(writeOf({ verb: 'toString' }), null, 'and a name off the prototype is not a verb this table knows');
  assert.deepEqual(lapseRefusal(LICENCE_STATES.LAPSED, writeOf({ verb: 'a-verb-nobody-wrote' })), refusedWrite());
});

test('an action record is classified by its verb and, for a confirm, by its target', () => {
  // D169's three verbs travel as records rather than as verbs (`client.js` says
  // why), so the record is what the write path has to read. Its fields are read
  // and never trusted: the schema validates the record later in the same call,
  // and a record this module cannot classify is refused at a lapse like any
  // other unnameable write.
  assert.equal(writeOf({ action: { verb: 'cancel', target: { run: 'run-42' } } }), WRITES.CANCEL);
  assert.equal(writeOf({ action: { verb: 'merge', target: { repo: 'acme/site', pr: 7 } } }), WRITES.MERGE);

  // A `confirm` is the one verb whose class is its target's: confirming a merge
  // is a repository write, confirming a cancel is not (§4.2's `merge` row).
  assert.equal(
    writeOf({ action: { verb: 'confirm', target: { repo: 'acme/site', pr: 7 } } }),
    WRITES.MERGE,
    'a confirmation naming a repository is the repository write',
  );
  assert.equal(
    writeOf({ action: { verb: 'confirm', target: { run: 'run-42' } } }),
    WRITES.CANCEL,
    'a confirmation naming a run is the cancel it confirms',
  );

  for (const [reason, action] of [
    ['not an object', 'cancel'],
    ['null', null],
    ['an array', []],
    ['an unknown verb', { verb: 'a-verb-nobody-wrote', target: { run: 'run-42' } }],
    ['no verb at all', { target: { run: 'run-42' } }],
  ]) {
    assert.equal(writeOf({ action }), null, `an action record that is ${reason} is not a write this table names`);
  }

  // The action record wins over a verb handed in beside it: what the write
  // carries is what it does.
  assert.equal(
    writeOf({ verb: 'launch', action: { verb: 'cancel', target: { run: 'run-42' } } }),
    WRITES.CANCEL,
    'the record is what is being written',
  );
});

test('the refusal is a sentence that names the state and the way out, and no figure', () => {
  const said = licenceSpeech(LICENCE_STATE_CODES.LAPSED);
  assert.equal(typeof said, 'string');
  assert.match(said, /lapsed/i, 'it names the state');
  assert.match(said, /licence key/i, 'and the way out of it');
  // No figure enters the copy (D430, T22; S0.1 pins it repository-wide): a
  // refusal names a state and a route, never an amount, and the renewal
  // reminder names the day rather than a price. The digits are the check --
  // the sentence above is the only one this table has, and a date or a price
  // would arrive as digits.
  assert.equal(/\d/.test(said), false, 'no figure in the copy');
  // A code the table does not carry answers `null` rather than a generic
  // sentence, exactly as `gate.js`'s `offlineSpeech` does: a caller that shows
  // a sentence for a reason nobody wrote is a caller telling the owner
  // something nobody decided.
  assert.equal(licenceSpeech('a-code-nobody-wrote'), null);
});

// --- the port the mailbox client is handed ---------------------------------

/** MemoryStore is the licence store as the port calls it: `keys()` and
 * nothing else, which is all `createLicenceState` may need. */
class MemoryStore {
  #keys;

  constructor(keys) {
    this.#keys = keys;
  }

  async keys() {
    return Object.freeze({ ok: true, keys: Object.freeze([...this.#keys]) });
  }
}

test('the port reads the store and answers the state the store holds', async () => {
  const production = signer('prod-2026');
  const key = mint(production, claimsFor());
  const port = createLicenceState({ store: new MemoryStore([key]), verifier: verifier([production]) });

  assert.equal((await port.at(at(ISSUED))).state, LICENCE_STATES.VALID);
  assert.equal((await port.at(at(ISSUED + 370 * DAY))).state, LICENCE_STATES.GRACE);
  assert.equal((await port.at(at(ISSUED + 400 * DAY))).state, LICENCE_STATES.LAPSED);
  const empty = createLicenceState({ store: new MemoryStore([]), verifier: verifier([production]) });
  assert.equal((await empty.at(at(ISSUED))).state, LICENCE_STATES.NO_KEY);

  // The port is asked at a moment and answers about that moment, and it reads
  // no clock of its own: two calls at one `now` are one answer.
  assert.deepEqual(await port.at(at(ISSUED + 400 * DAY)), await port.at(at(ISSUED + 400 * DAY)));
  assert.equal((await port.at(at(ISSUED + 400 * DAY))).state, LICENCE_STATES.LAPSED);
});

test('a store that will not answer reads as no key, and not as a lapse', async () => {
  const production = signer('prod-2026');
  // The design gives the app five states and none of them is "the store did
  // not answer". Failing closed here would refuse the launch to an
  // installation whose storage hiccuped -- a check no ruling has, in the
  // window D523 rules has none -- while the honest reading is the one this
  // module can make: nothing is installed as far as the app can tell, which is
  // §5.3's state and its hole (a lapsed user can reach it by deleting their
  // key too, and the design states that price rather than closing it).
  const broken = { keys: async () => Object.freeze({ ok: false, code: 'store-failed', field: 'keys' }) };
  const port = createLicenceState({ store: broken, verifier: verifier([production]) });
  const read = await port.at(at(ISSUED));
  assert.equal(read.state, LICENCE_STATES.NO_KEY);
  assert.equal(lapseRefusal(read.state, WRITES.LAUNCH), null, 'and so it refuses nothing');
});

test('the write path reads the port for the writes a lapse could refuse, and never for the ones it keeps', async () => {
  const production = signer('prod-2026');
  const port = createLicenceState({
    store: new MemoryStore([mint(production, claimsFor())]),
    verifier: verifier([production]),
  });
  const past = at(ISSUED + 400 * DAY);
  const live = at(ISSUED + 10 * DAY);

  // The verdicts, over a real store and a real key: the launch is refused once
  // the key has lapsed and not before, and the kept set is kept at both.
  assert.deepEqual(await lapseRefusalAt(port, { now: past, write: WRITES.LAUNCH }), refusedWrite());
  assert.equal(await lapseRefusalAt(port, { now: live, write: WRITES.LAUNCH }), null, 'a live key refuses nothing');
  assert.equal(await lapseRefusalAt(port, { now: past, write: WRITES.CANCEL }), null);

  // **And the kept writes are not read at all.** A cancel, a message and the
  // kill switch are kept at every state, so there is nothing to read for them:
  // the phone does not open its licence store to send an ordinary message. The
  // spy is the claim -- a port that answers a lapse and is never asked.
  const asked = [];
  const spy = { at: async (now) => { asked.push(now); return Object.freeze({ state: LICENCE_STATES.LAPSED, claims: null }); } };
  for (const write of LAPSE_KEPT_WRITES) {
    assert.equal(await lapseRefusalAt(spy, { now: past, write }), null, `${write} is kept, and needs no reading`);
  }
  assert.deepEqual(asked, [], 'a kept write never reads the store');
  // A write the table cannot name is NOT kept (§4.2's closed set), so it is read
  // and refused: the reading happens for everything the table cannot vouch for.
  assert.deepEqual(await lapseRefusalAt(spy, { now: past, write: null }), refusedWrite());
  assert.deepEqual(await lapseRefusalAt(spy, { now: past, write: 'a-write-nobody-named' }), refusedWrite());
  assert.deepEqual(asked, [past, past], 'an unnameable write is read, and at the moment it was handed');

  // The shipped default carries no reading and refuses nothing: D523's
  // honour-based product, and the reason a release that has not wired a verifier
  // yet does not refuse a launch.
  assert.equal(await lapseRefusalAt(UNENFORCED_LICENCE, { now: past, write: WRITES.LAUNCH }), null);
});

test('a port built over something that cannot read a store is a wiring bug, and throws', () => {
  const production = signer('prod-2026');
  const build = verifier([production]);
  assert.throws(() => createLicenceState({}), TypeError);
  assert.throws(() => createLicenceState({ store: { read: async () => null }, verifier: build }), TypeError);
  assert.throws(() => createLicenceState({ store: new MemoryStore([]) }), TypeError);
  assert.throws(() => createLicenceState({ store: new MemoryStore([]), verifier: {} }), TypeError);
});

// --- the module itself -----------------------------------------------------

/** The module as text: this suite runs with no `node_modules` (mobile/README),
 * so what `state.js` must NOT reach is a claim a scan makes and not one a load
 * makes. */
const STATE_SOURCE = readFileSync(join(import.meta.dirname, 'state.js'), 'utf8');

test('the state module imports nothing, reads no clock and reaches no network', () => {
  // The property the app's suite rests on, and the one `verify.js` is held to:
  // every module a test loads must import nothing, or the suite needs an
  // install to run. The mailbox client is a caller of this file, which is why
  // it can be imported by the app's one write path at all.
  assert.equal(/^\s*import\s/m.test(STATE_SOURCE), false, 'state.js must import nothing');
  assert.equal(/\brequire\s*\(/.test(STATE_SOURCE), false, 'state.js must require nothing');
  assert.equal(/^\s*\}?\s*from\s+['"]/m.test(STATE_SOURCE), false, 'state.js must import from nothing');

  // The rules that absence exists for, as the tokens a change would need, and
  // each with a plant the pattern must catch first -- a scan that matches
  // nothing is a scan that proves nothing.
  for (const [rule, plant] of [
    [/fetch\s*\(|XMLHttpRequest|WebSocket|EventSource/, "fetch('https://example.invalid/key')"],
    [/\bprocess\.env\b/, 'const key = process.env.FUGARO_LICENCE_KEY'],
    [/\bDate\.now\s*\(|new Date\s*\(/, 'const now = Date.now()'],
  ]) {
    assert.equal(rule.test(plant), true, `the scan must catch ${plant}`);
    assert.equal(rule.test(STATE_SOURCE), false, `state.js carries ${rule}`);
  }
});
