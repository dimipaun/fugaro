// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The device-approve page (§11.4, portal plan P3.5).
//
// **The red step is the code in the address.** This page's whole defence is an
// uninterrupted chain of reading -- the terminal prints a code, the person reads
// it off that screen and types it here, and only then does the page say which
// installation that code belongs to. A page that took the code from a query
// parameter would let anybody mail a link with the code already in it, and the
// person would be approving an installation they never looked at: so the first
// case here opens the page with `?user_code=…` and watches that the field is
// empty and the API is asked nothing at all.
//
// The rest pin what the plan's sentence leaves a reader to take on trust: the
// installation's name is drawn as text and never as markup, the code is sent as
// it was typed and the code the service read is what the approval carries, the
// choice among several licences is the person's, a refusal is the API's own
// sentence and stops the handover, a licence list that could not be read stops
// it too rather than handing over the account's own licence instead, and the
// approval is asked for **before** the binding, so a code that dies between the
// panel and the click leaves the licence unbound.

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { API_BASE } from '../../api/client'
import type { ApiClient } from '../../api/client'
import { DeviceScreen } from './device'
import {
  INSTALLATION,
  ORGANISATION,
  READ_CODE,
  TYPED_CODE,
  alreadyBound,
  pending,
  portalApi,
  secondPending,
} from './testkit'

afterEach(() => {
  cleanup()
  window.history.replaceState({}, '', '/')
})

/** The page, on a router at the address the person was told to open. */
function show(client: ApiClient, at = '/cli/device'): void {
  render(
    <MemoryRouter initialEntries={[at]}>
      <Routes>
        <Route path="/cli/device" element={<DeviceScreen client={client} />} />
      </Routes>
    </MemoryRouter>,
  )
}

function code(): HTMLInputElement {
  return screen.getByLabelText('Code') as HTMLInputElement
}

/** §11.4 step 1: the person types what the terminal shows. */
function type(text: string): void {
  fireEvent.change(code(), { target: { value: text } })
}

function continueOn(): void {
  fireEvent.click(screen.getByRole('button', { name: 'Continue' }))
}

function handOver(): void {
  fireEvent.click(screen.getByRole('button', { name: 'Hand over the licence' }))
}

