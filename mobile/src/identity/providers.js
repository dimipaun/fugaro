// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The provider sign-in exchange (task M1; D2793, D2785, F13).
//
// **What this module is.** D2793 answers "how does a phone hold several
// identities?" with REST sign-in against the identity project and no second
// Firebase SDK instance: the provider's own sheet hands the app a token, and
// this module exchanges that token at the identity project's REST endpoint for
// a session the app can hold. F13 states the reason a session is needed at
// all -- `@react-native-firebase/auth` holds one current user per app
// instance, so a second identity needs a session the SDK is not holding.
//
// **The packages the owner's go names.** M0 (Check 733, the two-phone spike)
// is the task that picks the packages, and its result document is not in the
// tree, so **no package is added here**: the exchange is HTTP and the request
// body is assembled from the platform's own `URLSearchParams`, exactly as
// `mobile/spike/src/firebase/idp.js` proved out. Whether a provider's native
// SDK yields a token this exchange accepts is U4, and it is a question for
// the device, not for this module: what arrives here is a token.
//
// **Why the request is built rather than spread.** The answer to a sign-in
// carries more than a session -- `oauthIdToken`, `displayName`, the whole
// envelope -- so the answer is read as a fixed field set and never as the
// body. A parser that returned what it was given would put whatever the
// service sends next into a record a screen lists.
//
// **The providers are spelled as the policy spells them.** `google.com`,
// `microsoft.com` and `apple.com` are `internal/projectpolicy`'s accepted
// providers, and the assertion's `provider` claim is that same spelling
// (C1's `Claims`). The app keeps one vocabulary rather than a short name here
// and a long one on the wire, because `IdentityID(provider, sub)` is what
// names a secure-store item and a paired-list entry: two spellings of one
// provider is a session that disappears at the next launch. Password sign-in
// is a different endpoint and is not this exchange; it is not offered here.
//
// **What is never carried.** No refusal in this module echoes the token, the
// API key or a URL that contains one: the refusals are codes and, at most,
// Firebase's own reason enum (`INVALID_ID_TOKEN`), which names the refusal
// without repeating a value. The one call site is `fetch` below, and
// `src/boundary.test.js`'s network rule carries this file on its allow-list so
// that the claim -- one reviewed file per egress -- still holds.

/** The endpoint the provider token is exchanged at (D2781's identity project). */
export const IDP_ENDPOINT = 'https://identitytoolkit.googleapis.com/v1/accounts:signInWithIdp';

/** The endpoint a session's ID token is renewed at. */
export const TOKEN_ENDPOINT = 'https://securetoken.googleapis.com/v1/token';

/** The `requestUri` the exchange insists on and ignores for an ID-token exchange. */
const REQUEST_URI = 'http://localhost';

/**
 * The three providers, keyed by the spelling the policy and the assertion use,
 * with the label a screen shows. It is the closed set this module signs in
 * with: a name outside it is refused rather than sent.
 */
export const PROVIDERS = Object.freeze({
  'google.com': Object.freeze({ label: 'Google' }),
  'microsoft.com': Object.freeze({ label: 'Microsoft' }),
  'apple.com': Object.freeze({ label: 'Apple' }),
});

/**
 * The domain Apple's private-relay addresses live under. It is the same
 * string `internal/credsvc/mobile_policy.go` refuses at pairing: an address
 * Apple made up to hide the person's own, so it is not the address an admin
 * invited. The app detects it here so the identity is flagged unusable for
 * pairing before a phone ever scans a QR, rather than at the refusal.
 */
export const APPLE_RELAY_DOMAIN = '@privaterelay.appleid.com';

/** The refusal codes this module returns. */
export const PROVIDER_CODES = Object.freeze({
  /** The API key or another piece of configuration is missing. */
  BAD_CONFIG: 'bad-config',
  /** A provider this module does not sign in with. */
  UNKNOWN_PROVIDER: 'unknown-provider',
  /** A request that names no token. */
  EMPTY_TOKEN: 'empty-token',
  /** The service refused the sign-in or the refresh; `reason` names Firebase's own enum. */
  SIGN_IN_REFUSED: 'sign-in-refused',
  /** The request could not be made at all. */
  NETWORK: 'network',
  /** A 200 whose body is not the shape this module reads. */
  BAD_RESPONSE: 'bad-response',
});

/** A frozen refusal, in the shape the tree's modules use. */
function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/** Is `name` one of the providers this module signs in with? */
export function isProvider(name) {
  return Object.hasOwn(PROVIDERS, name);
}

