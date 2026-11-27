// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The paired list, on the phone and in the identity project (task K3; D2796,
// D2785, F15, O16).
//
// **What the list is.** One entry per project the person has paired: the
// project's opaque id, the endpoint the phone calls it on, which of the
// person's signed-in identities paired it, and the fingerprint of the project's
// consumer public key (O16). Those four are C1's `PairedEntry`, which is also
// what K1's `/paired-list` stores, so the phone's entry and the identity
// project's are the same four fields. A readable name is the fifth field of the
// phone's own entry and never of the wire's: the spec's "readable names stay on
// the device" (D2796) is held by what a write sends, and `sync.test.js` asserts
// that no request body carries one.
//
// **Two copies, and the project's sync switch decides whether there are two.**
// The phone always keeps its own list, in one platform-keychain item per
// signed-in identity. The identity project keeps a sealed copy only while the
// project's *sync* switch is on (D2796). With the switch off, pairing sends
// nothing at all -- this task's red step -- and the phone's item is written at
// the platform's backing-up keychain level instead, which is the one thing that
// lets a new device of the same person find the list again ("When sync is off",
// spec). With sync on the item is written device-only, because the copy that
// has to outlive the phone is the identity project's and a second one on the
// platform's backup would be the list outside both Fugaro's reach and the
// person's device. M0's U10 and Check 733 read the SDK for the platform half:
// iOS `WHEN_UNLOCKED` is the closest `expo-secure-store` gets to an item that
// follows an iCloud backup, and Android has no synced keychain
// (`src/accounts/expo-secure-store.js` carries the port and its levels).
//
// **The switch is an argument, and a caller that does not know it sends
// nothing.** C1's `SessionPolicy` carries four values and `mobile.sync` is not
// one of them: neither the pair answer nor a session answer says whether the
// project syncs (the plan's CR1 records the *strict* switch's absence, and this
// one is the same gap), so the switch arrives as an argument and a value that is
// not a boolean is refused *before* any request is built. Guessing here would
// be the phone sending a list to a project that asked it not to.
//
// **The route is K1's, not C1's.** `/paired-list` is a path of the identity
// service (`ee/identitysvc/pairedlist.go`, `RoutePairedList`), while C1's
// closed route set is the *project's* mobile routes. The endpoint is therefore
// an argument too: the identity project's, never a project's own.
//
// **The item's name is this module's.** C1's `SECURE_STORE_ITEMS` names one
// item per signed-in identity, per device key and per paired project (D2790);
// the phone's whole list is none of those three, so the prefix below is K3's
// and `identityId` is the second half of it, for the reason a per-identity item
// is reachable: a new device signs in with the same identity and can compose
// the name it must probe.
//
// **The item is a keychain value, and the platform bounds one.** A list past
// `SECRET_VALUE_MAX` bytes (2048, `expo-secure-store`'s own bound on Android) is
// refused rather than written, because a value the platform truncates or warns
// about is a list that reads back as a different one. What is *not* bounded
// here is the identity project's copy: that is K1's 200 entries and 48 KiB.

import { SECURE_STORE_CODES, SECRET_VALUE_MAX } from '../accounts/index.js';

/** The identity service's route, which is K1's (`ee/identitysvc/pairedlist.go`). */
export const PAIRED_LIST_ROUTE = '/paired-list';

/** The keychain prefix of one identity's list. See the header on why it is not C1's set. */
export const PAIRED_LIST_PREFIX = 'fugaro.paired-list.';

/** The most entries one list may hold: K1's own bound, so a write is never refused by the service. */
export const PAIRED_LIST_MAX_ENTRIES = 200;

/** The longest readable name: a short label for a screen, and the phone's own field. */
export const NAME_MAX = 64;

/** The longest opaque project id: `shared/mobile-session/port.js`'s own bound. */
export const PROJECT_ID_MAX = 128;

/** The longest endpoint: the port's bound, and K1's. */
export const ENDPOINT_MAX = 512;

/** The longest identity id: `accounts/secure-store.js`'s bound for one. */
export const IDENTITY_ID_MAX = 200;

/** The longest consumer-key fingerprint. It is O16's format and not this module's: K1 is handed it and compares nothing. */
export const FINGERPRINT_MAX = 256;

