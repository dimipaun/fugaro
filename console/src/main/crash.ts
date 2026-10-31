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
 * the installation's setting allows reports, and an answer that is not `true`
 * otherwise -- `false`, or a `null` the document holds in the node's place,
 * which are both off. The absence of a value is the third case: nothing has read
 * it yet, not asked, unreadable, or answered by a CLI too old to carry the
 * field. That absence is D734's rule made expressible -- a console that folded it
 * into `false` could not tell "off" from "not known yet", and the two have the
 * same answer here for exactly as long as it takes something to read the setting,
 * but they are different words in a log line, which is the whole of what
 * `CrashReportingReason` is for. JSON has no `undefined`, so the absence is what
 * a settings document with no switch node in it decodes to; a node that is
 * present and empty decodes to `null`, which is an answer. */
export type CrashOptIn = boolean | null | undefined

/** CrashReportingInput is everything the gate is allowed to look at: the DSN the
 * build was given, and the switch. There is deliberately nothing else -- no
 * environment, no clock, no state -- so the decision cannot depend on anything
 * the SDK slice has to remember to pass.
 *
 * The switch is typed `unknown` rather than `CrashOptIn` because of where it
 * comes from: a settings node in a document this console did not write, decoded
 * from JSON. `CrashOptIn` is the answer when the document is the one the CLI
 * promises; anything else is a value the gate has to refuse, and a type that
 * admitted only the three good values would move that refusal into a cast at the
 * call site. */
