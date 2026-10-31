// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The console's crash reporter: the gate and the scrubber, with nothing wired to
// either yet (desktop-console plan Task 2.1, first slice; D733, D734; design
// sections 7.3 and 7.4).
//
// The owner's ruling of 2026-10-30 is that the console gains a crash reporter
// now and ships **dormant**: nothing is sent, because no DSN is configured, and
// the switch is **opt-in** -- off unless the installation's setting says on. The
// window before that setting has been read is a third state rather than a slow
// "off" (D734): in it, nothing is sent either, which is the whole of the
// promise that an opted-out operator never sends one report.
//
// F33 is why the payload is an **allow-list**. A Go panic value, an Electron
// error message and a JS exception string all carry repository names, branch
// names, task text, URLs or a credential, and this repository's own redactor is
// best-effort by its comment -- so a report here is the exception's type and its
// frames, and the message is dropped rather than filtered.
//
// The module is therefore pure and static, and it is deliberately importless:
// it reads no environment, opens no file, makes no call, and holds no DSN, no
// host and no endpoint. The DSN arrives with the SDK slice as a build-time value
// the owner sets and nobody commits. The types below are structural for the same
// reason -- the SDK slice hands `scrubCrashEvent` the very event its own
// `beforeSend` receives, and sends back what it returns, with no cast between.

/** CrashOptIn is the operator's switch as this module can see it: `true` when
 * the installation's setting allows reports, `false` when it forbids them, and
 * `null` while nothing has read it -- not asked yet, unreadable, or answered by
 * a CLI too old to carry the field. The third value is D734's rule made
 * expressible: a console that folded `null` into `false` could not tell "off"
 * from "not known yet", and the two have the same answer here for exactly as
 * long as it takes something to read the setting. */
export type CrashOptIn = boolean | null

/** CrashReportingInput is everything the gate is allowed to look at: the DSN the
 * build was given, and the switch. There is deliberately nothing else -- no
 * environment, no clock, no state -- so the decision cannot depend on anything
 * the SDK slice has to remember to pass. */
export interface CrashReportingInput {
  readonly dsn?: string | null
  readonly optedIn: CrashOptIn
}

/** CrashReportingReason is the word a log line gets, and it is a word rather
 * than the DSN: a reason ends up pasted into an issue, and a DSN is a URL with a
 * key in it. One word per way of being off, because "off" alone would leave the
 * dormant build indistinguishable from a build whose DSN the owner never set. */
export type CrashReportingReason = 'enabled' | 'opt-in-not-read' | 'opt-in-off' | 'no-dsn'

export interface CrashReportingDecision {
  readonly enabled: boolean
  readonly reason: CrashReportingReason
}

/**
 * crashReportingDecision is the gate, as a question asked afresh each time it
 * matters: the setting can change, and a reporter started under an open gate
 * must not keep sending under a closed one. The switch is read first, so a
 * console that has not read the setting says so rather than blaming the build
 * for the DSN it was never asked about.
 *
 * A DSN that is only whitespace is no DSN: the value comes from a build the
 * owner configures, and an empty one is what an unset variable looks like.
 */
export function crashReportingDecision(input: CrashReportingInput): CrashReportingDecision {
  if (input.optedIn === null) return { enabled: false, reason: 'opt-in-not-read' }
  if (!input.optedIn) return { enabled: false, reason: 'opt-in-off' }
  const dsn = input.dsn
  if (typeof dsn !== 'string' || dsn.trim() === '') return { enabled: false, reason: 'no-dsn' }
  return { enabled: true, reason: 'enabled' }
}

/**
 * CRASH_CLIENT_OPTIONS is what the SDK slice passes to the client's `init`
 * beside the DSN, and each entry is a decision rather than a default:
 *
 *   - nothing is collected that is about the *person* -- no default PII, no
 *     breadcrumb trail, and no local variables off the stack, which is where a
 *     credential is most likely to be sitting when something throws;
 *   - nothing is collected that is about the *session* -- no session tracking
 *     and no client reports, so the service receives error events and no
 *     account of who was running the app or for how long;
 *   - nothing is added to the events -- no automatic stacktrace attached to a
 *     message that would then be sent, and no default integrations, because the
 *     integrations are the ones that collect what the three lines above refuse.
 *
 * What is left out beyond this object is not an option of the client: **native
 * crash minidumps** (they are process memory, and D733's "stack frames only" is
 * not true of them) and **the renderer** (main process only, with no preload and
 * no IPC surface added, so renderer errors are not reported at all in v1). Both
 * belong to the wiring, like the DSN and the switch.
 *
 * The names are the SDK's own and are not checked here, since this module
 * imports nothing: the slice that adds the dependency confirms each one against
 * the pinned version before it initialises anything.
 */
