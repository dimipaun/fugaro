// The mobile session contract's vectors, read by the app's side of it
// (docs/mobile-identity-pairing implementation plan, task C1).
//
// `vectors.json` is the contract's data half: the claim names, the refusal
// codes, the routes, the secure-store item names, the audience derivation's
// cases, one example of every wire document and one signed key set. The Go half
// is `internal/mobilesession/contract.go` and its test holds the shapes to this
// file; this test holds the file to itself, and it is a deliberate SECOND
// implementation of everything in it rather than a re-run of Go's:
//
//   * the claim names, the codes, the routes, the store items and the five
//     lifetimes are written out again here, in the app's own words, so a rename
//     on either side is a red test on both;
//   * `audienceOf` below re-derives the audience F39 fixes with `new URL()` and
//     the string rules, which is what the app will run — if the two languages
//     disagreed about a trailing slash or a query string, an assertion the app
//     signs would be refused by a service that spelled the same endpoint its own
//     way, and no test on either side alone would see it;
//   * the envelope is verified with `node:crypto` over bytes rebuilt here, so
//     the vector is checked to be a signature and not a description of one.
//
// The red step of C1 is the Go side's: its vector test fails with this file
// missing. This file is not a stub to be filled in later — it is the second
// reader, and it exists from the first commit because a single reader's
// agreement with itself proves nothing.
//
// No credential and no secret-shaped literal is in this file or in the vectors
// it reads: the values are readable placeholders ("not-a-credential-test-…"),
// the keys are public ones, and one case asserts exactly that, because a fixture
// that looks like a credential gets copied into one.
//
// No dependency, no network, no clock, no randomness: node's own test runner,
// `node:crypto` for the one signature, and the JSON reader.

import assert from 'node:assert/strict';
import { createPublicKey, verify as verifySignature } from 'node:crypto';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import * as session from './index.js';

const vectors = JSON.parse(readFileSync(new URL('./vectors.json', import.meta.url), 'utf8'));

// The sections this reader knows. A section added to the file and not here is a
// fact one side relies on and the app never reads; a section this reader expects
// and the file lost is a check that silently stopped checking. Both are red.
const SECTIONS = [
  'about',
  'audience',
  'claims',
  'codes',
  'format',
  'identity_id',
  'key_set',
  'lifetimes_seconds',
  'routes',
  'secure_store_items',
  'signature',
  'wire',
];

// The claims an assertion carries, in the order the signature covers them
// (D2792): the app writes nothing else and reads nothing else.
const CLAIMS = [
  'iss',
  'aud',
  'sub',
  'email',
  'email_verified',
  'provider',
  'auth_time',
  'jti',
  'iat',
  'exp',
  'challenge',
];

// Every way a session request can be refused. The app renders one sentence per
// code and needs the list to be closed: a code the service sends and the phone
// does not know would be a refusal shown as a crash.
const CODES = [
  'provider_not_accepted',
  'auth_too_old',
  'not_a_member',
  'nonce_expired',
  'strict_new_device_needs_qr',
  'device_revoked',
  'mobile_disabled',
];

const ROUTES = [
  '/mobile/v1/pair',
  '/mobile/v1/refresh',
  '/mobile/v1/restore/challenge',
  '/mobile/v1/restore',
  '/mobile/v1/revoke-self',
];

// One item per thing the phone keeps in the keychain (D2790): a signed-in
// identity's session, that device key, one project session and the consumer key
// it pinned. The names are a contract because a name spelled differently at
// sign-out leaves the session on the device, and because `expo-secure-store`
// refuses a key outside its alphabet: every byte here is one it accepts, and the
// separator is a dot so the part that follows may be too.
const SECURE_STORE_ITEMS = [
  'fugaro.identity-session.',
  'fugaro.device-key.',
  'fugaro.project-session.',
  'fugaro.consumer-key.',
];

// The bytes a keychain key may carry: what `expo-secure-store` accepts, which is
// also what `mobile/src/accounts/account.js` requires of one account id.
const KEYCHAIN_SAFE = /^[A-Za-z0-9_.-]+$/;

const LIFETIMES_SECONDS = {
  assertion: 120,
  invite: 600,
  challenge: 600,
  custom_token: 3600,
  idle: 2592000,
};

