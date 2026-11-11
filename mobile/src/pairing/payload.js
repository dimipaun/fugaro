// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The scanned QR payload, judged before anything is stored (mobile plan, Task
// P6.8; the pairing plan's §4.4, §7.2 and D199).
//
// **What the QR is.** `internal/cli/pair.go`'s `pairQR`: `v`, the project and
// its GCP project, the database URL, the installation's restricted web API
// key, the one-shot pairing token, the pairing uid and the token's expiry.
// The phone cannot sign in before it knows where to sign in, so this is a
// **bootstrap**: what it needs to be reachable during one pairing. It is not
// the account's configuration -- the delivery's copy is (D199) -- and this
// module's job is to say whether the string is one the CLI could have written,
// before a single byte of it reaches a store.
//
// **Why this is a pure function.** "A scanned payload is validated before
// anything is stored" is only a fact about the app if there is no moment in
// which a half-read payload exists. `readScannedPayload` takes the QR's text
// and returns a frozen record or a refusal: it holds no state, takes no port,
// and cannot write. The caller (`session.js`) stores exactly what it returned,
// so the ordering is structural rather than a sequence somebody has to keep.
//
// **Why the QR is hostile input.** It is a photographable string on a screen,
// and nothing about it is authenticated: anybody who can show a phone a code
// can show it this one. So every field is bounded and typed, an unknown field
// is refused rather than dropped, a field named twice is refused rather than
// resolved, and the database URL -- the one field that names a *host* the
// phone will then talk to -- is judged by `shared/url`'s validator (P0.3,
// D124) against the hosts this payload's own project could have.
//
// **Why the project decides the hosts.** `internal/infra/firebase.go`'s
// `CheckFirebaseOutputsFor` is the CLI's own rule for "this is project `fp`'s
// database": exactly `https://<fp>-default-rtdb.firebaseio.com`, or the
// regional `https://<fp>-default-rtdb.<region>.firebasedatabase.app`. The
// region is a free-form label and the QR does not carry it, so an exact
// allow-list cannot spell the second shape -- and `shared/url` implements no
// wildcards by design. The composition below is therefore two calls of the
// same validator: the first with the project's legacy host, and, only when the
// first says `host-not-allowed`, a second with the project's regional host
// derived from the candidate. That derivation is safe because of where the
// validator's checks sit: `host-not-allowed` is its **last** refusal, so by
// the time it is returned the string has passed every other check (https,
// no userinfo, no port, no percent-encoded host, no punycode, no trailing
// dot), and `new URL(...).host` is then the host a browser would dial -- there
// is no parser differential left for a hostile string to use.

// The four configuration fields are judged by `config.js`, because the
// delivery's copy of them is judged by the same rules (D199): this file owns
// the QR, `config.js` owns "is this project `fp`'s configuration at all", and
// delivery.js is the second caller. Its refusals, its field names and its code
// strings are re-exported below so a reader of this module still sees one
// vocabulary.
import { CONFIG_CODES, judgeConfig } from './config.js';
import { isPairingUID } from './uid.js';

export { API_KEY_MAX_LENGTH, databaseHostsFor, validateDatabaseUrl } from './config.js';

/** The payload version this build reads. A later format is refused rather than
 * misread, which is the same rule and the same reason as the envelope's
 * version byte (§8) and every wire format in this tree. */
export const PAYLOAD_VERSION = 1;

/** The longest QR this app will look at, in code units. The real payload is
 * under 400 characters; this is a bound rather than a preference (§4.4's
 * reasoning about a hostile string reaching a native bridge unbounded). */
export const PAYLOAD_MAX_LENGTH = 4096;

/** The longest the pairing token may be: a signed JWT, which is an RS256
 * header, a payload naming the pairing and a 256-byte signature -- about 750
 * characters for the claims this command mints (§7.1 step 3). 2048 is a bound
 * rather than a preference, and it is not a length the CLI can reach. */
export const TOKEN_MAX_LENGTH = 2048;

// The pairing uid's shape is `uid.js`'s, in one place: this file reads the
// scanned name and the envelope's AAD binds the same one, so the two must
// refuse the same strings.

/** How this module refuses. `field` names the QR's own field -- the wire's
 * spelling, `database_url` and not `databaseUrl` -- because the thing that is
 * at fault is the payload the CLI wrote, and the payload is where a reader
 * looks. */
export const PAYLOAD_CODES = Object.freeze({
  // The four configuration fields' codes are `config.js`'s, spread rather than
  // retyped: a refusal about `database_url` has one string in this app, so a
  // caller can compare a scan's refusal with a delivery's.
  ...CONFIG_CODES,
  NOT_A_STRING: 'not-a-string',
  EMPTY: 'empty',
  TOO_LONG: 'too-long',
  CONTROL_CHARACTER: 'control-character',
  BACKSLASH: 'backslash',
  NOT_JSON: 'not-json',
  DUPLICATE_FIELD: 'duplicate-field',
  BAD_VERSION: 'bad-version',
  BAD_UID: 'bad-uid',
  BAD_EXPIRY: 'bad-expiry',
});

