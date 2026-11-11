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

import { URL_REFUSAL, validateExternalUrl } from '../../../shared/url/index.js';

import { decodeBase64url } from './base64url.js';

/** The payload version this build reads. A later format is refused rather than
 * misread, which is the same rule and the same reason as the envelope's
 * version byte (§8) and every wire format in this tree. */
export const PAYLOAD_VERSION = 1;

/** The longest QR this app will look at, in code units. The real payload is
 * under 400 characters; this is a bound rather than a preference (§4.4's
 * reasoning about a hostile string reaching a native bridge unbounded). */
export const PAYLOAD_MAX_LENGTH = 4096;

/** The longest the web API key may be. A Firebase web API key is 39
 * characters; 256 leaves room without letting the field be a container. */
export const API_KEY_MAX_LENGTH = 256;

/** The longest the pairing token may be: a signed JWT, which is an RS256
 * header, a payload naming the pairing and a 256-byte signature -- about 750
 * characters for the claims this command mints (§7.1 step 3). 2048 is a bound
 * rather than a preference, and it is not a length the CLI can reach. */
export const TOKEN_MAX_LENGTH = 2048;

/** The random part of a pairing uid: 32 bytes, `pairing.go`'s own draw. */
export const PAIRING_UID_BYTES = 32;

/** The prefix a pairing session's name carries (`token.PairingPrefix`). */
export const PAIRING_UID_PREFIX = 'p~';

/** How this module refuses. `field` names the QR's own field -- the wire's
 * spelling, `database_url` and not `databaseUrl` -- because the thing that is
 * at fault is the payload the CLI wrote, and the payload is where a reader
 * looks. */
export const PAYLOAD_CODES = Object.freeze({
  NOT_A_STRING: 'not-a-string',
  EMPTY: 'empty',
  TOO_LONG: 'too-long',
  CONTROL_CHARACTER: 'control-character',
  BACKSLASH: 'backslash',
  NOT_JSON: 'not-json',
  NOT_AN_OBJECT: 'not-an-object',
  UNKNOWN_FIELD: 'unknown-field',
  MISSING_FIELD: 'missing-field',
  DUPLICATE_FIELD: 'duplicate-field',
  BAD_TYPE: 'bad-type',
  BAD_VERSION: 'bad-version',
  BAD_TEXT: 'bad-text',
  BAD_UID: 'bad-uid',
  BAD_EXPIRY: 'bad-expiry',
  BAD_DATABASE_URL: 'bad-database-url',
});

/** The fields the payload carries, exactly. An object with any other key is
 * refused: this app reads the ones it knows, and a payload with a field it
 * does not know is a payload written by something else. */
const PAYLOAD_FIELDS = Object.freeze([
  'v',
  'project',
  'gcp_project',
  'database_url',
  'api_key',
  'token',
  'uid',
  'expires_at',
]);

// The Fugaro project's name: `internal/config`'s `ProjectNameRE`, the rule the
// CLI holds the name to when it writes a project, so a name the phone accepts
// is a name the CLI could have written.
const PROJECT_NAME = /^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$/;

// The GCP project id: `internal/infra`'s `fpIDRE`, for `ValidProjectID`. It
// matters more here than it looks: this name is what the database's host is
// derived from, and a name that could carry `/`, `.` or `@` could name a host
// this payload's own project does not own.
const GCP_PROJECT = /^[a-z][a-z0-9-]{4,28}[a-z0-9]$/;

// A control character (0x00-0x1F), a space, or DEL -- `shared/url`'s own
// class, and the reason is the same: the QR carries no whitespace, and
// trimming would validate a different string from the one that was scanned.
const CONTROL_OR_SPACE = /[\u0000- \u007f]/;

// Every field the CLI writes is printable ASCII: a project id is letters,
// digits and hyphens, a uid and a key are base64url, an instant is RFC3339.
// Anything else is a look-alike -- a Cyrillic `а` in a project name, a
// zero-width space in a token -- so the rule is ASCII-only rather than
// `Printable` plus a list of exceptions. It is also why no NFC check is
// needed here: a non-ASCII string is refused outright.
const PRINTABLE_ASCII = /^[!-~]+$/;

