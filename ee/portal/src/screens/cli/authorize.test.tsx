// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The authorize page (§11.3, portal plan P3.5).
//
// The plan names one case here -- the terminal's request is read on this page
// and the chosen licence is bound and handed over -- and the rest pin the parts
// of that sentence a reader would otherwise take on trust: which licences are
// offered, that the choice is the person's when there is one to make, that a
// refusal is the API's own sentence and stops the handover, and that the strings
// the terminal sent are drawn as text.
//
// **The handover is a navigation and the case watches for it.** What leaves this
// page for the terminal is the browser going to
// `http://127.0.0.1:<port>/callback` -- the address the service answered, with
// the one-time code on it -- so the screen is handed a `go` of the case's own
// (jsdom navigates nowhere) and the assertion is that it was given the service's
// address exactly once, after the binding and never before it. A form submission
// would carry the same code and could not: this page is served under the
// portal's policy, whose `form-action 'self'` a browser checks against where a
// submission ends (`ee/portal/firebase.json`, pinned by `test/hosting.test.ts`),
// so the case that pins the shape asserts there is no form on this page at all.

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { API_BASE } from '../../api/client'
import type { ApiClient } from '../../api/client'
import { AuthorizeScreen } from './authorize'
import { UNVERIFIABLE } from './flow'
import {
  ID_TOKEN,
  INSTALLATION,
  alreadyBound,
  authPort,
  expired,
  handedOver,
  pending,
  portalApi,
  secondPending,
  signedIn,
} from './testkit'

afterEach(cleanup)

const CALLBACK = 'http://127.0.0.1:53123/callback'
const HANDED = handedOver(CALLBACK, 'state-fixture')

/** The address `fugaro licence login` opens: §11.3's parameters, every one of
 * them a string the terminal chose. */
function address(overrides: Record<string, string> = {}): string {
  const params = new URLSearchParams({
    port: '53123',
    state: 'state-fixture',
    code_challenge: 'challenge-fixture',
    code_challenge_method: 'S256',
    project_id: INSTALLATION,
    ...overrides,
  })
  return `/cli/authorize?${params.toString()}`
}

/** The screen, on a router at the address the terminal opened, with the case's
 * own navigation where the browser's would be. */
function show(client: ApiClient, at = address(), go?: (address: string) => void): void {
  render(
    <MemoryRouter initialEntries={[at]}>
      <Routes>
        <Route
          path="/cli/authorize"
          element={<AuthorizeScreen client={client} auth={authPort(signedIn())} go={go} />}
        />
      </Routes>
    </MemoryRouter>,
  )
}

/** The body of the call the page sends to K2.4's authorize route, or undefined
 * while it has not sent one. */
function handover(api: { calls: { path: string; body: unknown }[] }): unknown {
  const call = api.calls.find((one) => one.path === `${API_BASE}/cli/authorize`)
  return call?.body
}

/**
 * The navigation the handover makes, in place of the browser's: what a case
 * reads is where the page went -- and that address is the service's, because
 * the page has no other way to reach the terminal's listener.
 *
 * A case hands this in and asserts on it, so a screen that navigated nowhere
 * fails the case rather than passing it quietly.
 */
function watched(): { go: (address: string) => void; went: () => string[] } {
  const went: string[] = []
  return {
    go: (address: string) => {
      went.push(address)
    },
    went: () => went,
  }
}