export interface CrashReportingInput {
  readonly dsn?: string | null
  readonly optedIn?: unknown
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
 * The gate is strict: `true` and nothing else opens it. The value arrives from a
 * document this console did not write, so `"true"`, `1`, `"yes"` and an object
 * are all things a settings node can hold, and a truthy check would read every
 * one of them as an operator's consent -- which is the one answer this gate may
 * not guess at. Everything that is not `true` is an answer that is off, except
 * the absence of an answer, which is the unread case and says so.
 *
 * A DSN that is only whitespace is no DSN: the value comes from a build the
 * owner configures, and an empty one is what an unset variable looks like.
 */
export function crashReportingDecision(input: CrashReportingInput): CrashReportingDecision {
  if (input.optedIn === undefined) return { enabled: false, reason: 'opt-in-not-read' }
  if (input.optedIn !== true) return { enabled: false, reason: 'opt-in-off' }
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
  /** `null` and holes among the frames are allowed for the reason the event's
   * exception is: this is a library's object, handed over as it was built. */
  readonly stacktrace?: { readonly frames?: readonly (CrashFrame | null | undefined)[] } | null
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
  /** The exception is `null`-able and its values may hold holes because the
   * reporter builds this: a library's event is not the shape its own types say
   * it is, and the scrubber reads every level of it through a guard rather than
   * trusting a level it was handed. */
  readonly exception?: { readonly values?: readonly (CrashExceptionValue | null | undefined)[] | null } | null
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

/** ANONYMOUS is what a frame's function becomes when the name it carries is not
 * a name: some builds put the frame's file in `function`, which is a path with a
 * home directory in it as often as it is a name, and a library can be handed a
 * sentence instead. */
const ANONYMOUS = '<anonymous>'

/** UNKNOWN is the same fixed word for an exception's `type`, whose place in a
 * report is "what kind of thing threw" -- a question a message, a path or a
 * sentence does not answer, whatever else it says. */
const UNKNOWN = '<unknown>'

/** IDENTIFIER_RE is what a name has to look like to be kept, and it is the shape
 * of a function or an exception type in a stack: a leading letter, `_` or `$`,
 * then the letters, digits, dots, angle brackets and indexing a compiled name
 * really carries (`Object.<anonymous>`, `Array.prototype.map`). A space is out on
 * purpose: `"acme secret task text"` is name-shaped too, and it is task text.
 * What fails the pattern becomes a fixed word rather than a trim, because which
 * part of a sentence is the sensitive part is not something this module can
 * know. */
const IDENTIFIER_RE = /^[A-Za-z_$][\w$.<>[\]]{0,128}$/

/** PACKED_ROOT is the archive the code lives in once the console is packaged,
 * and it is the second root a frame's path can be cut at: the app root a build
 * passes is the resources directory, and the part before this marker is a path on
 * somebody's disk. */
const PACKED_ROOT = 'app.asar/'

/** PACKED_SEGMENT is PACKED_ROOT as a whole path segment: the marker with the
 * separator that has to precede it. `app.asar` is a directory *name*, so a
 * directory whose name merely ends in it -- `/tmp/<x>-app.asar/` -- is a
 * directory on somebody's disk rather than the archive, and a search that finds
 * the marker anywhere inside a segment cuts at the wrong place and puts the
 * next directory on the wire. */
const PACKED_SEGMENT = `/${PACKED_ROOT}`

/** HOME_PATH_RE is a path that names a home directory, on any platform this
 * console runs on and in any case it is spelled in. It is checked on what is
 * *left* after the app root comes off, because the root itself is a path on the
 * operator's disk -- `/Users/<x>/dev/...` in a checkout -- and a frame that
 * matches the root but still carries a user name (a traversal, a vendored copy,
 * an archive built on another machine) is a frame that has not been made safe by
 * the cut. `root` is here beside `users` and `home` because it is a home
 * directory with no user-name segment to recognise, and `Documents and Settings`
 * is Windows' older spelling of the same thing. A segment counts at the start of
 * what is left as well as behind a separator: `Users/<x>/...` is what an archive
 * built on another machine leaves after its `app.asar/`, and reading it as a
 * directory of the app is the mistake this pattern is for. */
const HOME_PATH_RE = /(?:^|\/)(?:users|home)\/[^/]+\/|(?:^|\/)root\/|(?:^|\/)documents and settings\//i

/** WINDOWS_DRIVE_RE is a path rooted at a drive letter, which is absolute even
 * though it does not begin with a separator. */
const WINDOWS_DRIVE_RE = /^[A-Za-z]:\//

/** SCHEME_RE splits a URL's own prefix off a frame's path: the scheme name, and
 * the `//` an authority adds where there is one. */
const SCHEME_RE = /^([A-Za-z][A-Za-z0-9+.-]*):(\/\/)?/

/** FILE_SCHEMES are the two schemes whose remainder is still a path on a disk,
 * and the only two this module looks behind. `file://` is how a build reports a
 * frame, and `app://` is the archive's own URL in the Electron SDK; a frame
 * arrives under one of them as readily as it arrives bare. */
const FILE_SCHEMES: readonly string[] = ['file', 'app']

/** withoutScheme takes the URL off a path, and answers null when the URL is not
 * a path at all: for anything but `FILE_SCHEMES` the remainder is a host, a
 * query or a body rather than a file, and a path smuggled inside one of those is
 * a directory name this module would never look at.
 *
 * A file URL is a path only in its empty-authority form, `file:///...`. Two
 * slashes spell a host where the path belongs -- `file://host/opt/x.js` names a
 * machine, and a machine's name is exactly what a report may not carry -- and one
 * slash has no authority at all, which this module will not guess a meaning for.
 * Both are refused rather than read. */
function withoutScheme(source: string): string | null {
  const scheme = SCHEME_RE.exec(source)
  if (scheme === null) return source
  const name = (scheme[1] ?? '').toLowerCase()
  if (FILE_SCHEMES.includes(name)) {
    const rest = source.slice(scheme[0].length)
    return scheme[2] === '//' && rest.startsWith('/') ? rest : null
  }
  // A drive letter is one character with no authority behind it, and every
  // Windows path starts with one -- reading it as a scheme would refuse the
  // frames this console does have and does want.
  if (scheme[1]?.length === 1 && scheme[2] === undefined) return source
  return null
}

/** CONTROL_RE is the characters a file name never carries and a reader always
 * obeys: a newline, a carriage return, a NUL, a tab, a delete, and the ones
 * Unicode spells outside ASCII -- the next-line character, the line and
 * paragraph separators, and the format characters, `\p{Cf}` (a bidi override, a
 * zero-width joiner, a byte-order mark). A control character is not a separator
 * any two readers agree on -- `x\nhome/<x>/secret.js` is one segment to
 * `HOME_PATH_RE` and two to a log line, a terminal or a decoder -- so one inside
 * a path is a way past the check below rather than part of a name. The three
 * Unicode line breaks are that same case spelled elsewhere, and a format
 * character is that case made invisible: a reader that honours a bidi override
 * or a joiner reads a path this check never saw. */
const CONTROL_RE = /[\x00-\x1f\x7f\x85\u2028\u2029\p{Cf}]/u

/** QUERY_MARKERS are where a URL's query and its fragment begin, and neither is
 * part of a file's name: `x.js?token=<x>` is a file name with a credential after
 * it. They are refused rather than trimmed at, because the SDK that built the URL
 * may itself have cut the path at the marker -- so what is left before it can be
 * a name the frame never had, and a wrong name is not safer than no name. */
const QUERY_MARKERS: readonly string[] = ['?', '#']

/** unplaceable is the last gate a cut path passes, and it is the one the app root
 * cannot stand in for: cutting a prefix off is not a check, so a path that
 * reached under the root and then walked back out of it -- `/opt/console/../../x`
 * -- leaves a relative path pointing outside the app. What is refused here is
 * every way such a path is still *about* somewhere: a `..` segment, a leading
 * `~`, a colon or a percent-escape that another reader would decode into a
 * separator, a query or a fragment marker, a control character, and the empty
 * string, which is a path with no name in it at all. */
function unplaceable(rest: string): boolean {
  if (rest === '' || rest.startsWith('~')) return true
  if (rest.includes(':') || rest.includes('%')) return true
  if (CONTROL_RE.test(rest)) return true
  if (QUERY_MARKERS.some((marker) => rest.includes(marker))) return true
  return rest.split('/').includes('..')
}

/** appRelative is the one thing a kept frame says about where it came from: the
 * part of the path inside the app, POSIX-separated, or `EXTERNAL`.
 *
 * Only a rooted path is read. A relative one names a file next to something this
 * module never learned, and the part of a path that says *where* an operator
 * keeps their work is the part a relative path by definition does not have -- so
 * there is nothing to cut it at, and it is refused whole rather than reported as
 * the bare name a build happened to write. */
function appRelative(source: string, appRoot: string): string {
  const bare = withoutScheme(source)
  if (bare === null) return EXTERNAL
  const path = bare.replace(/\\/g, '/')
  if (!path.startsWith('/') && !WINDOWS_DRIVE_RE.test(path)) return EXTERNAL
  const root = appRoot.replace(/\\/g, '/').replace(/\/+$/, '')
  let rest: string | null
  if (root !== '' && path.startsWith(`${root}/`)) rest = path.slice(root.length + 1)
  else rest = null
  if (rest === null) {
    const packed = path.lastIndexOf(PACKED_SEGMENT)
    if (packed === -1) return EXTERNAL
    rest = path.slice(packed + PACKED_SEGMENT.length)
  } else if (rest.startsWith(PACKED_ROOT)) {
    // The archive sits inside the root once the app is packaged, and everything
    // before it -- the resources directory -- is a path on somebody's disk.
    rest = rest.slice(PACKED_ROOT.length)
  }
  if (unplaceable(rest) || HOME_PATH_RE.test(rest)) return EXTERNAL
  return rest
}

/** frameSource is the path to cut: the absolute one when the reporter has it,
 * and the relative one otherwise. Each of the two is read once, so a frame whose
 * accessor answers with a path when the field is looked at and with something
 * else when it is copied has no second answer to give. */
function frameSource(frame: CrashFrame): string | null {
  const absPath = frame.abs_path
  if (typeof absPath === 'string' && absPath !== '') return absPath
  const filename = frame.filename
  if (typeof filename === 'string' && filename !== '') return filename
  return null
}

/** scrubName keeps a string that reads as a name, and answers the fixed word for
 * the field when it does not: a path, a sentence or anything else is what a
 * library was handed rather than what it knows, and a value that is not a string
 * at all is not a name either. */
function scrubName(name: unknown, fallback: string): string | undefined {
  if (typeof name !== 'string') return undefined
  return IDENTIFIER_RE.test(name) ? name : fallback
}

/** wholePosition is a frame's position as the report may carry it: a whole
 * number of at least zero, which is what a line or a column in a file is. A
 * fraction is not a line, a negative one is not a place in a file, and the `NaN`
 * and the infinities JSON writes as `null` are not positions either -- so what
 * reaches the processor is a position it can point at or nothing at all. */
function wholePosition(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isInteger(value) && value >= 0 ? value : undefined
}

/** scrubFrame is the frame allow-list: the name, the app-relative path and the
 * position in the file. A *frame* in is a frame out, because the shape of the
 * stack is what the processor groups by -- a frame with no path to place is
 * `EXTERNAL`, the same word a frame outside the app gets, rather than a frame
 * dropped from the middle and a stack that no longer reads as itself. A value
 * that is not a frame is not one this module declined to place: it is not a
 * frame at all, and it is dropped by `scrubValue` before it reaches here.
 *
 * Every field is read once, into a local, and the local is what is checked and
 * what is copied: a frame is a library's object, and a property of one answers
 * as often as it is asked -- so a field read twice would be checked as one value
 * and sent as another. */
function scrubFrame(frame: CrashFrame, appRoot: string): ScrubbedFrame {
  const source = frameSource(frame)
  const name = scrubName(frame.function, ANONYMOUS)
  const lineno = wholePosition(frame.lineno)
  const colno = wholePosition(frame.colno)
  const inApp = frame.in_app
  return {
    filename: source === null ? EXTERNAL : appRelative(source, appRoot),
    ...(name === undefined ? {} : { function: name }),
    ...(lineno === undefined ? {} : { lineno }),
    ...(colno === undefined ? {} : { colno }),
    ...(typeof inApp === 'boolean' ? { in_app: inApp } : {}),
  }
}

/** scrubValue is the exception allow-list: the type, and the frames the reporter
 * placed. The message is not here at all, which is this module's whole answer to
 * F33 -- so an exception the reporter could not walk is one word and no frames,
 * rather than a sentence somebody typed. A value that is not an object is null
 * rather than an exception, and the caller drops it: reading it would be the
 * throw this module exists to prevent, and reporting it would be a value it never
 * checked. */
function scrubValue(
  value: CrashExceptionValue | null | undefined,
  appRoot: string,
): ScrubbedExceptionValue | null {
  if (value === null || value === undefined || typeof value !== 'object') return null
  const frames = value.stacktrace?.frames
  const type = scrubName(value.type, UNKNOWN)
  const kept: ScrubbedExceptionValue = { ...(type === undefined ? {} : { type }) }
  if (!Array.isArray(frames)) return kept
  const placed: ScrubbedFrame[] = []
  for (const frame of frames) {
    if (frame === null || frame === undefined || typeof frame !== 'object') continue
    placed.push(scrubFrame(frame, appRoot))
  }
  return { ...kept, stacktrace: { frames: placed } }
}

/** scrubCrashEvent is `beforeSend`, with the event the reporter built and the
 * app root as its only inputs. It returns a **new** event built from the kept
 * fields and never edits the one it was handed: the caller's object is the
 * SDK's, and emptying it in place would break the reporter this runs inside.
 *
 * It also never throws, whatever it is handed: this runs inside the reporter, so
 * a throw here is a crash in the crash reporter, and a value that is not the
 * shape its type claims is one of the things a library hands over. An event that
 * is not an object is an empty event, and a bad value inside one is dropped at
 * the level it is bad at -- so a report missing its exception is still a report,
 * and nothing that was not read is passed on.
 *
 * The reads below are guarded by a `catch` as well as by their own checks,
 * because "an object of the shape its type claims" is not a promise this module
 * can keep on its own: the event is a library's object, and a property of one can
 * be a getter that throws -- a `Proxy`, a class with a throwing accessor -- so
 * the guard that makes each read safe is the wrong shape for that case. An event
 * that throws anywhere is the empty event, since a field read past a throwing
 * sibling is a field this module cannot claim it checked.
 *
 * Every field is read exactly once, at every level of the event, into a local
 * that is what the allow-list checks and what the output copies. A getter can
 * answer differently on each read, so a field read twice is a field where one
 * value passes the check and another is sent: the id that is checked is the id
 * that goes on the wire, and the same holds for a timestamp, a frame's path and
 * the rest. */
export function scrubCrashEvent(event: CrashEvent | null | undefined, appRoot: string): ScrubbedCrashEvent {
  try {
    if (event === null || event === undefined || typeof event !== 'object') return {}
    const values = event.exception?.values
    if (!Array.isArray(values)) return scrubFields(event)
    const kept: ScrubbedExceptionValue[] = []
    for (const value of values) {
      const scrubbed = scrubValue(value, appRoot)
      if (scrubbed !== null) kept.push(scrubbed)
    }
    return { ...scrubFields(event), exception: { values: kept } }
  } catch {
    return {}
  }
}

/** EVENT_ID_RE is the one shape an event id has: 32 lower-case hexadecimal
 * characters, which is what the reporter's own generator writes. An id is not
 * read by this module and not by the processor -- it is a de-duplication key --
 * so a string that is not that shape is a string from somewhere else, and the
 * field is dropped rather than sent with a value the service would index. */
const EVENT_ID_RE = /^[0-9a-f]{32}$/

/** PLATFORM_RE is the shape the platform field has: the one lowercase word the
 * reporter writes (`node`, `javascript`). A longer or punctuated string is a
 * sentence, a path or a token -- every one of them a value that got into the
 * field rather than a platform this report runs on. */
const PLATFORM_RE = /^[a-z]{1,16}$/

/** RELEASE_RE is the shape this console's release string has: letters, digits,
 * dots, hyphens and the `+` a build's metadata carries, bounded at 64. The
 * underscore is left out on purpose -- it is the character a forge's personal
 * access token and an npm automation token both carry in their prefix, so a
 * release-shaped string with one in it is a credential that reached the field
 * rather than a version. */
const RELEASE_RE = /^[A-Za-z0-9][A-Za-z0-9.+-]{0,63}$/

/** scrubFields is the event-level allow-list, apart from the exception: what
 * every field above reads is a string or a number the reporter wrote, and a field
 * that is anything else is dropped by the same rule as one this type never
 * named. The three string fields are held to their own shapes as well as to being
 * strings, because "a string the reporter wrote" is the assumption an allow-list
 * may not make: the value arrives from a library, and a library that was handed a
 * path, a token or a sentence puts it in whichever field is nearest.
 *
 * Each field is read exactly once, into a local, and the local is what the
 * allow-list checks and what the output copies -- a property read once for the
 * check and again for the copy is checked as one value and sent as another. */
function scrubFields(event: CrashEvent): ScrubbedCrashEvent {
  const eventId = event.event_id
  const timestamp = event.timestamp
  const platform = event.platform
  const level = event.level
  const release = event.release
  return {
    ...(typeof eventId === 'string' && EVENT_ID_RE.test(eventId) ? { event_id: eventId } : {}),
    ...(typeof timestamp === 'number' && Number.isFinite(timestamp) ? { timestamp } : {}),
    ...(typeof platform === 'string' && PLATFORM_RE.test(platform) ? { platform } : {}),
    ...(LEVELS.includes(level as CrashLevel) ? { level: level as CrashLevel } : {}),
    ...(typeof release === 'string' && RELEASE_RE.test(release) ? { release } : {}),
  }
}
