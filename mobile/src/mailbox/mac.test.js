// The message tag, in the app (mobile plan, Task P3.6; P6.3's schema and
// `vectors.json`, functional-spec-reconciliation.md §3.5, D171).
//
// `mac.js` is where the app's arithmetic (`sha256.js`) meets the protocol
// (`shared/mailbox-schema`'s `canonicalBytes` and its `DIRECTIONS`). What it
// has to get right is not the hash -- that is proved next door against Node --
// but **which bytes** are signed and **which direction** they are signed for,
// because that is what the consumer on the other end recomputes.
//
// So the test that matters most in this file is not written here at all: it is
// `vectors.json`, the file P6.3 generated from the Node implementation and
// which the Go consumer also reimplements. Every case in it is re-computed by
// this module and compared byte for byte, and every `direction_pairs` entry is
// asserted to be a **different tag for the same message** -- the property the
// owner's ruling of 2026-10-18 added one field for. A tag that verified in two
// directions would let a reflected message pass as its own reply.
//
// The rest is the tampering the plan's P3.6 verification names: the id, the
// text and the timestamp each moved by one, a `mac` that is missing, empty, of
// the wrong length or the right shape with the wrong case, and a record whose
// field set the schema would refuse. A verifier that threw on any of them
// would be a verifier that a hostile record can crash; one that returned true
// for any of them is the bug the whole task exists to prevent.
//
// **Every value below is asserted to be a placeholder.** `vectors.json`'s key
// says so itself (`key_note`), and this file asserts it before using it: a
// fixture that could be mistaken for a real credential is a fixture a scanner
// has to be taught to ignore.

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import { DIRECTIONS, canonicalBytes, validateHeartbeat, validateMessage } from '../../../shared/mailbox-schema/index.js';
import { macOf as nodeMacOf, verifyMac as nodeVerifyMac } from '../../../shared/mailbox-schema/mac.js';
import { macOf, verifyMac } from './mac.js';

const VECTORS = JSON.parse(readFileSync(new URL('../../../shared/mailbox-schema/vectors.json', import.meta.url), 'utf8'));

/** P6.3's placeholder key: the vectors' own, and never a credential. */
const KEY = VECTORS.key;

test('the vectors\' key is a placeholder, so nothing here is a real credential', () => {
  // The positive control for the fixture rule. `vectors.json` states the same
  // in `key_note`; if the file ever grew a real-looking key, this fails rather
  // than the fixture quietly becoming one.
  assert.equal(typeof KEY, 'string');
  assert.match(VECTORS.key_note, /not a secret|placeholder|test/i, VECTORS.key_note);
  assert.ok(KEY.includes('not-a-secret') || KEY.includes('test'), KEY);
  assert.ok(KEY.length >= 8);
});

test('every case in vectors.json recomputes to the tag it carries', () => {
  // The cross-language contract: 24 records -- a plain message, prompts with
  // options, answers with `reply_to`, action records, agent names, acked
  // flags, non-ASCII text -- each with the canonical bytes and the tag P6.3's
  // Node implementation produced. `canonical` is asserted too, so a failure
  // says whether this module signed the wrong bytes or hashed them wrongly.
  assert.ok(VECTORS.cases.length >= 20, `expected the vectors' cases, found ${VECTORS.cases.length}`);
  let verified = 0;
  let refused = 0;
  for (const vector of VECTORS.cases) {
    const { direction, message, canonical, mac } = vector;
    assert.ok(DIRECTIONS.includes(direction), `${vector.name}: ${direction}`);
    // The bytes are the same question in both verdicts, and so is what the tag
    // of THIS record must be: the vectors carry the record's own (stale) `mac`
    // on the record and the tag of the record as given beside it. A `refused`
    // case is one where those two are different records' tags -- `text
    // swapped`, `inbox tag read as outbox` -- which is exactly the forgery
    // §3.5 is about.
    assert.equal(canonicalBytes(message, direction), canonical, `${vector.name}: the bytes`);
    assert.equal(macOf(message, KEY, direction), mac, `${vector.name}: the tag`);
    if (vector.verdict === 'verified') {
      assert.equal(message.mac, mac, `${vector.name}: the record carries its own tag`);
      assert.equal(verifyMac(message, KEY, direction), true, `${vector.name}: verifies`);
      verified += 1;
    } else {
      assert.equal(vector.verdict, 'refused', `${vector.name}: an unknown verdict`);
      assert.notEqual(message.mac, mac, `${vector.name}: the record carries another tag`);
      assert.equal(verifyMac(message, KEY, direction), false, `${vector.name}: refused`);
      refused += 1;
    }
  }
  // A positive control on the loop's own expectations: the file must contain
  // cases of both verdicts, or the loop above is one branch of a fixture
  // agreeing with itself.
  assert.ok(verified >= 10, `${verified} cases state a verified verdict`);
  assert.ok(refused >= 5, `${refused} cases state a refused verdict`);
});

