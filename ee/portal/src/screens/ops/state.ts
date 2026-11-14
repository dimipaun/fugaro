// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// What every operations section shares (portal plan P5.3): the sentence a
// failure gets, what a section may do, and one change's own state.
//
// **A failure is shown as the API wrote it, and nothing else is invented.** The
// API answers every refusal with one shape whose `message` is a sentence for a
// person (P1.0), and a screen that printed anything else -- a status, a code, an
// error object -- would be showing a reader something nobody wrote for them.
// What is not that shape (a proxy's page, a dropped connection) is answered
// with one sentence this file owns, because a transport failure has no sentence
// of the API's to show.
//
// **A change goes through one gate.** `run` is the shell's (`ops.tsx`), which is
// where a change that needs a fresh sign-in is refused before it is sent: the
// API refuses it too (D2187), and the screens' whole job here is that nobody
// meets that refusal. A section therefore never calls the API for a change
// itself -- it hands the call to `submit`, which asks the gate first.

import { useCallback, useState } from 'react'
import { ApiError } from '../../api/client'

const GENERIC_FAILURE = 'The portal could not reach the API. Try again.'

/** The sentence a person is shown for a failure. */
export function sentence(error: unknown): string {
  return error instanceof ApiError ? error.message : GENERIC_FAILURE
}

export interface OpsActions {
  /**
   * True when the person may change anything: an owner or an admin of the
   * operations organisation, and never a member (D2187, F9).
   */
  mayAct: boolean
  /**
   * Runs a change, or asks for a fresh sign-in instead of sending it. It
   * resolves either way: a section renders its own failure and never has to
   * tell "refused locally" from "done".
   */
  run(change: () => Promise<unknown>): Promise<void>
}

export interface Submission {
  busy: boolean
  failure: string | null
  submit(change: () => Promise<unknown>): Promise<void>
}

/** One change's state, so a section does not repeat it in every form. */
export function useSubmission(actions: OpsActions): Submission {
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)

  const submit = useCallback(
    async (change: () => Promise<unknown>) => {
      setBusy(true)
      setFailure(null)
      try {
        await actions.run(change)
      } catch (error: unknown) {
        setFailure(sentence(error))
      } finally {
        setBusy(false)
      }
    },
    [actions],
  )

  return { busy, failure, submit }
}