/**
 * Is `email` an Apple private-relay address? The suffix is the whole test,
 * case-folded, and it is deliberately not restricted to Apple: the address is
 * the thing the roster and the invite are keyed by, and an address under the
 * relay domain is not one an admin invited whoever the provider was.
 */
export function isRelayAddress(email) {
  return typeof email === 'string' && email.toLowerCase().endsWith(APPLE_RELAY_DOMAIN);
}

/** The base64url alphabet, and the value of each character, built once. */
const ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_';
const VALUES = (() => {
  const table = new Int8Array(128).fill(-1);
  for (let i = 0; i < ALPHABET.length; i += 1) table[ALPHABET.charCodeAt(i)] = i;
  return table;
})();

/**
 * `part`, the base64url of one JWT segment, as text -- or null for a character
 * outside the alphabet or bytes that are not UTF-8.
 *
 * **Written out rather than reached for, both steps.** `atob` is a browser
 * global that React Native's Hermes does not guarantee (the pairing module and
 * the identity spike say the same thing), and it decodes the padded standard
 * alphabet rather than this one; an engine without it would refuse *every*
 * sign-in here instead of reading a token. The UTF-8 step is hand-written for
 * the same reason, as `src/licence/verify.js` does for its own payload: a
 * primitive the format depends on is one this module should not have to hope
 * for, and a `TextDecoder` built at module scope would fail to load this file
 * at all on an engine without it.
 */
function base64UrlText(part) {
  const bytes = new Uint8Array(Math.floor((part.length * 6) / 8));
  let acc = 0;
  let bits = 0;
  let at = 0;
  for (let i = 0; i < part.length; i += 1) {
    const code = part.charCodeAt(i);
    const value = code < 128 ? VALUES[code] : -1;
    if (value < 0) return null;
    acc = (acc << 6) | value;
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      bytes[at] = (acc >> bits) & 0xff;
      at += 1;
      // Only the bits no byte has taken yet are kept: `<<` truncates to 32
      // bits, and a real payload is hundreds of characters long.
      acc &= (1 << bits) - 1;
    }
  }
  return utf8Text(bytes);
}

/**
 * `bytes` as text, or null when they are not UTF-8.
 *
 * Strict, like the licence verifier's reader: an overlong form, a surrogate, a
 * truncated sequence or a byte that starts no sequence is not text, and a
 * payload that is not text is not a claim set. A claim set the identity project
 * answered with is always UTF-8, so this refuses only a token that is not one.
 */
function utf8Text(bytes) {
  let out = '';
  for (let i = 0; i < bytes.length; ) {
    const first = bytes[i];
    let codePoint;
    let length;
    if (first < 0x80) {
      codePoint = first;
      length = 1;
    } else if (first >= 0xc2 && first <= 0xdf) {
      codePoint = first & 0x1f;
      length = 2;
    } else if (first >= 0xe0 && first <= 0xef) {
      codePoint = first & 0x0f;
      length = 3;
    } else if (first >= 0xf0 && first <= 0xf4) {
      codePoint = first & 0x07;
      length = 4;
    } else {
      // 0x80..0xc1 and 0xf5..0xff start no sequence: the first is a
      // continuation byte or an overlong form, the second is past U+10FFFF.
      return null;
    }
    if (i + length > bytes.length) return null;
    for (let j = 1; j < length; j += 1) {
      const next = bytes[i + j];
      if (next < 0x80 || next > 0xbf) return null;
      codePoint = (codePoint << 6) | (next & 0x3f);
    }
    // The forms whose first byte admits a shorter encoding of the same code
    // point, and the ones the standard puts outside the range at all: an
    // overlong sequence, a surrogate, and the 4-byte forms past U+10FFFF.
    if (length === 3 && codePoint < 0x800) return null;
    if (length === 4 && codePoint < 0x10000) return null;
    if (codePoint > 0x10ffff) return null;
    if (codePoint >= 0xd800 && codePoint <= 0xdfff) return null;
    out += String.fromCodePoint(codePoint);
    i += length;
  }
  return out;
}

/**
 * An ID token's payload claims, or null when the string is not a three-part
 * JWT with a JSON object payload. **The signature is not checked here**: this
 * reads the token the identity project just answered with, and the project is
 * what verifies signatures (A1). What the app needs from it is `auth_time`,
 * and copying out the whole payload is safe because the payload is the token
 * itself -- it carries no other credential.
 */
