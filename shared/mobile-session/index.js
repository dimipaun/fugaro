// The mobile session contract's app half (docs/mobile-identity-pairing
// implementation plan, task C1).
//
// The Go half is `internal/mobilesession/contract.go` and the data half is
// `vectors.json` beside this file; this is what the phone imports. It exports
// the contract's closed sets — the claim names, the refusal codes, the routes,
// the secure-store item names, the lifetimes, the key-set tags — and the three
// derivations that have to agree byte for byte with the service: the audience an
// assertion is signed for (F39), the identity id a store item and a paired-list
// entry are keyed by (D2790), and the message a device key signs (O6).
//
// It imports nothing, deliberately, and a test asserts it (`./vectors.test.js`).
// React Native is one of this package's two designated consumers (D119) and its
// bundler resolves no `node:` builtin, so the vectors are read by the test and
// by Go rather than at import time here: a JSON import would work under Node and
// would have to be checked against Metro for no benefit, and the derivations
// belong in one place that has no dependency to drag in.
//
// Nothing here decides anything: the codes are the service's, the lifetimes are
// the owner's (O4), and `audienceOf` and `identityId` refuse rather than guess.
// What a session may do is the port's (`port.js`, C2) and what the app stores is
// M1's and K3's.

/** The `format` a vectors document carries; a reader refuses anything else. */
export const VECTORS_FORMAT = 'fugaro mobile session contract vectors';

// The claims an assertion carries, in the order the signature covers them
// (D2792). The app builds the request and this list, and the service reads the
// same names: a claim renamed on one side is a signature the other refuses.
export const CLAIM_NAMES = Object.freeze([
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
]);

// Every way a session request can be refused. The set is closed and the app
// renders one sentence per code: a code the service sends and the phone does not
// know is a refusal shown as a crash.
export const REFUSAL_CODES = Object.freeze([
  'provider_not_accepted',
  'auth_too_old',
  'not_a_member',
  'nonce_expired',
  'strict_new_device_needs_qr',
  'device_revoked',
  'mobile_disabled',
]);

/** The routes of the public mobile service, under its one version prefix. */
export const ROUTES = Object.freeze({
  PAIR: '/mobile/v1/pair',
  REFRESH: '/mobile/v1/refresh',
  RESTORE_CHALLENGE: '/mobile/v1/restore/challenge',
  RESTORE: '/mobile/v1/restore',
  REVOKE_SELF: '/mobile/v1/revoke-self',
});

// One keychain item per thing the phone keeps (D2790): one signed-in identity's
// session, one device key and one project session per paired project, and the
// consumer key it pinned (O12). They are prefixes ending in a dot, and the
// identity id or the project's opaque id follows: an item written under one
// spelling and read under another is a session that disappears at the next
// launch, and an item whose key leaves KEYCHAIN_SAFE is one `expo-secure-store`
// throws on rather than stores.
export const SECURE_STORE_ITEMS = Object.freeze({
  IDENTITY_SESSION: 'fugaro.identity-session.',
  DEVICE_KEY: 'fugaro.device-key.',
  PROJECT_SESSION: 'fugaro.project-session.',
  CONSUMER_KEY: 'fugaro.consumer-key.',
});

// The bytes `expo-secure-store` accepts in a key: letters, digits, '.', '-' and
// '_'. Anything else makes the platform throw — not return a miss — and
// `mobile/src/accounts/account.js`'s ACCOUNT_ID_RE is the app's own copy of the
// rule for one part. Every name this module composes is checked against it.
const KEYCHAIN_SAFE = /^[A-Za-z0-9_.-]+$/;

// The lives from O4, in seconds, read rather than kept as a second copy: a
// device that outlived one of these would present something the service refuses.
export const LIFETIMES_SECONDS = Object.freeze({
  ASSERTION: 120,
  INVITE: 600,
  CHALLENGE: 600,
  CUSTOM_TOKEN: 3600,
  IDLE: 2592000,
});

/**
 * The separator an identity id joins on: `provider.sub` (D2790). A dot and not
 * a colon or a slash, because the id becomes the second half of a keychain key
 * and those bytes are refused by the platform (see `identityId`).
 */
export const IDENTITY_SEPARATOR = '.';

// What a device key signs. The label separates this contract's messages from the
// mailbox's, which the same key signs (O6): a signature over a refresh token
// must never also be a signature over a message.
export const SIGNATURE_LABEL = 'fugaro-mobile-';
export const SIGNATURE_SEPARATOR = ':';
export const SIGNATURE_PURPOSES = Object.freeze({
  REFRESH: 'refresh',
  REVOKE_SELF: 'revoke-self',
});

// The envelope's tags (O7): the JWS the root key signs over the key set. A
// fetched set is accepted only inside one of these.
export const KEY_SET_FORMAT = 'fugaro-identity-key-set';
export const KEY_SET_ALG = 'EdDSA';
export const KEY_SET_TYP = 'fugaro-key-set+jws';

const encoder = new TextEncoder();

/**
 * What a device key signs for `purpose` over `value`, or null when there is
 * nothing to sign: an unknown purpose or an empty value. Null is the refusal the
 * caller must handle — signing an empty value would be a signature over nothing
 * that verifies as one.
 */
export function signatureMessage(purpose, value) {
  if (typeof value !== 'string' || value.length === 0) return null;
  if (purpose !== SIGNATURE_PURPOSES.REFRESH && purpose !== SIGNATURE_PURPOSES.REVOKE_SELF) return null;
  return encoder.encode(`${SIGNATURE_LABEL}${purpose}${SIGNATURE_SEPARATOR}${value}`);
}

/**
 * The audience an assertion for `endpoint` must name (F39), or null when no
 * audience may be derived from it. The QR carries an endpoint and no project id,
 * so the audience has to come from it: the scheme and the host are folded to
 * lower case, one trailing slash goes, and anything a reader would have to
 * interpret — a userinfo, a query, a fragment, a scheme that is not http or
 * https — is refused rather than guessed at.
 */
export function audienceOf(endpoint) {
  if (typeof endpoint !== 'string' || endpoint.length === 0) return null;
  let url;
  try {
    url = new URL(endpoint);
  } catch {
    return null;
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') return null;
  if (url.username !== '' || url.password !== '') return null;
  if (url.search !== '' || url.hash !== '') return null;
  const path = url.pathname.endsWith('/') ? url.pathname.slice(0, -1) : url.pathname;
  return `${url.protocol}//${url.host}${path}`;
}

/**
 * The string that names one signed-in identity, or null when the pair cannot
 * name one: a part that is empty or carries a byte the keychain refuses would
 * name an item `expo-secure-store` throws on, and a collision here shows one
 * person's session under another's name. Only the *alphabet* is restricted, so
 * a part may keep dots of its own (an Apple subject id has them).
 */
export function identityId(provider, sub) {
  if (typeof provider !== 'string' || typeof sub !== 'string') return null;
  if (provider === '' || sub === '') return null;
  if (!KEYCHAIN_SAFE.test(provider) || !KEYCHAIN_SAFE.test(sub)) return null;
  return `${provider}${IDENTITY_SEPARATOR}${sub}`;
}
