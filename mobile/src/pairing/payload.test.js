// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The scanned payload, judged before anything is stored (mobile plan, Task
// P6.8; the pairing contract is docs/plans/2026-10-22-mobile-pairing-cli.md
// §7.2, and D199 and D124 decide the two halves of it).
//
// **The red step.** This file was written before `payload.js` existed and
// failed with ERR_MODULE_NOT_FOUND, which is the only failure a red step can
// report about a module that is not there yet. Everything below is the plan's
// first verification sentence -- "a scanned payload is validated before
// anything is stored" -- and the reason it can be *structural* rather than
// promised: `readScannedPayload` is a pure function of the QR's text. It takes
// no port, holds no state and returns either a frozen payload or a refusal, so
// there is no moment in which a half-read payload exists to be stored. The
// caller (`session.js`) stores what this function returned, and nothing else.
//
// **Why the database URL is judged against the project.** The QR is a
// photographable string, so its `database_url` is hostile input (D124's
// reasoning, and `shared/url`'s whole header). What makes a host *this*
// project's database is `internal/infra/firebase.go`'s `CheckFirebaseOutputsFor`
// rule -- `https://<fp>-default-rtdb.firebaseio.com` or
// `https://<fp>-default-rtdb.<region>.firebasedatabase.app` -- which no exact
// allow-list can spell (the region is a free-form label and is not in the
// payload). So the project's hosts are derived here and the string is judged
// by `shared/url`'s one validator (P0.3), which is the module D124 says must
// not be written twice.

import assert from 'node:assert/strict';
import test from 'node:test';

import { PAYLOAD_CODES, PAYLOAD_VERSION, PAYLOAD_MAX_LENGTH, databaseHostsFor, readScannedPayload } from './payload.js';

/** A pairing uid in the one canonical spelling: `p~`, then 32 bytes of
 * unpadded base64url whose last character carries the two bits this encoding
 * discards, which for `A` (value 0) they are. */
const PAIRING_UID = 'p~' + 'A'.repeat(43);

/** A payload the CLI really emits, with a fake token and a fake web key: the
 * shape is `internal/cli/pair.go`'s `pairQR` and the values are this file's. */
function scanned(over = {}) {
  return JSON.stringify({
    v: PAYLOAD_VERSION,
    project: 'aurora',
    gcp_project: 'fp-1234',
    database_url: 'https://fp-1234-default-rtdb.firebaseio.com',
    api_key: 'test-web-api-key',
    token: 'test-pairing-token-not-a-credential',
    uid: PAIRING_UID,
    expires_at: '2026-11-11T17:50:00Z',
    ...over,
  });
}

/** The refusal code for a text, or a failure if it was accepted. */
function codeOf(text) {
  const result = readScannedPayload(text);
  assert.equal(result.ok, false, `expected a refusal for ${text}`);
  return result.code;
}

test('a scanned payload is read, and every field the contract names is there', () => {
  const result = readScannedPayload(scanned());
  assert.equal(result.ok, true, JSON.stringify(result));
  assert.deepEqual(
    Object.keys(result.payload).sort(),
    ['apiKey', 'databaseHost', 'databaseUrl', 'expiresAt', 'gcpProject', 'project', 'token', 'uid', 'version'],
  );
  assert.equal(result.payload.project, 'aurora');
  assert.equal(result.payload.gcpProject, 'fp-1234');
  assert.equal(result.payload.databaseUrl, 'https://fp-1234-default-rtdb.firebaseio.com');
  assert.equal(result.payload.uid, PAIRING_UID);
  // The record is frozen: the caller stores it, and a caller that could edit
  // it afterwards could store something this function never judged.
  assert.equal(Object.isFrozen(result.payload), true);
  assert.equal(Object.isFrozen(result), true);
});

