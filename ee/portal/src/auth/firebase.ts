// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// Firebase Auth, behind `AuthPort` (portal plan P1.4; spec §Auth).
//
// **This is the only file in the portal that imports the Firebase SDK, and it
// is imported dynamically.** Two reasons, and both are about what a screen can
// reach: a screen that asked Firebase a question directly would be a screen no
// hermetic test can run (the plan's test rule: no network, no real cloud), and
// a static import would put the whole SDK in the first bundle, so a visitor
// who only reads the sign-in page would download the auth, firestore and
// storage clients before seeing it. `SessionProvider` is handed the port
// `createFirebaseAuthPort` answers, and every other file in `src/auth/` is
// pure.
//
// **The project's web config is read from Vite's environment and is not a
// secret.** `apiKey` is Firebase's public client identifier -- it names the
// project, it does not authorise anything, and Firebase's own documentation
// says it may ship in the page (what protects the project is the API's own
// verification and the rules, P1.3 and P1.5). A build with no config renders a
// notice instead of an app: a portal whose sign-in cannot work is a
// configuration failure, and a sign-in form that always fails is a worse way to
// say so.

import type { AuthPort, SignedInUser } from './session'
import { PROVIDER_OF } from './sso'
import type { FederatedMethod, SignInProvider } from './sso'

/** The Firebase web configuration, as P0.1's projects and P9.2's build supply it. */
export interface FirebaseConfig {
  apiKey: string
  authDomain: string
  projectId: string
  appId: string
}

/** The key the address a sign-in link was asked for is kept under. */
export const EMAIL_FOR_LINK_KEY = 'fugaro.portal.signInEmail'

/** The project's web config, or null when the build was not given one. */
export function firebaseConfig(env: Record<string, string | undefined>): FirebaseConfig | null {
  const config = {
    apiKey: env.VITE_FIREBASE_API_KEY ?? '',
    authDomain: env.VITE_FIREBASE_AUTH_DOMAIN ?? '',
    projectId: env.VITE_FIREBASE_PROJECT_ID ?? '',
    appId: env.VITE_FIREBASE_APP_ID ?? '',
  }
  const complete = Object.values(config).every((value) => value !== '')
  return complete ? config : null
}

/**
 * The provider a token's claim names. A claim the portal does not know is
 * answered as `password`, which is the not-SSO side of the rule: the API reads
 * the same claim itself and is what refuses (P1.3), so a value this file
 * guessed wrong can only change which sign-in buttons a screen offers.
 */
function providerFrom(claim: string | undefined): SignInProvider {
  switch (claim) {
    case 'google.com':
    case 'apple.com':
    case 'microsoft.com':
    case 'emailLink':
      return claim
    default:
      return 'password'
  }
}

/** The person as the token describes them. */
async function describe(user: {
  uid: string
  email: string | null
  getIdTokenResult: () => Promise<{ claims: Record<string, unknown> }>
}): Promise<SignedInUser> {
  const { claims } = await user.getIdTokenResult()
  const firebase = (claims.firebase ?? {}) as { sign_in_provider?: string }
  const authTime = typeof claims.auth_time === 'number' ? claims.auth_time * 1000 : Date.now()
  return {
    uid: user.uid,
    // An account with no address on it cannot hold a membership: an invitation
    // is bound to an address and every comparison is an address comparison, so
    // the empty string here is a user the API refuses rather than one this
    // file is entitled to invent an address for.
    email: user.email ?? '',
    provider: providerFrom(firebase.sign_in_provider),
    authTime,
  }
}

/**
 * The port, for the project whose web config this is. `signInUrl` is where a
 * mailed link returns to -- the sign-in screen, which is what completes it --
 * and it is handed in rather than written here so that this file names no route
 * and the app's own `SIGN_IN_PATH` stays the one place a path is spelled.
 */
export async function createFirebaseAuthPort(
  config: FirebaseConfig,
  signInUrl: string,
): Promise<AuthPort> {
  const { initializeApp, getApps, getApp } = await import('firebase/app')
  const firebaseAuth = await import('firebase/auth')

  const app = getApps().length > 0 ? getApp() : initializeApp(config)
  const instance = firebaseAuth.getAuth(app)
  const storage = () => window.localStorage

  return {
    watch(onUser) {
      let seen = 0
      return firebaseAuth.onAuthStateChanged(instance, (user) => {
        // The answers to two quick changes can arrive out of order, and a
        // session that went back to signed-in after a sign-out would be a
        // screen drawn for somebody who is no longer there.
        const mine = ++seen
        if (user === null) {
          onUser(null)
          return
        }
        void describe(user).then((described) => {
          if (mine === seen) {
            onUser(described)
          }
        })
      })
    },

    async idToken() {
      const user = instance.currentUser
      return user === null ? null : await user.getIdToken()
    },

    async signIn(method: FederatedMethod) {
      const provider =
        method === 'microsoft'
          ? new firebaseAuth.OAuthProvider(PROVIDER_OF.microsoft)
          : method === 'apple'
            ? new firebaseAuth.OAuthProvider(PROVIDER_OF.apple)
            : new firebaseAuth.GoogleAuthProvider()
      await firebaseAuth.signInWithPopup(instance, provider)
    },

    async sendSignInLink(email: string) {
      await firebaseAuth.sendSignInLinkToEmail(instance, email, {
        url: signInUrl,
        handleCodeInApp: true,
      })
      // The link's owner may open it in another tab of this browser, and
      // Firebase needs the address again to complete it; keeping it in local
      // storage is Firebase's own documented recipe for that case, and it is
      // the address the person just typed rather than anything about them.
      storage().setItem(EMAIL_FOR_LINK_KEY, email)
    },

    isSignInLink(href: string) {
      return firebaseAuth.isSignInWithEmailLink(instance, href)
    },

    async completeSignInLink(href: string) {
      const email = storage().getItem(EMAIL_FOR_LINK_KEY)
      if (email === null) {
        throw new Error(
          'Open this link in the browser you asked for it in, or ask for a new one.',
        )
      }
      await firebaseAuth.signInWithEmailLink(instance, email, href)
      storage().removeItem(EMAIL_FOR_LINK_KEY)
    },

    async signOut() {
      await firebaseAuth.signOut(instance)
    },
  }
}