/** The fields the payload carries, exactly. An object with any other key is
 * refused: this app reads the ones it knows, and a payload with a field it
 * does not know is a payload written by something else. */
export const PAYLOAD_FIELDS = Object.freeze([
  'v',
  'project',
  'gcp_project',
  'database_url',
  'api_key',
  'token',
  'uid',
  'expires_at',
]);

// A control character (0x00-0x1F), a space, or DEL -- `shared/url`'s own
// class, and the reason is the same: the QR carries no whitespace, and
// trimming would validate a different string from the one that was scanned.
const CONTROL_OR_SPACE = /[\u0000- \u007f]/;

// The fields this module judges on its own are printable ASCII: a uid is
// base64url, an instant is RFC3339, a token is base64url segments. Anything
// else is a look-alike -- a zero-width space in a token -- so the rule is
// ASCII-only rather than `Printable` plus a list of exceptions, which is also
// why no NFC check is needed. It is the same class `config.js` holds its three
// names to, kept here rather than imported because the fields are different.
const PRINTABLE_ASCII = /^[!-~]+$/;

// The instant `pair.go` writes: `time.RFC3339` of a UTC time, so a trailing
// `Z`, with the fractional seconds `RFC3339Nano` would add tolerated. An
// offset is not accepted: the CLI's own writer is UTC, so a payload that is
// not is not one this command wrote.
const INSTANT = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z$/;

function refuse(code, field) {
  return Object.freeze({ ok: false, code, ...(field === undefined ? {} : { field }) });
}

/**
 * Parse `text` and answer `{ ok: true, payload }` with a frozen record, or a
 * refusal.
 *
 * The record's field names are this module's (`gcpProject`), and `field` on a
 * refusal is the wire's (`gcp_project`): the first is what the rest of the app
 * reads, the second is what a person looking at the QR's own text can find.
 */
