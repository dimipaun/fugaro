// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The crash reporter's wiring: @sentry/electron 8.2.0 behind the gate and the
// scrubber in `./crash` (desktop-console plan Task 2.1, the SDK slice; the
// owner's rulings D733 and D734 of 2026-10-30, which design section 7.4 does
// not describe yet -- it still carries the promise they supersede, and Task 2.2
// amends it). This is the **only** file in the console
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
//     crash dumps, which are process memory rather than stack frames). The
//     captures this file installs by hand are the two in `integrations` below --
//     an uncaught exception and an unhandled rejection -- and there is no third;
//   - `ipcMode` is neither `Classic` nor `Protocol`, so `init` registers no
//     `ipcMain` handler and no `sentry-ipc://` route, and `configureClassic` /
//     `configureProtocol` are both skipped;
//   - `transport` is `makeElectronTransport` rather than the SDK's default
//     `makeElectronOfflineTransport`, whose store writes every envelope it could
//     not deliver to a queue under `<userData>/sentry/queue`. The console keeps
//     no such file: a report that cannot be sent is dropped, which is also what
//     "nothing is written" means for the console's own rule 10.
//
// **And one thing the SDK does that no setting covers.** `init` patches
// `electron.utilityProcess.fork` on every start -- `sdk.js` calls
// `configureUtilityProcessIPC()` unconditionally, and no option and no
// integration refuses it -- so the wiring takes that patch back off immediately
// after `init` returns (see `startCrashReporting`). It is the fourth mechanism
// and the only one that is a restore rather than a setting: the patched fork
// forwards what a child process sends with `client.sendEnvelope`, which is the
// one path in this SDK that does not pass `beforeSend`.
//
// `beforeSend` is the scrubber, and it is the whole of the payload rule: what
// leaves this process is exactly the event `scrubCrashEvent` returns, which is
// two things and no more. The **report** is the exception's type and its frames
// -- function, file, line, column, `in_app`. The **metadata** beside it is what
// `ScrubbedCrashEvent` declares: `event_id`, `timestamp`, `platform`, `level`
// and `release`. A reader checking the payload rule should read that interface
// rather than this paragraph, and it is the complete list: nothing else on the
// SDK's event survives the scrubber, and no breadcrumb, user, request, context or
// extra is among them.
//
// **The DSN is one public constant, not a secret and not a build input.** It is
// `./crash-dsn`'s `CONSOLE_CRASH_DSN`, embedded by the owner's ruling of
// 2026-10-31: a Sentry DSN can only send events, so it is a value a repository
// may hold and a shipped app cannot hide anyway. Nothing is built, written or
// read for it at run time, and this module never looks at the environment -- a
// DSN the running console found there would be whatever launched it rather than
// the project the console reports to.
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

import { utilityProcess } from 'electron'

import {
  close as closeClient,
  init as initClient,
  makeElectronTransport,
  onUncaughtExceptionIntegration,
  onUnhandledRejectionIntegration,
  type ElectronMainOptions,
  type ErrorEvent,
  type IPCMode,
} from '@sentry/electron/main'

import { crashReportingDecision, CRASH_CLIENT_OPTIONS, scrubCrashEvent } from './crash'
import { CONSOLE_CRASH_DSN } from './crash-dsn'

/** CLOSE_TIMEOUT_MS is how long `close` waits for what the transport has in
 * flight before giving up on it. It is the wiring's number rather than the
 * SDK's: the client's own `close` has no timeout of its own to pass on, and the
 * electron-net transport's `flush` -- the thing that would be waited on -- has
 * none either, so a `close()` that named no bound would wait for the send to
 * settle, however long that takes. `index.ts` holds `before-quit` for this
 * promise, so an unbounded one is a console that never quits when the reporting
 * endpoint is the thing that is broken -- which is the state a crash reporter is
 * most likely to be in when somebody quits the app. Two seconds is long enough
 * for a request already on the wire and short enough that a person does not
 * notice the quit. */
export const CLOSE_TIMEOUT_MS = 2000

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

/** CrashReportingInput is the caller's side of the gate. `dsn` is the embedded
 * constant when the caller does not supply one, and it exists for the tests and
 * the e2e fixture, which point a real client at a sink of their own; `appRoot`
 * is the prefix every kept frame's path is cut at. There is deliberately no
 * switch here beyond `optedIn`, which until the switch slice lands is `null`:
 * nothing has read the per-user switch yet, and D734 makes that third state
 * off. */
