// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// What each provider's stock Expo sign-in hands back, and what is read out of
// it (task M0; Check 733's U4; the reconciliation's D2785, D2790, D2793).
//
// **The routes this spike uses, and why they are the stock ones.**
//
//   * **Google** -- `expo-auth-session`'s `Google` provider, which is Expo's own
//     provider table for the Google OAuth endpoints. The ID token comes back as
//     `params.id_token`, which needs an `openid` scope and a client id of type
//     iOS/Android (a *Web* client id will not return one to a native app).
//   * **Microsoft** -- the same `useAuthRequest` against a discovery document
//     for the `/common/oauth2/v2.0/authorize` endpoint. There is no Expo
//     provider for Microsoft, so the endpoints are spelled out by the caller;
//     `params.id_token` is the token either way.
//   * **Apple** -- `expo-apple-authentication`'s `signInAsync`, which is the
//     platform's own sheet (ASAuthorizationController under it). It answers with
//     `identityToken` and a `nonce` the request was made with; on Android the
//     module refuses, which is exactly the "Apple only on iOS" half of U4.
//
// **What is read out of a token, and what never is.** The plan's member record
// is (email, provider, stable id) -- `sub` -- so those two claims are read
// straight out of the ID token's payload *for display and for the draft*. This
// is **not verification**: nobody checks a signature here, and a spike has no
// reason to (the exchange in `src/firebase/idp.js` is what verifies, or
// refuses). The token itself is passed to the exchange and never rendered,
// copied or stored -- `tokenFromAuthSession` returns it in a field the screens
// do not print, and `tokenFromApple` does the same.

/** The claims U4 needs, and the whole list this module will copy out of a token. */
const CLAIM_FIELDS = Object.freeze(['sub', 'email', 'email_verified', 'aud', 'iss', 'exp', 'iat']);

/**
 * The three providers, the platform each is offered on, and the package that
 * signs it in. `apple` has no Android route on purpose: Apple's own service is
 * iOS-only for a native app, and a screen that offered it on Android would be
 * showing a sign-in button that cannot work.
 */
export const PROVIDERS = Object.freeze({
  google: Object.freeze({ label: 'Google', package: 'expo-auth-session', platforms: ['ios', 'android'] }),
  microsoft: Object.freeze({ label: 'Microsoft', package: 'expo-auth-session', platforms: ['ios', 'android'] }),
  apple: Object.freeze({ label: 'Apple', package: 'expo-apple-authentication', platforms: ['ios'] }),
});

/** Whether a provider is offered on this platform at all. */
export function offeredOn(provider, platform) {
  return (PROVIDERS[provider]?.platforms ?? []).includes(platform);
}

/** The base64url alphabet, and the padding `atob` would want. */
const BASE64 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';

/**
 * A base64url string as bytes. Written out rather than reached for, because the
 * phone's JavaScript engine is not Node: `Buffer` is not there, `atob` is not
 * guaranteed and a spike that crashed decoding a token would report the crash
 * instead of the reading. The arithmetic is the standard six-bits-a-character
 * one, and `-`/`_` are the URL-safe spellings of `+`/`/`.
 */
export function base64UrlBytes(text) {
  const clean = String(text).replace(/=+$/, '');
  const bytes = [];
  let bits = 0;
  let value = 0;
  for (const character of clean) {
    const index = BASE64.indexOf(character === '-' ? '+' : character === '_' ? '/' : character);
    if (index < 0) throw new Error(`not base64url: ${JSON.stringify(character)}`);
    value = (value << 6) | index;
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      bytes.push((value >> bits) & 0xff);
      // Only the bits no byte has taken yet are kept. A real ID token's payload
      // is hundreds of characters, and `<<` truncates to 32 bits: without this
      // mask the accumulator wraps, and the reading would depend on a wrapped
      // value rather than on the encoding.
      value &= (1 << bits) - 1;
    }
  }
  return Uint8Array.from(bytes);
}