export function claimsOf(idToken) {
  if (typeof idToken !== 'string') return null;
  const parts = idToken.split('.');
  if (parts.length !== 3 || parts[1] === '') return null;
  const text = base64UrlText(parts[1]);
  if (text === null) return null;
  try {
    const claims = JSON.parse(text);
    // An array is JSON and is not a claims set: a payload that decodes to `[]`
    // is a token this module read nothing out of.
    if (claims === null || typeof claims !== 'object' || Array.isArray(claims)) return null;
    return claims;
  } catch {
    return null;
  }
}

/**
 * The sign-in request: the provider's token at the identity project's REST
 * exchange, asking for the session (`returnSecureToken: true`). M0's spike
 * asked for none, because U4 was only whether the token is accepted; an
 * identity the app holds needs the ID token and the refresh token that make it
 * a session.
 *
 * `nonce` is Apple's: an Apple ID token carries the nonce it was minted with,
 * and Firebase checks it when one is sent, so it is passed through when the
 * caller has one and left out otherwise.
 */
export function buildSignInWithIdp({ apiKey, provider, idToken, nonce }) {
  if (typeof apiKey !== 'string' || apiKey === '') return refuse(PROVIDER_CODES.BAD_CONFIG, 'apiKey');
  if (!isProvider(provider)) return refuse(PROVIDER_CODES.UNKNOWN_PROVIDER, 'provider');
  if (typeof idToken !== 'string' || idToken === '') return refuse(PROVIDER_CODES.EMPTY_TOKEN, 'idToken');
  const post = new URLSearchParams({ id_token: idToken, providerId: provider });
  if (typeof nonce === 'string' && nonce !== '') post.set('nonce', nonce);
  return Object.freeze({
    ok: true,
    request: Object.freeze({
      url: `${IDP_ENDPOINT}?key=${encodeURIComponent(apiKey)}`,
      method: 'POST',
      headers: Object.freeze({ 'Content-Type': 'application/json' }),
      body: JSON.stringify({ postBody: post.toString(), requestUri: REQUEST_URI, returnSecureToken: true }),
    }),
  });
}

/**
 * The request that renews an identity's ID token from its stored refresh
 * token. It is Firebase's documented `securetoken` grant, form-encoded.
 */
export function buildRefresh({ apiKey, refreshToken }) {
  if (typeof apiKey !== 'string' || apiKey === '') return refuse(PROVIDER_CODES.BAD_CONFIG, 'apiKey');
  if (typeof refreshToken !== 'string' || refreshToken === '') return refuse(PROVIDER_CODES.EMPTY_TOKEN, 'refreshToken');
  return Object.freeze({
    ok: true,
    request: Object.freeze({
      url: `${TOKEN_ENDPOINT}?key=${encodeURIComponent(apiKey)}`,
      method: 'POST',
      headers: Object.freeze({ 'Content-Type': 'application/x-www-form-urlencoded' }),
      body: new URLSearchParams({ grant_type: 'refresh_token', refresh_token: refreshToken }).toString(),
    }),
  });
}