// The instant `pair.go` writes: `time.RFC3339` of a UTC time, so a trailing
// `Z`, with the fractional seconds `RFC3339Nano` would add tolerated. An
// offset is not accepted: the CLI's own writer is UTC, so a payload that is
// not is not one this command wrote.
const INSTANT = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z$/;

// `internal/infra/firebase.go`'s regional shape, for this payload's project.
// `-default-rtdb` is the database's name, `.firebasedatabase.app` the
// regional domain, and everything between them is the region.
const REGIONAL_HOST = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?-default-rtdb\.[a-z0-9-]+\.firebasedatabase\.app$/;

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
  // (see the backslash rule above): a field's token can only occur as its key,
  // so a second occurrence is a second spelling of one field and the payload
  // is refused whole rather than resolved.
  for (const field of PAYLOAD_FIELDS) {
    if (occurrences(text, `"${field}"`) > 1) return refuse(PAYLOAD_CODES.DUPLICATE_FIELD, field);
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
  // Each text field's own rule, in one table rather than three loops: the two
  // names are shapes the CLI mints, and the two secrets-shaped fields are
  // bounded printable ASCII. Nothing here checks the `AIza…` shape of a web
  // key: `settings.js` is what refuses to store one, the CLI mints it, and a
  // fixture carrying a real-looking key is a secret-shaped literal this tree
  // does not write.
  const texts = [
    ['project', project, PROJECT_NAME, null],
    ['gcp_project', gcpProject, GCP_PROJECT, null],
    ['api_key', apiKey, PRINTABLE_ASCII, API_KEY_MAX_LENGTH],
    ['token', token, PRINTABLE_ASCII, TOKEN_MAX_LENGTH],
  ];
  for (const [field, value, shape, max] of texts) {
    if (typeof value !== 'string') return refuse(PAYLOAD_CODES.BAD_TYPE, field);
    if (max !== null && value.length > max) return refuse(PAYLOAD_CODES.BAD_TEXT, field);
    if (!shape.test(value)) return refuse(PAYLOAD_CODES.BAD_TEXT, field);
  }
  if (typeof uid !== 'string') return refuse(PAYLOAD_CODES.BAD_TYPE, 'uid');
  if (!isPairingUID(uid)) return refuse(PAYLOAD_CODES.BAD_UID, 'uid');
  if (typeof expiresAt !== 'string') return refuse(PAYLOAD_CODES.BAD_TYPE, 'expires_at');
  if (!INSTANT.test(expiresAt)) return refuse(PAYLOAD_CODES.BAD_EXPIRY, 'expires_at');
  const expiresAtMs = Date.parse(expiresAt);
  if (!Number.isFinite(expiresAtMs)) return refuse(PAYLOAD_CODES.BAD_EXPIRY, 'expires_at');
  // **The expiry is read, not enforced.** This is not the authority on the
  // pairing token's life: the token exchange is, and it fails loudly and at
  // once if the token really has expired. A phone whose clock runs fast would
  // otherwise refuse a pairing the operator's CLI would have completed, and
  // §7.2's own rule for a late delivery is "say the pairing expired", not
  // "refuse the scan".
  const url = validateDatabaseUrl(databaseUrl, gcpProject);
  if (!url.ok) return refuse(PAYLOAD_CODES.BAD_DATABASE_URL, 'database_url');

  return Object.freeze({
    ok: true,
    payload: Object.freeze({
      version: v,
      project,
      gcpProject,
      databaseUrl: url.url,
      databaseHost: url.host,
      apiKey,
      token,
      uid,
      expiresAt: expiresAtMs,
    }),
  });
}

