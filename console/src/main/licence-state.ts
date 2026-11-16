// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The console's read-only state after the grace period (subscriptions plan
// S6.2; the design is `docs/design/lapse-read-only.md`, D436 and T12).
//
// **What this file is.** Two halves of one subject: the licence state of an
// installation. The first is one mapping from the verifier's answer about the key
// an installation holds to the five states of the design's table, and the gate
// that takes exactly three operations away in the state the design calls the
// lapse: the launch, the configuration change and the record's move (GV3.2's
// `roadmap.move`). The second, at the end of this file, is the reader of the
// **project's** key as `fugaro licence status --json` answers it (licence-key-v2
// plan K3.6; D2137, D2143 and K3.5's warning field) -- the key that belongs to
// the installation rather than to one machine. Everything else -- every read,
// the console's own surface, the kill switch's state, the key screen -- answers
// exactly as it does today.
//
// **Why the gate is here and not in the renderer.** The design puts enforcement
// "in the console's main process, at the operation dispatch, before any child is
// spawned", and says the renderer is not the gate. A refusal the renderer made
// would be a refusal a renderer bug could skip, and it would come *after* the
// child the operation exists to spawn: the launch would have started, and the
// operator would be reading a sentence about a run that is already going.
//
// **Why it classifies by the table's column and not by a list of names.** The
// design's own rule: a cancel, whose row differs from the launch's in exactly
// that field, must not be refused the day it is added. A list of refused
// operation names kept beside `CONSOLE_OPERATIONS` would pass every test in
// `test/unit/licence-state.test.ts` and fail that one, so the class is read off
// the rows (`refusedAfterLapse`) and `assertReachableSurface` holds the two
// facts the design states about the column's shape.
//
// **Why nothing here is a security boundary.** The design says so where it
// states the hole: a lapsed operator who deletes their key is back in the no-key
// state, which keeps working. The instrument is the Terms and the customer's own
// word; what this file adds is the product behaving as the design says it does,
// not a check an operator could not walk around.

import { CONSOLE_OPERATIONS } from './ipc'
import type { ConsoleHandler, ConsoleHandlers, ConsoleOperation, OperationName } from './ipc'
import type { LicenceReply } from './licence-store'

/**
 * refusesAfterLapse answers whether one row is in the lapse's refused class: the
 * rows carrying `refusedAfterLapse` (`ipc.ts`).
 *
 * It takes the row as a `ConsoleOperation` while the caller iterates the literal
 * table, and that widening is the point of it: the table's rows do not each
 * declare the optional columns, so a row's own type has no `refusedAfterLapse`
 * to read -- and the caller must keep its literal type, because that is what
 * keys `ConsoleHandlers` by operation name.
 */
function refusesAfterLapse(operation: ConsoleOperation): boolean {
  return operation.refusedAfterLapse === true
}

/**
 * installationArgumentOf answers the payload argument a row acts on an
 * installation through, or `undefined` for a row that acts on the console's own
 * active binding. It takes the row widened for `refusesAfterLapse`'s reason and
 * no other: the optional columns are not on every row's literal type, and the
 * caller must keep its literal type because that is what keys `ConsoleHandlers`.
 */
function installationArgumentOf(operation: ConsoleOperation): string | undefined {
  return operation.installationArgument
}

/**
 * ProductLicenceState is the design's state table (§5.1-§5.5), in this console's
 * own words for it. It is deliberately **not** the verifier's `LicenceState`
 * (`src/main/licence.ts`): the verifier answers about a *key* -- `expired` is a
 * fact about a key's clock -- and these are the states the *installation* is in,
 * which is what a gate, a banner and a refusal are about.
 *
 * `unverified` is §5.4's row: a key this build does not verify (a key id a
 * rollback dropped, a key kept by a newer release). It reads as the no-key state
 * and is not a lapse, because a refused key never had a state to lapse from --
 * the verifier refuses rather than naming one, and a product that turned a
 * verifier's refusal into read-only would take a capability away on a fact
 * nothing judged.
 */
export type ProductLicenceState = 'no-key' | 'valid' | 'grace' | 'lapsed' | 'unverified'

