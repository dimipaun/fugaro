// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// U4: provider tokens through the REST `signInWithIdp` exchange, and two
// identities at once (task M0; Check 733).
//
// **The three routes, and why they are these.** Google and Microsoft go
// through `expo-auth-session` -- Google through Expo's own provider table with
// an `openid` scope, Microsoft through the v2 authorize endpoint spelled out
// by hand, because Expo has no Microsoft provider and the plan does not want a
// second SDK on the phone (D234, D2793). Apple goes through the platform's own
// sheet, and only on iOS: there is no Android route to Apple's service for a
// native app.
//
// **What the screen shows, and what it does not.** For each provider: whether
// the exchange accepted the token, the HTTP status, and Firebase's own error
// text when it did not. Never the token: it is passed to the exchange and to
// nothing that prints. The email and `sub` are read out of the token's payload
// for the draft's member record, and the `localId` Firebase answers with is
// shown beside them, because two providers answering with one `localId` is the
// account-linking behaviour U8 asks about (Check 731) -- an incidental reading
// here, and a useful one.
//
// **Two identities at once.** Every exchange's answer is kept in a list, and
// the screen shows the list: that is how this spike holds two identities, and
// it says so rather than implying a store. What M1 adds is one secure-store
// item per identity (D2790); the spike writes none, because a spike holding a
// provider session on disk is a session left on a handset.

import * as AppleAuthentication from 'expo-apple-authentication';
import { ResponseType, makeRedirectUri, useAuthRequest } from 'expo-auth-session';
import * as Google from 'expo-auth-session/providers/google';
import * as Crypto from 'expo-crypto';
import { useState } from 'react';
import { Platform } from 'react-native';

import { buildSignInWithIdp, signInWithIdp } from '../firebase/idp.js';
import { resultBlock } from '../format.js';
import { offeredOn, tokenFromApple, tokenFromAuthSession } from '../identity/provider-token.js';
import { Button, CopyResult, Head, Mono, Note, Page, Row } from '../ui/kit.js';

/** The tenant Microsoft's v2 endpoint is asked through; `common` accepts any work or personal account. */
const MICROSOFT_TENANT = 'common';

/** Microsoft's discovery document, spelled out: Expo ships no Microsoft provider. */
const MICROSOFT_DISCOVERY = {
  authorizationEndpoint: `https://login.microsoftonline.com/${MICROSOFT_TENANT}/oauth2/v2.0/authorize`,
  tokenEndpoint: `https://login.microsoftonline.com/${MICROSOFT_TENANT}/oauth2/v2.0/token`,
};

/** The redirect the auth session comes back on: the app's own scheme, from the config. */
const REDIRECT_URI = makeRedirectUri({ path: 'auth' });

/** A short prefix of a long value, for a `sub` or a `localId` on a phone screen. */
function short(value) {
  const text = typeof value === 'string' ? value : '';
  return text.length <= 16 ? text : `${text.slice(0, 16)}…`;
}

/** One provider's reading, as the screen and the block show it. */
function outcomeLine(result) {
  if (result === undefined) return 'not tried yet';
  if (result.error !== undefined && result.httpStatus === undefined) return `not run: ${result.error}`;
  if (result.ok) {
    return `ACCEPTED by signInWithIdp (HTTP ${result.httpStatus}); email ${result.email ?? 'absent'}, sub ${short(result.claims?.sub ?? result.localId)}`;
  }
  return `REFUSED (HTTP ${result.httpStatus ?? 'no answer'}): ${result.error?.message ?? 'no error text'}`;
}