/** The four fields C1's `PairedEntry` carries, in the order a writer sends them. */
export const WIRE_ENTRY_FIELDS = Object.freeze([
  'project_id',
  'endpoint',
  'identity',
  'consumer_key_fingerprint',
]);

/** The phone's entry: the wire's four and the readable name that never leaves the device. */
export const LOCAL_ENTRY_FIELDS = Object.freeze([...WIRE_ENTRY_FIELDS, 'name']);

/** The version of the phone's own document, so a later shape can be a second version. */
export const PAIRED_LIST_VERSION = 1;

// The identity service's refusal codes (K1). They are lower_snake_case, they
// are not C1's seven, and a code the app cannot name is a refusal shown as a
// crash -- so the app's set carries them.
const SERVICE_CODES = Object.freeze({
  INVALID_REQUEST: 'invalid_request',
  UNVERIFIED_ID_TOKEN: 'unverified_id_token',
  PAIRED_LIST_UNAVAILABLE: 'paired_list_unavailable',
  UNKNOWN_CODE: 'unknown-code',
});

/** The refusals this module returns: the service's and its own. */
export const SYNC_CODES = Object.freeze({
  ...SERVICE_CODES,
  /** The sync switch was not answered with a boolean, so nothing may be sent. */
  BAD_SWITCH: 'bad-switch',
  /** An identity id that cannot name a keychain item. */
  BAD_IDENTITY: SECURE_STORE_CODES.BAD_IDENTITY,
  /** An entry outside the shape this module writes. */
  BAD_ENTRY: 'bad-entry',
  /** An endpoint that is not `https://` with a host behind it. */
  BAD_ENDPOINT: 'bad-endpoint',
  /** A request that names no identity ID token. */
  BAD_TOKEN: 'bad-token',
  /** A piece of wiring the caller did not hand in. */
  BAD_CONFIG: 'bad-config',
  /** A 200 whose body is not the shape this module reads. */
  BAD_RESPONSE: 'bad-response',
  /** A list past what one keychain item holds (see the header). */
  LIST_TOO_BIG: 'list-too-big',
  /** The request could not be made at all. */
  NETWORK: 'network',
  /** No list is stored for this identity. */
  ABSENT: SECURE_STORE_CODES.ABSENT,
  /** A stored list is not one this module could have written. */
  UNREADABLE: SECURE_STORE_CODES.UNREADABLE,
  /** The platform store refused the write or the delete. */
  STORE_FAILED: SECURE_STORE_CODES.STORE_FAILED,
});

/** A frozen refusal, in the shape the tree's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

function isPlainObject(value) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

// A project id, an identity id and a name are printable ASCII with no space.
// The project id and the identity id are also the two halves of a keychain key
// (`fugaro.device-key.<project id>`, `fugaro.paired-list.<identity id>`), which
// is what makes the narrower alphabet below load-bearing rather than tidy: a
// byte outside it names an item `expo-secure-store` throws on.
const PRINTABLE_RE = /^[\x21-\x7e]+$/;
const KEYCHAIN_SAFE_RE = /^[A-Za-z0-9_.-]+$/;

/** The longest a document may be, in bytes: the platform's own bound for a value. */
export const PAIRED_LIST_MAX_BYTES = SECRET_VALUE_MAX;

/** How many UTF-8 bytes `text` is. The app holds no `Buffer`, and the value is JSON of ASCII. */
function byteLength(text) {
  let bytes = 0;
  for (let i = 0; i < text.length; i += 1) {
    const code = text.charCodeAt(i);
    if (code < 0x80) bytes += 1;
    else if (code < 0x800) bytes += 2;
    else if (code >= 0xd800 && code <= 0xdbff) bytes += 4;
    else bytes += 3;
  }
  return bytes;
}

/**
 * Is `identityId` one a keychain item can be named for? It is the rule
 * `shared/mobile-session/index.js`'s `identityId` composes by, applied to a
 * value that came back from the wire or from a stored document.
 */
export function isIdentityKeyId(identityId) {
  return typeof identityId === 'string' && identityId.length <= IDENTITY_ID_MAX && KEYCHAIN_SAFE_RE.test(identityId);
}

/** Is `projectId` an opaque project id: short, printable, and one a keychain item can be named for? */
export function isPairedProjectId(projectId) {
  return typeof projectId === 'string' && projectId.length <= PROJECT_ID_MAX && KEYCHAIN_SAFE_RE.test(projectId);
}