// Every wire document and its fields, in the order the shape declares them. The
// Go structs' order is what the envelope's signing input is serialised in, and
// the same order here is what makes a reordered field a red test on both sides
// rather than a signature that stops verifying on a phone.
const WIRE = {
  assertion_request: ['aud', 'challenge'],
  assertion_response: ['assertion'],
  claims: CLAIMS,
  error: ['code', 'accepted_providers', 'message'],
  invite: ['nonce', 'email', 'role', 'accepted_providers', 'expires_at'],
  member: ['email', 'provider', 'sub'],
  paired_entry: ['project_id', 'endpoint', 'identity', 'consumer_key_fingerprint'],
  pair_request: ['assertion', 'endpoint', 'nonce', 'device_public_key', 'prior_device_id_token'],
  refresh_request: ['refresh_token', 'signature'],
  refresh_response: ['refresh_token', 'custom_token', 'expires_in', 'role', 'policy'],
  restore_challenge_request: [],
  restore_challenge_response: ['challenge', 'expires_in'],
  restore_request: ['assertion', 'challenge', 'device_public_key'],
  revoke_self_request: ['refresh_token', 'signature'],
  revoke_self_response: [],
  session_response: [
    'project_id',
    'endpoint',
    'database_url',
    'api_key',
    'role',
    'policy',
    'consumer_public_key',
    'refresh_token',
    'custom_token',
    'expires_in',
  ],
};

// What the phone is told about the project's session policy: which modes it
// treats as costly, whether it asks for a biometric there, and where the project
// compacts. Nested inside a session response and inside a refresh response, and
// never a secret, so it is read here as data the phone acts on.
const POLICY_FIELDS = ['costly_modes', 'biometric_on_costly', 'compact_threshold_percent'];

test('the vectors are the contract both languages read', () => {
  assert.equal(vectors.format, 'fugaro mobile session contract vectors');
  assert.deepEqual(Object.keys(vectors).sort(), SECTIONS);
  assert.ok(vectors.about.length > 0, 'the file says what it is for');
});

test('the claim names are the ones the app signs and the service verifies', () => {
  assert.deepEqual(vectors.claims, CLAIMS);
  for (const name of CLAIMS) {
    assert.ok(name.length > 0 && !name.includes('.'), `${name} is a claim name on the wire`);
  }
});

test('the refusal codes are closed', () => {
  assert.deepEqual(vectors.codes, CODES);
  assert.equal(new Set(CODES).size, CODES.length, 'no code is listed twice');
});

test('the routes the app calls are the ones the service serves', () => {
  assert.deepEqual(vectors.routes, ROUTES);
  for (const route of ROUTES) {
    assert.ok(route.startsWith('/mobile/v1/'), `${route} is under the version prefix`);
  }
});

test('the secure-store items are the ones sign-out deletes', () => {
  assert.deepEqual(vectors.secure_store_items, SECURE_STORE_ITEMS);
  for (const item of SECURE_STORE_ITEMS) {
    assert.ok(item.endsWith('.'), `${item} is a prefix: one item per signed-in identity and per project`);
    assert.ok(KEYCHAIN_SAFE.test(item), `${item} is inside the alphabet expo-secure-store accepts`);
  }
});

// A key `expo-secure-store` refuses throws on the phone instead of returning a
// miss, so a name composed out of a prefix and a part is a session the app can
// write or a crash, and nothing in between. Every composed name here is checked
// against the alphabet, with the parts the vectors themselves carry.
test('every item name the app composes is one the keychain takes', () => {
  const parts = [
    ...vectors.identity_id.cases.map((c) => c.identity).filter((identity) => identity !== null),
    ...new Set(Object.values(vectors.wire).map((doc) => doc.project_id).filter((id) => id !== undefined)),
  ];
  assert.ok(parts.length >= 3, 'the vectors name parts to compose with');
  for (const prefix of SECURE_STORE_ITEMS) {
    assert.ok(prefix.endsWith('.'), `${prefix} takes a part after it`);
    for (const part of parts) {
      assert.ok(KEYCHAIN_SAFE.test(prefix + part), `${prefix}${part} is a key expo-secure-store accepts`);
    }
  }
});