export function U4Screen({ settings, http }) {
  const { clientIds } = settings;
  const [results, setResults] = useState({});
  const [held, setHeld] = useState([]);

  const [googleRequest, , googlePrompt] = Google.useIdTokenAuthRequest({
    clientId: clientIds.google,
    iosClientId: clientIds.googleIos || undefined,
    androidClientId: clientIds.googleAndroid || undefined,
    scopes: ['openid', 'profile', 'email'],
  });

  const [microsoftRequest, , microsoftPrompt] = useAuthRequest(
    {
      clientId: clientIds.microsoft,
      scopes: ['openid', 'profile', 'email'],
      responseType: ResponseType.IdToken,
      redirectUri: REDIRECT_URI,
    },
    MICROSOFT_DISCOVERY,
  );

  /** Record one provider's reading, and one identity if the exchange accepted it. */
  function record(provider, result) {
    setResults((previous) => ({ ...previous, [provider]: result }));
  }

  /**
   * The exchange itself, over the transport the shell handed in. Every way it
   * can fail -- no API key typed, no token, a dead network, a refusal -- lands
   * in the provider's own reading, so a screen never shows a pass it did not
   * get.
   */
  async function exchange(provider, token) {
    if (token.ok !== true) {
      record(provider, { error: token.reason });
      return;
    }
    try {
      const answer = await signInWithIdp(
        http,
        buildSignInWithIdp({ apiKey: settings.apiKey, provider, idToken: token.idToken, nonce: token.nonce }),
      );
      record(provider, { ...answer, claims: token.claims, provider });
      if (answer.ok) {
        setHeld((previous) => [
          ...previous.filter((entry) => entry.provider !== provider),
          { provider, email: answer.email ?? null, sub: token.claims?.sub ?? answer.localId ?? null },
        ]);
      }
    } catch (error) {
      record(provider, { error: error.message });
    }
  }

  async function signInWithGoogle() {
    const answer = await googlePrompt();
    await exchange('google', tokenFromAuthSession('google', answer));
  }

  async function signInWithMicrosoft() {
    const answer = await microsoftPrompt();
    await exchange('microsoft', tokenFromAuthSession('microsoft', answer));
  }

  async function signInWithApple() {
    try {
      // Apple's own rule, and the one everybody gets wrong: the sheet is given
      // the *hashed* nonce and the exchange is given the raw one, which is what
      // Firebase compares the token's claim against.
      const nonce = Crypto.randomUUID();
      const hashed = await Crypto.digestStringAsync(Crypto.CryptoDigestAlgorithm.SHA256, nonce);
      const answer = await AppleAuthentication.signInAsync({
        requestedScopes: [AppleAuthentication.AppleAuthenticationScope.EMAIL],
        nonce: hashed,
      });
      await exchange('apple', tokenFromApple({ ...answer, nonce }));
    } catch (error) {
      record('apple', { error: error.message });
    }
  }

  const block = resultBlock({
    question: 'U4',
    phone: settings.phone,
    fields: [
      ['Project', settings.projectId],
      ['Google', outcomeLine(results.google)],
      ['Microsoft', outcomeLine(results.microsoft)],
      [offeredOn('apple', settings.platform) ? 'Apple' : 'Apple (not offered on Android)', offeredOn('apple', settings.platform) ? outcomeLine(results.apple) : 'no Android route to Apple\'s service for a native app'],
      ['Identities held at once', held.length === 0 ? 'none yet' : `${held.length}: ${held.map((entry) => entry.provider).join(', ')}`],
      ['How they are held', 'in the app\'s memory for this run (an array of exchange answers); no session is written to disk, and M1 stores one secure-store item per identity instead'],
      ['Tokens shown', 'none'],
    ],
  });

  return (
    <Page>
      <Head
        title="U4 — provider tokens"
        question="Does signInWithIdp accept what each native sign-in hands back, and can the app hold two identities at once?"
      />
      <Note>
        The Firebase API key is not a credential in Firebase&apos;s model -- it names a project and the rules decide --
        but it is your project, so it is typed into Settings and held in memory only.
      </Note>

      <Row label="Google route" value={`expo-auth-session Google provider; client id ${clientIds.google ? 'set' : 'MISSING (set EXPO_PUBLIC_GOOGLE_CLIENT_ID)'}`} />
      <Button title="Sign in with Google" onPress={signInWithGoogle} disabled={googleRequest === null} />

      <Row label="Microsoft route" value={`expo-auth-session against Microsoft's ${MICROSOFT_TENANT} v2 endpoints; client id ${clientIds.microsoft ? 'set' : 'MISSING (set EXPO_PUBLIC_MICROSOFT_CLIENT_ID)'}`} />
      <Button title="Sign in with Microsoft" onPress={signInWithMicrosoft} disabled={microsoftRequest === null} />

      <Row
        label="Apple route"
        value={
          offeredOn('apple', Platform.OS)
            ? 'expo-apple-authentication (the platform sheet); Apple only on iOS'
            : 'not offered: Apple\'s service has no Android route for a native app'
        }
      />
      {offeredOn('apple', Platform.OS) ? (
        <AppleAuthentication.AppleAuthenticationButton
          buttonType={AppleAuthentication.AppleAuthenticationButtonType.SIGN_IN}
          buttonStyle={AppleAuthentication.AppleAuthenticationButtonStyle.WHITE}
          cornerRadius={8}
          style={{ height: 44, marginTop: 8 }}
          onPress={signInWithApple}
        />
      ) : null}

      <Row label="Google" value={outcomeLine(results.google)} />
      <Row label="Microsoft" value={outcomeLine(results.microsoft)} />
      <Row label="Apple" value={offeredOn('apple', Platform.OS) ? outcomeLine(results.apple) : 'not offered on this platform'} />
      <Row label="Identities held at once" value={held.length === 0 ? 'none yet' : `${held.length} — ${held.map((entry) => `${entry.provider} (${entry.email ?? 'no email'})`).join(', ')}`} />

      <Note>
        A second provider answering with the <Mono>localId</Mono> the first one did means Firebase linked them into one
        account; a different <Mono>localId</Mono> means two accounts. That is U8&apos;s question (Check 731) and this
        screen answers it by accident, so the block carries both.
      </Note>

      <CopyResult block={block} what="U4" />
    </Page>
  );
}
