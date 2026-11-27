// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The silent restore: a new device gets its projects back (task K3; D2784,
// D2783, F15, O16).
//
// **What a restore is.** A person signs in on a new phone with the same
// identity and gets the projects the old phone had, with no QR. Per project
// the phone asks for a single-use challenge, has the identity project sign an
// assertion bound to that challenge (and to the project's audience, F39), and
// posts the assertion with the new device's public key: the project applies the
// same policy pairing applies -- membership, accepted provider, age,
// `mobile.enabled`, the recorded `sub` -- and answers a session whose refresh
// token is bound to the new key (D2784). A project whose strict switch is on
// answers `strict_new_device_needs_qr` instead, which is a project's answer and
// not a failure of this one: that person pairs by QR (D2796).
//
// **O16 is the check this module exists for.** The answer carries the project's
// consumer public key, and the phone accepts it only when it hashes to the
// fingerprint its paired-list entry pinned -- the fingerprint the QR carried at
// pairing (P2) or the identity project's list carried ever since (K1). The
// derivation is fixed by O16: SHA-256 of the key's **decoded** bytes, unpadded
// base64url, which is the value `internal/mailbox/kmssigner.go`'s
// `ConsumerKeyFingerprint` computes and `internal/cli/pair_invite.go` puts in
// the QR. So forging a project's replies needs the public service and a second
// system, and an entry with no fingerprint restores nothing: nothing pinned is
// nothing to accept.
//
// **The assertion is a port.** The identity project's `/assertion` route
// (`ee/identitysvc/handler.go`) is the identity client's, and pairing asks it
// for one too (M2): what arrives here is a function that answers the compact
// JWS for an audience and a challenge, or null. Writing a second client for one
// route would be a second place where an identity's ID token leaves the phone.
//
// **Two list sources, and the switch picks one.** With the project's sync switch
// on the list comes from the identity project; with it off from the phone's own
// keychain backup (`PairedListStore`), which is what "with sync off it restores
// from the platform's synced keychain" comes to. The switch is refused when it
// is not a boolean, before either source is read -- and with it off no identity
// project is called at all, which is `restoreIdentity`'s own half of this
// task's red step.
//
// **Silent, and one project does not hold up another.** Nothing here prompts:
// what a project refused is named per project in the answer, and the projects
// that did answer restore. A session's refusal to be built at the end of a
// successful restore is the one case where the new device key and the stored
// token are taken back again -- a key left behind would refuse the next attempt
// `already-held` and cost the person a QR pairing.

import { REFUSAL_CODES, ROUTES, audienceOf } from '../../../shared/mobile-session/index.js';
import { SECURE_STORE_CODES } from '../accounts/index.js';
import { sha256 } from '../mailbox/sha256.js';
import { decodeBase64url, encodeBase64url } from '../pairing/base64url.js';
import { SESSION_CODES, createProjectSession } from '../session/index.js';

import { SYNC_CODES, isHttpsEndpoint, isPairedProjectId, mergeHeldNames, readEntries, readPairedList } from './sync.js';

/** An Ed25519 public key: C1's `ConsumerPublicKeySize`, and what the fingerprint is over. */
export const CONSUMER_KEY_BYTES = 32;

/** The four methods this module needs of a project session store. */
function isProjectStore(store) {
  return (
    store !== null &&
    typeof store === 'object' &&
    typeof store.put === 'function' &&
    typeof store.delete === 'function'
  );
}

/** The two methods this module needs of a device key store. */
function isDeviceKey(deviceKey) {
  return (
    deviceKey !== null &&
    typeof deviceKey === 'object' &&
    typeof deviceKey.create === 'function' &&
    typeof deviceKey.delete === 'function'
  );
}

/** The service's refusals, read from C1 rather than respelled: a code the app spells its own way is one it cannot name. */
const CONTRACT_CODES = Object.freeze(
  Object.fromEntries(REFUSAL_CODES.map((code) => [code.toUpperCase(), code])),
);

/**
 * The refusals a caller of this module switches on: the session's own (which
 * carry C1's seven), the list's shared spellings, and this module's. A device
 * key's own refusal -- `already-held`, which is what a phone that is not new
 * answers -- travels through verbatim and is not in this set, for the reason
 * `session/index.js` gives about the same store.
 */