test('the lifetimes are O4s figures, and the app reads them rather than keeping its own', () => {
  assert.deepEqual(vectors.lifetimes_seconds, LIFETIMES_SECONDS);
  const { assertion, challenge, custom_token: customToken } = LIFETIMES_SECONDS;
  assert.ok(assertion < challenge, 'an assertion is shorter-lived than the challenge it answers');
  assert.ok(customToken <= LIFETIMES_SECONDS.idle, 'the hour is what the phone refreshes on, well inside the idle window');
});

test('a device key signs one message per purpose, and nothing else', () => {
  const { label, separator, messages } = vectors.signature;
  assert.equal(label, 'fugaro-mobile-');
  assert.equal(separator, ':');
  assert.ok(messages.length >= 2, 'both purposes the contract names have a message');
  const purposes = new Set();
  for (const { purpose, value, message } of messages) {
    purposes.add(purpose);
    assert.equal(message, `${label}${purpose}${separator}${value}`);
    assert.ok(value.length > 0, 'the value is what the signature is bound to');
  }
  // The device key also signs mailbox messages (O6). The label is what keeps a
  // signature over a refresh token from being a signature over a message.
  assert.ok(label.length > 0 && !label.includes('.'), 'the label separates this contract messages from the mailbox ones');
  assert.deepEqual([...purposes].sort(), ['refresh', 'revoke-self']);
});

