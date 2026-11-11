// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// What a delivery carries, judged: the text's own rules (mobile plan, Task
// P6.8; the pairing plan's §4.4, §7.2, D199 and D171).
//
// **Why this file exists next to `envelope.test.js`.** That file proves the
// read path end to end on the bytes the Go command really sealed -- one
// document, written correctly. But a delivery is hostile input even though the
// tag verified (`delivery.js`'s header says why), and its refusals are what
// decide whether a document nobody wrote is read as one. The refusals that need
// no envelope are reachable on plaintext a test writes itself, so they are
// pinned here: the "one field, one value" rule in **every** object the document
// carries, the escape rule the exactness of that check rests on, and a control
// that the same document written once is accepted -- so a walk that refused
// everything would fail this file rather than pass it.
//
// Every value is synthetic, and no credential is in here: the token and the MAC
// key are shaped like nothing.

import assert from 'node:assert/strict';
import test from 'node:test';

import { CONFIG_FIELDS } from './config.js';
import { DELIVERY_CODES, DELIVERY_FIELDS, DEVICE_FIELDS, readDelivery } from './delivery.js';
import { PAYLOAD_VERSION, readScannedPayload } from './payload.js';

/** The pairing's uid, in the one canonical spelling. */
const UID = 'p~' + 'A'.repeat(43);

/** The configuration this file's deliveries carry: `MarshalPayload`'s four
 * fields, and the ones `judgeConfig` accepts as this project's. */
const CONFIG = Object.freeze({
  project: 'aurora',
  gcp_project: 'fp-1234',
  database_url: 'https://fp-1234-default-rtdb.firebaseio.com',
  api_key: 'test-web-api-key',
});

const DEVICE = Object.freeze({ uid: 'm~' + 'M'.repeat(21) + 'A', token: 'test-device-token-not-a-credential' });

/** The scan that started the pairing, read by the app's own reader: the
 * delivery is judged against it, so a test about the text must not trip over
 * the disagreement check. */
function scanOf(config = CONFIG) {
  const scanned = readScannedPayload(
    JSON.stringify({
      v: PAYLOAD_VERSION,
      ...config,
      token: 'test-pairing-token-not-a-credential',
      uid: UID,
      expires_at: '2026-11-11T17:50:00Z',
    }),
  );
  assert.equal(scanned.ok, true, JSON.stringify(scanned));
  return scanned.payload;
}

/** `MarshalPayload`'s document as **text**, because an object literal cannot
 * hold one key twice -- and a second spelling of a field is the one thing this
 * file is about. */
function documentText({ config = CONFIG, device = DEVICE, macKey = 'K'.repeat(42) + 'A' } = {}) {
  return `{"config":${JSON.stringify(config)},"device":${JSON.stringify(device)},"mac_key":${JSON.stringify(macKey)}}`;
}

/** The same document with `field` named twice: a first spelling inserted ahead
 * of the real one, so the last is the one `JSON.parse` keeps. */
function twice(text, field, value) {
  const at = text.indexOf(`"${field}":`);
  assert.notEqual(at, -1, `${field} is not in the document`);
  return `${text.slice(0, at)}"${field}":${JSON.stringify(value)},${text.slice(at)}`;
}

function judge(text) {
  return readDelivery({ plaintext: new TextEncoder().encode(text), scan: scanOf() });
}

test('a field named twice is refused whole, in every object the document carries', () => {
  const base = documentText();
  const cases = [
    ...DELIVERY_FIELDS.map((field) => [field, twice(base, field, 'x')]),
    ...DEVICE_FIELDS.map((field) => [field, twice(base, field, 'x')]),
    // The configuration's own four, which are the ones a walk over the outer
    // fields alone misses -- and the ones that decide what the app stores
    // (D199), so a second spelling of one is the worst place to resolve.
    ...CONFIG_FIELDS.map((field) => [field, twice(base, field, 'x')]),
  ];
  for (const [field, text] of cases) {
    const judged = judge(text);
    assert.equal(judged.ok, false, `${field} twice was accepted`);
    assert.equal(judged.code, DELIVERY_CODES.DUPLICATE_FIELD, field);
    assert.equal(judged.field, field, field);
  }
});

test('the document is refused rather than resolved, which is what the rule is for', () => {
  const text = twice(documentText(), 'project', 'other');
  // What a reader that skipped the check would get: the last spelling wins,
  // silently, and nothing on screen says a field was written twice.
  assert.equal(JSON.parse(text).config.project, CONFIG.project, 'the naive read keeps the last spelling');
  const judged = judge(text);
  assert.equal(judged.ok, false);
  assert.equal(judged.code, DELIVERY_CODES.DUPLICATE_FIELD);
});

test('an escape is refused, which is what makes the one-field rule exact', () => {
  // The fields are base64url, project names, a URL and a JWT, so `json.Marshal`
  // escapes nothing; with no backslash a `"` cannot occur inside a value, so a
  // field's token can only occur as its key. A document that escapes anything
  // is refused rather than read with that reasoning suspended.
  const judged = judge(documentText().replace('aurora', 'auro\\ra'));
  assert.equal(judged.ok, false);
  assert.equal(judged.code, DELIVERY_CODES.BACKSLASH);
});

test('the same document written once is the record the app stores', () => {
  // The control for the three tests above: every field named once is accepted,
  // so their refusals are the rule working and not the judge refusing a
  // document it never reads.
  const judged = judge(documentText());
  assert.equal(judged.ok, true, JSON.stringify(judged));
  assert.equal(judged.delivery.config.project, CONFIG.project);
  assert.equal(judged.delivery.config.databaseHost, 'fp-1234-default-rtdb.firebaseio.com');
  assert.equal(judged.delivery.device.uid, DEVICE.uid);
  assert.equal(judged.delivery.device.token, DEVICE.token);
  assert.equal(judged.delivery.macKey.length, 32);
});