/**
 * STATE_OF is the whole mapping, keyed by the reply's own state word.
 *
 * It is a `Record` over that union rather than a chain of comparisons so that a
 * sixth state a later release adds to `LicenceReply` is a **compile error here**
 * -- the one place the product's states are decided -- rather than a state that
 * quietly falls through to whatever the last branch said.
 */
const STATE_OF: Readonly<Record<LicenceReply['state'], ProductLicenceState>> = Object.freeze({
  none: 'no-key',
  valid: 'valid',
  grace: 'grace',
  expired: 'lapsed',
  refused: 'unverified',
})

/** licenceStateOf is the reply's product state. **The store's `refused` reply --
 * a console that could not read its own key file at all -- is `unverified` and
 * not a lapse**, and that direction is the design's: nothing refuses without a
 * licence that was read and judged to have lapsed, so a console whose own record
 * file is unreadable keeps working rather than grounding the installation over
 * its own fault. */
export function licenceStateOf(reply: LicenceReply): ProductLicenceState {
  return STATE_OF[reply.state]
}

/**
 * LAPSE_REFUSAL is what a refused launch and a refused settings write answer:
 * one fixed sentence of the console's own, naming the state and the way out.
 *
 * The design fixes the shape of this copy and not its words (§5.7): a refusal
 * names the same state and the same way out a banner does, never a claim read
 * out of a key, and **no figure appears in it** (D430, T22). So it names the
 * lapse, it names the Project tab's Licence key section -- the screen §5.6 keeps
 * ungated, which is what makes the sentence a way out rather than a dead end --
 * and it carries no day, no seat count and nothing else the reply held.
 *
 * It is deliberately **not** a verifier's sentence. The verifier's refusals are
 * about a key that does not verify, and this is not that: the key verified and
 * its clock ran out. A sentence that looked like the verifier's would send the
 * operator to check a key that is not broken.
 */
export const LAPSE_REFUSAL =
  'the licence key has lapsed, so this console cannot start a run or change a setting: paste a renewed key in the Project tab’s Licence key section'

/** lapsed throws the lapse's refusal. A handler refuses by throwing and
 * `dispatch` turns the throw into the reply, which is the one path every refusal
 * on this surface takes -- the same path `reducedModeHandlers`' `withheld` uses,
 * and the reason this is not a second reply shape. */
function lapsed(): never {
  throw new Error(LAPSE_REFUSAL)
}

/**
 * lapseHandlers wraps the surface so that the operations the design's lapse
 * takes away refuse **before the source is reached**.
 *
 * `current` is read per call and not captured once, for `reducedModeHandlers`'
 * reason and one more: the state is the store's answer about the key the
 * installation holds *now*, and an operator who pastes a renewal during a
 * session must get the launch back at the next call. A gate closed over one copy
 * of the reply would keep refusing until the console was restarted.
 *
 * **`current` is asked about the installation the operation acts on, and the
 * row says which that is.** A row carrying `installationArgument` (`ipc.ts`)
 * names the installation in its own payload, and that is the installation the
 * operation really reaches: `run.launch` resolves the name a renderer sent
 * against this console's records and spawns **that** installation, with that
 * installation's key in the child's environment (`src/main/launch.ts`). So a gate
 * that asked about the console's active binding for every row would be wrong in
 * both directions -- it would admit a launch of a lapsed installation while
 * driving a valid one, and it would refuse a valid installation's launch after
 * the rail moved -- and the first of those is the one that does the damage: the
 * launch goes out, with the lapsed installation's key in its child. A row with no
 * such column acts on the active installation (`settings.edit` writes the
 * settings document the console is driving), which is what `undefined` means to
 * `current`.
 *
 * **Only the rows carrying `refusedAfterLapse` are wrapped.** Every other
 * operation is the very handler it was handed -- the same function object, not a
 * wrapper that happens to call it -- so there is exactly one place a refusal can
 * be added and no second place it could be added in silence. That is also why
 * `assertReachableSurface` refuses the column on a row that spawns no command
 * (`ipc.ts`): the console's own answers are the design's kept set, and the way
 * out of the lapse is one of them.
 */
