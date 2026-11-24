// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The REST `accounts:signInWithIdp` exchange: the request, and the reading of
// its answer (task M0; Check 733's U4; the reconciliation's F13 and D2793).
//
// **What U4 asks.** Whether the provider token a native sign-in yields is one
// Firebase's REST exchange accepts -- because D2793 builds the app's several
// identities on that exchange and on nothing else, and D221 keeps a provider
// session out of every project. A token the exchange refuses is a finding, not
// a failure: it is what makes the plan's fallback (keep the OAuth route rather
// than the exchange) a chosen path rather than an assumption.
//
// **Why this file builds the request by hand.** `@react-native-firebase/auth`
// would answer the same question through its own SDK, and the plan deliberately
// refuses a per-project Firebase app on the phone (D234) and a second SDK on
// the identity path (D2793). A `POST` to the documented endpoint answers it
// with the identity project's own Web API key and no SDK on the device at all,
// which is the shape M1 will build on.
//
// **Two decisions, both about what is *not* in the answer.**
//
//   * `returnSecureToken: false`. The exchange can mint a Firebase ID token and
//     a refresh token, and neither is wanted here: U4 asks only whether the
//     provider token is accepted. Asking for them would put a live credential
//     into the spike's own memory and into anything the screen prints, for a
//     reading that does not need it.
//   * the parsed answer is a **fixed field set**, never the response object.
//     Firebase's success body carries `idToken`, `refreshToken` and
//     `oauthIdToken`; a parser that returned what it was given would hand them
//     to a screen, to a clipboard block and to whatever reads one next. So
//     `parseIdpResponse` names every field it copies out, and
//     `idp.test.js` hands it a success body full of tokens and asserts none of
//     them is in the answer.
//
//  The API key is not a secret in Firebase's model (it is not a credential: it
//  names the project and the rules decide), but it is the owner's project, so
//  it is typed into the app's own settings field at run time, held in memory
//  and never committed -- `README.md`, "What you type into the app".
//
//  **The one call site.** `signInWithIdp` below calls the platform's own
//  `fetch`, and this file is the spike's only file that does: the repo's
//  network rule (`mobile/src/boundary.test.js`, rule 6) walks every `.js` under
//  `mobile/` and may open the network from `src/voice-switch/hop.js` and nowhere
//  else, so this path is named on that allow-list rather than reached through a
//  spelling the pattern does not read. The rule's claim is unchanged -- one
//  file that talks to a third party is a file a reviewer reads whole -- and the
//  list now carries the two.

/** The endpoint, and the project the key in the query names is the identity project (D2781). */
export const IDP_ENDPOINT =
  'https://identitytoolkit.googleapis.com/v1/accounts:signInWithIdp';

/** The `providerId` Firebase spells each provider with, which is the whole vocabulary. */
export const PROVIDER_IDS = Object.freeze({
  google: 'google.com',
  microsoft: 'microsoft.com',
  apple: 'apple.com',
});

/**
 * The error a bad call raises. **The field, and never the value**: a message
 * that quoted a mistyped key or token is a message in a screen, a clipboard and
 * a pasted draft.
 */
function refused(what, why) {
  return new Error(`${what} ${why} (the value is not repeated here).`);
}

/**
 * The request the exchange is asked with.
 *
 * `postBody` is Firebase's own little form: the provider's ID token and the
 * provider's name, URL-encoded. `requestUri` is required by the endpoint and is
 * ignored by it for an ID-token exchange, so the convention every Firebase
 * client uses (`http://localhost`) is what this sends -- it is not a redirect,
 * it is a field the API insists on.
 *
 * `nonce` is Apple's: an Apple ID token carries the nonce it was minted with,
 * and Firebase checks it when one is sent. It is left out unless the caller has
 * one.
 */
