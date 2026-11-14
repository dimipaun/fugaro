// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The Firebase Auth port (portal plan P1.4; D2187).
//
// **The one behaviour here is which listener the session follows.** The API's
// freshness rule reads `auth_time` out of the token (P1.3's `FreshWithin`,
// P5.1), and signing in again is a new token for the same person rather than a
// new person: Firebase's token listener fires for it and its auth-state
// listener does not. A port watching the person would leave the session holding
// the instant of the sign-in before, so a step-up prompt would hide itself and
// every change after it would be stopped again.
//
// The SDK is mocked: nothing here reaches Firebase, and no test needs a network
// or an account.

import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { SignedInUser } from '../src/auth/session'
import { createFirebaseAuthPort } from '../src/auth/firebase'

/**
 * The SDK's two listeners, and the behaviour the port turns on: `signIn` is a
 * new token for whoever it names -- the same person, a new instant -- and only
 * the token listeners hear it.
 */
const sdk = vi.hoisted(() => {
  const tokens: Array<(user: unknown) => void> = []
  const states: Array<(user: unknown) => void> = []
  let current: unknown = null
  const tell = (listeners: Array<(user: unknown) => void>, user: unknown) => {
    for (const listener of listeners) {
      listener(user)
    }
  }
  return {
    tokens,
    states,
    get currentUser() {
      return current
    },
    signIn(user: unknown) {
      current = user
      tell(tokens, user)
    },
    signOut() {
      current = null
      tell(tokens, null)
      tell(states, null)
    },
    reset() {
      tokens.length = 0
      states.length = 0
      current = null
    },
  }
})

vi.mock('firebase/app', () => ({
  getApps: () => [],
  getApp: () => ({ name: 'portal' }),
  initializeApp: () => ({ name: 'portal' }),
}))

vi.mock('firebase/auth', () => ({
  getAuth: () => ({ name: 'auth' }),
  onIdTokenChanged: (_instance: unknown, onUser: (user: unknown) => void) => {
    sdk.tokens.push(onUser)
    // The SDK answers the person it already holds at once, which is what the
    // session's `loading` state waits for.
    onUser(sdk.currentUser)
    return () => {}
  },
  onAuthStateChanged: (_instance: unknown, onUser: (user: unknown) => void) => {
    sdk.states.push(onUser)
    onUser(sdk.currentUser)
    return () => {}
  },
}))

const CONFIG = {
  apiKey: 'portal-web-api-key',
  authDomain: 'portal.example.test',
  projectId: 'portal-test',
  appId: 'portal-test-app',
}

const SIGN_IN_URL = 'https://portal.example.test/signin'

/** A person as the SDK describes them, with the instant their token claims. */
function sdkUser(authTime: number) {
  return {
    uid: 'uid-1',
    email: 'person@example.com',
    getIdTokenResult: () =>
      Promise.resolve({
        claims: { auth_time: authTime, firebase: { sign_in_provider: 'google.com' } },
      }),
  }
}

/** The port, with a watcher whose answers a test can read in order. */
async function watched(): Promise<{
  answers: (SignedInUser | null)[]
  settle(): Promise<void>
}> {
  const auth = await createFirebaseAuthPort(CONFIG, SIGN_IN_URL)
  const answers: (SignedInUser | null)[] = []
  auth.watch((user) => {
    answers.push(user)
  })
  return {
    answers,
    // The port describes a person through the token's claims, so an answer
    // arrives one turn after the SDK announces them.
    settle: () => new Promise((resolve) => setTimeout(resolve, 0)),
  }
}

describe('the Firebase Auth port', () => {
  // The listeners a case registers outlive it otherwise, and the next case's
  // sign-in would be answered by both.
  beforeEach(() => {
    sdk.reset()
  })

  it('follows the token, because a second sign-in moves no uid', async () => {
    const { answers } = await watched()

    // Nobody is signed in, which the SDK answers as soon as it is watched.
    expect(answers).toEqual([null])
    // And the port is on the token listener and not the person listener: the
    // person listener is silent for a same-uid sign-in, which is the whole of
    // why a step-up would otherwise not reach the session.
    expect(sdk.tokens).toHaveLength(1)
    expect(sdk.states).toHaveLength(0)
  })

  it('answers a second sign-in of the same person with the new sign-in instant', async () => {
    const { answers, settle } = await watched()

    sdk.signIn(sdkUser(1_700_000_000))
    await settle()
    expect(answers.at(-1)?.authTime).toBe(1_700_000_000_000)

    sdk.signIn(sdkUser(1_700_000_300))
    await settle()

    const again = answers.at(-1)
    expect(again?.uid).toBe('uid-1')
    expect(again?.authTime).toBe(1_700_000_300_000)
  })

  it('answers nothing when a refresh announces the same claims again', async () => {
    const { answers, settle } = await watched()
    sdk.signIn(sdkUser(1_700_000_000))
    await settle()
    const answered = answers.length

    // A token refresh is not a change: the person and every claim are the same,
    // and an answer would re-run every read keyed on the session.
    sdk.signIn(sdkUser(1_700_000_000))
    await settle()

    expect(answers).toHaveLength(answered)
  })

  it('answers a sign-out as nobody', async () => {
    const { answers, settle } = await watched()
    sdk.signIn(sdkUser(1_700_000_000))
    await settle()

    sdk.signOut()
    await settle()

    expect(answers.at(-1)).toBeNull()
  })
})
