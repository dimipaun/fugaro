// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.
//
// Error reporting (portal plan P1.4; spec §Architecture, plan constraint "Sentry
// sends no personal data").
//
// **Off without a DSN, and off means off.** A build with no DSN does not load
// the SDK at all: `createTelemetry` answers a reporter that does nothing and
// never imports `@sentry/react`. The portal is deployed to two projects with
// their own Sentry projects (P0.2), and a staging build that reported into
// production's would put one environment's failures in the other's history --
// so the DSN is configuration and the absence of it is a working state.
//
// **No personal data, and no replay.** Three things say that and each is worth
// naming: `sendDefaultPii` is false, so no address, cookie or IP is attached;
// no replay integration is added and both replay rates are zero, so no
// recording of a session is ever uploaded -- a replay of the portal would be a
// recording of somebody's licence keys and their customer list, which is the
// one thing this app must never send anywhere; and `scrubEvent` deletes the
// two fields the SDK would fill in anyway (`user`, `request`), so an event that
// reached Sentry through some other path still carries neither. A token, a key
// and a licence bundle are never passed to `report` in the first place: the
// files that hold them do not import this module.

import type { ErrorEvent as TelemetryErrorEvent, EventHint } from '@sentry/react'

/** What a build tells the reporter about itself. */
export interface TelemetryOptions {
  /** The project's DSN, or null when this build reports nothing. */
  dsn: string | null
  environment: string
  release?: string
}

/**
 * The exact options the SDK is initialised with. It is a named type rather
 * than an inline object because it is what the test asserts on: the fields
 * below are the promise this file makes, and a test reads them by name.
 */
export interface TelemetryInit {
  dsn: string
  environment: string
  release?: string
  sendDefaultPii: false
  tracesSampleRate: 0
  replaysSessionSampleRate: 0
  replaysOnErrorSampleRate: 0
  beforeSend: (event: TelemetryErrorEvent, hint: EventHint) => TelemetryErrorEvent | null
}

/** The SDK, as far as this file needs it. Injected so a test never loads it. */
export interface TelemetryClient {
  init(options: TelemetryInit): void
  captureException(error: unknown): void
}

export interface Telemetry {
  /** Reports a failure. A no-op when this build reports nothing. */
  report(error: unknown): void
}

/**
 * An event with the two fields that carry a person removed. It is exported
 * because it is a rule a reader should be able to test on its own, and because
 * the SDK calls it after the event is built rather than before.
 */
export function scrubEvent<T extends { user?: unknown; request?: unknown }>(event: T): T {
  const scrubbed = { ...event }
  delete scrubbed.user
  delete scrubbed.request
  return scrubbed
}

/** The SDK-backed client, imported only when a DSN is configured. */
async function sentryClient(): Promise<TelemetryClient> {
  const sentry = await import('@sentry/react')
  return {
    init: (options) => {
      sentry.init(options)
    },
    captureException: (error) => {
      sentry.captureException(error)
    },
  }
}

const REPORTING_OFF: Telemetry = { report: () => {} }

export async function createTelemetry(
  options: TelemetryOptions,
  client?: TelemetryClient,
): Promise<Telemetry> {
  if (options.dsn === null || options.dsn === '') {
    return REPORTING_OFF
  }
  const target = client ?? (await sentryClient())
  const init: TelemetryInit = {
    dsn: options.dsn,
    environment: options.environment,
    sendDefaultPii: false,
    tracesSampleRate: 0,
    replaysSessionSampleRate: 0,
    replaysOnErrorSampleRate: 0,
    beforeSend: (event) => scrubEvent(event),
  }
  if (options.release !== undefined) {
    init.release = options.release
  }
  target.init(init)
  return {
    report: (error) => {
      target.captureException(error)
    },
  }
}
