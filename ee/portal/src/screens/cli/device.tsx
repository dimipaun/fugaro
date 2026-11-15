// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The device-approve page, §11.4's no-browser fallback (portal plan P3.5; K2.4
// and K3.3 as the portal's reconciliation amends them).
//
// **The code is typed, and it is never read from the address.** The whole
// defence of this page is an uninterrupted chain of reading: the terminal
// prints a code, the person reads it off that screen and types it here, and the
// page answers with the name of the installation that code belongs to. A page
// that filled the field in from a query parameter -- `?user_code=…` is the
// shape a link would carry -- would let anybody mail a link that arrives with
// the code already in it, and the person would be confirming an installation
// they never looked at. So the field starts empty however the page was
// reached, and the state below is what the person typed and nothing else.
//
// **The installation's name is shown before anything is handed over, as text.**
// It is the caller's string (K2.4 bounds it and escapes it); it is drawn through
// `Untrusted`, and the page says in words that nothing verified it. A person who
// does not recognise it closes the page, which is the case this page exists for.
//
// **The licence is bound here and the key is minted where the poll asks for
// it.** Binding is P3.1's route (the same call the workspace screen makes), so
// the installation's Firebase project id is checked for a real Fugaro
// installation before anything is bound (D2205); the terminal then exchanges the
// code for the signed key (`POST /cli/device/token`, K2.4), which is the step
// that mints. A code nobody polls again therefore leaves no key behind, and the
// page a person leaves open is not a licence handed out.

import { useState } from 'react'
import type { FormEvent } from 'react'
import type { ApiClient } from '../../api/client'
import type { Entitlement } from '../../api/types/entitlements'
import {
  LicenceChoice,
  Untrusted,
  bind,
  readLicences,
  sentence,
  unbound,
} from './flow'
import './cli.css'

/**
 * What `POST /api/cli/device` answers for a code somebody typed -- K2.4's
 * device step registered on the portal's API (P1.0's table, the service's own
 * `DeviceAsk`): the name the requesting installation gave, which is the
 * installation's Firebase project id, the organisation the licence belongs to,
 * and the code as the service read it, so the page shows the code it is about to
 * approve rather than the text that was typed.
 *
 * The route is under `/api` and not at the service's root because this page is
 * served from the portal's own origin and Hosting rewrites `/api/**` to the
 * service and every other path to the SPA (F21): the root route is a page's, and
 * a page's answer is HTML.
 */
export interface DeviceAsk {
  installation: string
  organisation: string
  user_code: string
}

export interface DeviceScreenProps {
  /** Built by the route from the session; a test hands in a fake. */
  client: ApiClient
}

export function DeviceScreen({ client }: DeviceScreenProps) {
  const [typed, setTyped] = useState('')
  const [asking, setAsking] = useState<DeviceAsk | null>(null)
  const [licences, setLicences] = useState<Entitlement[] | null>(null)
  // Reading, read, or refused -- and refused is a state of its own rather than
  // an empty list. The licences here decide which licence the approval binds,
  // so a read that failed and was drawn as "none" would leave the button
  // enabled over a list nobody read, and the approval would hand the
  // installation the licence the account already runs on instead of the one
  // the person meant to choose.
  const [read, setRead] = useState<'reading' | 'done' | 'failed'>('reading')
  const [chosen, setChosen] = useState<string | null>(null)
  const [done, setDone] = useState<DeviceAsk | null>(null)
  const [failure, setFailure] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const offered = licences ?? []
  const single = offered.length === 1 ? offered[0] : undefined
  const selected = chosen ?? single?.id ?? null
  const binds = asking !== null && asking.installation !== ''
  const mustChoose = binds && offered.length > 0 && selected === null

  /** §11.4 step 2: the code is read back and the two records are read with it,
   * because the panel that follows asks which licence this installation gets. */
  async function lookUp(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault()
    setFailure(null)
    setBusy(true)
    try {
      const answer = await client.request<DeviceAsk>('/cli/device', {
        method: 'POST',
        body: { user_code: typed.trim() },
      })
      setAsking(answer)
      // An installation that gave no name has nothing to bind, so there is no
      // choice to read: the login hands over the licence the account runs on.
      if (answer.installation !== '') {
        setLicences(unbound(await readLicences(client)))
      }
      setRead('done')
    } catch (error: unknown) {
      setFailure(sentence(error))
      setRead('failed')
    } finally {
      setBusy(false)
    }
  }

  /** §11.4 step 2's answer: the licence is bound to the installation the code
   * named, and the approval is what the terminal's next poll reads. */
  async function approve(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault()
    if (asking === null) {
      return
    }
    setFailure(null)
    setBusy(true)
    try {
      if (binds && selected !== null) {
        await bind(client, selected, asking.installation)
      }
      await client.request<void>('/cli/device', {
        method: 'POST',
        body: { user_code: asking.user_code, confirm: true },
      })
      setDone(asking)
    } catch (error: unknown) {
      setFailure(sentence(error))
    } finally {
      setBusy(false)
    }
  }

  if (done !== null) {
    return (
      <main className="cli">
        <h1>Done</h1>
        <p>
          {done.installation === '' ? (
            'The installation has been handed a licence'
          ) : (
            <>
              <Untrusted value={done.installation} /> has been handed a licence
            </>
          )}{' '}
          for <strong>{done.organisation}</strong>. You can close this page and return to the
          terminal.
        </p>
      </main>
    )
  }

  return (
    <main className="cli">
      <h1>Approve a sign-in</h1>

      {asking === null ? (
        <>
          <p className="cli__reason">Type the code the terminal is showing.</p>
          <form
            className="cli__code"
            onSubmit={(event) => {
              void lookUp(event)
            }}
          >
            <label htmlFor="cli-user-code">Code</label>
            <input
              id="cli-user-code"
              name="user_code"
              type="text"
              required
              autoComplete="off"
              autoCapitalize="off"
              spellCheck={false}
              value={typed}
              onChange={(event) => {
                setTyped(event.target.value)
              }}
            />
            <button type="submit" disabled={busy}>
              {busy ? 'Reading it…' : 'Continue'}
            </button>
          </form>
        </>
      ) : (
        <form
          className="cli__approve"
          onSubmit={(event) => {
            void approve(event)
          }}
        >
          <p>
            {binds ? (
              <>
                The installation <Untrusted value={asking.installation} /> is asking for a licence
                for <strong>{asking.organisation}</strong>.
              </>
            ) : (
              <>
                An installation that gave no name is asking for a licence for{' '}
                <strong>{asking.organisation}</strong>.
              </>
            )}{' '}
            Nothing has verified that name -- it is text the terminal chose -- so check it is the
            installation you mean before handing a key to it.
          </p>
          <p>
            The code is <Untrusted value={asking.user_code} />. Handing a key over lets that
            terminal run on this account's licence. If you did not start this, close this page.
          </p>

          {read === 'reading' ? <p role="status">Loading…</p> : null}
          {read === 'failed' ? (
            <p>Reload this page and type the code again: nothing has been handed over.</p>
          ) : null}
          {read === 'done' ? (
            <>
              {binds ? (
                <LicenceChoice licences={offered} chosen={selected} onChoose={setChosen} />
              ) : null}
              <button type="submit" disabled={busy || mustChoose}>
                {busy ? 'Handing it over…' : 'Hand over the licence'}
              </button>
            </>
          ) : null}
        </form>
      )}

      {failure === null ? null : <p role="alert">{failure}</p>}
    </main>
  )
}