/** Bytes as text, decoding UTF-8 by hand for the same reason as above. */
export function utf8(bytes) {
  let text = '';
  for (let i = 0; i < bytes.length; ) {
    const byte = bytes[i];
    let code;
    let length;
    if (byte < 0x80) {
      code = byte;
      length = 1;
    } else if ((byte & 0xe0) === 0xc0) {
      code = byte & 0x1f;
      length = 2;
    } else if ((byte & 0xf0) === 0xe0) {
      code = byte & 0x0f;
      length = 3;
    } else if ((byte & 0xf8) === 0xf0) {
      code = byte & 0x07;
      length = 4;
    } else {
      throw new Error('not UTF-8');
    }
    if (i + length > bytes.length) throw new Error('not UTF-8: the sequence is cut');
    for (let k = 1; k < length; k += 1) {
      if ((bytes[i + k] & 0xc0) !== 0x80) throw new Error('not UTF-8: a continuation byte is missing');
      code = (code << 6) | (bytes[i + k] & 0x3f);
    }
    text += String.fromCodePoint(code);
    i += length;
  }
  return text;
}

/**
 * An ID token's payload claims, as the fixed field set above -- or `null` for
 * anything that is not a three-part JWT with a JSON payload. **The signature is
 * not read and not checked**: this is a display and a recording, and
 * `README.md` says so where the draft records `sub`.
 */
export function claimsOf(idToken) {
  if (typeof idToken !== 'string') return null;
  const parts = idToken.split('.');
  if (parts.length !== 3 || parts[1] === '') return null;
  try {
    const claims = JSON.parse(utf8(base64UrlBytes(parts[1])));
    // An array is JSON and is not a claims set: a payload that decodes to `[]`
    // (`a.W10.c`) is a token this module read nothing out of, and copying an
    // empty object out of it would put a blank member record in the draft.
    if (claims === null || typeof claims !== 'object' || Array.isArray(claims)) return null;
    const read = {};
    for (const field of CLAIM_FIELDS) {
      if (claims[field] !== undefined) read[field] = claims[field];
    }
    return read;
  } catch {
    return null;
  }
}

/**
 * One `useAuthRequest`/`promptAsync` answer, as the token and its claims -- or
 * as the reason there is none, in the words the screen shows.
 *
 * `dismiss` and `cancel` are the owner closing the sheet, which is a reading
 * worth distinguishing from a refusal: the first is "not tried", the second is
 * "tried and refused", and the draft should not confuse them.
 */
export function tokenFromAuthSession(provider, response) {
  const type = response?.type;
  if (type === 'dismiss' || type === 'cancel') return { ok: false, reason: `the ${PROVIDERS[provider]?.label ?? provider} sheet was closed without finishing` };
  if (type !== 'success') {
    const detail = response?.error?.message ?? response?.errorCode ?? response?.params?.error ?? type;
    return { ok: false, reason: `the sign-in did not succeed: ${detail}` };
  }
  const idToken = response?.params?.id_token;
  if (typeof idToken !== 'string' || idToken === '') {
    // The usual cause, and worth saying: a client id of the wrong type, or an
    // `openid` scope that was not asked for, returns an authorization code and
    // no ID token.
    return { ok: false, reason: 'the response carried no id_token: an ID token needs the openid scope and an iOS/Android client id' };
  }
  return { ok: true, provider, idToken, claims: claimsOf(idToken) };
}

/**
 * `AppleAuthentication.signInAsync`'s answer. Apple's answer carries no ID
 * token in `params`: it is `identityToken`, and the stable id is `user` (which
 * is also the token's `sub`). The `nonce` is the one the request was made with
 * and is carried through, because Firebase checks it when one is sent.
 */
export function tokenFromApple(result) {
  const idToken = result?.identityToken;
  if (typeof idToken !== 'string' || idToken === '') {
    return { ok: false, reason: 'Apple answered without an identityToken' };
  }
  return {
    ok: true,
    provider: 'apple',
    idToken,
    nonce: typeof result.nonce === 'string' ? result.nonce : undefined,
    claims: claimsOf(idToken),
  };
}
