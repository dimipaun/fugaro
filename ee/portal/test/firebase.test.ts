// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The Firebase port (portal plan P1.4; spec §Auth).
//
// **This suite replaces the SDK, which is the one place the portal does that.**
// `src/auth/firebase.ts` is the only file that imports Firebase, and every other
// suite reaches the app through `AuthPort` so that none of them needs a network
// or an account -- which leaves the port itself, the file the session actually
// depends on, pinned by nothing. The behaviour that matters here is which of the
// SDK's two listeners the session is watched through, so the fake answers the
// way the SDK does: `notifyAuthListeners` hands every notification to the token
// listeners, and hands it to the auth-state listeners only when the uid has
// moved (`authStateSubscription.next` sits behind `lastNotifiedUid`). A person
// signing in again as themselves -- the step-up a stale change asks for (D2187)
// -- moves the token and not the uid, so it is a notification only the token
// listener hears.

import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { SignedInUser } from '../src/auth/session'
import { createFirebaseAuthPort } from '../src/auth/firebase'

/** The two listeners the SDK keeps, and the notification it hands them. */
const sdk = vi.hoisted(() => {
  const tokenListeners = new Set<(user: unknown) => void>()
  const authListeners = new Set<(user: unknown) => void>()
  let lastNotifiedUid: string | null = null
  return {
    tokenListeners,
    authListeners,
    onIdTokenChanged: (_auth: unknown, listener: (user: unknown) => void) => {
      tokenListeners.add(listener)
      return (): void => {
        tokenListeners.delete(listener)
      }
    },
    onAuthStateChanged: (_auth: unknown, listener: (user: unknown) => void) => {
      authListeners.add(listener)
      return (): void => {
        authListeners.delete(listener)
      }
    },
    notify(user: { uid: string } | null): void {
      const uid = user === null ? null : user.uid
      for (const listener of tokenListeners) {
        listener(user)
      }
      if (uid !== lastNotifiedUid) {
        lastNotifiedUid = uid
        for (const listener of authListeners) {
          listener(user)
        }
      }
    },
    reset(): void {
      tokenListeners.clear()
      authListeners.clear()
      lastNotifiedUid = null
    },
  }
})

vi.mock('firebase/app', () => ({
  getApps: () => [],
  getApp: () => ({}),
  initializeApp: () => ({}),
}))

vi.mock('firebase/auth', () => ({
  getAuth: () => ({}),
  onIdTokenChanged: sdk.onIdTokenChanged,
  onAuthStateChanged: sdk.onAuthStateChanged,
}))

beforeEach(() => {
  sdk.reset()
})

const CONFIG = { apiKey: 'a-public-client-key', authDomain: 'portal.example.test', projectId: 'example', appId: 'example' }

const SIGN_IN_URL = 'https://portal.example.test/signin'

/** A Firebase user, as the SDK answers one: the identity, and the token's claims. */
function firebaseUser(uid: string, signedInAt: number) {
  return {
    uid,
    email: 'ops@example.com',
    getIdTokenResult: () =>
      Promise.resolve({
        claims: { auth_time: signedInAt, firebase: { sign_in_provider: 'google.com' } },
      }),
  }
}

describe('the Firebase port', () => {
  it('watches the session through the token listener and not the auth-state one', async () => {
    const port = await createFirebaseAuthPort(CONFIG, SIGN_IN_URL)
    port.watch(() => {})

    // The other listener is the bug: it never hears a sign-in that keeps the
    // same uid, so `authTime` would stay as old as the session and every change
    // under it would be refused as stale for as long as the tab is open.
    expect(sdk.tokenListeners.size).toBe(1)
    expect(sdk.authListeners.size).toBe(0)
  })

  it('hands the session a sign-in again as the same person', async () => {
    const port = await createFirebaseAuthPort(CONFIG, SIGN_IN_URL)
    const seen: SignedInUser[] = []
    port.watch((signedIn) => {
      if (signedIn !== null) {
        seen.push(signedIn)
      }
    })

    sdk.notify(firebaseUser('uid-ops', 1_700_000_000))
    await vi.waitFor(() => {
      expect(seen).toHaveLength(1)
    })

    // The same person signing in again: the same uid and a token whose
    // `auth_time` has moved, which is what the step-up prompt clears on.
    sdk.notify(firebaseUser('uid-ops', 1_700_000_900))
    await vi.waitFor(() => {
      expect(seen).toHaveLength(2)
    })
    expect(seen[1]).toEqual({
      uid: 'uid-ops',
      email: 'ops@example.com',
      provider: 'google.com',
      authTime: 1_700_000_900_000,
    })
  })
})