/**
 * Is `url` the database of project `gcpProject`, in one of the two spellings
 * the CLI accepts? The same call judges the scan's copy and the delivery's
 * (D199: the delivery's copy is the record, and it is held to the same rule,
 * so a delivery cannot name a database the scan would have refused).
 *
 * It answers the validator's `{ ok: true, url, host }` -- the **candidate
 * verbatim**, never a re-serialisation, so the string that was checked is the
 * string the app hands to the database client -- or a refusal carrying the
 * validator's own code, which is what makes this module's refusals
 * diagnosable without a second vocabulary.
 */
export function validateDatabaseUrl(url, gcpProject) {
  if (typeof url !== 'string' || typeof gcpProject !== 'string') {
    return Object.freeze({ ok: false, code: URL_REFUSAL.NOT_A_STRING });
  }
  const legacy = `${gcpProject.toLowerCase()}-default-rtdb.firebaseio.com`;
  const first = validateExternalUrl(url, [legacy]);
  if (first.ok) return bareAuthority(first);
  if (first.code !== URL_REFUSAL.HOST_NOT_ALLOWED) return first;
  // Every other check has passed (`host-not-allowed` is the validator's last),
  // so the host `new URL` resolves here is the host it would have matched.
  const host = hostOf(url);
  if (host === null || !REGIONAL_HOST.test(host)) return first;
  // The regional spelling is this project's only if its database label is the
  // project's own: `<project>-default-rtdb`. One character changed is another
  // database, however well formed it looks.
  if (!host.startsWith(`${gcpProject.toLowerCase()}-default-rtdb.`)) return first;
  return bareAuthority(validateExternalUrl(url, [host]));
}

/**
 * The validator answers "may this string be opened", which a link may with a
 * path, a query or a fragment. A Realtime Database URL may not: it is the
 * database's address and nothing else, which is `internal/rtdb`'s
 * `ValidateURL` rule ("must be just the database's address, with no
 * credentials, path, query or fragment") and what stops a payload from naming
 * a path the phone would then read as part of the database.
 *
 * The check is a length because the validator has already done the work: it
 * guarantees the string spells `https://`, that the authority carries no
 * userinfo, port, `%` or trailing dot, and it hands back the candidate
 * **verbatim** with its lower-cased host. So "the string is exactly
 * `https://` and the host" is exactly "there is nothing after the authority".
 * It is stricter than the Go rule in one place, deliberately: a bare trailing
 * `/` is refused here and accepted there, and the CLI writes neither.
 */
function bareAuthority(judged) {
  if (!judged.ok) return judged;
  if (judged.url.length !== 'https://'.length + judged.host.length) {
    return Object.freeze({ ok: false, code: URL_REFUSAL.NOT_A_URL });
  }
  return judged;
}

/**
 * The hosts this payload's project may name, for a caller that wants to say
 * why rather than only that it refused: the legacy spelling, and the regional
 * one when `url` is one. It is the same derivation `validateDatabaseUrl`
 * makes, exported so the app's diagnostics and its validation cannot drift.
 */
export function databaseHostsFor(url, gcpProject) {
  const hosts = [`${String(gcpProject).toLowerCase()}-default-rtdb.firebaseio.com`];
  const host = hostOf(url);
  if (host !== null && REGIONAL_HOST.test(host)) hosts.push(host);
  return Object.freeze(hosts);
}

/** The host of a string that has already passed `shared/url`'s shape checks,
 * or `null` when it does not parse at all. */
function hostOf(url) {
  try {
    const parsed = new URL(url);
    return parsed.host === '' ? null : parsed.host.toLowerCase();
  } catch {
    return null;
  }
}

/** A pairing uid: `p~` and 32 bytes of unpadded base64url, strictly decoded --
 * the one spelling §2 (e) pins, and the spelling the delivery's AAD binds. */
export function isPairingUID(uid) {
  if (typeof uid !== 'string' || !uid.startsWith(PAIRING_UID_PREFIX)) return false;
  return decodeBase64url(uid.slice(PAIRING_UID_PREFIX.length), PAIRING_UID_BYTES).ok;
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