export function readScannedPayload(text) {
  if (typeof text !== 'string') return refuse(PAYLOAD_CODES.NOT_A_STRING);
  if (text.trim().length === 0) return refuse(PAYLOAD_CODES.EMPTY);
  if (text.length > PAYLOAD_MAX_LENGTH) return refuse(PAYLOAD_CODES.TOO_LONG);
  if (CONTROL_OR_SPACE.test(text)) return refuse(PAYLOAD_CODES.CONTROL_CHARACTER);
  // No escape, anywhere. This is `shared/url`'s backslash rule and it is
  // load-bearing for a different reason here: a JSON escape is a way to spell
  // a key or a value that is not the string the payload appears to carry
  // (`\u0070roject` is `project` to the parser), and the CLI's writer emits
  // none -- every field's text is ASCII with no character `json.Marshal`
  // escapes. So a backslash is a payload this command did not write, refused
  // before anything reads it -- and, with it gone, a `"` inside a value is
  // impossible too, because a JSON string cannot hold one. That is what makes
  // the "named once" check below exact rather than a heuristic.
  if (text.includes('\\')) return refuse(PAYLOAD_CODES.BACKSLASH);

  // One field, one value. `JSON.parse` lets the last of two equal keys win,
  // silently, and its reviver never sees the loser -- by the time a reviver
  // runs the object is built. So the check is on the text, where it is exact
  // (see the backslash rule above), and the needle is the **key**: `"field":`.
  // The colon is what makes it a key and not a value, and two of the rules
  // above are what make it enough -- there is no whitespace, so the colon
  // follows the quote directly, and no backslash, so no value can hold a `"`.
  // A value is therefore never followed by a colon, and a field's name inside
  // a longer key (`"gcp_project"` holds `project`, not `"project"`) does not
  // carry the quote. Counting the bare name instead would refuse the QR of an
  // installation whose own name is one of these words, which is a name
  // `ProjectNameRE` allows and the owner may have chosen.
  for (const field of PAYLOAD_FIELDS) {
    if (occurrences(text, `"${field}":`) > 1) return refuse(PAYLOAD_CODES.DUPLICATE_FIELD, field);
  }

  const parsed = parseOnce(text);
  if (!parsed.ok) return parsed;
  const raw = parsed.value;
  if (raw === null || Array.isArray(raw) || typeof raw !== 'object') return refuse(PAYLOAD_CODES.NOT_AN_OBJECT);

  // The field set first, then the fields: a payload carrying something this
  // build does not know is refused whole, and never partially read.
  for (const key of Object.keys(raw)) {
    if (!PAYLOAD_FIELDS.includes(key)) return refuse(PAYLOAD_CODES.UNKNOWN_FIELD, key);
  }
  for (const field of PAYLOAD_FIELDS) {
    if (!Object.prototype.hasOwnProperty.call(raw, field)) return refuse(PAYLOAD_CODES.MISSING_FIELD, field);
  }

  // Every field is read exactly once, into locals, before any of them is
  // judged: a getter cannot answer one value to a check and another to the
  // record the caller keeps. (`parseOnce` has already refused a class
  // instance, so these are own data properties of a plain object.)
  const v = raw.v;
  const project = raw.project;
  const gcpProject = raw.gcp_project;
  const databaseUrl = raw.database_url;
  const apiKey = raw.api_key;
  const token = raw.token;
  const uid = raw.uid;
  const expiresAt = raw.expires_at;

  if (typeof v !== 'number') return refuse(PAYLOAD_CODES.BAD_TYPE, 'v');
  if (v !== PAYLOAD_VERSION) return refuse(PAYLOAD_CODES.BAD_VERSION, 'v');
  // Nothing here checks the `AIza…` shape of a web key: `settings.js` is what
  // refuses to store one, the CLI mints it, and a fixture carrying a
  // real-looking key is a secret-shaped literal this tree does not write.
  if (typeof token !== 'string') return refuse(PAYLOAD_CODES.BAD_TYPE, 'token');
  if (token.length > TOKEN_MAX_LENGTH) return refuse(PAYLOAD_CODES.BAD_TEXT, 'token');
  if (!PRINTABLE_ASCII.test(token)) return refuse(PAYLOAD_CODES.BAD_TEXT, 'token');
  if (typeof uid !== 'string') return refuse(PAYLOAD_CODES.BAD_TYPE, 'uid');
  if (!isPairingUID(uid)) return refuse(PAYLOAD_CODES.BAD_UID, 'uid');
  if (typeof expiresAt !== 'string') return refuse(PAYLOAD_CODES.BAD_TYPE, 'expires_at');
  if (!INSTANT.test(expiresAt)) return refuse(PAYLOAD_CODES.BAD_EXPIRY, 'expires_at');
  const expiresAtMs = Date.parse(expiresAt);
  if (!Number.isFinite(expiresAtMs)) return refuse(PAYLOAD_CODES.BAD_EXPIRY, 'expires_at');
  // The four configuration fields, in the CLI's own rule (`config.js`), with
  // this carrier's field names in a refusal: the QR's fields are flat and are
  // named as the QR names them.
  const judged = judgeConfig({ project, gcpProject, databaseUrl, apiKey }, '');
  if (!judged.ok) return judged;
  // **The expiry is read here and judged in `session.js`.** This file's job is
  // the shape of the QR, and the instant is parsed -- a scan is refused for
  // `expires_at` being a number, not being an RFC3339 instant, or not parsing
  // -- and handed on. What the deadline *means* is the session's, and it is not
  // a judgement about the token: `expires_at` is the pairing token's own `exp`
  // (`token.PairingLifetime` mints it as the CLI's wait plus `PairingSlack`),
  // so the session ends the wait on the phone's clock at the instant the token
  // itself dies, and the exchange stays the authority on the token's real life
  // -- it fails loudly and at once if the token has expired.
  //
  // The phone's clock is therefore the one that could be wrong, and the slack
  // is what absorbs it: a clock running fast by less than `PairingSlack` (30 s)
  // gives up that much before the token dies and still after the CLI's own wait
  // has ended, so it refuses nothing the CLI would have completed. A clock
  // wrong by more than the slack loses the pairing, and the repair is §7.2's
  // for any pairing that did not complete: start a new one.

  return Object.freeze({
    ok: true,
    payload: Object.freeze({
      version: v,
      ...judged.config,
      token,
      uid,
      expiresAt: expiresAtMs,
    }),
  });
}
/** How many times `needle` occurs in `text`, counted without a regular
 * expression: the needles here contain no regex metacharacter that a caller
 * could get wrong, and `indexOf` cannot be made to backtrack. */
function occurrences(text, needle) {
  let count = 0;
  let at = text.indexOf(needle);
  while (at !== -1) {
    count += 1;
    at = text.indexOf(needle, at + needle.length);
  }
  return count;
}

/**
 * `JSON.parse`, and nothing else: every hazard a hostile string could use is
 * refused before this is reached (the text's own shape, the escapes, the
 * duplicate keys), so what is left is a plain parse. `__proto__` is accepted
 * here as the own property it is and refused a line later by the field-set
 * check, which is the same rule as any other name this build does not know --
 * nothing in this module writes through a key.
 */
function parseOnce(text) {
  let value;
  try {
    value = JSON.parse(text);
  } catch {
    return refuse(PAYLOAD_CODES.NOT_JSON);
  }
  return Object.freeze({ ok: true, value });
}
