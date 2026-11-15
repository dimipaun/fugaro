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
// **The handover is a form submission and the case watches for it.** What leaves
// this page for the terminal is a browser following the service's redirect to
// `http://127.0.0.1:<port>/callback`, and the one mechanism that reaches a
// loopback listener is the browser submitting the form -- so `submit()` is
// spied on and the assertion is that it was called exactly once, after the
// binding and never before it. A case that only counted the API calls would not
// see the submission at all, which is the half of this page that carries the
// credential.

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { API_BASE } from '../../api/client'
import type { ApiClient } from '../../api/client'
import { AuthorizeScreen } from './authorize'
import {
  ID_TOKEN,
  INSTALLATION,
  alreadyBound,
  authPort,
  expired,
  pending,
  portalApi,
  secondPending,
  signedIn,
} from './testkit'

afterEach(cleanup)

const CALLBACK = 'http://127.0.0.1:53123/callback'

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

/** The screen, on a router at the address the terminal opened. */
function show(client: ApiClient, at = address()): void {
  render(
    <MemoryRouter initialEntries={[at]}>
      <Routes>
        <Route
          path="/cli/authorize"
          element={<AuthorizeScreen client={client} auth={authPort(signedIn())} />}
        />
      </Routes>
    </MemoryRouter>,
  )
}

function handover(): HTMLFormElement {
  const button = screen.getByRole('button', { name: 'Hand over the licence' })
  const form = button.closest('form')
  if (form === null) {
    throw new Error('the handover button is not inside a form')
  }
  return form
}

/** What a hidden field carries, which is what the service will read. */
function field(form: HTMLFormElement, name: string): string {
  const input = form.elements.namedItem(name)
  if (!(input instanceof HTMLInputElement)) {
    throw new Error(`the handover form has no field named ${name}`)
  }
  return input.value
}

/**
 * The submission the browser would make. jsdom navigates nowhere, so what a
 * case reads is what the form was carrying at the instant the page told the
 * browser to submit it: React renders again after that call, and a token read
 * from the field later is not necessarily the one that travelled.
 */
function watchSubmit(): { calls: () => number; token: () => string } {
  let calls = 0
  let token = ''
  vi.spyOn(HTMLFormElement.prototype, 'submit').mockImplementation(function (
    this: HTMLFormElement,
  ) {
    calls += 1
    token = field(this, 'id_token')
  })
  return { calls: () => calls, token: () => token }
}

describe('the authorize page', () => {
  it('hands the licence over to the terminal that opened the page', async () => {
    const api = portalApi([pending()])
    const submitted = watchSubmit()
    show(api.client)

    // The name and the address are the caller's text, and both are drawn as
    // text a person can read against their own terminal.
    expect((await screen.findByText(INSTALLATION)).tagName).toBe('CODE')
    expect(screen.getByText(CALLBACK).tagName).toBe('CODE')

    const form = handover()
    expect(form.getAttribute('action')).toBe(`${API_BASE}/cli/authorize`)
    expect(form.getAttribute('method')).toBe('post')
    expect(field(form, 'redirect_uri')).toBe(CALLBACK)
    expect(field(form, 'state')).toBe('state-fixture')
    expect(field(form, 'code_challenge')).toBe('challenge-fixture')
    expect(field(form, 'code_challenge_method')).toBe('S256')
    // The one licence there is, chosen already: there is nothing to decide.
    expect((screen.getByRole('radio') as HTMLInputElement).checked).toBe(true)

    fireEvent.click(screen.getByRole('button', { name: 'Hand over the licence' }))

    await waitFor(() => {
      expect(submitted.calls()).toBe(1)
    })
    expect(api.calls).toEqual([
      { path: `${API_BASE}/entitlements`, method: 'GET', body: undefined },
      {
        path: `${API_BASE}/workspaces/bind`,
        method: 'POST',
        body: { entitlement_id: 'ent_example', project_id: INSTALLATION },
      },
    ])
    // The token goes into the form at the instant it is submitted, so it
    // travels in the submission and is never held on the page.
    expect(submitted.token()).toBe(ID_TOKEN)
  })

  it('offers the licences nothing has bound, and none that is over', async () => {
    const api = portalApi([pending(), alreadyBound(), expired()])
    show(api.client)

    const radios = await screen.findAllByRole('radio')
    expect(radios.map((radio) => radio.getAttribute('value'))).toEqual(['ent_example'])
  })

  it('leaves the choice to the person when there is one to make', async () => {
    const api = portalApi([pending(), secondPending()])
    const submitted = watchSubmit()
    show(api.client)

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
      expect(submitted.calls()).toBe(1)
    })
    expect(api.calls[1]?.body).toEqual({
      entitlement_id: 'ent_second',
      project_id: INSTALLATION,
    })
  })

  it('hands the account its own licence over when there is nothing to bind', async () => {
    const api = portalApi([pending()])
    const submitted = watchSubmit()
    show(api.client, address({ project_id: '' }))

    // A terminal that named no installation has nothing to bind, so there is no
    // licence to choose: the login hands over the one the account runs on, and
    // the page says exactly that rather than offering a choice it cannot make.
    expect(
      await screen.findByText(/sent no Firebase project id/, { exact: false }),
    ).not.toBeNull()
    expect(screen.queryAllByRole('radio')).toHaveLength(0)

    fireEvent.click(screen.getByRole('button', { name: 'Hand over the licence' }))

    await waitFor(() => {
      expect(submitted.calls()).toBe(1)
    })
    // No binding was asked for, because nothing named an installation to bind
    // to: the login used what the account already runs on.
    expect(api.calls.map((call) => call.method)).toEqual(['GET'])
  })

  it('still hands a licence over when nothing is unbound', async () => {
    const api = portalApi([alreadyBound()])
    const submitted = watchSubmit()
    show(api.client)

    expect(
      await screen.findByText(/Every licence this organisation holds is bound/, { exact: false }),
    ).not.toBeNull()
    expect(screen.queryAllByRole('radio')).toHaveLength(0)

    fireEvent.click(screen.getByRole('button', { name: 'Hand over the licence' }))

    await waitFor(() => {
      expect(submitted.calls()).toBe(1)
    })
    // The ordinary repeat login: this installation already holds a licence, so
    // there is nothing left to bind and nothing was asked for beyond the read.
    expect(api.calls.map((call) => call.method)).toEqual(['GET'])
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
    const submitted = watchSubmit()
    show(api.client)

    fireEvent.click(await screen.findByRole('button', { name: 'Hand over the licence' }))

    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toBe('That licence belongs to another installation already.')
    // The failure is the API's sentence and nothing was handed over: no code
    // was minted, and no token left this page.
    expect(submitted.calls()).toBe(0)
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
    show(api.client, address({ redirect_uri: 'https://elsewhere.example/callback' }))

    const shown = await screen.findByText('https://elsewhere.example/callback')
    expect(shown.tagName).toBe('CODE')
    expect(field(handover(), 'redirect_uri')).toBe('https://elsewhere.example/callback')
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
    const submitted = watchSubmit()
    show(api.client)

    expect((await screen.findByRole('alert')).textContent).toBe(
      'This account has no licence to hand over.',
    )
    // A list that failed to arrive is not an organisation that holds nothing:
    // the page says so and offers no handover, rather than handing over a
    // licence the person never chose.
    expect(screen.queryByRole('button', { name: 'Hand over the licence' })).toBeNull()
    expect(screen.getByText(/Reload this page to try again/)).not.toBeNull()
    expect(submitted.calls()).toBe(0)
  })
})