test('a refusal carries no payload, and never the text it refused', () => {
  const hostile = scanned({ database_url: 'https://evil.example/db' });
  const result = readScannedPayload(hostile);
  assert.equal(result.ok, false);
  assert.equal('payload' in result, false, 'a refusal must not carry a half-read payload');
  // D124's note and `shared/url`'s: the text arrived from a photograph, so no
  // refusal echoes it.
  assert.equal(JSON.stringify(result).includes('evil.example'), false);
});

test('the text itself is bounded, and it is never coerced', () => {
  assert.equal(codeOf(undefined), PAYLOAD_CODES.NOT_A_STRING);
  assert.equal(codeOf(null), PAYLOAD_CODES.NOT_A_STRING);
  assert.equal(codeOf(42), PAYLOAD_CODES.NOT_A_STRING);
  assert.equal(codeOf({}), PAYLOAD_CODES.NOT_A_STRING);
  assert.equal(codeOf(''), PAYLOAD_CODES.EMPTY);
  assert.equal(codeOf(' '.repeat(10)), PAYLOAD_CODES.EMPTY);
  assert.equal(codeOf('x'.repeat(PAYLOAD_MAX_LENGTH + 1)), PAYLOAD_CODES.TOO_LONG);
  // A control character is refused rather than trimmed: a QR carries no
  // whitespace, and trimming would validate a different string from the one
  // that was scanned.
  assert.equal(codeOf(scanned() + '\n'), PAYLOAD_CODES.CONTROL_CHARACTER);
  assert.equal(codeOf('\t' + scanned()), PAYLOAD_CODES.CONTROL_CHARACTER);
  // An escape is a way to spell a key or a value that is not the string it
  // looks like (`\u0070roject` is `project` to the parser), and the CLI's
  // writer emits none: every field's text is ASCII with nothing
  // `json.Marshal` escapes. So a backslash is refused before anything reads
  // the payload, and it is what makes the "one field, one value" check below
  // exact rather than a guess.
  assert.equal(codeOf(scanned({ project: 'aurora' }).replace('"aurora"', '"aur\\u006fra"')), PAYLOAD_CODES.BACKSLASH);
});

test('the JSON is an object with exactly the contract’s fields, once each', () => {
  assert.equal(codeOf('nope'), PAYLOAD_CODES.NOT_JSON);
  assert.equal(codeOf('[1,2,3]'), PAYLOAD_CODES.NOT_AN_OBJECT);
  assert.equal(codeOf('"astring"'), PAYLOAD_CODES.NOT_AN_OBJECT);
  assert.equal(codeOf('null'), PAYLOAD_CODES.NOT_AN_OBJECT);
  // A field the contract does not name is refused rather than ignored: the
  // reader would otherwise store a payload whose other half it never read.
  assert.equal(codeOf(scanned({ extra: 'x' })), PAYLOAD_CODES.UNKNOWN_FIELD);
  // The refusal names the field so a reader can find it, and that name is the
  // payload's own text: it is echoed only when it is a name the CLI could have
  // written. A key carrying a right-to-left override is legal inside a JSON
  // string and legal in this text -- neither `CONTROL_OR_SPACE` nor the length
  // bound refuses it -- so without the rule the app's own refusal would hold a
  // stranger's string.
  const hostile = readScannedPayload(scanned().replace('}', ',"\u202egnp.txt\u2028":1}'));
  assert.equal(hostile.code, PAYLOAD_CODES.UNKNOWN_FIELD);
  assert.equal('field' in hostile, false, 'a name that is not one is not echoed');
  assert.equal(JSON.stringify(hostile).includes('gnp.txt'), false);
  assert.equal(readScannedPayload(scanned({ extra: 'x' })).field, 'extra', 'a name the CLI could write is named');
  for (const field of ['project', 'gcp_project', 'database_url', 'api_key', 'token', 'uid', 'expires_at', 'v']) {
    const payload = JSON.parse(scanned());
    delete payload[field];
    assert.equal(codeOf(JSON.stringify(payload)), PAYLOAD_CODES.MISSING_FIELD, `${field} is required`);
  }
  // One field, one value. `JSON.parse` lets the last of two equal keys win --
  // silently, and differently from every strict reader of the same bytes (the
  // CLI decodes this shape with `encoding/json`, whose answer here is also
  // "last wins" but whose *field set* the payload's own writer never
  // produces) -- so a payload that names a field twice is refused outright.
  assert.equal(
    codeOf('{"v":1,"v":1,"project":"aurora","gcp_project":"fp-1234","database_url":"https://fp-1234-default-rtdb.firebaseio.com","api_key":"k","token":"t","uid":"' + PAIRING_UID + '","expires_at":"2026-11-11T17:50:00Z"}'),
    PAYLOAD_CODES.DUPLICATE_FIELD,
  );
  assert.equal(codeOf(scanned({ v: '1' })), PAYLOAD_CODES.BAD_TYPE);
  assert.equal(codeOf(scanned({ project: 7 })), PAYLOAD_CODES.BAD_TYPE);
  assert.equal(codeOf(scanned({ expires_at: 0 })), PAYLOAD_CODES.BAD_TYPE);
});