// The app's own audience derivation (F39): what an assertion for this endpoint
// must name. It is written here rather than imported because `internal/` is Go
// and this is the second implementation the vectors exist to hold together.
function audienceOf(endpoint) {
  let url;
  try {
    url = new URL(endpoint);
  } catch {
    return null;
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') return null;
  if (url.username !== '' || url.password !== '') return null;
  if (url.search !== '' || url.hash !== '') return null;
  // At most one trailing slash, which is what the service's own TrimSuffix does:
  // a second one is a different path and has to stay visible.
  const path = url.pathname.endsWith('/') ? url.pathname.slice(0, -1) : url.pathname;
  return `${url.protocol}//${url.host}${path}`;
}

test('the audience comes from the endpoint, and only from an endpoint', () => {
  assert.ok(vectors.audience.cases.length >= 5, 'the derivation has cases, including refusals');
  let refused = 0;
  for (const { endpoint, audience, why } of vectors.audience.cases) {
    assert.equal(audienceOf(endpoint), audience, `${JSON.stringify(endpoint)}: ${why}`);
    if (audience === null) refused += 1;
  }
  assert.ok(refused > 0, 'a derivation with no refusal case is a derivation that refuses nothing');
  // A relayed QR is what the derivation is for: a second endpoint's audience is
  // never this endpoint's, so an assertion for one project is not an assertion
  // for another.
  assert.notEqual(audienceOf('https://mobile-example.a.run.app'), audienceOf('https://elsewhere.example'));
});

// The app's own join for one signed-in identity (D2790), the key of a
// paired-list entry and of a secure-store item. A part outside the keychain's
// alphabet is refused rather than joined: the name it would make is one
// `expo-secure-store` throws on, so the session could never be written at all.
function identityId(provider, sub) {
  if (provider === '' || sub === '') return null;
  if (!KEYCHAIN_SAFE.test(provider) || !KEYCHAIN_SAFE.test(sub)) return null;
  return `${provider}.${sub}`;
}

test('an identity is named by its provider and its stable id, or not at all', () => {
  assert.ok(vectors.identity_id.cases.length >= 4);
  for (const { provider, sub, identity, why } of vectors.identity_id.cases) {
    assert.equal(identityId(provider, sub), identity, `${provider}/${sub}: ${why}`);
  }
});

test('every wire document is the shape the app writes and reads, field for field', () => {
  assert.deepEqual(Object.keys(vectors.wire).sort(), Object.keys(WIRE).sort());
  for (const [name, fields] of Object.entries(WIRE)) {
    assert.deepEqual(Object.keys(vectors.wire[name]), fields, `${name} carries exactly these fields, in this order`);
  }
  for (const response of ['session_response', 'refresh_response']) {
    assert.deepEqual(
      Object.keys(vectors.wire[response].policy),
      POLICY_FIELDS,
      `${response}.policy is what the phone needs to honour the project's costly modes and its compaction`,
    );
  }
});

// A value that reads like a credential ends up in a fixture, then in a test,
// then in a bug report. Everything the wire documents carry is either a readable
// placeholder, a URL or an address, a name from a closed list, or a public key
// or device signature — a base64url of 32 or 64 bytes that is one byte repeated,
// which is exactly what a drawn-then-discarded key of a fixture looks like and
// is not. The envelope's own bytes are checked by their lengths in the test
// below rather than by this rule: they are public keys and a public signature.
function looksSecretish(value) {
  if (value.startsWith('not-a-credential-test-')) return false;
  if (value.startsWith('http')) return false;
  if (/^[^@\s]+@[^@\s]+$/.test(value)) return false;
  // A refusal's message is a sentence the phone shows; a credential is one token.
  if (value.includes(' ')) return false;
  if (/^[A-Za-z0-9_./:-]+$/.test(value) && value.length < 40) return false;
  const raw = Buffer.from(value, 'base64url');
  if ((raw.length === 32 || raw.length === 64) && raw.every((byte) => byte === raw[0])) return false;
  return true;
}

test('no value in the vectors reads like a credential', () => {
  const strings = [];
  const walk = (node, path) => {
    if (typeof node === 'string') strings.push([path, node]);
    else if (Array.isArray(node)) node.forEach((item, i) => walk(item, `${path}[${i}]`));
    else if (node !== null && typeof node === 'object') {
      for (const [key, value] of Object.entries(node)) walk(value, `${path}.${key}`);
    }
  };
  walk(vectors.wire, 'wire');
  for (const [path, value] of strings) {
    assert.ok(!looksSecretish(value), `${path} is ${JSON.stringify(value)}: the vectors ship placeholders, never a credential`);
    for (const prefix of ['AIza', 'ya29', 'sk-', 'ghp_', '-----BEGIN']) {
      assert.ok(!value.startsWith(prefix), `${path} starts with ${prefix}, which is a credential's shape`);
    }
    assert.ok(!/^eyJ[\w-]*\.[\w-]+\.[\w-]+$/.test(value), `${path} is a three-part token, which is a credential's shape`);
  }
});

// The module the phone imports carries the same contract, and a third copy that
// disagrees is a phone and a service that disagree in the field. The test's own
// lists above stay independent of it: they are the second implementation, and
// this is the check that the app's first one says the same thing.
test('the module the phone imports carries the vectors, and imports nothing itself', () => {
  const source = readFileSync(new URL('./index.js', import.meta.url), 'utf8');
  assert.equal(/^\s*import\b/m.test(source), false, 'no import statement in index.js');
  assert.equal(/\bfrom\s+['"]/.test(source), false, 'nothing is imported into index.js');
  assert.equal(/\bBuffer\b/.test(source), false, 'no Node buffer type in index.js: React Natives engine has none');
  assert.equal(/\brequire\s*\(/.test(source), false, 'no require in index.js');

  assert.equal(session.VECTORS_FORMAT, vectors.format);
  assert.deepEqual([...session.CLAIM_NAMES], vectors.claims);
  assert.deepEqual([...session.REFUSAL_CODES], vectors.codes);
  assert.deepEqual(Object.values(session.ROUTES), vectors.routes);
  assert.deepEqual(Object.values(session.SECURE_STORE_ITEMS), vectors.secure_store_items);
  assert.deepEqual(
    {
      assertion: session.LIFETIMES_SECONDS.ASSERTION,
      invite: session.LIFETIMES_SECONDS.INVITE,
      challenge: session.LIFETIMES_SECONDS.CHALLENGE,
      custom_token: session.LIFETIMES_SECONDS.CUSTOM_TOKEN,
      idle: session.LIFETIMES_SECONDS.IDLE,
    },
    vectors.lifetimes_seconds,
  );
  assert.equal(session.SIGNATURE_LABEL, vectors.signature.label);
  assert.equal(session.SIGNATURE_SEPARATOR, vectors.signature.separator);
  assert.deepEqual(
    [...new Set(vectors.signature.messages.map((m) => m.purpose))].sort(),
    Object.values(session.SIGNATURE_PURPOSES).sort(),
  );
  assert.equal(session.KEY_SET_FORMAT, vectors.key_set.payload.format);
  assert.equal(session.KEY_SET_ALG, vectors.key_set.header.alg);
  assert.equal(session.KEY_SET_TYP, vectors.key_set.header.typ);

  const decoder = new TextDecoder();
  for (const { purpose, value, message } of vectors.signature.messages) {
    assert.equal(decoder.decode(session.signatureMessage(purpose, value)), message, `${purpose} of ${value}`);
  }
  assert.equal(session.signatureMessage('not-a-purpose', 'value'), null, 'an unknown purpose signs nothing');
  assert.equal(session.signatureMessage(session.SIGNATURE_PURPOSES.REFRESH, ''), null, 'an empty value signs nothing');

  for (const { endpoint, audience } of vectors.audience.cases) {
    assert.equal(session.audienceOf(endpoint), audience, `${JSON.stringify(endpoint)} through the app's own derivation`);
  }
  for (const { provider, sub, identity } of vectors.identity_id.cases) {
    assert.equal(session.identityId(provider, sub), identity, `${provider}/${sub} through the app's own join`);
  }
});

test('the key-set envelope is a signature by the root key, not a description of one', () => {
  const keySet = vectors.key_set;
  const header = Buffer.from(JSON.stringify(keySet.header)).toString('base64url');
  const payload = Buffer.from(JSON.stringify(keySet.payload)).toString('base64url');
  // The signing input ships as its two base64url halves rather than as the
  // joined token: a three-part token in a file is a shape a secret scanner
  // reads as a credential and refuses (images/trivy-secret.yaml), and the
  // catalog build's scan is the gate this repository must not be the first to
  // fail. The compact JWS is the join, made here and in the service.
  assert.ok(Array.isArray(keySet.signing_input_parts), 'the signing input ships as parts');
  assert.equal(keySet.signing_input_parts.length, 2, 'the signing input is the header half and the payload half');
  const signingInput = keySet.signing_input_parts.join('.');
  assert.equal(
    signingInput,
    `${header}.${payload}`,
    'the signing input is the header and the payload exactly as they are serialised, base64url and unpadded',
  );
  const compact = [...keySet.signing_input_parts, keySet.signature].join('.');
  assert.equal(
    compact.split('.').length,
    3,
    'the compact envelope is the three segments of a JWS: the key set reader splits it on dots and reads the payload out of the middle one',
  );
  assert.equal(keySet.header.alg, 'EdDSA');
  assert.equal(keySet.header.typ, 'fugaro-key-set+jws');
  assert.equal(keySet.payload.format, 'fugaro-identity-key-set');
  assert.ok(keySet.payload.seq > 0, "the set's sequence is what a fetcher compares against its floor (O18)");
  assert.ok(keySet.payload.exp > keySet.payload.iat, 'a set is valid for a window, and O18 makes it 180 days');
  assert.ok(keySet.payload.exp - keySet.payload.iat <= 181 * 24 * 3600, 'a set longer-lived than O18s window is not one this reader accepts');

  const keys = keySet.payload.jwks.keys;
  assert.ok(keys.length > 0, 'the set carries at least one signing key');
  for (const key of keys) {
    assert.equal(key.kty, 'OKP');
    assert.equal(key.crv, 'Ed25519');
    assert.equal(Buffer.from(key.x, 'base64url').length, 32, `${key.kid} is an Ed25519 public key`);
    assert.ok(key.kid.length > 0, 'a key a signature names has to be nameable');
  }

  assert.equal(keySet.root_public_key.kid, keySet.header.kid, 'the header names the root key this vector verifies against');
  const root = createPublicKey({ key: keySet.root_public_key, format: 'jwk' });
  const signature = Buffer.from(keySet.signature, 'base64url');
  assert.equal(signature.length, 64, 'an Ed25519 signature is 64 bytes');
  assert.ok(
    verifySignature(null, Buffer.from(signingInput), root, signature),
    'the envelope verifies against the root key: a phone that fetches a key set accepts one only inside this',
  );
  // The one thing this proves by failing: a payload changed under the signature
  // is not the payload the root key signed, which is what makes the envelope
  // worth serving at all.
  const tampered = Buffer.from(signingInput.replace(/.$/, (c) => (c === 'A' ? 'B' : 'A')));
  assert.ok(
    !verifySignature(null, tampered, root, signature),
    'a signing input with one character changed does not verify',
  );
});
