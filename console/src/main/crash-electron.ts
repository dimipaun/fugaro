// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The crash reporter's wiring: @sentry/electron 8.2.0 behind the gate and the
// scrubber in `./crash` (desktop-console plan Task 2.1, the SDK slice; D733,
// D734; design sections 7.3 and 7.4). This is the **only** file in the console
// that imports the SDK, and the only entry of it that is imported is `main`.
//
// **What the process sends, and what it never does.** The owner's ruling of
// 2026-10-30 is main process only: no renderer reporting, no preload, no new IPC
// channel, no custom protocol, and the CSP unchanged. Three settings are what
// make that true rather than hoped for, and each one is asserted in the unit
// test beside this file:
//
//   - `defaultIntegrations: false` (from `CRASH_CLIENT_OPTIONS`) installs none of
//     the SDK's own integrations, which are exactly the ones that collect the
//     person and the session: `preloadInjectionIntegration` (the preload that
//     would carry a renderer's events to main), `mainProcessSessionIntegration`
//     (the session file under `userData`), and the minidump integrations (native
//     crash dumps, which are process memory rather than stack frames);
//   - `ipcMode` is neither `Classic` nor `Protocol`, so `init` registers no
//     `ipcMain` handler and no `sentry-ipc://` route, and `configureClassic` /
//     `configureProtocol` are both skipped;
//   - `transport` is `makeElectronTransport` rather than the SDK's default
//     `makeElectronOfflineTransport`, whose store writes every envelope it could
//     not deliver to a queue under `<userData>/sentry/queue`. The console keeps
//     no such file: a report that cannot be sent is dropped, which is also what
//     "nothing is written" means for the console's own rule 10.
//
// `beforeSend` is the scrubber, and it is the whole of the payload rule: what
// leaves this process is the event `scrubCrashEvent` returns, which is the
// exception's type and its frames and nothing else.
//
// **The DSN is the build's, never the repository's.** No agent and no commit
// holds one: `npm run build`'s last step (`tools/crash-dsn/`, where the release
// build's variable is spelled) writes the value into the build's own
// `dist/main/`, so a build that was given no such variable produces no artifact
// and initialises nothing. This module reads the artifact back and never writes
// it, and it never looks at the environment: the value the running console would
// find there is whatever launched it, which is not the build's.
//
// **Where a frame's path comes from.** The SDK hands `beforeSend` whatever its
// own stack parser built, and in the main process that is a real file's
// `abs_path`, which `appRoot` cuts to an app-relative path. A frame the SDK
// writes as an `app://` URL is left as it arrives: `scrubCrashEvent` refuses a
// URL it cannot place and answers `<external>`. That is the decision this slice
// makes on purpose -- the `app://` root is the bundle the window loads
// (`<appRoot>/dist/bundle`), not the app root, so re-deriving a filesystem path
// from that URL in `beforeSend` would be this module inventing a path the SDK
// never built. A frame's name and line still reach the processor; only the
// archive-relative file is lost.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import {
  close as closeClient,
  init as initClient,
  makeElectronTransport,
  type ElectronMainOptions,
  type ErrorEvent,
  type IPCMode,
} from '@sentry/electron/main'

import { crashReportingDecision, CRASH_CLIENT_OPTIONS, scrubCrashEvent } from './crash'

/** DSN_ARTIFACT_PATH is the artifact's path inside the app, relative to
 * `app.getAppPath()`. It is `dist/main/` because that is what the build compiles
 * and what `build.files` packs; the JSON and not a `.js` module, because what
 * this module does with it is read a value rather than execute one. */
const DSN_ARTIFACT_PATH = ['dist', 'main', 'crash-dsn.json']

/** MAX_DSN_LENGTH bounds what the artifact may contribute. The file is the
 * build's own, so a value longer than a DSN can be is a file that is not the one
 * the build wrote -- and reading it anyway would put whatever it holds on a
 * request. */
const MAX_DSN_LENGTH = 2048

/** NO_IPC is the SDK's `IPCMode` with neither of its two bits set, and the union
 * has no member for it: `Classic` (1) and `Protocol` (2) are the two ways the
 * SDK listens to renderers, and the console wants neither. Spelled as the number
 * because the enum has no name for "off", and asserted in the unit test, so a
 * later SDK that renamed the members could not quietly change which bits this is. */