describe('the device-approve page', () => {
  it('ignores a code that arrived in the address and waits to be typed at', () => {
    const api = portalApi([pending()])
    // The address carries the code in both places a page can read one from:
    // this page is always reached by a full page load, so the window's own
    // address is as real as the router's, and a screen that took either would
    // be a screen a link can fill in.
    window.history.replaceState({}, '', `/cli/device?user_code=${TYPED_CODE}`)
    show(api.client, `/cli/device?user_code=${TYPED_CODE}`)

    expect(code().value).toBe('')
    // Nothing was looked up, nothing is named, and no licence is offered: the
    // page has read nothing from the address at all.
    expect(api.calls).toEqual([])
    expect(screen.queryByRole('button', { name: 'Hand over the licence' })).toBeNull()
    expect(screen.queryByText(/is asking for a licence/)).toBeNull()
  })

  it('reads the code back, binds the licence and approves the installation', async () => {
    const api = portalApi([pending()])
    show(api.client)

    type(TYPED_CODE)
    continueOn()

    // The installation's name and the code the service read, both as text: what
    // the person checks against the terminal in front of them.
    expect((await screen.findByText(INSTALLATION)).tagName).toBe('CODE')
    expect(screen.getByText(READ_CODE).tagName).toBe('CODE')
    expect(screen.getByText(/Nothing has verified that name/)).not.toBeNull()
    expect((screen.getByRole('radio') as HTMLInputElement).checked).toBe(true)

    handOver()

    await waitFor(() => {
      expect(api.approved).toEqual([READ_CODE])
    })
    // The approval is asked for before the binding: the confirmation is the
    // step that can refuse (a code that died after the read above), so a refused
    // one leaves no licence bound to an installation that never got a key.
    expect(api.calls.map((call) => `${call.method} ${call.path}`)).toEqual([
      `POST ${API_BASE}/cli/device`,
      `GET ${API_BASE}/entitlements`,
      `POST ${API_BASE}/cli/device`,
      `POST ${API_BASE}/workspaces/bind`,
    ])
    // The code is typed by a person and read by the service: what was typed
    // goes up, and what came back is what the approval carries.
    expect(api.calls[0]?.body).toEqual({ user_code: TYPED_CODE })
    expect(api.calls[2]?.body).toEqual({ user_code: READ_CODE, confirm: true })
    expect(api.calls[3]?.body).toEqual({
      entitlement_id: 'ent_example',
      project_id: INSTALLATION,
    })
    expect(screen.getByRole('heading', { name: 'Done' })).not.toBeNull()
  })

  it('sends the code as it was typed and spells out no alphabet of its own', async () => {
    const api = portalApi([pending()])
    show(api.client)

    // Lower case, with the group's dash: K2.4 is what normalises a code, and a
    // page that rewrote one would be a second spelling of the rule.
    type('  test-code ')
    continueOn()

    await waitFor(() => {
      expect(api.calls).toHaveLength(2)
    })
    expect(api.calls[0]?.body).toEqual({ user_code: 'test-code' })
  })

  it('leaves the choice to the person when there is one to make', async () => {
    const api = portalApi([pending(), secondPending()])
    show(api.client)

    type(TYPED_CODE)
    continueOn()

    const radios = await screen.findAllByRole('radio')
    expect(radios).toHaveLength(2)
    expect(radios.some((radio) => (radio as HTMLInputElement).checked)).toBe(false)
    const button = screen.getByRole('button', { name: 'Hand over the licence' }) as HTMLButtonElement
    expect(button.disabled).toBe(true)

    fireEvent.click(screen.getByRole('radio', { name: /10 seats/ }))
    expect(button.disabled).toBe(false)
    handOver()

    await waitFor(() => {
      expect(api.approved).toEqual([READ_CODE])
    })
    // The binding the person chose, asked for after the confirmation.
    expect(api.calls[2]?.body).toEqual({ user_code: READ_CODE, confirm: true })
    expect(api.calls[3]?.body).toEqual({
      entitlement_id: 'ent_second',
      project_id: INSTALLATION,
    })
  })

  it('still hands a licence over when nothing is unbound', async () => {
    const api = portalApi([alreadyBound()])
    show(api.client)

    type(TYPED_CODE)
    continueOn()

    expect(
      await screen.findByText(/Every licence this organisation holds is bound/, { exact: false }),
    ).not.toBeNull()
    expect(screen.queryAllByRole('radio')).toHaveLength(0)

    handOver()

    await waitFor(() => {
      expect(api.approved).toEqual([READ_CODE])
    })
    // Nothing was bound, because there was nothing left to bind: the ordinary
    // repeat login is not a dead end.
    expect(api.calls.map((call) => call.method)).toEqual(['POST', 'GET', 'POST'])
  })

  it('hands over without binding when the installation gave no name', async () => {
    const api = portalApi([pending()], { installation: '' })
    show(api.client)

    type(TYPED_CODE)
    continueOn()

    expect(await screen.findByText(/gave no name is asking for a licence/)).not.toBeNull()
    expect(screen.getByText(ORGANISATION)).not.toBeNull()

    handOver()

    await waitFor(() => {
      expect(api.approved).toEqual([READ_CODE])
    })
    // There is no installation to bind to, so the licences are not even read.
    expect(api.calls.map((call) => call.method)).toEqual(['POST', 'POST'])
  })

  it("binds nothing when the code dies before the approval", async () => {
    // The panel is drawn from a live code and the person takes a moment to read
    // the name: the confirmation is what refuses -- the read above was answered
    // -- and the licence must not be bound by it. Binding first would leave the
    // installation bound to a licence no terminal ever got a key for, which only
    // a rebind undoes.
    const api = portalApi([pending()], {
      confirmRefusal: {
        status: 403,
        code: 'device_refused',
        message: 'That code is not one this site is asking for.',
      },
    })
    show(api.client)

    type(TYPED_CODE)
    continueOn()
    expect((await screen.findByText(INSTALLATION)).tagName).toBe('CODE')

    handOver()

    expect((await screen.findByRole('alert')).textContent).toBe(
      'That code is not one this site is asking for.',
    )
    expect(api.approved).toEqual([])
    expect(api.calls.map((call) => call.path)).toEqual([
      `${API_BASE}/cli/device`,
      `${API_BASE}/entitlements`,
      `${API_BASE}/cli/device`,
    ])
    // Nothing was handed over, and the button is still there: the person can
    // click again and the approval and the binding are retried.
    expect(screen.queryByRole('heading', { name: 'Done' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Hand over the licence' })).not.toBeNull()
  })

  it('draws the installation name as text, never as markup', async () => {
    const api = portalApi([pending()], { installation: '<b>not-an-installation</b>' })
    show(api.client)

    type(TYPED_CODE)
    continueOn()

    const name = await screen.findByText('<b>not-an-installation</b>')
    expect(name.tagName).toBe('CODE')
    expect(document.querySelector('b')).toBeNull()
  })

  it("shows the API's refusal for a code it does not know, and offers nothing", async () => {
    const api = portalApi([pending()], {
      refusals: {
        [`POST ${API_BASE}/cli/device`]: {
          status: 404,
          code: 'unknown_code',
          message: 'That code is not one this site is asking for.',
        },
      },
    })
    show(api.client)

    type(TYPED_CODE)
    continueOn()

    expect((await screen.findByRole('alert')).textContent).toBe(
      'That code is not one this site is asking for.',
    )
    expect(screen.queryByRole('button', { name: 'Hand over the licence' })).toBeNull()
    // The code field is still there to type into again.
    expect(code().value).toBe(TYPED_CODE)
  })

  it("shows the API's refusal when the licences cannot be read, and approves nothing", async () => {
    const api = portalApi([pending()], {
      refusals: {
        [`GET ${API_BASE}/entitlements`]: {
          status: 403,
          code: 'no_licence',
          message: 'This account has no licence to hand over.',
        },
      },
    })
    show(api.client)

    type(TYPED_CODE)
    continueOn()

    expect((await screen.findByRole('alert')).textContent).toBe(
      'This account has no licence to hand over.',
    )
    // The name is still shown -- the person can see who was asking -- but there
    // is no handover: a list that failed to arrive is not an organisation that
    // holds nothing, and approving over it would hand the terminal the licence
    // this account runs on instead of the one the person meant to choose.
    expect(screen.getByText(INSTALLATION)).not.toBeNull()
    expect(screen.queryByRole('button', { name: 'Hand over the licence' })).toBeNull()
    expect(api.approved).toEqual([])
  })
})