export const CRASH_CLIENT_OPTIONS = Object.freeze({
  sendDefaultPii: false,
  autoSessionTracking: false,
  sendClientReports: false,
  attachStacktrace: false,
  maxBreadcrumbs: 0,
  includeLocalVariables: false,
  defaultIntegrations: false,
})

/** CrashFrame is one frame as the reporter hands it over: everything the
 * scrubber may read, and the rest of the SDK's frame as well, which is what the
 * optional fields leave room for. */
export interface CrashFrame {
  readonly function?: string
  readonly filename?: string
  readonly abs_path?: string
  readonly lineno?: number
  readonly colno?: number
  readonly in_app?: boolean
  readonly context_line?: string
  readonly pre_context?: readonly string[]
  readonly post_context?: readonly string[]
  readonly vars?: Readonly<Record<string, unknown>>
  readonly module?: string
}

export interface CrashExceptionValue {
  readonly type?: string
  readonly value?: string
  readonly mechanism?: unknown
  readonly module?: string
  readonly stacktrace?: { readonly frames?: readonly CrashFrame[] }
}

/** CrashEvent is the part of an error event this module reads. The fields it
 * names are the ones it keeps, drops or relativises; a field the reporter also
 * carries and this type does not name (`user`, `contexts`, `tags`, `extra`,
 * `breadcrumbs`, `request`, `server_name`, `modules`, `environment`, `sdk`,
 * `debug_meta`, or a key a later SDK adds) is dropped by the same rule, because
 * the output is built from the allow-list rather than filtered against a
 * deny-list that would always be one key behind. */
export interface CrashEvent {
  readonly event_id?: unknown
  readonly timestamp?: unknown
  readonly platform?: unknown
  readonly level?: unknown
  readonly release?: unknown
  readonly exception?: { readonly values?: readonly CrashExceptionValue[] }
}

/** CrashLevel is the set of levels the processor understands. A level outside it
 * is dropped rather than passed through: it is a string out of a library, and
 * inventing a severity for it here would be this module making a claim the
 * processor cannot read. */
export type CrashLevel = 'fatal' | 'error' | 'warning' | 'log' | 'info' | 'debug'

const LEVELS: readonly CrashLevel[] = ['fatal', 'error', 'warning', 'log', 'info', 'debug']

export interface ScrubbedFrame {
  readonly function?: string
  readonly filename: string
  readonly lineno?: number
  readonly colno?: number
  readonly in_app?: boolean
}

export interface ScrubbedExceptionValue {
  readonly type?: string
  readonly stacktrace?: { readonly frames: readonly ScrubbedFrame[] }
}

export interface ScrubbedCrashEvent {
  readonly event_id?: string
  readonly timestamp?: number
  readonly platform?: string
  readonly level?: CrashLevel
  readonly release?: string
  readonly exception?: { readonly values: readonly ScrubbedExceptionValue[] }
}

/** EXTERNAL is what a frame outside the app becomes. It is a fixed word rather
 * than a trimmed path: a path outside the app belongs to a machine or to a
 * dependency's build, and the part of it that identifies the operator is the
 * part no rule can reliably find. */
const EXTERNAL = '<external>'

/** ANONYMOUS is what a function name that is really a path becomes: some builds
 * put the frame's file in `function`, which is a path with a home directory in it
 * as often as it is a name. */
const ANONYMOUS = '<anonymous>'

/** PACKED_ROOT is the archive the code lives in once the console is packaged,
 * and it is the second root a frame's path can be cut at: the app root a build
 * passes is the resources directory, and the part before this marker is a path on
 * somebody's disk. */
const PACKED_ROOT = 'app.asar/'

/** HOME_PATH_RE is a path under a home directory, on either platform. It is
 * checked on what is *left* after the app root comes off, because the root itself
 * is a path on the operator's disk -- `/Users/<x>/dev/...` in a checkout -- and a
 * frame that matches the root but still carries a user name (a traversal, a
 * vendored copy, an archive built on another machine) is a frame that has not
 * been made safe by the cut. */
const HOME_PATH_RE = /\/Users\/[^/]+\/|\/home\/[^/]+\//