export function lapseHandlers(
  handlers: ConsoleHandlers,
  current: (installation: string | undefined) => LicenceReply,
): ConsoleHandlers {
  const wrapped: Record<OperationName, ConsoleHandler> = { ...handlers }
  for (const operation of CONSOLE_OPERATIONS) {
    if (!refusesAfterLapse(operation)) continue
    const inner = handlers[operation.name]
    const named = installationArgumentOf(operation)
    wrapped[operation.name] = async (args) => {
      // The argument is required and validated before any handler runs, and
      // `assertReachableSurface` holds the column to a required
      // `installationName` argument of this very row -- so this is a narrowing
      // and not a second check, and `undefined` here is the row that names no
      // installation and means the active binding.
      const target = named === undefined ? undefined : args[named]
      const installation = typeof target === 'string' && target !== '' ? target : undefined
      if (licenceStateOf(current(installation)) === 'lapsed') lapsed()
      return inner(args)
    }
  }
  return Object.freeze(wrapped)
}

// ---------------------------------------------------------------------------
// The project key's state, as `fugaro licence status --json` answers it
// (licence-key-v2 plan K3.6; D2137, D2143, and K3.5's warning field).
//
// **Why the project's key is read here at all.** A licence key belongs to the
// installation and not to the machine (D2137): `fugaro licence set` writes it to
// the project's own store, every member reads it there, and the console's
// per-machine file (`licence-store.ts`) is only what it hands a child it spawns.
// So what this console can say about the *installation's* licence is what the
// CLI answers about the project's key, and this half of the file is the reader
// of that answer.
//
// Four rules shape it, and each is a test rather than an argument:
//
//   1. **No key is Free** (D2143). The CLI's `free` -- an installation with no
//      key at all, which is the ordinary Free state and not a failure -- reads as
//      this console's `no-key`, the state that keeps working. It is deliberately
//      **not** `lapsed`: a reader that turned an absent key into a lapse would
//      take a capability away over a state the design says is the normal one.
//   2. **A document this console cannot vouch for is refused whole**, with the
//      console's own sentence naming the field and **never the value** -- the
//      rule every other document reader here follows (`installlist.ts`), and the
//      reason a refusal carries no text out of the child. The one word this
//      reader decides on is `state`: a word it does not know is a refusal and
//      never a state it guesses at (D7: a screen that misreports because it read
//      a shape it did not understand is worse than one that says it cannot read
//      it).
//   3. **A key this console does not know is ignored**, because the CLI's JSON
//      only grows (`shared/schema.ts`): a field a newer CLI adds reaches no
//      screen and no decision. A field this reader *does* use, arriving as the
//      wrong type or missing, is rule 2's refusal.
//   4. **A warning is weighed against the reader's own role** (K3.5). The CLI's
//      field carries the key's policy windows -- the roles each reaches and when
//      it opened -- because the windows do not all reach the same roles at the
//      same instant; the console knows who is looking and the CLI does not, so
//      the choice is made here and not there.
//
// **What this half deliberately does not do.** It spawns nothing: the argv that
// would print this document is the spawn allow-list's (`spawn.ts`, and the §5.1
// table), and K3.6 names neither. It gates nothing: the lapse gate above is fed
// by the console's own store and is unchanged, and what a revoked or expired
// *project* key should take away is enforcement, held with TN1. It also never
// parses the document's stamps into instants: a date here is a figure a screen
// formats, and a second RFC3339 reader beside the verifier's (`licence.ts`'s
// `stampInstantOf`, which is that file's own) would be the duplicate helper this
// repository asks a writer to look for first.
// ---------------------------------------------------------------------------

/**
 * MAX_LICENCE_STATUS_BYTES bounds the document this reader will look at. The
 * CLI's answer is a few hundred bytes -- a state word, a handful of figures and
 * at most one warning sentence -- so this is generous by two orders of magnitude
 * and still refuses a pipe of something else. It is counted in UTF-16 code units,
 * which is what `text.length` is.
 */
export const MAX_LICENCE_STATUS_BYTES = 64 * 1024

/** MAX_LICENCE_STATUS_STRING bounds every string this reader keeps. A project
 * name, a tier word, a stamp and a warning sentence are all far shorter than
 * this; the bound is here so that a document cannot make a screen's text
 * unbounded, exactly as `MAX_DOCUMENT_STRING` bounds the installation list's. */