/**
 * Is `endpoint` one this app may call: `https` with a host behind it, and
 * nothing else. It is the rule `port.js` applies to a session's endpoint and
 * `refresh.js` to the one a token is presented at, and the same rule covers the
 * identity project's base URL -- the one difference between them is which
 * routes are appended.
 */
export function isHttpsEndpoint(endpoint) {
  if (typeof endpoint !== 'string' || endpoint.length > ENDPOINT_MAX || !PRINTABLE_RE.test(endpoint)) return false;
  const host = endpoint.startsWith('https://') ? endpoint.slice('https://'.length) : '';
  return host.length > 0 && !host.startsWith('/');
}

/**
 * The keychain item one identity's list lives under. Throws on an id it cannot
 * compose, for the reason `projectSessionItem` does: a key built from an
 * unchecked id is a key that points at another person's list.
 */
export function pairedListItem(identityId) {
  if (!isIdentityKeyId(identityId)) throw new TypeError('not an identity id');
  return `${PAIRED_LIST_PREFIX}${identityId}`;
}

/**
 * The member of an entry outside `fields`, or null when the entry is exactly
 * those members -- plus, when `optional` names any, those, each of the kinds
 * its own rule names.
 *
 * One function for the three spellings this module reads an entry in: C1's four
 * fields, which is what the wire carries; the four and a name, which is what
 * the phone keeps; and either of the two, which is what a writer accepts
 * because both a phone entry and an entry the identity project handed back are
 * written out as the four. `optional` is what makes the third one a shape of
 * its own rather than one of the other two: a name is a member a phone entry
 * may carry and a wire entry may not, and one that is there is checked either
 * way -- an unvalidated optional member is a name the screen renders.
 */
function defectOf(entry, fields, optional = []) {
  if (!isPlainObject(entry)) return 'entry';
  for (const field of Object.keys(entry)) {
    if (!fields.includes(field) && !optional.includes(field)) return field;
  }
  for (const field of fields) {
    if (!Object.hasOwn(entry, field)) return field;
  }
  const { project_id: projectId, endpoint, identity, consumer_key_fingerprint: fingerprint } = entry;
  if (!isPairedProjectId(projectId)) return 'project_id';
  // The endpoint has to name a project, and `https` is the scheme a keychain
  // item's list is allowed to hold: `port.js` refuses the same shape for the
  // same reason, and K1 refuses it a third time at the service.
  if (!isHttpsEndpoint(endpoint)) return 'endpoint';
  if (!isIdentityKeyId(identity)) return 'identity';
  if (typeof fingerprint !== 'string' || fingerprint.length === 0 || fingerprint.length > FINGERPRINT_MAX) {
    return 'consumer_key_fingerprint';
  }
  if (!PRINTABLE_RE.test(fingerprint)) return 'consumer_key_fingerprint';
  if ([...fields, ...optional].includes('name') && Object.hasOwn(entry, 'name')) {
    const name = entry.name;
    // Empty is a name the app has not been given yet, and the list still holds
    // the project: the screen shows the opaque id until somebody names it.
    if (typeof name !== 'string' || name.length > NAME_MAX) return 'name';
    if (name !== '' && !PRINTABLE_RE.test(name)) return 'name';
  }
  return null;
}

/** The three shapes an entry is read in: the wire's four, the phone's five, and either. */
const SHAPES = Object.freeze({
  wire: (entry) => defectOf(entry, WIRE_ENTRY_FIELDS),
  local: (entry) => defectOf(entry, LOCAL_ENTRY_FIELDS),
  // Either: the wire's four, and a name a phone entry may carry. A name that is
  // present is checked like any other member -- a writer that sent an
  // unvalidated name to the wire would put a field on a screen and not on the
  // list.
  either: (entry) => defectOf(entry, WIRE_ENTRY_FIELDS, ['name']),
});

/**
 * The phone's entry for a wire one, with the name it is given -- or the refusal
 * that names the member outside the shape.
 */
export function localEntry(entry, name = '') {
  const made = Object.freeze({ ...entry, name });
  const defect = SHAPES.local(made);
  if (defect !== null) return refuse(SYNC_CODES.BAD_ENTRY, defect);
  return Object.freeze({ ok: true, entry: made });
}