const NO_IPC = 0 as IPCMode

/** CrashReportingHandle is what a started reporter hands back: the one thing the
 * caller has to be able to do with it is close it before the process exits, so
 * that the transport's in-flight request is not cut off mid-send. `close`
 * resolves whether or not the client had anything to flush and never rejects --
 * a shutdown path that can throw is a shutdown path that can strand it. */
export interface CrashReportingHandle {
  readonly close: () => Promise<void>
}

/** CrashReportingInput is the caller's side of the gate. `dsn` is the build's
 * artifact when the caller does not supply one; `appRoot` is both the DSN
 * artifact's root and the prefix every kept frame's path is cut at. There is
 * deliberately no switch here beyond `optedIn`, which until the switch slice
 * lands is `null`: nothing has read the installation's setting yet, and D734
 * makes that third state off. */
export interface CrashReportingInput {
  readonly optedIn: unknown
  readonly appRoot: string
  readonly dsn?: string | null
}

/** crashDsnFromBuild reads the DSN the build wrote, and answers null for every
 * way of not having one: no file, a file that is not the shape, an empty string.
 * It never throws -- this runs at startup, and a console that refused to launch
 * because a build artifact was missing would be worse than a console that
 * reports nothing, which is exactly the dormant build. */
export function crashDsnFromBuild(appRoot: string): string | null {
  let raw: string
  try {
    raw = readFileSync(join(appRoot, ...DSN_ARTIFACT_PATH), 'utf8')
  } catch {
    return null
  }
  try {
    const parsed: unknown = JSON.parse(raw)
    if (typeof parsed !== 'object' || parsed === null) return null
    const dsn = (parsed as { readonly dsn?: unknown }).dsn
    if (typeof dsn !== 'string' || dsn.trim() === '' || dsn.length > MAX_DSN_LENGTH) return null
    return dsn
  } catch {
    return null
  }
}

/** toSdkEvent runs the scrubber and hands its result back as the SDK's own event
 * type, because `init`'s `beforeSend` takes an `ErrorEvent` and answers one. The
 * two types differ in one place: `./crash` declares the event it returns with
 * `readonly` fields and arrays -- it builds a fresh event and nothing downstream
 * may change it -- while the SDK's is the mutable core `Event`, plus the
 * `type: undefined` marker that says "an error" rather than a transaction. The
 * value is serialised on its way out and never read for those fields, so the
 * assertion is about two interfaces meeting, and it is kept to this one
 * expression rather than spread across the options. */
function toSdkEvent(event: ErrorEvent, appRoot: string): ErrorEvent {
  return scrubCrashEvent(event, appRoot) as unknown as ErrorEvent
}

/** startCrashReporting asks the gate and, only when it opens, initialises the
 * SDK's main-process client. A closed gate is not an error and not a log line:
 * it is the console's normal state, and the answer the switch slice will change.
 *
 * The options are `CRASH_CLIENT_OPTIONS` corrected against 8.2.0, plus the four
 * this file owns: the DSN, `NO_IPC`, the non-offline transport, and `beforeSend`.
 * `scrubCrashEvent` is handed the event the SDK built and returns the event that
 * goes on the wire. The SDK's `ErrorEvent` differs from the type `./crash`
 * declares by one always-undefined marker, so the wiring restates that rather
 * than casting between the two. */
export function startCrashReporting(input: CrashReportingInput): CrashReportingHandle {
  const dsn = input.dsn ?? crashDsnFromBuild(input.appRoot)
  const decision = crashReportingDecision({ dsn, optedIn: input.optedIn })
  if (!decision.enabled) return { close: () => Promise.resolve() }
  const options: ElectronMainOptions = {
    ...CRASH_CLIENT_OPTIONS,
    dsn: dsn ?? undefined,
    ipcMode: NO_IPC,
    transport: makeElectronTransport,
    beforeSend: (event) => toSdkEvent(event, input.appRoot),
  }
  initClient(options)
  return {
    close: async () => {
      await closeClient().catch(() => false)
    },
  }
}