describe('the authorize page', () => {
  it('hands the licence over to the terminal that opened the page', async () => {
    const api = portalApi([pending()])
    const went = watched()
    show(api.client, address(), went.go)

    // The name and the address are the caller's text, and both are drawn as
    // text a person can read against their own terminal.
    expect((await screen.findByText(INSTALLATION)).tagName).toBe('CODE')
    expect(screen.getByText(CALLBACK).tagName).toBe('CODE')

    // Nothing here is a form: the code reaches the terminal by this page going
    // to the address the service answered, and a submission from a page under
    // the portal's own policy would be dropped before the listener saw it.
    expect(document.querySelector('form')).toBeNull()
    // The one licence there is, chosen already: there is nothing to decide.
    expect((screen.getByRole('radio') as HTMLInputElement).checked).toBe(true)

    fireEvent.click(screen.getByRole('button', { name: 'Hand over the licence' }))

    await waitFor(() => {
      expect(went.went()).toEqual([HANDED])
    })
    expect(api.calls).toEqual([
      { path: `${API_BASE}/entitlements`, method: 'GET', body: undefined },
      {
        path: `${API_BASE}/workspaces/bind`,
        method: 'POST',
        body: { entitlement_id: 'ent_example', project_id: INSTALLATION },
      },
      {
        path: `${API_BASE}/cli/authorize`,
        method: 'POST',
        body: {
          redirect_uri: CALLBACK,
          state: 'state-fixture',
          code_challenge: 'challenge-fixture',
          code_challenge_method: 'S256',
          // The token is read at the instant of the call and put in the request,
          // so it travels in it and is never held on the page.
          id_token: ID_TOKEN,
        },
      },
    ])
  })

  it('offers the licences nothing has bound, and none that is over', async () => {
    const api = portalApi([pending(), alreadyBound(), expired()])
    show(api.client)

    const radios = await screen.findAllByRole('radio')
    expect(radios.map((radio) => radio.getAttribute('value'))).toEqual(['ent_example'])
  })

  it('leaves the choice to the person when there is one to make', async () => {
    const api = portalApi([pending(), secondPending()])
    const went = watched()
    show(api.client, address(), went.go)

    const radios = await screen.findAllByRole('radio')
    expect(radios).toHaveLength(2)
    expect(radios.some((radio) => (radio as HTMLInputElement).checked)).toBe(false)
    // Nothing is preselected, so nothing may be handed over before the person
    // picks: a default among equals is a licence handed over by a click they
    // did not mean.
    expect((screen.getByRole('button', { name: 'Hand over the licence' }) as HTMLButtonElement).disabled).toBe(true)

    // Chosen by what it says, which is what the person reads: the id never
    // reaches them.
    const annual = screen.getByRole('radio', { name: /10 seats/ })
    expect(annual.getAttribute('value')).toBe('ent_second')
    fireEvent.click(annual)
    const button = screen.getByRole('button', { name: 'Hand over the licence' }) as HTMLButtonElement
    expect(button.disabled).toBe(false)
    fireEvent.click(button)

    await waitFor(() => {
      expect(went.went()).toEqual([HANDED])
    })
    expect(api.calls[1]?.body).toEqual({
      entitlement_id: 'ent_second',
      project_id: INSTALLATION,
    })
  })

  it('hands the account its own licence over when there is nothing to bind', async () => {
    const api = portalApi([pending()])
    const went = watched()
    show(api.client, address({ project_id: '' }), went.go)

    // A terminal that named no installation has nothing to bind, so there is no
    // licence to choose: the login hands over the one the account runs on, and
    // the page says exactly that rather than offering a choice it cannot make.
    expect(
      await screen.findByText(/sent no Firebase project id/, { exact: false }),
    ).not.toBeNull()
    expect(screen.queryAllByRole('radio')).toHaveLength(0)

    fireEvent.click(screen.getByRole('button', { name: 'Hand over the licence' }))

    await waitFor(() => {
      expect(went.went()).toEqual([HANDED])
    })
    // No binding was asked for, because nothing named an installation to bind
    // to: the login used what the account already runs on.
    expect(api.calls.map((call) => call.path)).toEqual([
      `${API_BASE}/entitlements`,
      `${API_BASE}/cli/authorize`,
    ])
  })

  it('still hands a licence over when nothing is unbound', async () => {
    const api = portalApi([alreadyBound()])
    const went = watched()
    show(api.client, address(), went.go)

    expect(
      await screen.findByText(/Every licence this organisation holds is bound/, { exact: false }),
    ).not.toBeNull()
    expect(screen.queryAllByRole('radio')).toHaveLength(0)

    fireEvent.click(screen.getByRole('button', { name: 'Hand over the licence' }))

    await waitFor(() => {
      expect(went.went()).toEqual([HANDED])
    })
    // The ordinary repeat login: this installation already holds a licence, so
    // there is nothing left to bind and nothing was asked for beyond the read.
    expect(api.calls.map((call) => call.path)).toEqual([
      `${API_BASE}/entitlements`,
      `${API_BASE}/cli/authorize`,
    ])
  })

  it("shows the API's refusal and submits nothing when the binding fails", async () => {
    const api = portalApi([pending()], {
      refusals: {
        [`POST ${API_BASE}/workspaces/bind`]: {
          status: 409,
          code: 'already_bound',
          message: 'That licence belongs to another installation already.',
        },
      },
    })
    const went = watched()
    show(api.client, address(), went.go)

    fireEvent.click(await screen.findByRole('button', { name: 'Hand over the licence' }))

    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toBe('That licence belongs to another installation already.')
    // The failure is the API's sentence and nothing was handed over: no code was
    // minted, the sign-in was never sent, and the page stayed where it was.
    expect(went.went()).toEqual([])
    expect(handover(api)).toBeUndefined()
  })

  it('draws a name that arrived in the address as text, never as markup', async () => {
    const api = portalApi([pending()])
    show(api.client, address({ project_id: '<b>not-an-installation</b>' }))

    const name = await screen.findByText('<b>not-an-installation</b>')
    expect(name.tagName).toBe('CODE')
    expect(document.querySelector('b')).toBeNull()
  })

  it("hands the caller's own redirect_uri to the service as it stands", async () => {
    // The page does not decide this one: it shows the address to the person and
    // passes it on, and K2.4's `loopbackRedirect` refuses anything that is not
    // the loopback. So a hostile address is visible here and useless there --
    // and a page that quietly replaced it would be hiding what the terminal
    // asked for from the person reading this screen.
    const api = portalApi([pending()])
    const went = watched()
    show(api.client, address({ redirect_uri: 'https://elsewhere.example/callback' }), went.go)

    const shown = await screen.findByText('https://elsewhere.example/callback')
    expect(shown.tagName).toBe('CODE')

    fireEvent.click(screen.getByRole('button', { name: 'Hand over the licence' }))

    expect((await screen.findByRole('alert')).textContent).toBe(UNVERIFIABLE)
    expect(handover(api)).toMatchObject({
      redirect_uri: 'https://elsewhere.example/callback',
    })
    // The service refused it, so there is no code and no address to go to: the
    // page goes nowhere at all.
    expect(went.went()).toEqual([])
  })

  it('refuses a request it has no state or challenge for, and asks the API nothing', async () => {
    const api = portalApi([pending()])
    show(api.client, address({ state: '' }))

    expect((await screen.findByRole('alert')).textContent).toBe(
      'This sign-in could not be verified. Return to your terminal and try again.',
    )
    expect(screen.queryByRole('button', { name: 'Hand over the licence' })).toBeNull()
    expect(api.calls).toEqual([])
  })

  it('refuses a port that is not one, rather than building an address from it', async () => {
    const api = portalApi([pending()])
    show(api.client, address({ port: '70000' }))

    expect((await screen.findByRole('alert')).textContent).toBe(
      'This sign-in could not be verified. Return to your terminal and try again.',
    )
    expect(api.calls).toEqual([])
  })

  it("shows the API's refusal when the licences cannot be read, and hands nothing over", async () => {
    const api = portalApi([], {
      refusals: {
        [`GET ${API_BASE}/entitlements`]: {
          status: 403,
          code: 'no_licence',
          message: 'This account has no licence to hand over.',
        },
      },
    })
    const went = watched()
    show(api.client, address(), went.go)

    expect((await screen.findByRole('alert')).textContent).toBe(
      'This account has no licence to hand over.',
    )
    // A list that failed to arrive is not an organisation that holds nothing:
    // the page says so and offers no handover, rather than handing over a
    // licence the person never chose.
    expect(screen.queryByRole('button', { name: 'Hand over the licence' })).toBeNull()
    expect(screen.getByText(/Reload this page to try again/)).not.toBeNull()
    expect(went.went()).toEqual([])
    expect(handover(api)).toBeUndefined()
  })
})