/**
 * One list of entries, each read in `shape` -- or the refusal that names the
 * first member outside it. It is what every reader and writer of an entry goes
 * through, so one entry's rules are one function.
 */
export function readEntries(value, shape = 'wire') {
  const defectOfEntry = SHAPES[shape];
  if (defectOfEntry === undefined) throw new TypeError('not a shape an entry is read in');
  if (!Array.isArray(value)) return refuse(SYNC_CODES.BAD_ENTRY, 'entries');
  if (value.length > PAIRED_LIST_MAX_ENTRIES) return refuse(SYNC_CODES.BAD_ENTRY, 'entries');
  const entries = [];
  const seen = new Set();
  for (const entry of value) {
    const defect = defectOfEntry(entry);
    if (defect !== null) return refuse(SYNC_CODES.BAD_ENTRY, defect);
    // One project, one entry: two entries for one project would make the
    // endpoint and the pinned fingerprint depend on which was read first, and
    // K1 refuses the same list at the service.
    if (seen.has(entry.project_id)) return refuse(SYNC_CODES.BAD_ENTRY, 'project_id');
    seen.add(entry.project_id);
    entries.push(Object.freeze({ ...entry }));
  }
  return Object.freeze({ ok: true, entries: Object.freeze(entries) });
}

/** One entry as the wire's four fields, or the refusal that names the member outside the shape. */
export function wireEntry(entry) {
  const defect = SHAPES.either(entry);
  if (defect !== null) return refuse(SYNC_CODES.BAD_ENTRY, defect);
  return Object.freeze({
    ok: true,
    entry: Object.freeze({
      project_id: entry.project_id,
      endpoint: entry.endpoint,
      identity: entry.identity,
      consumer_key_fingerprint: entry.consumer_key_fingerprint,
    }),
  });
}

/**
 * The phone's list as the wire's, which is where the readable names come off:
 * the body a write sends is C1's four fields per entry, and nothing else.
 */
export function wireEntries(entries) {
  const read = readEntries(entries, 'either');
  if (!read.ok) return read;
  const wire = [];
  for (const entry of read.entries) {
    const made = wireEntry(entry);
    if (!made.ok) return made;
    wire.push(made.entry);
  }
  return Object.freeze({ ok: true, entries: Object.freeze(wire) });
}

/** One identity's list, as an identity project reads it. */
export function buildPairedListRead({ identityEndpoint, idToken }) {
  if (!isHttpsEndpoint(identityEndpoint)) return refuse(SYNC_CODES.BAD_ENDPOINT, 'identityEndpoint');
  if (typeof idToken !== 'string' || idToken === '') return refuse(SYNC_CODES.BAD_TOKEN, 'idToken');
  return Object.freeze({
    ok: true,
    request: Object.freeze({
      url: `${identityEndpoint}${PAIRED_LIST_ROUTE}`,
      method: 'GET',
      headers: Object.freeze({ Authorization: `Bearer ${idToken}` }),
    }),
  });
}

/** One identity's list, as an identity project takes it. */
export function buildPairedListWrite({ identityEndpoint, idToken, entries }) {
  if (!isHttpsEndpoint(identityEndpoint)) return refuse(SYNC_CODES.BAD_ENDPOINT, 'identityEndpoint');
  if (typeof idToken !== 'string' || idToken === '') return refuse(SYNC_CODES.BAD_TOKEN, 'idToken');
  const wire = wireEntries(entries);
  if (!wire.ok) return wire;
  return Object.freeze({
    ok: true,
    request: Object.freeze({
      url: `${identityEndpoint}${PAIRED_LIST_ROUTE}`,
      method: 'PUT',
      headers: Object.freeze({ Authorization: `Bearer ${idToken}`, 'Content-Type': 'application/json' }),
      body: JSON.stringify({ entries: wire.entries }),
    }),
  });
}

/** The parsed JSON of a body, or null when it is not JSON at all. */
function safeJson(body) {
  if (typeof body !== 'string') return null;
  try {
    return JSON.parse(body);
  } catch {
    return null;
  }
}

/**
 * One answer from the identity project, read as the caller's list -- or as a
 * refusal. A non-200 carries K1's own refusal shape (`{"error":{"code":...}}`),
 * and a code the app does not know is `unknown-code` rather than a code shown
 * as if a screen had been written for it.
 */
