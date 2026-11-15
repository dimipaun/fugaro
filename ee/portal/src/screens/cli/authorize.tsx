// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The authorize page `fugaro licence login` opens in a browser (portal plan
// P3.5; spec §11.3; K2.4 and K3.3 as the portal's reconciliation amends them).
//
// **What the page is for.** The terminal opens it with the port its own
// listener is on, a `state`, a PKCE challenge and the Firebase project id of
// the installation asking for a licence. The person signs in, reads what that
// terminal sent -- as text, because nothing has proved it -- picks which of the
// organisation's unbound licences that installation gets, and hands one over.
// The handover itself is the service's step -- K2.4's authorize route, which
// registers under the portal's API prefix (`POST /api/cli/authorize`, P1.0's
// table): it mints a one-time code and answers with the address of the
// terminal's listener, which this page goes to, so the code reaches the process
// that started this. It is the service's address and not a route of this page's
// building: the prefix is what makes it reachable at all, because this page is
// served from the portal's own origin and Hosting rewrites `/api/**` to the
// service and every other path to the SPA (F21), and the address itself is
// rebuilt there from the port that same request validated. The code is in the
// answer this page reads, which is one step further than a redirect would carry
// it: it is single-use, bound to the PKCE challenge, and spendable only by
// whoever holds the verifier -- which never leaves the terminal (K2.4's
// exchange) -- so a page that read it could do nothing with it the terminal's
// own exchange does not already decide.
//
// **The handover is a navigation, and this page's own policy is why it is not a
// form.** The API answers this step with the address of the terminal's listener
// -- `http://127.0.0.1:<port>/callback` with the one-time code on it -- and the
// page goes there. A form submission cannot: this page is served under Hosting's
// policy, whose `form-action 'self'` a browser checks against where a submission
// ends as well as against the form's own action (the reading K2.4's own page is
// built around, `ee/keyservice`'s `signInRedirectCSP`), so a submission from
// here would be dropped at the loopback and the terminal would wait for a code
// nothing sent. A navigation is not a submission and no policy of the portal's
// governs where it goes, so the handover completes without widening what a form
// on this origin may do -- and the address is the service's own, rebuilt from
// the port it validated, never one this page assembled.
//
// That is also why the token is read at the instant of the call and put in the
// request rather than held in state (`src/auth/session.tsx`).
//
// **A name that arrived in the address is not a name this page trusts.** The
// project id and the port are the caller's text: they are drawn by `Untrusted`
// and nowhere else, the port is turned into the callback address only in the
// shape §11.3 says (the service validates that address again), and the page
// says in words that nothing has verified the name. A page that hid the name,
// or drew it as though the portal had chosen it, would be the phishing surface
// the plan names.
//
// **The licence is bound by this page and the key is minted by the service.**
// Binding is P3.1's route, called with the person's own token as every other
// screen calls it (D2188); the API checks that a Fugaro installation answers in
// that project before it binds (D2205) and the minter signs at the exchange
// (K2.4). So a binding that failed leaves the person on this page with the
// API's own sentence and nothing minted, and the terminal waiting.

import { useEffect, useState } from 'react'
import { useLocation } from 'react-router-dom'
import type { ApiClient } from '../../api/client'
import type { Entitlement } from '../../api/types/entitlements'
import type { AuthPort } from '../../auth/session'
import {
  LicenceChoice,
  UNVERIFIABLE,
  Untrusted,
  bind,
  loopbackCallback,
  readLicences,
  sentence,
  unbound,
} from './flow'
import './cli.css'

const SIGNED_OUT = 'You are signed out. Sign in again to hand the licence over.'

/**
 * What `POST /api/cli/authorize` answers a sign-in that landed: the terminal's
 * own listener with the one-time code on it, which is the address this page goes
 * to. It is the service's `AuthorizeHandover`, rebuilt from the port K2.4's
 * `loopbackRedirect` validated and never from an address this page sent, so the
 * page navigates to a place the terminal chose and the service checked.
 */
export interface AuthorizeHandover {
  redirect: string
}

/** The browser going to an address, which is the whole of what the handover's
 * last step is. */
function browserGo(address: string): void {
  window.location.assign(address)
}

export interface AuthorizeScreenProps {
  /** Built by the route from the session; a test hands in a fake. */
  client: ApiClient
  /** The session's port, for the one token the handover carries. */
  auth: AuthPort
  /**
   * Where the handover goes, which is the browser's own navigation and neither
   * the router's nor a route of this SPA's: the address is the terminal's
   * listener, on another origin. The route hands in nothing and the default is
   * the browser; a suite hands in its own, because jsdom navigates nowhere and
   * its `location.assign` cannot even be read back.
   */
  go?: (address: string) => void
}

