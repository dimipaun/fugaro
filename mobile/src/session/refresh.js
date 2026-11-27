// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The hourly refresh, the custom-token exchange and the rotation (task M3;
// D2783, D2794).
//
// **What this module is.** D2783 answers "what does the phone hold for a
// project?" with a Fugaro refresh token bound to the device key, rotated on
// every use, and a one-hour Firebase custom token exchanged for an ID token.
// This module is the wire half of that: it builds the two requests that carry
// the token (`/mobile/v1/refresh` and `/mobile/v1/revoke-self`), reads their
// answers as C1's shapes, exchanges the custom token, and keeps one rotating
// token per project in platform secure storage.
//
// **The Firebase refresh token is dropped, and that is D2783.** The exchange
// answers with an `idToken`, a Firebase `refreshToken` and an `expiresIn`; the
// app keeps the first and the third and never the second, because a Firebase
// refresh token is a credential that outlives the hour and reaches Firebase
// rather than Fugaro. `parseCustomTokenExchange` reads three fields out of the
// answer and the refresh token is not one of them.
//
// **The endpoint is where a device-bound token is presented**, so a cleartext
// scheme is refused before the token leaves: `http://` in a project endpoint is
// a token disclosed to whoever is on the path, and the port (`C2`) refuses the
// same shape for the same reason.
//
// **Why the request is built and the answer is parsed, rather than spread.**
// The refresh answer carries more than the session needs and the exchange
// answer carries more than the app keeps, so each is read as a fixed field set
// and never as the body: a parser that returned what it was given would put
// whatever the service sends next into a store. A refusal is a code from C1's
// closed set (`shared/mobile-session/index.js`'s `REFUSAL_CODES`), never a
// sentence and never a value -- `refresh.test.js` asserts that no refusal
// carries the presented token. The one value a refusal does carry is the
// successor of a rotation whose exchange failed, and that is deliberate: the
// presented token is dead by then, so the successor is the only one the phone
// can present next (see `refreshSession`).
//
// **The network is opened here and in one other file.** The `fetch` this module
// calls is handed in by the caller (`src/session/index.js`, whose own caller is
// the app's entry), so no test needs a device and no screen can reach a URL of
// its own; `src/boundary.test.js`'s network rule carries this file on its
// allow-list, deliberately, because this is where a device-bound refresh token
// leaves the phone.

import { REFUSAL_CODES, ROUTES, SECURE_STORE_ITEMS, SIGNATURE_PURPOSES, signatureMessage } from '../../../shared/mobile-session/index.js';
import { SECURE_STORE_CODES } from '../accounts/secure-store.js';
import { SECRET_VALUE_MAX } from '../accounts/secret.js';
import { encodeBase64url } from '../pairing/base64url.js';

/** The Firebase custom-token exchange, which the ID token is obtained from. */
export const CUSTOM_TOKEN_ENDPOINT = 'https://identitytoolkit.googleapis.com/v1/accounts:signInWithCustomToken';

/**
 * How long before its hour a held ID token is refreshed rather than trusted.
 * The skew is the app's own: a token that expires while a request is in flight
 * is a 401 the phone would have to explain, and one minute is far longer than
 * a refresh takes.
 */
export const REFRESH_SKEW_MS = 60_000;

/** The longest project id: `shared/mobile-session/port.js`'s own bound. */
export const PROJECT_ID_MAX = 128;

/** The characters a project id may carry: `expo-secure-store`'s key alphabet. */
const PROJECT_ID_RE = new RegExp(`^[A-Za-z0-9_.-]{1,${PROJECT_ID_MAX}}$`);

/** The characters a stored token may carry: printable ASCII, no space. */
const PRINTABLE_ASCII_RE = /^[\x21-\x7e]+$/;

/** The prefix every project session key carries, read from C1 rather than spelled again. */
export const PROJECT_SESSION_PREFIX = SECURE_STORE_ITEMS.PROJECT_SESSION;

