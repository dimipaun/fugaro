// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The operations screens (portal plan P5.3; D2187, D2207, F9).
//
// **Two gates, and both are the API's own rule drawn early.** The sections are
// answered by the API's membership row for the signed-in person: an owner or an
// admin of the operations organisation changes things, a member reads and is
// offered no button (D2187, F9). And a change needs a sign-in from the last five
// minutes, read from the token's own `auth_time` -- a session that has been
// open all afternoon is not the same evidence as one opened a minute ago. Both
// are enforced by P5.1 whatever this app draws; what the shell buys is that
// nobody reaches the refusal at all, which is the whole of why a screen holds a
// gate the API already has.
//
// **A stale sign-in stops the change and says what to do, and does not resend
// it afterwards.** Re-running the change once the person has signed in again
// would send an action nobody asked for twice -- a person who signs in again
// while thinking about something else would mint. So the change is dropped
// where it was refused, and the prompt says to send it again.
//
// **A person who is not in the operations organisation sees the refusal and
// nothing else.** The membership read is the first thing this screen asks, and
// it is the API that answers who the caller is (D2188): the sections are not
// mounted at all for a caller it refuses, so no screen has to decide what a
// stranger may see.
//
// The API client is built here from the session, because the session is the
// only thing that holds a token (`src/api/client.ts` asks for it per request).
// The `api` prop is the seam a test hands a client built over a fake fetch
// through; `route.tsx` mounts the screen without one.

import { useCallback, useEffect, useMemo, useState } from 'react'
import { createApiClient } from '../../api/client'
import type { Membership } from '../../api/types/orgs'
import { useSession } from '../../auth/session'
import { AuditSection } from './audit'
import type { OpsApi } from './api'
import { opsApi } from './api'
import { LedgerSection } from './ledger'
import { ListsSection } from './lists'
import { LookupSection } from './lookup'
import { QueueSection } from './queue'
import { ReportsSection } from './reports'
import type { OpsActions } from './state'
import { sentence } from './state'
import { TermsSection } from './terms'

/**
 * How fresh a sign-in has to be for a change (D2187). It is the same five
 * minutes P1.3's `FreshWithin` reads on the API side, and it is written out
 * here rather than imported because the constant is Go's and this is the
 * courtesy that keeps the refusal from being how a person finds out.
 */
const FRESH_SIGN_IN_MS = 5 * 60 * 1000

/**
 * Where a visitor with no session signs in. Spelled here and not imported from
 * `src/routes.ts` for the reason `src/screens/signin/route.tsx` gives: the
 * collector is an eager glob, so a screen that read its constant would be a
 * cycle the bundler resolves with the constant not yet initialised.
 */
const SIGN_IN_PATH = '/signin'

/** What the membership read has answered so far. */
type Gate =
  | { status: 'loading' }
  | { status: 'refused'; because: string }
  | { status: 'known'; membership: Membership }

export interface OpsScreenProps {
  /** The API, or nothing to build one from the session. */
  api?: OpsApi
}

export function OpsScreen({ api }: OpsScreenProps = {}) {
  const { state, auth } = useSession()
  const ops = useMemo(
    () => api ?? opsApi(createApiClient({ token: () => auth.idToken() })),
    [api, auth],
  )
  const [gate, setGate] = useState<Gate>({ status: 'loading' })
  const [stepUp, setStepUp] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)
  const user = state.status === 'signed-in' ? state.user : null

  useEffect(() => {
    if (user === null) {
      setGate({ status: 'loading' })
      return
    }
    let live = true
    ops.membership().then(
      (membership) => {
        if (live) {
          setGate({ status: 'known', membership })
        }
      },
      (error: unknown) => {
        if (live) {
          setGate({ status: 'refused', because: sentence(error) })
        }
      },
    )
    return () => {
      live = false
    }
  }, [ops, user])

  const mayAct =
    gate.status === 'known' && (gate.membership.role === 'owner' || gate.membership.role === 'admin')

  const run = useCallback(
    async (change: () => Promise<unknown>): Promise<void> => {
      if (user === null || Date.now() - user.authTime > FRESH_SIGN_IN_MS) {
        setFailure(null)
        setStepUp(true)
        return
      }
      await change()
    },
    [user],
  )

  const actions = useMemo<OpsActions>(() => ({ mayAct, run }), [mayAct, run])

  async function signInAgain(): Promise<void> {
    try {
      await auth.signIn('google')
      setStepUp(false)
    } catch (error: unknown) {
      setStepUp(false)
      setFailure(sentence(error))
    }
  }

  if (state.status === 'loading') {
    return (
      <main>
        <h1>Operations</h1>
        <p role="status">Loading…</p>
      </main>
    )
  }

  if (user === null) {
    return (
      <main>
        <h1>Operations</h1>
        <p>
          These screens are for the operations team. <a href={SIGN_IN_PATH}>Sign in</a> to reach
          them.
        </p>
      </main>
    )
  }

  return (
    <main>
      <h1>Operations</h1>

      {stepUp ? (
        <div role="alert">
          <p>
            This action needs a sign-in from the last five minutes. Sign in again, then send the
            change again.
          </p>
          <button
            type="button"
            onClick={() => {
              void signInAgain()
            }}
          >
            Sign in again
          </button>
        </div>
      ) : null}

      {failure === null ? null : <p role="alert">{failure}</p>}

      {gate.status === 'loading' ? <p role="status">Loading…</p> : null}
      {gate.status === 'refused' ? <p role="alert">{gate.because}</p> : null}

      {gate.status === 'known' ? (
        <>
          {mayAct ? null : (
            <p>
              You are a member of the operations organisation: members read the screens and change
              nothing.
            </p>
          )}
          <LookupSection api={ops} />
          <QueueSection api={ops} actions={actions} />
          <TermsSection api={ops} actions={actions} />
          <LedgerSection api={ops} />
          <ReportsSection api={ops} />
          <AuditSection api={ops} />
          <ListsSection api={ops} actions={actions} />
        </>
      ) : null}
    </main>
  )
}