export function AuthorizeScreen({ client, auth, go = browserGo }: AuthorizeScreenProps) {
  const location = useLocation()
  const params = new URLSearchParams(location.search)
  const port = params.get('port') ?? ''
  // The terminal sends the port (§11.3) and the service's own page builds the
  // callback from it; a `redirect_uri` is taken as it stands, because that is
  // what a caller that sent one sent and the service validates whichever
  // arrives.
  const callback = params.get('redirect_uri') ?? loopbackCallback(port)
  const state = params.get('state') ?? ''
  const challenge = params.get('code_challenge') ?? ''
  const method = params.get('code_challenge_method') ?? ''
  const projectId = params.get('project_id') ?? ''

  const [licences, setLicences] = useState<Entitlement[] | null>(null)
  // Reading, read, or refused: a refused read is its own state and not an empty
  // list, because a list that failed to arrive is not an organisation that
  // holds no unbound licence, and a page that drew one as the other would hand
  // over a licence the person never chose.
  const [read, setRead] = useState<'reading' | 'done' | 'failed'>('reading')
  const [chosen, setChosen] = useState<string | null>(null)
  const [failure, setFailure] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  // What the service will refuse anyway, refused here as well: a page that drew
  // a handover for parameters it cannot work with would be a page whose only
  // possible answer is a refusal, and the person would read it as their own
  // mistake.
  const carriesTheRequest = callback !== null && state !== '' && challenge !== ''

  useEffect(() => {
    if (!carriesTheRequest) {
      return
    }
    let live = true
    readLicences(client).then(
      (held) => {
        if (live) {
          setLicences(unbound(held))
          setRead('done')
        }
      },
      (error: unknown) => {
        if (live) {
          setFailure(sentence(error))
          setRead('failed')
        }
      },
    )
    return () => {
      live = false
    }
  }, [client, carriesTheRequest])

  const offered = licences ?? []
  // One unbound licence is preselected, because there is nothing to decide;
  // several are left to the person, because a default among equals is a licence
  // handed over by a click somebody did not mean.
  const single = offered.length === 1 ? offered[0] : undefined
  const selected = chosen ?? single?.id ?? null
  const binds = projectId !== ''
  const mustChoose = binds && offered.length > 0 && selected === null

  async function handOver(): Promise<void> {
    setFailure(null)
    setBusy(true)
    try {
      if (binds && selected !== null) {
        await bind(client, selected, projectId)
      }
      const id = await auth.idToken()
      if (id === null) {
        setFailure(SIGNED_OUT)
        return
      }
      const handed = await client.request<AuthorizeHandover>('/cli/authorize', {
        method: 'POST',
        body: {
          redirect_uri: callback ?? '',
          state,
          code_challenge: challenge,
          code_challenge_method: method,
          id_token: id,
        },
      })
      go(handed.redirect)
    } catch (error: unknown) {
      setFailure(sentence(error))
    } finally {
      setBusy(false)
    }
  }

  if (!carriesTheRequest) {
    return (
      <main className="cli">
        <h1>Hand a licence to a terminal</h1>
        <p role="alert">{UNVERIFIABLE}</p>
      </main>
    )
  }

  return (
    <main className="cli">
      <h1>Hand a licence to a terminal</h1>
      <p className="cli__reason">
        The terminal that opened this page is waiting for a licence key. Signing in here hands it
        one.
      </p>

      <section className="cli__who" aria-label="What the terminal sent">
        <h2>The terminal's request</h2>
        {projectId === '' ? (
          <p>
            This terminal sent no Firebase project id, so nothing can be bound here: the login
            hands over the licence your account already runs on.
          </p>
        ) : (
          <p>
            The installation <Untrusted value={projectId} /> is asking for a licence. Nothing has
            verified that name -- it is text the terminal chose -- so check it is the installation
            you mean before handing a key to it.
          </p>
        )}
        <p>
          The key it is handed goes to the terminal listening at <Untrusted value={callback} />.
          If you did not start this, close this page.
        </p>
      </section>

      {failure === null ? null : <p role="alert">{failure}</p>}

      {read === 'reading' ? <p role="status">Loading…</p> : null}
      {read === 'failed' ? <p>Reload this page to try again.</p> : null}

      {read === 'done' ? (
        <div className="cli__handover">
          {binds ? (
            <LicenceChoice licences={offered} chosen={selected} onChoose={setChosen} />
          ) : null}

          <button
            type="button"
            onClick={() => {
              void handOver()
            }}
            disabled={busy || mustChoose}
          >
            {busy ? 'Handing it over…' : 'Hand over the licence'}
          </button>
        </div>
      ) : null}
    </main>
  )
}