export function parsePairedListResponse({ status, body } = {}) {
  const parsed = safeJson(body);
  if (status !== 200) {
    const code = parsed?.error?.code;
    if (typeof code !== 'string' || !Object.values(SERVICE_CODES).includes(code)) {
      return refuse(SYNC_CODES.UNKNOWN_CODE, 'code');
    }
    return refuse(code, 'response');
  }
  if (!isPlainObject(parsed)) return refuse(SYNC_CODES.BAD_RESPONSE, 'response');
  for (const field of Object.keys(parsed)) {
    if (field !== 'entries') return refuse(SYNC_CODES.BAD_RESPONSE, field);
  }
  if (!Array.isArray(parsed.entries)) return refuse(SYNC_CODES.BAD_RESPONSE, 'entries');
  const read = readEntries(parsed.entries);
  if (!read.ok) return refuse(SYNC_CODES.BAD_RESPONSE, read.field);
  return read;
}

/**
 * One request, one answer's status and body -- or the network's refusal. What
 * the platform threw is never carried: it may quote the URL, and a caller
 * cannot act on a message anyway.
 */
async function send(request, fetch) {
  let response;
  try {
    response = await fetch(request.url, { method: request.method, headers: request.headers, body: request.body });
  } catch {
    return refuse(SYNC_CODES.NETWORK, 'network');
  }
  let body;
  try {
    body = await response.text();
  } catch {
    return refuse(SYNC_CODES.NETWORK, 'network');
  }
  return Object.freeze({ ok: true, status: response.status, body });
}

/** One identity's list, read from the identity project. */
export async function readPairedList({ identityEndpoint, idToken, fetch } = {}) {
  if (typeof fetch !== 'function') return refuse(SYNC_CODES.BAD_CONFIG, 'fetch');
  const built = buildPairedListRead({ identityEndpoint, idToken });
  if (!built.ok) return built;
  const answer = await send(built.request, fetch);
  if (!answer.ok) return answer;
  return parsePairedListResponse(answer);
}

/** One identity's list, written to the identity project, which answers with what it stored. */
export async function writePairedList({ identityEndpoint, idToken, entries, fetch } = {}) {
  if (typeof fetch !== 'function') return refuse(SYNC_CODES.BAD_CONFIG, 'fetch');
  const built = buildPairedListWrite({ identityEndpoint, idToken, entries });
  if (!built.ok) return built;
  const answer = await send(built.request, fetch);
  if (!answer.ok) return answer;
  return parsePairedListResponse(answer);
}

/** The list with `entry` replacing the entry for its project, or added after the ones held. */
export function upsertPairedEntry(entries, entry) {
  const kept = entries.filter((held) => held.project_id !== entry.project_id);
  return Object.freeze([...kept, entry]);
}

/** The phone's entries, with the names it already held kept beside the fields a read brought back. */
export function mergeHeldNames(entries, held) {
  const names = new Map(held.map((entry) => [entry.project_id, entry.name]));
  return entries.map((entry) => {
    const name = names.get(entry.project_id);
    return name === undefined ? Object.freeze({ ...entry, name: '' }) : Object.freeze({ ...entry, name });
  });
}

/**
 * The phone's own list, one keychain item per signed-in identity.
 *
 * The item is written at the level the caller names -- `synced` is the switch's
 * own answer -- and what comes back is re-read by the same rules the write
 * applied, because a stored value this module could not have written is not
 * repaired into one: "nearly the right list" is a list that restores a project
 * with the wrong endpoint.
 */
export class PairedListStore {
  #port;

  constructor({ port } = {}) {
    if (port === null || typeof port !== 'object') throw new TypeError('a secure-store port is required');
    this.#port = port;
  }

  /** One identity's list: the entries it holds, or an empty list when it holds none. */
  async read(identityId) {
    if (!isIdentityKeyId(identityId)) return refuse(SYNC_CODES.BAD_IDENTITY, 'identityId');
    let stored;
    try {
      stored = await this.#port.getItem(pairedListItem(identityId));
    } catch {
      return refuse(SYNC_CODES.STORE_FAILED, 'identityId');
    }
    if (stored === null || stored === undefined) return Object.freeze({ ok: true, entries: Object.freeze([]) });
    const parsed = safeJson(stored);
    if (!isPlainObject(parsed) || parsed.v !== PAIRED_LIST_VERSION) return refuse(SYNC_CODES.UNREADABLE, 'list');
    for (const field of Object.keys(parsed)) {
      if (field !== 'v' && field !== 'entries') return refuse(SYNC_CODES.UNREADABLE, field);
    }
    const read = readEntries(parsed.entries, 'local');
    if (!read.ok) return refuse(SYNC_CODES.UNREADABLE, read.field);
    return read;
  }