test('a project named after one of the payload’s own fields is not a duplicate', () => {
  // The "one field, one value" needle is the key -- `"field":` -- and not the
  // bare name, because a *value* can be one: `ProjectNameRE` accepts `v`,
  // `token`, `uid`, `project`, `config` and `device`, and the GCP project id's
  // rule takes the six-character ones. The name is the owner's (`internal/config` is the
  // rule, D114's list is theirs), so an installation really can be called
  // `token` -- and a check that counted values would refuse its QR for ever,
  // with no way on the phone to pair it. (The same walk is on the delivery's
  // text, `delivery.test.js`, for the same reason.)
  for (const name of ['v', 'token', 'uid', 'project', 'config', 'device']) {
    const result = readScannedPayload(scanned({ project: name }));
    assert.equal(result.ok, true, `project: "${name}" was refused: ${JSON.stringify(result)}`);
    assert.equal(result.payload.project, name);
  }
  for (const name of ['config', 'device', 'project']) {
    const result = readScannedPayload(
      scanned({ gcp_project: name, database_url: `https://${name}-default-rtdb.firebaseio.com` }),
    );
    assert.equal(result.ok, true, `gcp_project: "${name}" was refused: ${JSON.stringify(result)}`);
  }
  // ... and the rule still bites: the same field as a key twice is refused, and
  // its name being this payload's project does not hide that.
  assert.equal(
    codeOf(scanned({ project: 'token' }).replace('"token":', '"token":"x","token":')),
    PAYLOAD_CODES.DUPLICATE_FIELD,
  );
});

test('the version is this build’s, and nothing else is guessed at', () => {
  const result = readScannedPayload(scanned());
  assert.equal(result.ok, true);
  assert.equal(result.payload.version, PAYLOAD_VERSION);
  assert.equal(codeOf(scanned({ v: 2 })), PAYLOAD_CODES.BAD_VERSION);
  assert.equal(codeOf(scanned({ v: 0 })), PAYLOAD_CODES.BAD_VERSION);
  assert.equal(codeOf(scanned({ v: 1.5 })), PAYLOAD_CODES.BAD_VERSION);
});

test('the uid is a pairing uid, in the one spelling the AAD binds', () => {
  assert.equal(codeOf(scanned({ uid: 'not-a-pairing-uid' })), PAYLOAD_CODES.BAD_UID);
  assert.equal(codeOf(scanned({ uid: 'p~' + 'A'.repeat(42) })), PAYLOAD_CODES.BAD_UID, 'too short');
  assert.equal(codeOf(scanned({ uid: 'p~' + 'A'.repeat(44) })), PAYLOAD_CODES.BAD_UID, 'too long');
  assert.equal(codeOf(scanned({ uid: '' })), PAYLOAD_CODES.BAD_UID);
  // A device uid is a different family: a delivery sealed for a pairing and
  // addressed by a device's name would bind an AAD nothing reproduces.
  assert.equal(codeOf(scanned({ uid: 'm~' + 'A'.repeat(43) })), PAYLOAD_CODES.BAD_UID);
  // Canonical or nothing: 32 bytes is 43 characters whose last one carries two
  // discarded bits, so three other last characters decode to the same 32 bytes
  // and each is a second name for one pairing (§2 (e), `DecodeDeviceKey`).
  assert.equal(codeOf(scanned({ uid: 'p~' + 'A'.repeat(42) + 'B' })), PAYLOAD_CODES.BAD_UID, 'non-canonical');
  assert.equal(codeOf(scanned({ uid: PAIRING_UID + '=' })), PAYLOAD_CODES.BAD_UID, 'padded');
});