export interface CrashReportingInput {
  readonly optedIn: unknown
  readonly appRoot: string
  readonly dsn?: string | null
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
 * The switch is the gate's whole input, and it is an allow test, so a state
 * nobody has defined yet -- the `null` this slice is called with, and whatever
 * the switch slice adds beside `true` -- is off without this file having to know
 * its name. `index.ts` asks the same question (`optedIn === true`) before it
 * decides to load this module at all.
 *
 * The options are `CRASH_CLIENT_OPTIONS` corrected against 8.2.0, plus the four
 * this file owns: the DSN, `NO_IPC`, the non-offline transport, and `beforeSend`.
 * `scrubCrashEvent` is handed the event the SDK built and returns the event that
 * goes on the wire, through `toSdkEvent` above -- the one assertion in this
 * wiring, and the one place a reader auditing the hand-off should look.
 *
 * **Errors only, and explicitly.** The client sends error events, and the two
 * other kinds of payload a Sentry client can be handed are refused here rather
 * than left to a default: `beforeSendTransaction` answers `null` for every
 * transaction (a trace is a record of *how long the session ran and what it did*
 * in that time and D733's rule is stack frames only), and `beforeSendLog`
 * answers `null` for every log line for the same reason a log line is the wrong
 * shape for this rule: it is free text the console wrote about what it was
 * doing. `beforeSend` above is the third and the one that matters most.
 *
 * **What actually keeps transactions out today, and why it is not that line.**
 * The pinned `@sentry/core` 11.7.0 -- the version `@sentry/electron` 8.2.0
 * depends on, and the one that implements this callback -- ignores
 * `beforeSendTransaction` unless `traceLifecycle` is
 * `'static'`; it streams spans by default and says so out loud -- setting the
 * option makes the client log "`beforeSendTransaction` ... ignored with
 * `traceLifecycle: 'stream'`". The callback stays because it is the option a
 * switch to `'static'` would need, and because writing it costs nothing, but a
 * reader must not take it for the guard. The guard is that nothing in this
 * client opens a span: `defaultIntegrations: false` installs no tracing
 * integration and this module starts none by hand. The replacement the SDK
 * points at is not one either -- `beforeSendSpan`'s callback returns a span and
 * cannot answer `null`, so it cannot drop one, and `ignoreSpans` matches a
 * span's description or op, so a span with neither passes it. A later task that
 * adds tracing to this client therefore owes an errors-only decision rather than
 * a new line beside these; the unit test pins both mechanisms in the types of
 * the pinned version.
 *
 * `enableLogs` is deliberately *not* among them: the pinned 8.2.0 does not have
 * it. `@sentry/electron`'s renderer JSDoc says logs "require `enableLogs` to be
 * enabled in the Electron main process", but neither `ElectronMainOptions`,
 * `@sentry/node`'s `NodeOptions` nor `@sentry/core`'s `Options` declares that
 * key, and the main process has no such gate to switch -- so writing it here
 * would be an untyped key that type-checked nowhere and did nothing.
 * `beforeSendLog` is the control the version actually has, and the client sends
 * no log without it answering, which is the behaviour the missing option was
 * wanted for; the unit test pins all three names against the SDK's types. */
export function startCrashReporting(input: CrashReportingInput): CrashReportingHandle {
  // The switch is the gate's whole input, and `crashReportingDecision` is asked
  // rather than tested inline so that the reason it answers is a word the same
  // module spells for both this wiring and `./crash`'s own test. Nothing else
  // can close the gate: the DSN is a constant of this build rather than an input
  // that can be missing, so a yes is a yes.
  const decision = crashReportingDecision({ optedIn: input.optedIn })
  if (!decision.enabled) return { close: () => Promise.resolve() }
  const options: ElectronMainOptions = {
    ...CRASH_CLIENT_OPTIONS,
    dsn: input.dsn ?? CONSOLE_CRASH_DSN,
    ipcMode: NO_IPC,
    transport: makeElectronTransport,
    beforeSend: (event) => toSdkEvent(event, input.appRoot),
    beforeSendTransaction: () => null,
    beforeSendLog: () => null,
    // **The two ways a crash becomes a report, and the only two.** With
    // `defaultIntegrations: false` a client that is handed no list captures
    // nothing at all -- `captureException` still works, but nothing calls it --
    // so the console names the two it wants by hand, and nothing else is added
    // beside them: no minidump (process memory, not stack frames), no session, no
    // preload and no breadcrumb integration, which is the same list the header
    // gives. Both are the SDK's own, unmodified, and neither takes an option that
    // would let it collect anything more.
    //
    // **Neither of them decides this process's exit.** The Electron SDK's
    // `onUncaughtExceptionIntegration` takes no options at all -- it is not
    // `@sentry/node`'s, whose `exitEvenIfOtherHandlersAreRegistered` and
    // `onFatalError` are what let a reporter call `process.exit` -- so what
    // happens to the process after an uncaught error is exactly what happened
    // before this file existed: Electron's own handler, and the console's
    // `reportAndQuit` for the paths it owns. The unit test pins that neither of
    // those two options is set anywhere in these options, and that the installed
    // integration registers a listener and nothing else.
    //
    // `mode: 'none'` is the same decision for a rejected promise: the integration
    // exists to *capture* it, and the two modes that would do more (`'warn'`
    // writes Node's warning to the console, `'strict'` calls
    // `logAndExitProcess`) are exactly the process-wide behaviour this console
    // leaves alone. Its default is `'warn'`, so this is a choice and not a
    // silence.
    integrations: [onUncaughtExceptionIntegration(), onUnhandledRejectionIntegration({ mode: 'none' })],
  }
  // `configureUtilityProcessIPC`, which the pinned `init` calls on every start
  // whatever the IPC mode, replaces `electron.utilityProcess.fork` with a Proxy
  // that opens a port to the child process and forwards what the child sends
  // with `client.sendEnvelope` -- the one path in this SDK that does not pass
  // `beforeSend`. No option and no integration turns it off, so the patch is
  // taken back off here, immediately: the console forks no utility process
  // today, and a global left patched for the whole run is a surface this wiring
  // did not ask for and cannot see. It is put back only if `init` changed it,
  // and only when there is a `utilityProcess` to put it back on.
  const forked = utilityProcess?.fork
  initClient(options)
  if (forked !== undefined && utilityProcess !== undefined && utilityProcess.fork !== forked) {
    utilityProcess.fork = forked
  }
  return {
    close: async () => {
      await closeClient(CLOSE_TIMEOUT_MS).catch(() => false)
    },
  }
}