  /**
   * Write one identity's list. `synced` is the project's switch: a list written
   * while sync is off is the one a new device of the same person may find in
   * the platform's backup, and it is refused past the platform's own value
   * bound rather than written (see the header).
   */
  async write(identityId, entries, { synced = false } = {}) {
    if (!isIdentityKeyId(identityId)) return refuse(SYNC_CODES.BAD_IDENTITY, 'identityId');
    const read = readEntries(entries, 'local');
    if (!read.ok) return read;
    const document = JSON.stringify({ v: PAIRED_LIST_VERSION, entries: read.entries });
    if (byteLength(document) > PAIRED_LIST_MAX_BYTES) return refuse(SYNC_CODES.LIST_TOO_BIG, 'entries');
    try {
      await this.#port.setItem(pairedListItem(identityId), document, { synced });
    } catch {
      // Loud, and what the platform said is not carried: it may quote the value.
      return refuse(SYNC_CODES.STORE_FAILED, 'identityId');
    }
    return Object.freeze({ ok: true, entries: read.entries });
  }

  /**
   * Forget one identity's list. **The delete is never gated on a read**, for the
   * reason `ProjectSessionStore.delete` gives: a read fails exactly when the
   * device is locked, and skipping then would leave a list with no owner.
   */
  async delete(identityId) {
    if (!isIdentityKeyId(identityId)) return refuse(SYNC_CODES.BAD_IDENTITY, 'identityId');
    try {
      await this.#port.deleteItem(pairedListItem(identityId));
    } catch {
      return refuse(SYNC_CODES.STORE_FAILED, 'identityId');
    }
    return Object.freeze({ ok: true });
  }
}

/**
 * Record one pairing: the phone's entry first, and -- only when the project's
 * sync switch is on -- the identity project's copy of the list.
 *
 * **The switch is checked before anything else and nothing is sent when it is
 * off.** That is this task's red step stated as code: with the switch off the
 * identity project is not called at all, and the network port the caller handed
 * in is never reached. It is also why the switch has no default: "not asked"
 * and "off" are the same answer here, and the safe half of that ambiguity is to
 * send nothing.
 *
 * **The phone's copy is written before the identity project's**, so a project
 * that cannot be reached leaves the phone paired with a list that says so --
 * what failed is then the copy, which the caller may push again
 * (`writePairedList`), and not the pairing.
 *
 * **Both copies are read before they are written.** The identity project's list
 * may hold projects this phone has never seen (another device of the same
 * identity paired them), and a write that replaced it with this phone's own
 * would delete them: so the held list is read, this entry replaces the entry
 * for its project or joins the end, and the whole list goes back.
 */
export async function recordPairing({ identityId, entry, sync, store, identityEndpoint, idToken, fetch } = {}) {
  if (typeof sync !== 'boolean') return refuse(SYNC_CODES.BAD_SWITCH, 'sync');
  if (!(store instanceof PairedListStore)) return refuse(SYNC_CODES.BAD_CONFIG, 'store');
  const made = localEntry(entry, entry?.name ?? '');
  if (!made.ok) return made;
  const held = await store.read(identityId);
  if (!held.ok) return held;
  const entries = upsertPairedEntry(held.entries, made.entry);
  const written = await store.write(identityId, entries, { synced: sync === false });
  if (!written.ok) return written;
  if (sync === false) return Object.freeze({ ok: true, entries, sent: false });
  if (typeof fetch !== 'function') return refuse(SYNC_CODES.BAD_CONFIG, 'fetch');
  const remote = await readPairedList({ identityEndpoint, idToken, fetch });
  if (!remote.ok) return remote;
  const wire = wireEntry(made.entry);
  if (!wire.ok) return wire;
  const pushed = await writePairedList({
    identityEndpoint,
    idToken,
    entries: upsertPairedEntry(remote.entries, wire.entry),
    fetch,
  });
  if (!pushed.ok) return pushed;
  return Object.freeze({ ok: true, entries, sent: true });
}