test('the same message signed for two directions is two different tags', () => {
  // The owner's ruling of 2026-10-18: the direction is inside the covered
  // bytes. Without it, a message the phone wrote could be reflected back into
  // its own outbox and read as a reply the consumer never wrote -- the exact
  // shape §3.5's forgery model is about.
  assert.ok(VECTORS.direction_pairs.length >= 3, 'the vectors state the direction pairs');
  for (const pair of VECTORS.direction_pairs) {
    const a = macOf(pair.a.message, KEY, pair.a.direction);
    const b = macOf(pair.b.message, KEY, pair.b.direction);
    assert.equal(a, pair.a.mac, `${pair.name}: ${pair.a.direction}`);
    assert.equal(b, pair.b.mac, `${pair.name}: ${pair.b.direction}`);
    assert.notEqual(a, b, `${pair.name}: the direction must change the tag`);
    // ... and each tag verifies only in its own direction.
    assert.equal(verifyMac(pair.a.message, KEY, pair.b.direction), false, `${pair.name}: a is not b`);
    assert.equal(verifyMac(pair.b.message, KEY, pair.a.direction), false, `${pair.name}: b is not a`);
  }
});

test('the module agrees with P6.3\'s Node implementation on every direction', () => {
  // Two implementations, one protocol: over the vectors' own records and every
  // direction, including the two the record was not written for.
  for (const vector of VECTORS.cases) {
    for (const direction of DIRECTIONS) {
      assert.equal(
        macOf(vector.message, KEY, direction),
        nodeMacOf(vector.message, KEY, direction),
        `${vector.name}: ${direction}`,
      );
      assert.equal(
        verifyMac(vector.message, KEY, direction),
        nodeVerifyMac(vector.message, KEY, direction),
        `${vector.name}: ${direction}`,
      );
    }
  }
});

test('a message with any field moved is refused', () => {
  // "Verifies the MAC over each outbox message's id, text and timestamp"
  // (P3.6). The schema's own field set is walked, so a field added to P6.3
  // later is covered by this test the day it lands rather than the day
  // somebody remembers.
  const message = Object.freeze({
    id: 'm-0001',
    sender: 'phone',
    text: 'merge it',
    at: 1_700_000_000_000,
    mac: 'f'.repeat(64),
  });
  const signed = Object.freeze({ ...message, mac: macOf(message, KEY, 'outbox') });
  assert.equal(verifyMac(signed, KEY, 'outbox'), true, 'the control: it verifies as written');

  const moved = [
    ['id', 'm-0002'],
    ['id', 'm-000'],
    ['sender', 'someone-else'],
    ['text', 'merge it now'],
    ['text', 'Merge it'],
    ['text', 'merge i'],
    ['at', 1_700_000_000_001],
    ['at', 1_700_000_000_000 - 1],
  ];
  for (const [field, value] of moved) {
    assert.equal(verifyMac({ ...signed, [field]: value }, KEY, 'outbox'), false, `${field} -> ${value}`);
  }

  // The extension fields too: a `kind`, an `agent`, an action record, an
  // options list. Each changes the bytes and so the tag.
  const extended = {
    kind: 'prompt',
    agent: 'orchestrator',
    options: ['merge it', 'leave it'],
    reply_to: 'm-0001',
  };
  for (const [field, value] of Object.entries(extended)) {
    assert.equal(verifyMac({ ...signed, [field]: value }, KEY, 'outbox'), false, `${field} added`);
    const withField = { ...message, [field]: value };
    const signedWith = { ...withField, mac: macOf(withField, KEY, 'outbox') };
    assert.equal(verifyMac(signedWith, KEY, 'outbox'), true, `${field}: signed properly it verifies`);
    assert.equal(verifyMac({ ...signedWith, [field]: 'something else' }, KEY, 'outbox'), false, `${field} moved`);
  }

  // `acked` is the one field outside the bytes (the phone writes it after the
  // tag was made), and a tag that covered it would make the ack impossible.
  assert.equal(verifyMac({ ...signed, acked: true }, KEY, 'outbox'), true, 'acked is not covered');
  // An unknown field, by contrast, is covered -- absent fields are omitted,
  // present ones are signed -- so a record with one does not verify.
  assert.equal(verifyMac({ ...signed, run: 'r-1' }, KEY, 'outbox'), false, 'an unknown field is covered');
});