export const MAX_LICENCE_STATUS_STRING = 4096

/** MAX_LICENCE_WARNING_WINDOWS bounds the windows one warning may list. The
 * policy is the key's own (spec section 3.2), and a key carrying more windows
 * than this is not one this console draws a banner for. */
export const MAX_LICENCE_WARNING_WINDOWS = 64

/** CliLicenceState is the state word `fugaro licence status --json` prints: the
 * CLI's own four, and not this console's product states. `free` is the
 * installation with no key at all, which is why the mapping below sends it to
 * `no-key` rather than to a lapse. */
type CliLicenceState = 'free' | 'active' | 'grace' | 'expired'

/**
 * PROJECT_STATE_OF is the whole mapping from the CLI's state word to this file's
 * product states, and it is the place D2143 is decided: **an installation with no
 * key is Free, so `free` is `no-key`** -- the state that keeps working -- and not
 * `lapsed`.
 *
 * A `Record` over the CLI's union rather than a chain of comparisons, for
 * `STATE_OF`'s reason above: a fifth word a later CLI adds is a compile error in
 * the one place the product's states are decided, rather than a word that
 * quietly falls through to whatever the last branch said.
 *
 * `expired` is `lapsed` whether or not the document's `revoked` is set: the CLI
 * answers a revoked key `expired` with `revoked: true`, and Free from a
 * revocation and Free from an expiry are the same behaviour and different news,
 * which is what the flag beside the state is for.
 */
const PROJECT_STATE_OF: Readonly<Record<CliLicenceState, ProductLicenceState>> = Object.freeze({
  free: 'no-key',
  active: 'valid',
  grace: 'grace',
  expired: 'lapsed',
})

/** isCliLicenceState narrows a document's `state` to the four words above. It
 * asks the mapping's own keys rather than a second list, so a word added there
 * is a word this reader knows. */
function isCliLicenceState(word: string): word is CliLicenceState {
  return Object.prototype.hasOwnProperty.call(PROJECT_STATE_OF, word)
}

/** ProjectLicenceWindow is one window of the key's warning policy that is open
 * now: the roles it reaches and the instant it opened, which is the start of the
 * key's grace or a whole number of days after it. Both are the CLI's own strings
 * and neither is parsed here (see the section's note on stamps). */
export interface ProjectLicenceWindow {
  readonly opens: string
  readonly roles: readonly string[]
}

/** ProjectLicenceWarning is K3.5's field: the windows open at the instant the
 * CLI answered, and the one sentence the CLI writes for them.
 *
 * **The sentence does not vary by window.** The CLI composes it once for the
 * whole warning, so which window reached a reader decides *whether* there is a
 * warning and not *what it says* -- which is why `projectLicenceWarning` answers
 * the sentence rather than the window. */
export interface ProjectLicenceWarning {
  readonly message: string
  readonly windows: readonly ProjectLicenceWindow[]
}

/** ProjectLicenceCheck is the last revocation check the installation ran, in the
 * shape the CLI prints it (`project.Check`). `keyId` is deliberately not read:
 * the console draws nothing from a key id, and a reader that carried one would be
 * one writer away from a screen. */
export interface ProjectLicenceCheck {
  readonly at: string
  readonly status: string
  readonly failures: number
  readonly lastOk: string | null
}

/**
 * ProjectLicenceStatus is `fugaro licence status --json` as this console may be
 * told about it: the state in this file's own vocabulary, and the figures a
 * licence section draws beside it.
 *
 * The four words the CLI omits when they are empty (`tier`, `kind`,
 * `renewal_mode`, `source`) read as `null` here, which is the CLI's own
 * `omitempty` meaning; every other field is one the CLI always writes, so a
 * document missing one is not this document.
 */
export interface ProjectLicenceStatus {
  readonly project: string
  readonly state: ProductLicenceState
  readonly tier: string | null
  readonly seats: number
  readonly paidThrough: string | null
  readonly kind: string | null
  readonly renewalMode: string | null
  readonly source: string | null
  readonly revoked: boolean
  readonly lastCheck: ProjectLicenceCheck | null
  readonly warning: ProjectLicenceWarning | null
}