test('the expiry is an instant, and a payload whose instant has gone is still read', () => {
  const result = readScannedPayload(scanned({ expires_at: '2026-11-11T17:50:00Z' }));
  assert.equal(result.ok, true);
  assert.equal(result.payload.expiresAt, Date.parse('2026-11-11T17:50:00Z'));
  assert.equal(codeOf(scanned({ expires_at: 'yesterday' })), PAYLOAD_CODES.BAD_EXPIRY);
  assert.equal(codeOf(scanned({ expires_at: '' })), PAYLOAD_CODES.BAD_EXPIRY);
  // A payload whose instant has gone is *read* and not refused: this file
  // judges the shape of the QR and hands the instant on, and what the deadline
  // means is `session.js`'s (`start` ends a scan past it, `tick` ends a watch
  // past it) -- on a clock that is the phone's, which is why the QR's instant
  // is the pairing token's own `exp` and not the CLI's wait.
  const past = readScannedPayload(scanned({ expires_at: '2000-01-01T00:00:00Z' }));
  assert.equal(past.ok, true);
  assert.equal(past.payload.expiresAt, Date.parse('2000-01-01T00:00:00Z'));
});

test('the database URL is judged against the project, by shared/url’s validator', () => {
  // The two spellings `internal/infra` accepts, for this payload's project.
  assert.equal(readScannedPayload(scanned()).ok, true);
  assert.equal(
    readScannedPayload(scanned({ database_url: 'https://fp-1234-default-rtdb.europe-west1.firebasedatabase.app' })).ok,
    true,
    'the regional spelling is the same database',
  );
  // Another project's database is another installation: the phone would read
  // and write a tree that is not the one the QR named.
  assert.equal(codeOf(scanned({ database_url: 'https://fp-9999-default-rtdb.firebaseio.com' })), PAYLOAD_CODES.BAD_DATABASE_URL);
  // The suffix trick the validator's own header names: `endsWith` would take
  // `…firebaseio.com.evil.example` for this project's database.
  assert.equal(
    codeOf(scanned({ database_url: 'https://fp-1234-default-rtdb.firebaseio.com.evil.example' })),
    PAYLOAD_CODES.BAD_DATABASE_URL,
  );
  assert.equal(codeOf(scanned({ database_url: 'https://evil.example/' })), PAYLOAD_CODES.BAD_DATABASE_URL);
  assert.equal(codeOf(scanned({ database_url: 'https://fp-1234-default-rtdb.firebaseio.com.' })), PAYLOAD_CODES.BAD_DATABASE_URL);
  assert.equal(codeOf(scanned({ database_url: 'http://fp-1234-default-rtdb.firebaseio.com' })), PAYLOAD_CODES.BAD_DATABASE_URL);
  assert.equal(codeOf(scanned({ database_url: 'https://fp-1234-default-rtdb.firebaseio.com:8443' })), PAYLOAD_CODES.BAD_DATABASE_URL);
  assert.equal(codeOf(scanned({ database_url: 'https://u@fp-1234-default-rtdb.firebaseio.com' })), PAYLOAD_CODES.BAD_DATABASE_URL);
  assert.equal(codeOf(scanned({ database_url: 'https://fp-1234-default-rtdb.firebaseio.com/db' })), PAYLOAD_CODES.BAD_DATABASE_URL);
  // The regional shape is the project's own, label for label: one character
  // changed in the database's name is another database.
  assert.equal(
    codeOf(scanned({ database_url: 'https://fp-12345-default-rtdb.europe-west1.firebasedatabase.app' })),
    PAYLOAD_CODES.BAD_DATABASE_URL,
  );
  assert.equal(
    codeOf(scanned({ database_url: 'https://fp-1234-default-rtdb.europe-west1.firebasedatabase.app.evil.example' })),
    PAYLOAD_CODES.BAD_DATABASE_URL,
  );
  // ... and the refusal names the URL as the field at fault, so a screen can
  // point at it.
  const refused = readScannedPayload(scanned({ database_url: 'https://evil.example/' }));
  assert.equal(refused.field, 'database_url');
  // The hosts a refusal would name are the same derivation the validator made,
  // which is what `config.js`'s `databaseHostsFor` promises: this project's
  // legacy spelling, plus the regional one **when it is this project's**. The
  // second call is the one that would drift -- a regional host whose database
  // label is another project's is another installation's database, and the
  // validator refuses it a few lines above.
  assert.deepEqual(databaseHostsFor('https://fp-1234-default-rtdb.europe-west1.firebasedatabase.app', 'fp-1234'), [
    'fp-1234-default-rtdb.firebaseio.com',
    'fp-1234-default-rtdb.europe-west1.firebasedatabase.app',
  ]);
  assert.deepEqual(databaseHostsFor('https://fp-9999-default-rtdb.europe-west1.firebasedatabase.app', 'fp-1234'), [
    'fp-1234-default-rtdb.firebaseio.com',
  ]);
});

