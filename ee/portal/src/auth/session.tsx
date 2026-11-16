// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// Who is signed in, and the one seam the app signs in through (portal plan
// P1.4).
//
// **The port is a seam, and it is what keeps the screens testable.** Every
// screen reads the session from this module and never touches Firebase: the
// SDK is behind `AuthPort`, `src/auth/firebase.ts` is its one implementation,
// and a test hands the provider a fake. That is the plan's hermetic-test rule
// (no network, no real cloud) applied to the one thing in the app that would
// otherwise need both.
//
// **The session carries what the API's rule reads, and nothing about the
// person beyond it.** `provider` and `authTime` are the token's own claims
// (`ee/keyservice` reads them per P1.3): the provider is what the SSO rule
// follows, and the sign-in instant is what a mint or a reveal needs to be
// fresh (P1.3's `FreshWithin`, P5.1). A copy of the display name, the photo or
// the provider's raw answers is deliberately not here -- nothing in the portal
// draws them, and a field kept "in case a screen wants it" is personal data
// with no reader.
//
// The token itself is never in the session: `idToken()` asks the port for the
// current one at the moment a request is made, because a token held in a state
// variable is a token that is still sent after `signOut` (see
// `src/api/client.ts`).

import { createContext, useContext, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import type { FederatedMethod, SignInProvider } from './sso'

/** The person the portal is signed in as, as the token describes them. */
export interface SignedInUser {
  uid: string
  email: string
  provider: SignInProvider
  /** When the identity provider last authenticated them, in epoch ms. */
  authTime: number
}

/** Everything the app may do about sign-in, and nothing about how it is done. */
export interface AuthPort {
  /** Answers the person now and again on every change; returns its own unsubscribe. */
  watch(onUser: (user: SignedInUser | null) => void): () => void
  /** The current ID token, or null when nobody is signed in. */
  idToken(): Promise<string | null>
  signIn(method: FederatedMethod): Promise<void>
  /** Mails a sign-in link to the address. */
  sendSignInLink(email: string): Promise<void>
  /** True when this URL is a sign-in link that came back from the mail. */
  isSignInLink(href: string): boolean
  /** Completes the sign-in a mailed link asked for. */
  completeSignInLink(href: string): Promise<void>
  signOut(): Promise<void>
}

export type SessionState =
  | { status: 'loading' }
  | { status: 'signed-out' }
  | { status: 'signed-in'; user: SignedInUser }

export interface Session {
  state: SessionState
  /** The port itself, for a screen that has to sign somebody in or out. */
  auth: AuthPort
}

const SessionContext = createContext<Session | null>(null)

export function SessionProvider({
  port,
  children,
}: {
  port: AuthPort
  children: ReactNode
}) {
  const [state, setState] = useState<SessionState>({ status: 'loading' })

  useEffect(() => {
    // The first answer may arrive long after the subscription is set up (the
    // SDK reads a stored session before it answers), and a screen that treated
    // "not answered yet" as "signed out" would flash the sign-in form at
    // somebody who is already signed in.
    return port.watch((user) => {
      setState(user === null ? { status: 'signed-out' } : { status: 'signed-in', user })
    })
  }, [port])

  const session = useMemo<Session>(() => ({ state, auth: port }), [state, port])
  return <SessionContext value={session}>{children}</SessionContext>
}

/** The session, or a loud failure -- which is a wiring mistake, not a state. */
export function useSession(): Session {
  const session = useContext(SessionContext)
  if (session === null) {
    throw new Error('useSession was called outside a SessionProvider')
  }
  return session
}
