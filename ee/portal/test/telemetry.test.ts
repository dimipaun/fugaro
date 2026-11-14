// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// Error reporting (portal plan P1.4; the plan's constraint that Sentry sends no
// personal data, and that it is off without a DSN).
//
// **Off is a state, not a lowered setting.** A build with no DSN must not
// initialise the SDK at all -- not with an empty DSN, not with a DSN and
// sampling turned down -- so the test watches the module the real code imports,
// not a setting it passes.
//
// **What the SDK is initialised with is the promise this file makes**, so it is
// asserted by name: no default PII, no tracing, no replay, and a `beforeSend`
// that removes the two fields that carry a person.

import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  createTelemetry,
  scrubEvent,
  type TelemetryClient,
  type TelemetryInit,
} from '../src/telemetry'

const sentry = vi.hoisted(() => ({
  inits: [] as Record<string, unknown>[],
  captured: [] as unknown[],
}))

vi.mock('@sentry/react', () => ({
  init: (options: Record<string, unknown>) => {
    sentry.inits.push(options)
  },
  captureException: (error: unknown) => {
    sentry.captured.push(error)
  },
}))

beforeEach(() => {
  sentry.inits = []
  sentry.captured = []
})

function fakeClient(): TelemetryClient & { inits: TelemetryInit[] } {
  const client = {
    inits: [] as TelemetryInit[],
    init: (options: TelemetryInit) => {
      client.inits.push(options)
    },
    captureException: () => {},
  }
  return client
}

describe('the reporter', () => {
  it('initialises nothing when the build has no DSN', async () => {
    const telemetry = await createTelemetry({ dsn: null, environment: 'test' })
    telemetry.report(new Error('boom'))
    const empty = await createTelemetry({ dsn: '', environment: 'test' })
    empty.report(new Error('boom'))

    expect(sentry.inits).toEqual([])
    expect(sentry.captured).toEqual([])
  })

  it('never asks the SDK for anything when no DSN is configured', async () => {
    // The injected client is the seam: a build with no DSN must not touch it
    // either, because a reporter that initialised lazily would load the SDK the
    // moment something failed.
    const client = fakeClient()
    const telemetry = await createTelemetry({ dsn: null, environment: 'test' }, client)
    telemetry.report(new Error('boom'))
    expect(client.inits).toEqual([])
  })

  it('initialises the SDK with no personal data and no replay', async () => {
    const client = fakeClient()
    await createTelemetry({ dsn: 'https://public@example.invalid/1', environment: 'staging' }, client)
    expect(client.inits).toHaveLength(1)
    const options = client.inits[0]!
    expect(options.dsn).toBe('https://public@example.invalid/1')
    expect(options.environment).toBe('staging')
    expect(options.sendDefaultPii).toBe(false)
    expect(options.tracesSampleRate).toBe(0)
    expect(options.replaysSessionSampleRate).toBe(0)
    expect(options.replaysOnErrorSampleRate).toBe(0)
  })

  it('reaches the real SDK with the same promise, through the one import', async () => {
    const telemetry = await createTelemetry({
      dsn: 'https://public@example.invalid/1',
      environment: 'production',
      release: 'portal-1',
    })
    expect(sentry.inits).toHaveLength(1)
    const options = sentry.inits[0]!
    expect(options['release']).toBe('portal-1')
    expect(options['sendDefaultPii']).toBe(false)

    const failure = new Error('boom')
    telemetry.report(failure)
    expect(sentry.captured).toEqual([failure])
  })

  it('leaves the release out when the build does not name one', async () => {
    const client = fakeClient()
    await createTelemetry({ dsn: 'https://public@example.invalid/1', environment: 'test' }, client)
    expect(Object.hasOwn(client.inits[0]!, 'release')).toBe(false)
  })

  it('removes the fields that carry a person before an event is sent', async () => {
    const client = fakeClient()
    await createTelemetry({ dsn: 'https://public@example.invalid/1', environment: 'test' }, client)
    const beforeSend = client.inits[0]!.beforeSend as unknown as (event: unknown) => unknown

    const scrubbed = beforeSend({
      message: 'boom',
      user: { email: 'someone@example.invalid' },
      request: { headers: { accept: 'application/json' } },
    }) as Record<string, unknown>

    expect(scrubbed['message']).toBe('boom')
    expect(scrubbed['user']).toBeUndefined()
    expect(scrubbed['request']).toBeUndefined()
  })

  it('scrubs an event without touching anything else in it', () => {
    const event = { message: 'boom', tags: { area: 'billing' }, user: { id: 'uid' } }
    expect(scrubEvent(event)).toEqual({ message: 'boom', tags: { area: 'billing' } })
    expect(event.user).toEqual({ id: 'uid' })
  })
})