export const RESTORE_CODES = Object.freeze({
  ...SESSION_CODES,
  ...CONTRACT_CODES,
  /** An entry outside the shape this module restores from. */
  BAD_ENTRY: SYNC_CODES.BAD_ENTRY,
  /** The sync switch was not answered with a boolean, so no list source was read. */
  BAD_SWITCH: SYNC_CODES.BAD_SWITCH,
  /** An identity id that cannot name a keychain item. */
  BAD_IDENTITY: SYNC_CODES.BAD_IDENTITY,
  /** An endpoint that is not `https://` with a host behind it. */
  BAD_ENDPOINT: SYNC_CODES.BAD_ENDPOINT,
  /** A piece of wiring the caller did not hand in. */
  BAD_CONFIG: SYNC_CODES.BAD_CONFIG,
  /** A 200 whose body is not the shape this module reads. */
  BAD_RESPONSE: SYNC_CODES.BAD_RESPONSE,
  /** The request could not be made at all. */
  NETWORK: SYNC_CODES.NETWORK,
  /** A refusal code C1's set does not carry. Never shown: it is not a screen. */
  UNKNOWN_CODE: 'unknown-code',
  /** The challenge's answer carried no challenge, or one that is not a string. */
  BAD_CHALLENGE: 'bad-challenge',
  /** The identity project answered no assertion for this challenge. */
  ASSERTION_ABSENT: 'assertion-absent',
  /** The returned consumer key does not hash to the fingerprint the entry pinned (O16). */
  CONSUMER_KEY_MISMATCH: 'consumer-key-mismatch',
  /** The answer names another project than the entry's. */
  PROJECT_MISMATCH: 'project-mismatch',
  /** The platform store refused a write. */
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

/**
 * The fingerprint of a consumer public key, from the one spelling the service
 * sends it in: unpadded base64url, decoded strictly, hashed, and written back
 * in the same spelling (O16). A key that is not 32 bytes, or not that spelling,
 * is refused rather than decoded leniently -- a repaired key is a key whose
 * fingerprint is over other bytes.
 */
export function consumerKeyFingerprint(spelling) {
  const decoded = decodeBase64url(spelling, CONSUMER_KEY_BYTES);
  if (!decoded.ok) return refuse(RESTORE_CODES.BAD_RESPONSE, 'consumer_public_key');
  return Object.freeze({ ok: true, fingerprint: encodeBase64url(sha256(decoded.bytes)) });
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

/** The endpoint a challenge may be asked of: `https` with a host behind it, and nothing else. */
function isSecureEndpoint(endpoint) {
  if (typeof endpoint !== 'string') return false;
  const host = endpoint.startsWith('https://') ? endpoint.slice('https://'.length) : '';
  return host.length > 0 && !host.startsWith('/');
}

/**
 * The challenge request: the project's own route and nothing else. C1's
 * `RestoreChallengeRequest` is an empty object -- a challenge is a value an
 * assertion is bound to and not an admission -- so the body says so rather than
 * sending whatever a caller would have attached.
 */
export function buildChallengeRequest({ endpoint }) {
  if (!isSecureEndpoint(endpoint)) return refuse(RESTORE_CODES.BAD_ENDPOINT, 'endpoint');
  return Object.freeze({
    ok: true,
    request: Object.freeze({
      url: `${endpoint}${ROUTES.RESTORE_CHALLENGE}`,
      method: 'POST',
      headers: Object.freeze({ 'Content-Type': 'application/json' }),
      body: '{}',
    }),
  });
}

/** One challenge answer, read as the challenge and its life -- or as a refusal. */
export function parseChallengeResponse({ status, body } = {}) {
  const parsed = safeJson(body);
  if (status !== 200) {
    const code = parsed?.code;
    if (typeof code !== 'string' || !REFUSAL_CODES.includes(code)) return refuse(RESTORE_CODES.UNKNOWN_CODE, 'code');
    return refuse(code, 'response');
  }
  if (!isPlainObject(parsed)) return refuse(RESTORE_CODES.BAD_RESPONSE, 'response');
  if (typeof parsed.challenge !== 'string' || parsed.challenge === '') {
    return refuse(RESTORE_CODES.BAD_CHALLENGE, 'challenge');
  }
  if (!Number.isInteger(parsed.expires_in) || parsed.expires_in <= 0) {
    return refuse(RESTORE_CODES.BAD_RESPONSE, 'expires_in');
  }
  return Object.freeze({ ok: true, challenge: parsed.challenge, expiresIn: parsed.expires_in });
}

/** Ask one project for a fresh single-use challenge. */
export async function requestRestoreChallenge({ endpoint, fetch } = {}) {
  if (typeof fetch !== 'function') return refuse(RESTORE_CODES.BAD_CONFIG, 'fetch');
  const built = buildChallengeRequest({ endpoint });
  if (!built.ok) return built;
  const answer = await send(built.request, fetch);
  if (!answer.ok) return answer;
  return parseChallengeResponse(answer);
}

/**
 * The restore request: the assertion, the challenge it is bound to, and the new
 * device's public key in C1's spelling. The challenge travels beside the
 * assertion rather than out of it, because the project checks the MAC it put in
 * the challenge value before it reads anything else (K2).
 */
export function buildRestoreRequest({ endpoint, assertion, challenge, devicePublicKey }) {
  if (!isSecureEndpoint(endpoint)) return refuse(RESTORE_CODES.BAD_ENDPOINT, 'endpoint');
  if (typeof assertion !== 'string' || assertion === '') return refuse(RESTORE_CODES.ASSERTION_ABSENT, 'assertion');
  if (typeof challenge !== 'string' || challenge === '') return refuse(RESTORE_CODES.BAD_CHALLENGE, 'challenge');
  if (typeof devicePublicKey !== 'string' || devicePublicKey === '') {
    return refuse(RESTORE_CODES.BAD_RESPONSE, 'devicePublicKey');
  }
  return Object.freeze({
    ok: true,
    request: Object.freeze({
      url: `${endpoint}${ROUTES.RESTORE}`,
      method: 'POST',
      headers: Object.freeze({ 'Content-Type': 'application/json' }),
      body: JSON.stringify({ assertion, challenge, device_public_key: devicePublicKey }),
    }),
  });
}

/**
 * One restore answer, read as the bootstrap values a session is built from --
 * or as a refusal.
 *
 * **The consumer key is checked here and not by the caller**, because the check
 * is what makes the rest of the answer worth reading: `consumerKeyFingerprint`
 * is the entry's, and a key that does not hash to it is a refusal whatever else
 * the body says (O16). The comparison is a plain one: both values are hashes of
 * a public key, so there is nothing here a timing difference could leak.
 *
 * `database_url` is carried through even though the session port has no member
 * for it: it is part of C1's answer and the mailbox client is what reads it
 * next, and a parser that dropped it would make the caller reread the body.
 */
export function parseRestoreResponse({ status, body, consumerKeyFingerprint: pinned } = {}) {
  const parsed = safeJson(body);
  if (status !== 200) {
    const code = parsed?.code;
    if (typeof code !== 'string' || !REFUSAL_CODES.includes(code)) return refuse(RESTORE_CODES.UNKNOWN_CODE, 'code');
    return refuse(code, 'response');
  }
  if (!isPlainObject(parsed)) return refuse(RESTORE_CODES.BAD_RESPONSE, 'response');
  if (typeof pinned !== 'string' || pinned === '') return refuse(RESTORE_CODES.BAD_ENTRY, 'consumer_key_fingerprint');
  const derived = consumerKeyFingerprint(parsed.consumer_public_key);
  if (!derived.ok) return derived;
  if (derived.fingerprint !== pinned) return refuse(RESTORE_CODES.CONSUMER_KEY_MISMATCH, 'consumer_public_key');
  if (!isPairedProjectId(parsed.project_id)) return refuse(RESTORE_CODES.BAD_RESPONSE, 'project_id');
  if (!isHttpsEndpoint(parsed.endpoint)) return refuse(RESTORE_CODES.BAD_RESPONSE, 'endpoint');
  if (typeof parsed.api_key !== 'string' || parsed.api_key === '') return refuse(RESTORE_CODES.BAD_RESPONSE, 'api_key');
  if (typeof parsed.database_url !== 'string' || parsed.database_url === '') {
    return refuse(RESTORE_CODES.BAD_RESPONSE, 'database_url');
  }
  if (typeof parsed.role !== 'string' || parsed.role === '') return refuse(RESTORE_CODES.BAD_RESPONSE, 'role');
  if (!isPlainObject(parsed.policy)) return refuse(RESTORE_CODES.BAD_RESPONSE, 'policy');
  if (typeof parsed.refresh_token !== 'string' || parsed.refresh_token === '') {
    return refuse(RESTORE_CODES.BAD_RESPONSE, 'refresh_token');
  }
  if (typeof parsed.custom_token !== 'string' || parsed.custom_token === '') {
    return refuse(RESTORE_CODES.BAD_RESPONSE, 'custom_token');
  }
  if (!Number.isInteger(parsed.expires_in) || parsed.expires_in <= 0) {
    return refuse(RESTORE_CODES.BAD_RESPONSE, 'expires_in');
  }
  return Object.freeze({
    ok: true,
    projectId: parsed.project_id,
    endpoint: parsed.endpoint,
    databaseUrl: parsed.database_url,
    apiKey: parsed.api_key,
    role: parsed.role,
    policy: parsed.policy,
    refreshToken: parsed.refresh_token,
    customToken: parsed.custom_token,
    expiresIn: parsed.expires_in,
  });
}

/** One request, one answer's status and body -- or the network's refusal. */
async function send(request, fetch) {
  let response;
  try {
    response = await fetch(request.url, { method: request.method, headers: request.headers, body: request.body });
  } catch {
    return refuse(RESTORE_CODES.NETWORK, 'network');
  }
  let body;
  try {
    body = await response.text();
  } catch {
    return refuse(RESTORE_CODES.NETWORK, 'network');
  }
  return Object.freeze({ ok: true, status: response.status, body });
}

/**
 * Restore one project onto this device, and hand back the session C2's port
 * describes.
 *
 * `assertionFor` is the identity project's client, handed in as
 * `async ({ audience, challenge, identityId }) => assertion | null`: the
 * audience is C1's `audienceOf(endpoint)`, which is what the project requires
 * the assertion's `aud` to be (F39), and null is a refusal this module reports
 * as `assertion-absent` rather than retrying.
 */
export async function restoreProject({
  entry,
  identityId,
  assertionFor,
  deviceKey,
  projectStore,
  fetch,
  now,
} = {}) {
  const read = readEntries([entry], 'local');
  if (!read.ok) return read;
  const held = read.entries[0];
  if (typeof assertionFor !== 'function') return refuse(RESTORE_CODES.BAD_CONFIG, 'assertionFor');
  if (typeof fetch !== 'function') return refuse(RESTORE_CODES.BAD_CONFIG, 'fetch');
  if (!isDeviceKey(deviceKey)) return refuse(RESTORE_CODES.BAD_CONFIG, 'deviceKey');
  if (!isProjectStore(projectStore)) return refuse(RESTORE_CODES.BAD_CONFIG, 'projectStore');
  const audience = audienceOf(held.endpoint);
  if (audience === null) return refuse(RESTORE_CODES.BAD_ENDPOINT, 'endpoint');

  // The device's own key first: `already-held` is what a phone that is not new
  // answers, and every failure below takes the key back again -- a key left
  // behind is a phone that would refuse its own next attempt.
  const keyed = await deviceKey.create(held.project_id);
  if (!keyed.ok) return keyed;
  const forget = async () => {
    await deviceKey.delete(held.project_id);
  };
  const publicKey = encodeBase64url(keyed.publicKey);

  const challenged = await requestRestoreChallenge({ endpoint: held.endpoint, fetch });
  if (!challenged.ok) {
    await forget();
    return challenged;
  }
  let assertion;
  try {
    assertion = await assertionFor({ audience, challenge: challenged.challenge, identityId });
  } catch {
    // What the identity client threw is not carried: it may quote a token.
    await forget();
    return refuse(RESTORE_CODES.ASSERTION_ABSENT, 'assertion');
  }
  if (typeof assertion !== 'string' || assertion === '') {
    await forget();
    return refuse(RESTORE_CODES.ASSERTION_ABSENT, 'assertion');
  }
  const built = buildRestoreRequest({
    endpoint: held.endpoint,
    assertion,
    challenge: challenged.challenge,
    devicePublicKey: publicKey,
  });
  if (!built.ok) {
    await forget();
    return built;
  }
  const answer = await send(built.request, fetch);
  if (!answer.ok) {
    await forget();
    return answer;
  }
  const parsed = parseRestoreResponse({ ...answer, consumerKeyFingerprint: held.consumer_key_fingerprint });
  if (!parsed.ok) {
    await forget();
    return parsed;
  }
  // A project that answers with another project's id is not answering this
  // entry's question, whatever its key hashes to.
  if (parsed.projectId !== held.project_id) {
    await forget();
    return refuse(RESTORE_CODES.PROJECT_MISMATCH, 'project_id');
  }
  const stored = await projectStore.put(parsed.projectId, parsed.refreshToken);
  if (!stored.ok) {
    await forget();
    return stored;
  }
  const created = createProjectSession({
    projectId: parsed.projectId,
    endpoint: parsed.endpoint,
    role: parsed.role,
    policy: parsed.policy,
    refreshToken: parsed.refreshToken,
    store: projectStore,
    deviceKey,
    apiKey: parsed.apiKey,
    fetch,
    now,
  });
  if (!created.ok) {
    await projectStore.delete(parsed.projectId);
    await forget();
    return created;
  }
  return Object.freeze({
    ok: true,
    session: created.session,
    entry: held,
    databaseUrl: parsed.databaseUrl,
  });
}

/**
 * Restore every entry of one list: the sessions that came back, and the
 * projects that did not, each named with the code that refused it. A project
 * that is unreachable, one whose strict switch refuses a device without a QR,
 * and one whose consumer key does not match are all one line here and never a
 * reason to stop: the person asked for their projects back, and the ones that
 * answered are back.
 */
export async function restoreAll({ identityId, entries, ...rest } = {}) {
  const read = readEntries(entries, 'local');
  if (!read.ok) return read;
  const sessions = [];
  const failed = [];
  for (const entry of read.entries) {
    const one = await restoreProject({ entry, identityId, ...rest });
    if (one.ok) sessions.push(one.session);
    else failed.push(Object.freeze({ project_id: entry.project_id, code: one.code }));
  }
  return Object.freeze({
    ok: failed.length === 0,
    sessions: Object.freeze(sessions),
    failed: Object.freeze(failed),
  });
}

/**
 * Restore one identity's projects onto this device, from whichever list the
 * project's sync switch names: the identity project's when it is on, and the
 * phone's own keychain item when it is off.
 *
 * The phone's own list is written either way afterwards -- with sync on it is
 * what the identity project holds with the names this phone already had beside
 * it, and with sync off it is the same list re-levelled at the level the switch
 * asks for. A list the phone could not write does not undo a session that came
 * back, so it is reported beside the restore rather than instead of it.
 */
export async function restoreIdentity({
  identityId,
  sync,
  identityEndpoint,
  idToken,
  store,
  ...rest
} = {}) {
  if (typeof sync !== 'boolean') return refuse(RESTORE_CODES.BAD_SWITCH, 'sync');
  if (store === null || typeof store !== 'object' || typeof store.read !== 'function' || typeof store.put !== 'function') {
    return refuse(RESTORE_CODES.BAD_CONFIG, 'store');
  }
  const held = await store.read(identityId);
  if (!held.ok) return held;
  let entries = held.entries;
  if (sync === true) {
    // The identity project is reached only here: with the switch off this
    // branch is not taken and `fetch` is never handed a request of this
    // module's.
    const remote = await readPairedList({ identityEndpoint, idToken, fetch: rest.fetch });
    if (!remote.ok) return remote;
    entries = mergeHeldNames(remote.entries, held.entries);
  }
  const restored = await restoreAll({ identityId, entries, ...rest });
  const stored = await store.put(identityId, entries, { synced: sync === false });
  return Object.freeze({ ...restored, stored: stored.ok, ok: restored.ok && stored.ok });
}