/** LicenceStatusParse is the parse's answer: the document, or the one sentence
 * saying why this text is not one. */
export type LicenceStatusParse =
  | { readonly ok: true; readonly status: ProjectLicenceStatus }
  | { readonly ok: false; readonly reason: string }

/** isRecord is this reader's own test, spelled here rather than imported:
 * `installations.ts`'s and `licence-store.ts`'s are private to their modules. */
function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

/** usableText is one string field this reader keeps: a string within the bound,
 * and `null` for anything else -- absent, `null`, another type, too long. Every
 * caller turns that `null` into its own refusal naming the field, which is why
 * no reason is composed here. */
function usableText(value: unknown): string | null {
  if (typeof value !== 'string' || value.length > MAX_LICENCE_STATUS_STRING) return null
  return value
}

/** optionalText is one of the four words the CLI leaves out when it is empty:
 * `null` for a field that is absent or `null` (the CLI's `omitempty`), the
 * string when it is one this reader keeps, and `undefined` for a value that is
 * there and is not -- which is the caller's refusal. */
function optionalText(value: unknown): string | null | undefined {
  if (value === undefined || value === null) return null
  return usableText(value) ?? undefined
}

/** refuseStatus is every refusal: the console's own sentence naming the field
 * and never the value, so no text out of the child reaches a sentence a screen
 * draws. */
function refuseStatus(what: string): LicenceStatusParse {
  return { ok: false, reason: `the CLI’s licence status ${what}` }
}

/** readCheck is the `last_check` object, or `null` for one this reader cannot
 * read. It answers one value rather than a refusal per field because every field
 * of it is a figure about a check and none of them decides the state: a check
 * this console cannot read is a document it does not understand (the caller's
 * refusal) and never a check it draws half of. */
function readCheck(raw: Record<string, unknown>): ProjectLicenceCheck | null {
  const at = usableText(raw['at'])
  const status = usableText(raw['status'])
  if (at === null || status === null) return null
  const failures = raw['failures'] === undefined || raw['failures'] === null ? 0 : raw['failures']
  if (typeof failures !== 'number' || !Number.isSafeInteger(failures) || failures < 0) return null
  const lastOk = optionalText(raw['last_ok'])
  if (lastOk === undefined) return null
  return Object.freeze({ at, status, failures, lastOk })
}

/** readWarning is K3.5's field: the windows open now, each with the roles it
 * reaches and when it opened, and the CLI's one sentence. It answers `null` for
 * a warning this reader cannot read, on `readCheck`'s rule. */
function readWarning(raw: Record<string, unknown>): ProjectLicenceWarning | null {
  const message = usableText(raw['message'])
  const windows = raw['windows']
  if (message === null || !Array.isArray(windows) || windows.length > MAX_LICENCE_WARNING_WINDOWS) {
    return null
  }
  const read: ProjectLicenceWindow[] = []
  for (const window of windows) {
    if (!isRecord(window)) return null
    const opens = usableText(window['opens'])
    const roles = window['roles']
    if (opens === null || !Array.isArray(roles) || roles.length > MAX_LICENCE_WARNING_WINDOWS) {
      return null
    }
    const named: string[] = []
    for (const role of roles) {
      const name = usableText(role)
      if (name === null) return null
      named.push(name)
    }
    read.push(Object.freeze({ opens, roles: Object.freeze(named) }))
  }
  return Object.freeze({ message, windows: Object.freeze(read) })
}

/**
 * parseLicenceStatusDocument reads the document `fugaro licence status --json`
 * printed. It is a *document* parser and not a decoder for a payload: the text
 * arrived from a child's stdout, and everything here is about refusing a shape
 * this console cannot draw rather than about recovering from it.
 *
 * The order is the shape's own and not a preference: the text, then the object,
 * then the one word the state is read from, and only then the figures beside it
 * -- so a document that is not one of these is refused before anything in it is
 * kept.
 */