test('a tag that is missing, malformed, or another record\'s is refused', () => {
  const message = { id: 'm-0001', sender: 'phone', text: 'run 42 finished', at: 1_700_000_000_000 };
  const mac = macOf(message, KEY, 'inbox');
  assert.equal(verifyMac({ ...message, mac }, KEY, 'inbox'), true, 'the control');

  const other = { ...message, id: 'm-0002', mac: macOf({ ...message, id: 'm-0002' }, KEY, 'inbox') };
  for (const bad of [
    ['absent', undefined],
    ['null', null],
    ['a number', 1],
    ['empty', ''],
    ['short', mac.slice(0, 63)],
    ['long', `${mac}0`],
    ['uppercase', mac.toUpperCase()],
    ['not hex', 'z'.repeat(64)],
    ['another record\'s', other.mac],
    ['all f', 'f'.repeat(64)],
    ['a prefix', mac.slice(0, 40)],
  ]) {
    const record = { ...message, ...(bad[1] === undefined ? {} : { mac: bad[1] }) };
    assert.equal(verifyMac(record, KEY, 'inbox'), false, `mac is ${bad[0]}`);
  }
});

test('a tag that differs only past the eighth character is refused too', () => {
  // The comparison is over the WHOLE tag. Every other case in this file differs
  // in the first character (a wrong case, a `z`, an `f`) or in the length, so a
  // comparison that stopped after eight hex characters -- 32 bits, which an
  // attacker can search -- would answer `true` here and every one of them would
  // still pass. The positions below are spread over the rest of the tag,
  // including the last character.
  const message = { id: 'm-0001', sender: 'phone', text: 'run 42 finished', at: 1_700_000_000_000 };
  const mac = macOf(message, KEY, 'inbox');
  assert.equal(verifyMac({ ...message, mac }, KEY, 'inbox'), true, 'the control');
  assert.equal(mac.length, 64);
  for (const position of [8, 9, 15, 16, 31, 32, 48, 63]) {
    const flipped = `${mac.slice(0, position)}${mac[position] === 'a' ? 'b' : 'a'}${mac.slice(position + 1)}`;
    assert.notEqual(flipped, mac);
    assert.equal(
      verifyMac({ ...message, mac: flipped }, KEY, 'inbox'),
      false,
      `a tag whose hex character ${position} was changed`,
    );
    // ... and the same record signed again with the altered tag is not equal to
    // the original either: the control is about the comparison, not the hash.
    assert.equal(macOf(message, KEY, 'inbox'), mac);
  }
});

test('a key that is not the pairing key is refused, and the refusal never throws', () => {
  const message = { id: 'm-0001', sender: 'phone', text: 'run 42 finished', at: 1_700_000_000_000 };
  const signed = { ...message, mac: macOf(message, KEY, 'inbox') };
  for (const key of ['', 'not-the-key', `${KEY}x`, KEY.slice(0, -1), KEY.toUpperCase()]) {
    assert.equal(verifyMac(signed, key, 'inbox'), false, JSON.stringify(key));
  }
  // A key that is not a string at all is a wiring bug, and the answer is still
  // `false` rather than a throw: the caller is a message loop.
  for (const key of [undefined, null, 42, {}, [], new Uint8Array(0)]) {
    assert.equal(verifyMac(signed, key, 'inbox'), false, String(key));
  }
  // A direction that is not one of the three is refused -- never silently
  // signed as something else, and never an exception out of the loop.
  for (const direction of [undefined, null, '', 'inbox ', 'INBOX', 'inboxs', 0, {}]) {
    assert.equal(verifyMac(signed, KEY, direction), false, String(direction));
  }
});