/** The parsed JSON of a body, or null when it is not JSON at all. */
function safeJson(text) {
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

/**
 * Firebase's own reason for a refusal, when the body carries one: its error
 * enum (`INVALID_ID_TOKEN`, `TOKEN_EXPIRED`), which names the refusal and
 * repeats no value. Anything that does not look like an enum is dropped --
 * a message that quoted the token would be a message on a screen.
 */
function reasonOf(parsed) {
  const message = parsed?.error?.message;
  if (typeof message !== 'string') return null;
  const enumName = message.split(/[\s:]/)[0];
  return /^[A-Z][A-Z0-9_]*$/.test(enumName) ? enumName : null;
}

/** The refusal of a sign-in or a refresh, with Firebase's reason when it named one. */
function refusedBy(parsed) {
  const reason = reasonOf(parsed);
  return Object.freeze({
    ok: false,
    code: PROVIDER_CODES.SIGN_IN_REFUSED,
    field: 'response',
    ...(reason === null ? {} : { reason }),
  });
}

/** A positive integer, from a number or the decimal string Firebase sends. */
function countOf(value) {
  if (typeof value === 'number' && Number.isInteger(value) && value > 0) return value;
  if (typeof value === 'string' && /^[0-9]+$/.test(value) && Number(value) > 0) return Number(value);
  return null;
}

/**
 * One sign-in answer, read as a session -- or as a refusal.
 *
 * The session is a fixed field set: `provider`, `sub` (the provider's stable
 * id, which is `federatedId` and what the member record keeps), `email` (null
 * when the provider shared none, as Apple may not), `emailVerified`, the
 * `idToken` and `refreshToken` the app holds, `expiresIn` in seconds and
 * `authTime` read out of the ID token -- the last authentication time, which
 * is what the project's maximum age is measured against.
 */
export function parseSignIn({ provider, status, body }) {
  const parsed = typeof body === 'string' ? safeJson(body) : body;
  if (status !== 200) return refusedBy(parsed);
  if (parsed === null || typeof parsed !== 'object') return refuse(PROVIDER_CODES.BAD_RESPONSE, 'response');
  if (parsed.providerId !== provider) return refuse(PROVIDER_CODES.BAD_RESPONSE, 'provider');
  const sub = parsed.federatedId;
  if (typeof sub !== 'string' || sub === '') return refuse(PROVIDER_CODES.BAD_RESPONSE, 'sub');
  const idToken = parsed.idToken;
  const refreshToken = parsed.refreshToken;
  if (typeof idToken !== 'string' || idToken === '') return refuse(PROVIDER_CODES.BAD_RESPONSE, 'idToken');
  if (typeof refreshToken !== 'string' || refreshToken === '') return refuse(PROVIDER_CODES.BAD_RESPONSE, 'refreshToken');
  const expiresIn = countOf(parsed.expiresIn);
  if (expiresIn === null) return refuse(PROVIDER_CODES.BAD_RESPONSE, 'expiresIn');
  const email = parsed.email === undefined || parsed.email === null ? null : parsed.email;
  if (email !== null && (typeof email !== 'string' || email === '')) return refuse(PROVIDER_CODES.BAD_RESPONSE, 'email');
  // A real `auth_time` is a Unix time, so zero and a negative are refusals the
  // same way a missing one is -- and the store refuses them again when it reads
  // its own records, so accepting one here would write a document the store
  // could not read back.
  const authTime = claimsOf(idToken)?.auth_time;
  if (!Number.isInteger(authTime) || authTime <= 0) return refuse(PROVIDER_CODES.BAD_RESPONSE, 'authTime');
  return Object.freeze({
    ok: true,
    session: Object.freeze({
      provider,
      sub,
      email,
      emailVerified: parsed.emailVerified === true,
      idToken,
      refreshToken,
      expiresIn,
      authTime,
    }),
  });
}

/** One refresh answer, read as the successor tokens -- or as a refusal. */
export function parseRefresh({ status, body }) {
  const parsed = typeof body === 'string' ? safeJson(body) : body;
  if (status !== 200) return refusedBy(parsed);
  if (parsed === null || typeof parsed !== 'object') return refuse(PROVIDER_CODES.BAD_RESPONSE, 'response');
  const idToken = parsed.id_token;
  const refreshToken = parsed.refresh_token;
  const expiresIn = countOf(parsed.expires_in);
  if (typeof idToken !== 'string' || idToken === '') return refuse(PROVIDER_CODES.BAD_RESPONSE, 'idToken');
  if (typeof refreshToken !== 'string' || refreshToken === '') return refuse(PROVIDER_CODES.BAD_RESPONSE, 'refreshToken');
  if (expiresIn === null) return refuse(PROVIDER_CODES.BAD_RESPONSE, 'expiresIn');
  return Object.freeze({ ok: true, tokens: Object.freeze({ idToken, refreshToken, expiresIn }) });
}

/** One request, one parsed answer. The two call sites share this and nothing else. */
async function exchange(request, parse) {
  let response;
  try {
    response = await fetch(request.url, { method: request.method, headers: request.headers, body: request.body });
  } catch {
    // The platform's own error is not carried: a URL in it would name the API
    // key, and a message is not a reading the caller can act on anyway.
    return Object.freeze({ ok: false, code: PROVIDER_CODES.NETWORK, field: 'network' });
  }
  let text;
  try {
    text = await response.text();
  } catch {
    return Object.freeze({ ok: false, code: PROVIDER_CODES.NETWORK, field: 'network' });
  }
  return parse({ status: response.status, body: text });
}

/**
 * Sign one provider token in at the identity project, over the platform's own
 * `fetch`. The build's own refusal is answered unchanged, so a missing key
 * never reaches the network.
 */
export async function signInWithIdp({ apiKey, provider, idToken, nonce }) {
  const built = buildSignInWithIdp({ apiKey, provider, idToken, nonce });
  if (!built.ok) return built;
  return exchange(built.request, (answer) => parseSignIn({ provider, ...answer }));
}

/** Renew an identity's ID token from its stored refresh token. */
export async function refreshTokens({ apiKey, refreshToken }) {
  const built = buildRefresh({ apiKey, refreshToken });
  if (!built.ok) return built;
  return exchange(built.request, parseRefresh);
}