// The service's refusals are C1's closed set, read rather than respelled: a
// code the app spelled its own way would be a refusal it cannot name, and
// `parseRefreshResponse` carries one through to the caller verbatim.
const CONTRACT_CODES = Object.freeze(
  Object.fromEntries(REFUSAL_CODES.map((code) => [code.toUpperCase(), code])),
);

/**
 * The refusal codes this module returns. The service's seven are C1's; the
 * rest are this module's own, and the store's three are `secure-store.js`'s
 * vocabulary, read so that the app has one spelling of `absent`.
 */
export const REFRESH_CODES = Object.freeze({
  ...CONTRACT_CODES,
  /** An endpoint that does not spell `https://` with a host behind it. */
  BAD_ENDPOINT: 'bad-endpoint',
  /** A request that names no token. */
  BAD_TOKEN: 'bad-token',
  /** A request that names no signature, or a signature of the wrong size. */
  BAD_SIGNATURE: 'bad-signature',
  /** A piece of configuration the request needs is missing. */
  BAD_CONFIG: 'bad-config',
  /** A 200 whose body is not the shape this module reads. */
  BAD_RESPONSE: 'bad-response',
  /** A refusal code C1's set does not carry. Never shown: it is not a screen. */
  UNKNOWN_CODE: 'unknown-code',
  /** The request could not be made at all. */
  NETWORK: 'network',
  /** The device key could not sign: the key is gone or the primitive refused. */
  SIGN_FAILED: 'sign-failed',
  /** The exchange was refused, so there is no ID token. */
  EXCHANGE_REFUSED: 'exchange-refused',
  /** No token is stored for this project. */
  ABSENT: SECURE_STORE_CODES.ABSENT,
  /** A stored token is not one this module could have written. */
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

/**
 * Is `projectId` a project id a keychain item can be named for? It is the same
 * rule the port applies to `project_id` narrowed to the bytes
 * `expo-secure-store` accepts in a key, because the id becomes the second half
 * of one.
 */
export function isProjectKeyId(projectId) {
  return typeof projectId === 'string' && PROJECT_ID_RE.test(projectId);
}

/**
 * The key one project's session lives under. Throws on an id it cannot compose,
 * for the reason `secretKey` does: a key built from an unchecked id is a key
 * that points at another project's session, and there is no safe fallback.
 */
export function projectSessionItem(projectId) {
  if (!isProjectKeyId(projectId)) throw new TypeError('not a project id');
  return `${PROJECT_SESSION_PREFIX}${projectId}`;
}

/** Is `endpoint` one a device-bound token may be presented at? */
function isSecureEndpoint(endpoint) {
  if (typeof endpoint !== 'string') return false;
  const host = endpoint.startsWith('https://') ? endpoint.slice('https://'.length) : '';
  return host.length > 0 && !host.startsWith('/');
}

/**
 * The refresh request: the token and the device key's signature over it. The
 * signature is C1's `signatureMessage(PurposeRefresh, the token)`, spelled in
 * unpadded base64url -- the one spelling this tree uses for a key or a
 * signature, and the one the service decodes strictly.
 */
export function buildRefreshRequest({ endpoint, refreshToken, signature }) {
  if (!isSecureEndpoint(endpoint)) return refuse(REFRESH_CODES.BAD_ENDPOINT, 'endpoint');
  if (typeof refreshToken !== 'string' || refreshToken === '') return refuse(REFRESH_CODES.BAD_TOKEN, 'refreshToken');
  if (typeof signature !== 'string' || signature === '') return refuse(REFRESH_CODES.BAD_SIGNATURE, 'signature');
  return Object.freeze({
    ok: true,
    request: Object.freeze({
      url: `${endpoint}${ROUTES.REFRESH}`,
      method: 'POST',
      headers: Object.freeze({ 'Content-Type': 'application/json' }),
      body: JSON.stringify({ refresh_token: refreshToken, signature }),
    }),
  });
}

/**
 * The revoke-self request: the same shape as the refresh, at the route that
 * ends the caller's own family (D2794). It carries the same proof of possession
 * because it is the same claim -- that this caller is the device and not
 * somebody holding a copied token.
 */
export function buildRevokeSelfRequest({ endpoint, refreshToken, signature }) {
  if (!isSecureEndpoint(endpoint)) return refuse(REFRESH_CODES.BAD_ENDPOINT, 'endpoint');
  if (typeof refreshToken !== 'string' || refreshToken === '') return refuse(REFRESH_CODES.BAD_TOKEN, 'refreshToken');
  if (typeof signature !== 'string' || signature === '') return refuse(REFRESH_CODES.BAD_SIGNATURE, 'signature');
  return Object.freeze({
    ok: true,
    request: Object.freeze({
      url: `${endpoint}${ROUTES.REVOKE_SELF}`,
      method: 'POST',
      headers: Object.freeze({ 'Content-Type': 'application/json' }),
      body: JSON.stringify({ refresh_token: refreshToken, signature }),
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
 * The policy as C2's port carries it, from C1's session-policy shape: the
 * threshold's percent becomes the port's fraction and 0 becomes the port's
 * null, and the other three travel unchanged. **The unit is the whole of this
 * function's job** -- the shape is the reader's (`parseRefreshResponse` refuses
 * a policy outside it, and `assertSessionPort` refuses one again at the door).
 */
export function sessionPolicyOf(policy) {
  const percent = policy?.compact_threshold_percent;
  return Object.freeze({
    costly_modes: Object.freeze([...(policy?.costly_modes ?? [])]),
    biometric_on_costly: policy?.biometric_on_costly ?? false,
    compaction_threshold: Number.isInteger(percent) && percent > 0 ? percent / 100 : null,
    crash_reports: policy?.crash_reports ?? false,
  });
}

/** The member of a policy outside C1's shape, or null when it is one. */
function policyDefect(policy) {
  if (!isPlainObject(policy)) return 'policy';
  if (!Array.isArray(policy.costly_modes) || policy.costly_modes.some((mode) => typeof mode !== 'string' || mode === '')) {
    return 'policy.costly_modes';
  }
  if (typeof policy.biometric_on_costly !== 'boolean') return 'policy.biometric_on_costly';
  if (!Number.isInteger(policy.compact_threshold_percent) || policy.compact_threshold_percent < 0) {
    return 'policy.compact_threshold_percent';
  }
  if (typeof policy.crash_reports !== 'boolean') return 'policy.crash_reports';
  return null;
}

/**
 * One policy in C1's shape, read as C2's port carries it -- or the refusal that
 * names the member outside the shape.
 *
 * It exists because `sessionPolicyOf` alone is a mapper and a mapper is
 * fail-open: a shape missing `costly_modes` would map to an empty list, which
 * is a policy the port accepts and a phone that offers a read-back for nothing.
 * Every reader of a policy from the wire goes through here.
 */
export function readSessionPolicy(policy) {
  const defect = policyDefect(policy);
  if (defect !== null) return refuse(REFRESH_CODES.BAD_RESPONSE, defect);
  return Object.freeze({ ok: true, policy: sessionPolicyOf(policy) });
}

/** A positive integer, from a number or the decimal string Firebase sends. */
function countOf(value) {
  if (typeof value === 'number' && Number.isInteger(value) && value > 0) return value;
  if (typeof value === 'string' && /^[0-9]+$/.test(value) && Number(value) > 0) return Number(value);
  return null;
}

/**
 * One refresh answer, read as the successor tokens, the role and the policy --
 * or as a refusal.
 *
 * A non-200 is the service's refusal, and its `code` is C1's: it is carried
 * through verbatim so that `device_revoked` reaches the session as itself, and
 * a code the contract does not carry is refused as `unknown-code` rather than
 * shown as if it were a screen somebody wrote.
 */
export function parseRefreshResponse({ status, body } = {}) {
  const parsed = safeJson(body);
  if (status !== 200) {
    const code = parsed?.code;
    if (typeof code !== 'string' || !REFUSAL_CODES.includes(code)) return refuse(REFRESH_CODES.UNKNOWN_CODE, 'code');
    return refuse(code, 'response');
  }
  if (!isPlainObject(parsed)) return refuse(REFRESH_CODES.BAD_RESPONSE, 'response');
  const refreshToken = parsed.refresh_token;
  const customToken = parsed.custom_token;
  if (typeof refreshToken !== 'string' || refreshToken === '') return refuse(REFRESH_CODES.BAD_RESPONSE, 'refresh_token');
  if (typeof customToken !== 'string' || customToken === '') return refuse(REFRESH_CODES.BAD_RESPONSE, 'custom_token');
  const expiresIn = countOf(parsed.expires_in);
  if (expiresIn === null) return refuse(REFRESH_CODES.BAD_RESPONSE, 'expires_in');
  if (typeof parsed.role !== 'string' || parsed.role === '') return refuse(REFRESH_CODES.BAD_RESPONSE, 'role');
  const read = readSessionPolicy(parsed.policy);
  if (!read.ok) return read;
  return Object.freeze({
    ok: true,
    refreshToken,
    customToken,
    expiresIn,
    role: parsed.role,
    policy: read.policy,
  });
}

/** The custom-token exchange: the token at Firebase's REST route, asking for the session. */
export function buildCustomTokenExchange({ apiKey, customToken }) {
  if (typeof apiKey !== 'string' || apiKey === '') return refuse(REFRESH_CODES.BAD_CONFIG, 'apiKey');
  if (typeof customToken !== 'string' || customToken === '') return refuse(REFRESH_CODES.BAD_TOKEN, 'customToken');
  return Object.freeze({
    ok: true,
    request: Object.freeze({
      url: `${CUSTOM_TOKEN_ENDPOINT}?key=${encodeURIComponent(apiKey)}`,
      method: 'POST',
      headers: Object.freeze({ 'Content-Type': 'application/json' }),
      body: JSON.stringify({ token: customToken, returnSecureToken: true }),
    }),
  });
}

/**
 * One exchange answer, read as the ID token and its life -- or as a refusal.
 *
 * **Firebase's `refreshToken` is not read**, and that is the point of reading a
 * fixed field set rather than the body: D2783 forbids the app to keep it, and a
 * parser that returned the body would keep it in one line.
 */
export function parseCustomTokenExchange({ status, body } = {}) {
  const parsed = safeJson(body);
  if (status !== 200) return refuse(REFRESH_CODES.EXCHANGE_REFUSED, 'response');
  if (!isPlainObject(parsed)) return refuse(REFRESH_CODES.BAD_RESPONSE, 'response');
  const idToken = parsed.idToken;
  if (typeof idToken !== 'string' || idToken === '') return refuse(REFRESH_CODES.BAD_RESPONSE, 'idToken');
  const expiresIn = countOf(parsed.expiresIn);
  if (expiresIn === null) return refuse(REFRESH_CODES.BAD_RESPONSE, 'expiresIn');
  return Object.freeze({ ok: true, idToken, expiresIn });
}

/**
 * One request, one answer's status and body -- or the network's refusal.
 *
 * The platform's own error is never carried: a URL in it would name the API
 * key, and the caller cannot act on a message anyway. What it gets is a code.
 */
async function post(request, fetch) {
  let response;
  try {
    response = await fetch(request.url, { method: request.method, headers: request.headers, body: request.body });
  } catch {
    return refuse(REFRESH_CODES.NETWORK, 'network');
  }
  let body;
  try {
    body = await response.text();
  } catch {
    return refuse(REFRESH_CODES.NETWORK, 'network');
  }
  return Object.freeze({ ok: true, status: response.status, body });
}

/** The signature over `value` for `purpose`, or a refusal. */
async function signValue({ purpose, value, sign }) {
  const message = signatureMessage(purpose, value);
  if (message === null) return refuse(REFRESH_CODES.BAD_TOKEN, 'value');
  let signature;
  try {
    signature = await sign(message);
  } catch {
    return refuse(REFRESH_CODES.SIGN_FAILED, 'sign');
  }
  if (!(signature instanceof Uint8Array) || signature.length === 0) return refuse(REFRESH_CODES.BAD_SIGNATURE, 'signature');
  return Object.freeze({ ok: true, signature: encodeBase64url(signature) });
}

/**
 * Rotate one project's session: sign the held token, present it, read the
 * successor, and exchange the custom token for an ID token.
 *
 * **A refusal that is `device_revoked` is carried with `revoked: true`**, which
 * is how the session knows to sign the project out rather than merely fail: the
 * token it holds is dead and no later call will recover it (D2794). A network
 * failure carries no such member, because the token is still good and a later
 * refresh may succeed.
 *
 * **The successor is returned and not stored here.** Where the token lives is
 * `ProjectSessionStore`'s, and a function that both rotated and wrote would
 * make a failed write indistinguishable from a failed rotation -- which are two
 * different answers for the caller, one of them recoverable.
 *
 * **A rotation whose exchange failed still carries its successor**, because the
 * rotation has already happened by then: the presented token is dead whatever
 * Firebase answered, so the successor travels with the refusal and the caller
 * writes it before it reports the failure. Dropping it would leave the phone
 * holding a burned token and presenting it on the next call, which the service
 * answers `device_revoked` -- a replay the phone inflicted on itself (D2783).
 */
export async function refreshSession({ endpoint, refreshToken, sign, apiKey, fetch }) {
  if (typeof sign !== 'function') return refuse(REFRESH_CODES.SIGN_FAILED, 'sign');
  const signed = await signValue({ purpose: SIGNATURE_PURPOSES.REFRESH, value: refreshToken, sign });
  if (!signed.ok) return signed;
  const built = buildRefreshRequest({ endpoint, refreshToken, signature: signed.signature });
  if (!built.ok) return built;
  const answer = await post(built.request, fetch);
  if (!answer.ok) return answer;
  const parsed = parseRefreshResponse(answer);
  if (!parsed.ok) {
    if (parsed.code === REFRESH_CODES.DEVICE_REVOKED) return Object.freeze({ ...parsed, revoked: true });
    return parsed;
  }
  const exchanged = await exchangeCustomToken({ apiKey, customToken: parsed.customToken, fetch });
  if (!exchanged.ok) return Object.freeze({ ...exchanged, refreshToken: parsed.refreshToken });
  return Object.freeze({
    ok: true,
    refreshToken: parsed.refreshToken,
    idToken: exchanged.idToken,
    expiresIn: exchanged.expiresIn,
    role: parsed.role,
    policy: parsed.policy,
  });
}

/** Exchange one custom token for an ID token at Firebase's REST route. */
export async function exchangeCustomToken({ apiKey, customToken, fetch }) {
  const built = buildCustomTokenExchange({ apiKey, customToken });
  if (!built.ok) return built;
  const answer = await post(built.request, fetch);
  if (!answer.ok) return answer;
  return parseCustomTokenExchange(answer);
}

/**
 * End one project's refresh family (D2794): sign the held token, present it at
 * `revoke-self`, and read nothing back -- the answer carries nothing, because
 * the family is gone and there is no state left to report.
 */
export async function revokeSelf({ endpoint, refreshToken, sign, fetch }) {
  if (typeof sign !== 'function') return refuse(REFRESH_CODES.SIGN_FAILED, 'sign');
  const signed = await signValue({ purpose: SIGNATURE_PURPOSES.REVOKE_SELF, value: refreshToken, sign });
  if (!signed.ok) return signed;
  const built = buildRevokeSelfRequest({ endpoint, refreshToken, signature: signed.signature });
  if (!built.ok) return built;
  const answer = await post(built.request, fetch);
  if (!answer.ok) return answer;
  if (answer.status !== 200) {
    const parsed = safeJson(answer.body);
    const code = parsed?.code;
    if (typeof code !== 'string' || !REFUSAL_CODES.includes(code)) return refuse(REFRESH_CODES.UNKNOWN_CODE, 'code');
    return refuse(code, 'response');
  }
  return Object.freeze({ ok: true });
}

/** The refusal of a value this module may not write, or null when it may. */
function judgeToken(token) {
  if (typeof token !== 'string' || token.length === 0) return refuse(REFRESH_CODES.BAD_TOKEN, 'token');
  // The ceiling first, so an enormous value is refused without being walked.
  // It is a code-unit count and the value is printable ASCII, so it is also the
  // byte count `expo-secure-store` measures.
  if (token.length > SECRET_VALUE_MAX) return refuse(REFRESH_CODES.BAD_TOKEN, 'token');
  if (!PRINTABLE_ASCII_RE.test(token)) return refuse(REFRESH_CODES.BAD_TOKEN, 'token');
  return null;
}

/**
 * One project's rotating refresh token, in platform secure storage.
 *
 * The port is the same three-method one `SecretStore` takes, and the store
 * addresses exactly the items named by project ids, so one project's token is
 * reached by its own id or by nothing. A `get` re-applies the write's rules to
 * what came back, because a stored value this module could not have written is
 * not repaired into one -- "nearly the right credential" authenticates as
 * nobody.
 */
export class ProjectSessionStore {
  #port;

  constructor(port) {
    if (port === null || typeof port !== 'object') throw new TypeError('a secure-store port is required');
    this.#port = port;
  }

  /** Write one project's token. */
  async put(projectId, token) {
    if (!isProjectKeyId(projectId)) return refuse(REFRESH_CODES.BAD_TOKEN, 'projectId');
    const bad = judgeToken(token);
    if (bad !== null) return bad;
    try {
      await this.#port.setItem(projectSessionItem(projectId), token);
    } catch {
      // Loud, and what the platform said is not carried: it may quote the value.
      return refuse(REFRESH_CODES.STORE_FAILED, 'token');
    }
    return Object.freeze({ ok: true });
  }

  /** Read one project's token back. */
  async get(projectId) {
    if (!isProjectKeyId(projectId)) return refuse(REFRESH_CODES.BAD_TOKEN, 'projectId');
    let stored;
    try {
      stored = await this.#port.getItem(projectSessionItem(projectId));
    } catch {
      return refuse(REFRESH_CODES.STORE_FAILED, 'token');
    }
    if (stored === null || stored === undefined) return refuse(REFRESH_CODES.ABSENT, 'token');
    if (judgeToken(stored) !== null) return refuse(REFRESH_CODES.UNREADABLE, 'token');
    return Object.freeze({ ok: true, token: stored });
  }

  /**
   * Forget one project's token. **The delete is never gated on a read**, for
   * the reason `SecretStore.forget` gives: a read fails exactly when the device
   * is locked, and skipping then would leave a credential with no owner. A
   * delete that fails is a `store-failed` refusal, and the caller keeps the
   * record so the removal can be run again.
   */
  async delete(projectId) {
    if (!isProjectKeyId(projectId)) return refuse(REFRESH_CODES.BAD_TOKEN, 'projectId');
    try {
      await this.#port.deleteItem(projectSessionItem(projectId));
    } catch {
      return refuse(REFRESH_CODES.STORE_FAILED, 'token');
    }
    return Object.freeze({ ok: true });
  }
}