test('the project’s own name is bounded text, and it is what the host is derived from', () => {
  assert.equal(codeOf(scanned({ project: '' })), PAYLOAD_CODES.BAD_TEXT);
  assert.equal(codeOf(scanned({ gcp_project: '' })), PAYLOAD_CODES.BAD_TEXT);
  assert.equal(codeOf(scanned({ project: 'aurora x' })), PAYLOAD_CODES.CONTROL_CHARACTER, 'whitespace is refused outright');
  assert.equal(codeOf(scanned({ project: 'Aurora' })), PAYLOAD_CODES.BAD_TEXT, 'a name the CLI would not write');
  assert.equal(codeOf(scanned({ project: 'aurora.example' })), PAYLOAD_CODES.BAD_TEXT, 'a dot is not in a project name');
  // A project name that is not a project name cannot name a database, so a
  // payload whose URL matches its own nonsense project is still refused: the
  // host is derived from the name and `<x>/../` is not a host.
  assert.equal(
    codeOf(scanned({ gcp_project: 'fp-1234/../evil', database_url: 'https://fp-1234/../evil-default-rtdb.firebaseio.com' })),
    PAYLOAD_CODES.BAD_TEXT,
  );
});

test('the token and the web key are bounded text and are never coerced', () => {
  assert.equal(codeOf(scanned({ token: '' })), PAYLOAD_CODES.BAD_TEXT);
  assert.equal(codeOf(scanned({ api_key: '' })), PAYLOAD_CODES.BAD_TEXT);
  assert.equal(codeOf(scanned({ token: 'a b' })), PAYLOAD_CODES.CONTROL_CHARACTER);
  assert.equal(codeOf(scanned({ token: 'tøken' })), PAYLOAD_CODES.BAD_TEXT, 'printable ASCII or nothing');
  assert.equal(codeOf(scanned({ api_key: 'x'.repeat(500) })), PAYLOAD_CODES.BAD_TEXT);
  // The web key's `AIza…` shape is not checked here, and deliberately: the CLI
  // mints it, `settings.js` is what refuses to *store* one, and a fixture
  // carrying a real-looking key is a secret-shaped literal this tree does not
  // write. What the phone refuses is a value that is not text at all.
  assert.equal(readScannedPayload(scanned({ api_key: 'test-web-api-key' })).ok, true);
});