test('a record the serialisation refuses is refused, not thrown over', () => {
  // `canonicalBytes` throws for the three classes the two languages spell
  // differently -- a null or undefined field, a non-integer number, a
  // separator character -- and for a record carrying its own direction. A
  // verifier that propagated the throw would hand a hostile record a way to
  // stop the phone's message loop; the answer is `false`.
  const base = { id: 'm-0001', sender: 'phone', text: 'run 42 finished', at: 1_700_000_000_000, mac: 'f'.repeat(64) };
  // The control: the record as written signs fine, so the refusals below are
  // about the field that was added and not about the record.
  assert.equal(typeof macOf(base, KEY, 'inbox'), 'string');

  for (const bad of [
    ['a null field', { ...base, text: null }],
    ['an undefined field', { ...base, agent: undefined }],
    ['a float', { ...base, at: 1_700_000_000_000.5 }],
    ['a separator in the text', { ...base, text: 'run 42' }],
    ['a lone surrogate', { ...base, text: 'run\uD800' }],
    ['its own direction', { ...base, direction: 'inbox' }],
    ['an array', [base]],
    ['null', null],
    ['a string', 'm-0001'],
    ['a number', 42],
  ]) {
    assert.equal(verifyMac(bad[1], KEY, 'inbox'), false, bad[0]);
    assert.throws(() => macOf(bad[1], KEY, 'inbox'), TypeError, `${bad[0]}: macOf throws`);
  }
});

test('a heartbeat is signed and verified like a message, over its own fields', () => {
  // P3.6 reads the heartbeat and treats an unsigned or wrongly signed one as
  // offline. The tag covers the advertised agent list too, which is why the
  // phone ignores that list unless the tag verifies: an unsigned heartbeat
  // would otherwise let any minted token announce itself as the agent the
  // owner talks to.
  const heartbeat = { at: 1_700_000_000_000, boot: 'boot-1' };
  const mac = macOf(heartbeat, KEY, 'heartbeat');
  assert.equal(verifyMac({ ...heartbeat, mac }, KEY, 'heartbeat'), true);
  // ... and the schema accepts it, which is what makes it a record the
  // consumer could have written.
  assert.equal(validateHeartbeat({ ...heartbeat, mac }).ok, true);
  assert.equal(verifyMac({ ...heartbeat, mac }, KEY, 'outbox'), false, 'not an outbox record');
  assert.equal(verifyMac({ ...heartbeat, boot: 'boot-2', mac }, KEY, 'heartbeat'), false);
  assert.equal(verifyMac({ ...heartbeat, at: heartbeat.at + 1, mac }, KEY, 'heartbeat'), false);

  const announced = { ...heartbeat, agents: [{ id: 'orchestrator', name: 'Orchestrator' }] };
  const announcedMac = macOf(announced, KEY, 'heartbeat');
  assert.equal(validateHeartbeat({ ...announced, mac: announcedMac }).ok, true);
  assert.equal(verifyMac({ ...announced, mac: announcedMac }, KEY, 'heartbeat'), true);
  assert.equal(verifyMac({ ...announced, mac }, KEY, 'heartbeat'), false, 'the list is covered');
  assert.equal(
    verifyMac({ ...announced, agents: [{ id: 'orchestrator', name: 'Impostor' }], mac: announcedMac }, KEY, 'heartbeat'),
    false,
    'a renamed agent in the list is a different tag',
  );
});

test('macOf refuses a key that is not a string rather than coercing one', () => {
  // The other half of the asymmetry: `verifyMac` answers `false` for anything
  // it cannot decide, because it is called on values that arrived from
  // outside; `macOf` is called by the writer and throws, because a writer that
  // silently produced a tag from `String(undefined)` would be a writer whose
  // messages nobody can verify.
  const message = { id: 'm-0001', sender: 'phone', text: 'run 42 finished', at: 1_700_000_000_000 };
  for (const key of [undefined, null, 42, {}, [], new Uint8Array(0)]) {
    assert.throws(() => macOf(message, key, 'inbox'), TypeError, String(key));
  }
  assert.throws(() => macOf(message, KEY, 'nowhere'), TypeError, 'an unknown direction');
  assert.throws(() => macOf(message, KEY, undefined), TypeError, 'a missing direction');
});

test('the tag is lowercase hex of the schema\'s length, and the schema takes it', () => {
  const message = { id: 'm-0001', sender: 'phone', text: 'run 42 finished', at: 1_700_000_000_000 };
  const mac = macOf(message, KEY, 'inbox');
  assert.match(mac, /^[0-9a-f]{64}$/);
  assert.equal(validateMessage({ ...message, mac }).ok, true, 'the schema accepts what this signs');
  // The schema's own shape rule refuses a tag this module could not have
  // written, so the two ends cannot disagree about what a tag looks like.
  assert.equal(validateMessage({ ...message, mac: mac.toUpperCase() }).ok, false);
  assert.equal(validateMessage({ ...message }).ok, false, 'and an unsigned message is refused');
});