export function parseLicenceStatusDocument(text: string): LicenceStatusParse {
  if (text.length > MAX_LICENCE_STATUS_BYTES) {
    return refuseStatus('is longer than this console reads')
  }
  let value: unknown
  try {
    value = JSON.parse(text) as unknown
  } catch {
    return refuseStatus('is not JSON')
  }
  if (!isRecord(value)) return refuseStatus('is not a JSON object')

  const project = usableText(value['project'])
  if (project === null) return refuseStatus('has no usable `project`')

  const word = usableText(value['state'])
  if (word === null) return refuseStatus('has no usable `state`')
  if (!isCliLicenceState(word)) return refuseStatus('names a state this console does not know')

  const seats = value['seats']
  if (typeof seats !== 'number' || !Number.isSafeInteger(seats) || seats < 0) {
    return refuseStatus('has no usable `seats`')
  }

  const revoked = value['revoked']
  if (typeof revoked !== 'boolean') return refuseStatus('has no usable `revoked`')

  // The three keys the CLI always writes and that are `null` where there is
  // nothing to say: absent is not one of the two, so a document without them is
  // not this document. `null` is a value here and an absent key is not.
  const paidThroughRaw = value['paid_through']
  let paidThrough: string | null = null
  if (paidThroughRaw !== null) {
    paidThrough = usableText(paidThroughRaw)
    if (paidThrough === null) return refuseStatus('has no usable `paid_through`')
  }

  const lastCheckRaw = value['last_check']
  let lastCheck: ProjectLicenceCheck | null = null
  if (lastCheckRaw !== null) {
    if (!isRecord(lastCheckRaw)) return refuseStatus('has no usable `last_check`')
    lastCheck = readCheck(lastCheckRaw)
    if (lastCheck === null) return refuseStatus('has no usable `last_check`')
  }

  const warningRaw = value['warning']
  let warning: ProjectLicenceWarning | null = null
  if (warningRaw !== null) {
    if (!isRecord(warningRaw)) return refuseStatus('has no usable `warning`')
    warning = readWarning(warningRaw)
    if (warning === null) return refuseStatus('has no usable `warning`')
  }

  const tier = optionalText(value['tier'])
  if (tier === undefined) return refuseStatus('has a `tier` that is not a string')
  const kind = optionalText(value['kind'])
  if (kind === undefined) return refuseStatus('has a `kind` that is not a string')
  const renewalMode = optionalText(value['renewal_mode'])
  if (renewalMode === undefined) return refuseStatus('has a `renewal_mode` that is not a string')
  const source = optionalText(value['source'])
  if (source === undefined) return refuseStatus('has a `source` that is not a string')

  return {
    ok: true,
    status: Object.freeze({
      project,
      state: PROJECT_STATE_OF[word],
      tier,
      seats,
      paidThrough,
      kind,
      renewalMode,
      source,
      revoked,
      lastCheck,
      warning,
    }),
  }
}

/**
 * LicenceReaderRole is the roles a warning window can reach, which are the CLI's
 * own three (`internal/access`): an admin, an operator, and a viewer who is free
 * (spec section 10). The console knows who is looking and the CLI does not, which
 * is the whole reason the windows are listed rather than resolved.
 */
export type LicenceReaderRole = 'admin' | 'operator' | 'viewer'

/**
 * projectLicenceWarning answers the sentence a reader in one role is owed, or
 * `null` when this key's policy reaches them nowhere.
 *
 * **The newest window that reaches the role is the one that decides**, which is
 * the CLI's own rule (`ee/register/warn.go`): two windows may reach one role, and
 * the later one is the news that has just changed. The windows are listed in the
 * order they open -- the CLI's own promise for the field -- so the last one that
 * names the role is the newest, and the stamps are never compared here (a
 * sub-second spelling sorts before a whole second's as text, which is exactly the
 * arithmetic a reader must not do on strings).
 *
 * The sentence is the CLI's one sentence for the whole warning and is the same
 * whichever window matched, so that is what comes back: what the window decides
 * is whether the reader is owed a warning at all.
 */
export function projectLicenceWarning(
  status: ProjectLicenceStatus,
  role: LicenceReaderRole,
): string | null {
  const warning = status.warning
  if (warning === null) return null
  let reached = false
  for (const window of warning.windows) {
    if (window.roles.includes(role)) reached = true
  }
  return reached ? warning.message : null
}