export function buildSignInWithIdp({ apiKey, provider, idToken, requestUri = 'http://localhost', nonce }) {
  if (typeof apiKey !== 'string' || apiKey.trim() === '') {
    throw refused('The Firebase Web API key', 'is not set: type it into the settings screen');
  }
  const providerId = PROVIDER_IDS[provider];
  if (providerId === undefined) {
    throw refused(`The provider ${JSON.stringify(provider)}`, `is not one of ${Object.keys(PROVIDER_IDS).join(', ')}`);
  }
  if (typeof idToken !== 'string' || idToken.trim() === '') {
    throw refused(`The ${provider} ID token`, 'is empty, so there is nothing to exchange');
  }

  const post = new URLSearchParams({ id_token: idToken, providerId });
  if (typeof nonce === 'string' && nonce !== '') post.set('nonce', nonce);

  return {
    url: `${IDP_ENDPOINT}?key=${encodeURIComponent(apiKey)}`,
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ postBody: post.toString(), requestUri, returnIdpCredential: true, returnSecureToken: false }),
  };
}

/** The fields a `signInWithIdp` answer is read for, and nothing else. */
const ANSWER_FIELDS = Object.freeze([
  'federatedId',
  'providerId',
  'localId',
  'email',
  'emailVerified',
  'displayName',
  'photoUrl',
]);

/**
 * One exchange's answer, as a fixed set of fields plus the outcome.
 *
 * `status` is the HTTP status and `body` the parsed JSON (or the raw text, when
 * the service answered with something that is not JSON -- a proxy's error page,
 * which is itself a reading worth keeping). A refusal is
 * `{ ok: false, httpStatus, error: { code, message } }` with Firebase's own
 * `message` copied through: it is the text that names the refusal in
 * `app-spike.md`, and `INVALID_IDP_RESPONSE`, `OPERATION_NOT_ALLOWED` and
 * `INVALID_PROVIDER_ID` mean different things to the plan.
 *
 * **No token field is ever copied out**, in either direction, whatever the body
 * carries. That is the point of naming the fields rather than spreading the
 * body.
 */
export function parseIdpResponse({ status, body }) {
  const parsed = typeof body === 'string' ? safeJson(body) : body;
  const answer = { ok: false, httpStatus: status };

  if (parsed !== null && typeof parsed === 'object') {
    const error = parsed.error;
    if (error !== undefined && error !== null && typeof error === 'object') {
      answer.error = {
        code: typeof error.code === 'number' ? error.code : null,
        message: typeof error.message === 'string' ? error.message : '',
      };
    }
  }
  for (const field of ANSWER_FIELDS) {
    const value = parsed !== null && typeof parsed === 'object' ? parsed[field] : undefined;
    if (value !== undefined) answer[field] = value;
  }

  // The endpoint answers 200 with a body; anything else is a refusal whether or
  // not it carried an `error` object, so the outcome is the status's and not
  // the body's.
  answer.ok = status === 200 && answer.error === undefined;
  if (!answer.ok && answer.error === undefined) {
    answer.error = { code: status, message: typeof body === 'string' ? body : 'the service answered without an error body' };
  }
  return answer;
}

function safeJson(text) {
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

/**
 * The exchange, over the platform's own `fetch` -- the call site the header
 * names, which a test drives by replacing `globalThis.fetch` with a fake. The
 * call's throw -- no network, a DNS failure -- is answered as a reading too,
 * because "the phone had no route to Firebase" is a thing Check 733 must be
 * able to record.
 */
export async function signInWithIdp(request) {
  let response;
  try {
    response = await fetch(request.url, { method: request.method, headers: request.headers, body: request.body });
  } catch (error) {
    return { ok: false, httpStatus: null, error: { code: null, message: `the request could not be made: ${plainError(error)}` } };
  }
  const text = await response.text();
  return parseIdpResponse({ status: response.status, body: text });
}

/** An error's message, or its name: a thrown value is not always an Error. */
function plainError(error) {
  if (error !== null && typeof error === 'object' && typeof error.message === 'string') return error.message;
  return String(error);
}