/** WINDOWS_DRIVE_RE is a path rooted at a drive letter, which is absolute even
 * though it does not begin with a separator. */
const WINDOWS_DRIVE_RE = /^[A-Za-z]:\//

/** appRelative is the one thing a kept frame says about where it came from: the
 * part of the path inside the app, POSIX-separated, or `EXTERNAL`. */
function appRelative(source: string, appRoot: string): string {
  const path = source.replace(/\\/g, '/')
  const root = appRoot.replace(/\\/g, '/').replace(/\/+$/, '')
  let rest: string | null
  if (root !== '' && path.startsWith(`${root}/`)) rest = path.slice(root.length + 1)
  // A path that is not absolute came from the reporter already relative to the
  // app, and there is nothing to cut it at.
  else if (path.startsWith('/') || WINDOWS_DRIVE_RE.test(path)) rest = null
  else rest = path
  if (rest === null) {
    const packed = path.lastIndexOf(PACKED_ROOT)
    if (packed === -1) return EXTERNAL
    rest = path.slice(packed + PACKED_ROOT.length)
  } else if (rest.startsWith(PACKED_ROOT)) {
    // The archive sits inside the root once the app is packaged, and everything
    // before it -- the resources directory -- is a path on somebody's disk.
    rest = rest.slice(PACKED_ROOT.length)
  }
  if (rest === '' || HOME_PATH_RE.test(rest)) return EXTERNAL
  return rest
}

/** frameSource is the path to cut: the absolute one when the reporter has it,
 * and the relative one otherwise. */
function frameSource(frame: CrashFrame): string | null {
  if (typeof frame.abs_path === 'string' && frame.abs_path !== '') return frame.abs_path
  if (typeof frame.filename === 'string' && frame.filename !== '') return frame.filename
  return null
}

/** scrubFunction keeps a function name and refuses a path wearing one. */
function scrubFunction(name: string | undefined): string | undefined {
  if (typeof name !== 'string') return undefined
  return name.includes('/') || name.includes('\\') ? ANONYMOUS : name
}

/** scrubFrame is the frame allow-list: the name, the app-relative path and the
 * position in the file. A frame with no path at all is dropped rather than kept
 * with an invented `<external>`, which would claim the reporter had said
 * something it did not. */
function scrubFrame(frame: CrashFrame, appRoot: string): ScrubbedFrame | null {
  const source = frameSource(frame)
  if (source === null) return null
  const name = scrubFunction(frame.function)
  return {
    filename: appRelative(source, appRoot),
    ...(name === undefined ? {} : { function: name }),
    ...(typeof frame.lineno === 'number' ? { lineno: frame.lineno } : {}),
    ...(typeof frame.colno === 'number' ? { colno: frame.colno } : {}),
    ...(typeof frame.in_app === 'boolean' ? { in_app: frame.in_app } : {}),
  }
}

function scrubValue(value: CrashExceptionValue, appRoot: string): ScrubbedExceptionValue {
  const frames = value.stacktrace?.frames
  const type = typeof value.type === 'string' ? value.type : undefined
  if (!Array.isArray(frames)) return { ...(type === undefined ? {} : { type }) }
  return {
    ...(type === undefined ? {} : { type }),
    stacktrace: {
      frames: frames
        .map((frame) => scrubFrame(frame, appRoot))
        .filter((frame): frame is ScrubbedFrame => frame !== null),
    },
  }
}

/** scrubCrashEvent is `beforeSend`, with the event the reporter built and the
 * app root as its only inputs. It returns a **new** event built from the kept
 * fields and never edits the one it was handed: the caller's object is the
 * SDK's, and emptying it in place would break the reporter this runs inside. */
export function scrubCrashEvent(event: CrashEvent, appRoot: string): ScrubbedCrashEvent {
  const values = event.exception?.values
  return {
    ...(typeof event.event_id === 'string' ? { event_id: event.event_id } : {}),
    ...(typeof event.timestamp === 'number' ? { timestamp: event.timestamp } : {}),
    ...(typeof event.platform === 'string' ? { platform: event.platform } : {}),
    ...(LEVELS.includes(event.level as CrashLevel) ? { level: event.level as CrashLevel } : {}),
    ...(typeof event.release === 'string' ? { release: event.release } : {}),
    ...(Array.isArray(values)
      ? { exception: { values: values.map((value) => scrubValue(value, appRoot)) } }
      : {}),
  }
}
