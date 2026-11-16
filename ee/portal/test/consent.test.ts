// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// The consent gate (portal plan P1.4; D93).
//
// The two readings worth testing are the ones a page can get wrong in a way
// nobody notices: a visit with **no stored decision** must do nothing at all --
// not "start and hope", and not even read the tag -- and a withdrawal must
// forget the identifiers the tag wrote, because a cookie left in a browser
// keeps identifying somebody who has said no.
//
// Nothing here reaches Google or Firebase: the module under test is the gate,
// and the tag behind it is either a fake or a mocked module.

import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  applyConsent,
  browserConsentStore,
  CONSENT_KEY,
  firebaseAnalyticsClient,
  forgetAnalyticsCookies,
  grantConsent,
  measurementId,
  refuseConsent,
  type AnalyticsClient,
  type ConsentStore,
} from '../src/consent'

/** What the mocked SDK records, so a test can watch it without a network. */
const sdk = vi.hoisted(() => ({
  apps: [] as unknown[],
  loads: 0,
  enabled: [] as boolean[],
}))

vi.mock('firebase/app', () => ({
  getApps: () => sdk.apps,
  getApp: () => ({ name: 'portal' }),
}))

vi.mock('firebase/analytics', () => ({
  getAnalytics: () => {
    sdk.loads += 1
    return { name: 'analytics' }
  },
  setAnalyticsCollectionEnabled: (_analytics: unknown, enabled: boolean) => {
    sdk.enabled.push(enabled)
  },
}))

function fakeStore(stored: string | null = null): ConsentStore & { written: string[] } {
  const written: string[] = []
  let value = stored
  return {
    written,
    read: () => (value === 'granted' || value === 'refused' ? value : null),
    write: (decision) => {
      value = decision
      written.push(decision)
    },
  }
}

function fakeClient(): AnalyticsClient & { started: number; stopped: number } {
  const client = {
    started: 0,
    stopped: 0,
    start: () => {
      client.started += 1
    },
    stop: () => {
      client.stopped += 1
    },
  }
  return client
}

beforeEach(() => {
  sdk.apps = []
  sdk.loads = 0
  sdk.enabled = []
  window.localStorage.clear()
  for (const name of ['_ga', '_ga_ABC', 'fugaro_session']) {
    document.cookie = `${name}=; path=/; expires=Thu, 01 Jan 1970 00:00:00 GMT`
  }
})

describe('the consent gate', () => {
  it('starts nothing and stops nothing without a stored decision', () => {
    const client = fakeClient()
    expect(applyConsent(fakeStore(null), client)).toBeNull()
    expect([client.started, client.stopped]).toEqual([0, 0])
  })

  it('starts on a grant and stops on a refusal', () => {
    const granted = fakeClient()
    expect(applyConsent(fakeStore('granted'), granted)).toBe('granted')
    expect(granted.started).toBe(1)

    const refused = fakeClient()
    expect(applyConsent(fakeStore('refused'), refused)).toBe('refused')
    expect(refused.stopped).toBe(1)
  })

  it('records the decision before it drives the tag', () => {
    const store = fakeStore()
    const client = fakeClient()
    grantConsent(store, client)
    refuseConsent(store, client)
    expect(store.written).toEqual(['granted', 'refused'])
    expect([client.started, client.stopped]).toEqual([1, 1])
  })

  it('reads the decision from the browser, and only that decision', () => {
    const store = browserConsentStore()
    expect(store.read()).toBeNull()

    window.localStorage.setItem(CONSENT_KEY, 'yes')
    expect(store.read()).toBeNull()

    store.write('granted')
    expect(store.read()).toBe('granted')
    expect(window.localStorage.getItem(CONSENT_KEY)).toBe('granted')
  })

  it('takes the measurement id from the build, and nothing for an empty one', () => {
    expect(measurementId({ VITE_FIREBASE_MEASUREMENT_ID: 'G-TEST' })).toBe('G-TEST')
    expect(measurementId({ VITE_FIREBASE_MEASUREMENT_ID: '' })).toBeNull()
    expect(measurementId({})).toBeNull()
  })
})

describe('the analytics tag behind the gate', () => {
  it('does not even download the client in order to switch it off', () => {
    // A withdrawal is the one moment the tag must not be fetched: loading it to
    // tell it to stop would download the script the person has just refused.
    firebaseAnalyticsClient('G-TEST').stop()
    expect(sdk.loads).toBe(0)
    expect(sdk.enabled).toEqual([])
  })

  it('starts the client only when a measurement id is configured', async () => {
    const off = firebaseAnalyticsClient(null)
    off.start()
    expect(sdk.loads).toBe(0)

    sdk.apps = [{}]
    const on = firebaseAnalyticsClient('G-TEST')
    on.start()
    await vi.waitFor(() => {
      expect(sdk.enabled).toEqual([true])
    })
  })

  it('switches a tag that was already loaded back off', async () => {
    sdk.apps = [{}]
    const client = firebaseAnalyticsClient('G-TEST')
    client.start()
    await vi.waitFor(() => {
      expect(sdk.enabled).toEqual([true])
    })
    client.stop()
    await vi.waitFor(() => {
      expect(sdk.enabled).toEqual([true, false])
    })
  })

  it('does nothing when the build has no Firebase app at all', async () => {
    firebaseAnalyticsClient('G-TEST').start()
    await Promise.resolve()
    expect(sdk.enabled).toEqual([])
  })
})

describe('forgetting what the tag wrote', () => {
  it('deletes its own cookies and only its own', () => {
    document.cookie = '_ga=1; path=/'
    document.cookie = '_ga_ABC=2; path=/'
    document.cookie = 'fugaro_session=3; path=/'

    forgetAnalyticsCookies('portal.example.com')

    expect(document.cookie).not.toContain('_ga')
    expect(document.cookie).toContain('fugaro_session=3')
  })

  it('forgets when a withdrawal stops the client', () => {
    document.cookie = '_ga=1; path=/'
    firebaseAnalyticsClient(null).stop()
    expect(document.cookie).not.toContain('_ga')
  })
})
