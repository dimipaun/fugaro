// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The console's lapse state, at the handler (subscriptions plan S6.2; the
// design is `docs/design/lapse-read-only.md`, D436 and T12).
//
// What this file holds, and why each part is a test rather than an argument:
//
//   - **the refused class is the table's, not a list of names.** The lapse takes
//     three operations away -- `run.launch`, `settings.edit` and GV3.2's
//     `roadmap.move` -- and it must classify them by the column their own rows
//     carry (`refusedAfterLapse`),
//     because the design's rule is that a later cancel, whose row differs from
//     the launch's in exactly that field, must not be refused the day it is
//     added. A gate built from a list beside the table would pass every case
//     below and fail that one, so the set is asserted here twice: as the three
//     names, and as the rows the column marks;
//   - **the refusal happens in the main process, before any child exists.** The
//     renderer is not the gate: the channels come from the operation table and
//     from nothing else, so the refusal tests go through the real registrar and
//     the real bridge, and assert that the source behind the handler was never
//     reached -- a gate applied after the handler would answer the same sentence
//     and have spawned the launch anyway;
//   - **the gate asks about the installation the operation acts on.** A row whose
//     payload names an installation (`run.launch`, whose row carries
//     `installationArgument`) is judged on *that* installation's key: the
//     launcher resolves the name a renderer sent and spawns that installation,
//     with that installation's key in the child's environment, so a gate keyed
//     on the console's active binding would admit a lapsed installation's launch
//     while driving a valid one, and would refuse a valid installation's launch
//     after the rail moved. Rows that name none (`settings.edit`, whose flow
//     writes the active installation's own settings document) are judged on the
//     binding, which is the installation they act on;
//   - **every other operation answers**, at every state but the lapse. That is
//     the design's kept set: every read (the paid views included), the console's
//     own surface, the kill switch's state, and the key screen, which is the way
//     out of the state and is therefore never gated;
//   - **the state is read per call**, so a renewal pasted during a session lifts
//     the refusal at the next call with no restart;
//   - **the no-key state and an unverifiable key keep working** (design §5.3 and
//     §5.4): nothing refuses without a key, and a key this build cannot verify
//     is not a lapse.
//
// The whole-surface cases call the wrapped handler directly, with the empty
// payload -- `reducedModeHandlers` is held the same way in `ipc.test.ts`, and for
// the same reason: what is under test is the gate, and a payload the schema
// would refuse would be a refusal this file is not about. Every operation whose
// schema requires nothing is then reached a second time *through the bridge*, so
// the end-to-end path is covered where it can be without a second copy of
// `ipc.test.ts`'s well-formed table, and the two refused operations -- the
// subject of the task -- are reached through the bridge with their own payloads.
//
// Nothing here loads Electron, and nothing here reaches a child: the `ipcMain`
// and the renderer are fakes, exactly as `ipc.test.ts` builds them.

import { describe, expect, it } from 'vitest'

import { CONSOLE_OPERATIONS, registerConsoleHandlers } from '../../src/main/ipc'
import type {
  ConsoleEventLike,
  ConsoleHandler,
  ConsoleHandlers,
  ConsoleOperation,
  ConsoleReply,
  OperationName,
} from '../../src/main/ipc'
import { GRACE_DAYS } from '../../src/main/licence'
import {
  LAPSE_REFUSAL,
  lapseHandlers,
  licenceStateOf,
  MAX_LICENCE_STATUS_BYTES,
  MAX_LICENCE_STATUS_STRING,
  MAX_LICENCE_WARNING_WINDOWS,
  parseLicenceStatusDocument,
  projectLicenceWarning,
} from '../../src/main/licence-state'
import type { LicenceReaderRole, ProjectLicenceStatus } from '../../src/main/licence-state'
import type { LicenceReply } from '../../src/main/licence-store'
import { APP_ENTRY_URL } from '../../src/main/protocol'
import { createConsoleBridge, EXPOSED_OPERATIONS } from '../../src/preload/index'
import type { RendererLike } from '../../src/preload/index'

/** DAY is one day in the seconds a key's two time claims are counted in, and
 * `EXPIRES` is a day an annual key may carry. The tests below are about states
 * and not about arithmetic, so the claims are written out here rather than
 * verified: `licence.test.ts` holds the verifier's half. */
const DAY = 86_400
const EXPIRES = 1_800_000_000

/** CLAIMS is one verified claim set, as `licence.read` answers one. */
const CLAIMS = Object.freeze({
  tier: 'team' as const,
  features: Object.freeze([] as string[]),
  seats: 5,
  organisation: 'acme.com',
  issued: EXPIRES - 365 * DAY,
  expires: EXPIRES,
})

/**
 * AT is the five product states of the design's table, as the replies the
 * console's own store answers for them: no key, a key within its term, a key
 * inside the grace, a key past the grace, and a key this build does not verify.
 *
 * `lapsed` is the verifier's own word for the state -- `expired` -- because that
 * is what `LicenceStore.read` answers, and the point of `licenceStateOf` is that
 * the console's gate never reads that word as its decision.
 */
const AT: Readonly<Record<string, LicenceReply>> = Object.freeze({
  noKey: Object.freeze({ state: 'none' }) as LicenceReply,
  valid: Object.freeze({ state: 'valid', claims: CLAIMS }) as LicenceReply,
  grace: Object.freeze({ state: 'grace', claims: CLAIMS }) as LicenceReply,
  lapsed: Object.freeze({ state: 'expired', claims: CLAIMS }) as LicenceReply,
  unverified: Object.freeze({
    state: 'refused',
    reason: 'this build does not hold the key id the licence names',
  }) as LicenceReply,
})

/** OPERATIONS is the surface's table as `ConsoleOperation` rows: the literal
 * table's rows do not each declare every optional column, so the column this
 * file is about is read off the widened view. */
const OPERATIONS: readonly ConsoleOperation[] = CONSOLE_OPERATIONS

/** THE_REFUSED is the lapsed state's whole class, written out as a literal so
 * the column is checked against the design rather than against itself: T12 takes
 * the console's launch and its configuration change away, and GV3.2 adds the
 * console's third write -- the move of a task between the record's releases
 * (design section 5.1 rule 14) -- and nothing else does. */
const THE_REFUSED: readonly OperationName[] = ['run.launch', 'settings.edit', 'roadmap.move']

/** THE_KEPT is every other operation: the non-vacuity guard for a wrapper that
 * refused the whole surface. */
const THE_KEPT: readonly OperationName[] = CONSOLE_OPERATIONS.map((operation) => operation.name).filter(
  (name) => !THE_REFUSED.includes(name),
)

/** EMPTY_PAYLOADS is the kept set's operations whose schema requires nothing, so
 * a test may reach them through the bridge with no payload at all. It is derived
 * from the table rather than listed, so an operation that later grows a required
 * argument drops out of the end-to-end half instead of failing it. */
const EMPTY_PAYLOADS: readonly OperationName[] = THE_KEPT.filter((name) => {
  const row = CONSOLE_OPERATIONS.find((operation) => operation.name === name)
  return row !== undefined && row.args.every((argument) => !argument.required)
})

/**
 * WELL_FORMED is what the registrar's own schema accepts for the three refused
 * operations. The tests that go through the bridge for a refusal are about the
 * gate, and a payload the schema refuses would answer a refusal this file is not
 * about; `ipc.test.ts` holds the same three payloads for the same reason. The
 * move's two ids are a document key and a release key as the graph document
 * spells them (contract section 6.22) -- the gate never reads them, and the
 * source behind this fake handler is never reached.
 */
const WELL_FORMED: Readonly<Record<string, Record<string, unknown>>> = Object.freeze({
  'run.launch': { installation: 'sandbox', repository: 'acme/app', task: 'do the thing' },
  'settings.edit': { op: 'set', key: 'budget.cap_usd', value: '25' },
  'roadmap.move': { node: 'coordinator:plans/gv/T1', toRelease: 'a'.repeat(64), confirmed: true },
})

/** recordingIpcMain is Electron's `ipcMain`, as far as the registrar uses it: a
 * registry of the channels that were handled, so a test can deliver a message to
 * one. */
function recordingIpcMain() {
  const listeners = new Map<string, (event: ConsoleEventLike, ...args: unknown[]) => unknown>()
  return {
    listeners,
    handle(
      channel: string,
      listener: (event: ConsoleEventLike, ...args: unknown[]) => unknown,
    ): void {
      listeners.set(channel, listener)
    },
  }
}

/** consoleEvent is a message from the console's own top frame, which is the one
 * frame the surface answers; the URL is imported from `src/main/protocol.ts`
 * rather than spelled here. */
function consoleEvent(): ConsoleEventLike {
  const frame = { url: APP_ENTRY_URL }
  return { senderFrame: frame, sender: { mainFrame: frame } }
}

/** answeredBy is a source spy: every operation the surface can reach, recording
 * that it was called and answering an empty document. A gate that refuses
 * *after* calling the source has not refused anything -- the child already
 * spawned -- so "was the source reached" is the assertion, not the reply. */
function answeredBy() {
  const reached: string[] = []
  const handlers: Record<string, ConsoleHandler> = {}
  for (const operation of CONSOLE_OPERATIONS) {
    handlers[operation.name] = () => {
      reached.push(operation.name)
      return {}
    }
  }
  return { reached, handlers: handlers as unknown as ConsoleHandlers }
}

/**
 * lapsedConsole is the surface as `src/main/index.ts` registers it: the whole
 * allow-list, wrapped by the lapse gate, behind the real registrar and the real
 * bridge. `current` is a function, so a test can move the state under the gate
 * and answer for more than one installation -- it is handed the installation the
 * operation acts on, or `undefined` for a row that names none; `wrapped` is what
 * `lapseHandlers` answered, for the whole-surface cases.
 */
function lapsedConsole(current: (installation: string | undefined) => LicenceReply) {
  const source = answeredBy()
  const ipcMain = recordingIpcMain()
  const wrapped = lapseHandlers(source.handlers, current)
  registerConsoleHandlers(ipcMain, wrapped)
  const renderer: RendererLike = {
    async invoke(channel: string, ...args: unknown[]): Promise<unknown> {
      const listener = ipcMain.listeners.get(channel)
      if (listener === undefined) throw new Error(`nothing handles ${channel}`)
      return listener(consoleEvent(), args[0])
    },
  }
  return { bridge: createConsoleBridge(renderer), reached: source.reached, wrapped }
}

/** methodFor is the bridge method an operation is reached by, read from the
 * preload's own table rather than spelled here. */
function methodFor(name: OperationName): string {
  const row = EXPOSED_OPERATIONS.find(([candidate]) => candidate === name)
  if (row === undefined) throw new Error(`${name} is not on the surface`)
  return row[1]
}

/** asCallable widens a bridge method to a function of anything, so a test can
 * hand the preload an ordinary payload without a cast per call site. */
function asCallable(method: unknown): (args?: unknown) => Promise<ConsoleReply> {
  return method as (args?: unknown) => Promise<ConsoleReply>
}

/** bridgeMethod reaches one operation's method on a bridge by name. */
function bridgeMethod(bridge: unknown, name: OperationName): (args?: unknown) => Promise<ConsoleReply> {
  return asCallable((bridge as Record<string, unknown>)[methodFor(name)])
}

describe('the five states a licence reply puts the console in', () => {
  it('maps the store\'s three states, the empty store and a refused key onto the design\'s table', () => {
    expect(licenceStateOf(AT['noKey'] as LicenceReply)).toBe('no-key')
    expect(licenceStateOf(AT['valid'] as LicenceReply)).toBe('valid')
    expect(licenceStateOf(AT['grace'] as LicenceReply)).toBe('grace')
    expect(licenceStateOf(AT['lapsed'] as LicenceReply)).toBe('lapsed')
    expect(licenceStateOf(AT['unverified'] as LicenceReply)).toBe('unverified')
  })

  it('names the lapse its own word, and not the verifier\'s word for a key', () => {
    // The verifier answers `expired` about a *key*; the design's fifth state is
    // the installation's. A module that handed the verifier's word to the gate
    // would leave the refusal reading a key's expiry as the reason a launch
    // cannot start -- and §5.4's unverifiable key, which the verifier answers
    // `refused` about, shows why the two must not be the same value: a key
    // nothing judged would become a capability taken away.
    expect(licenceStateOf(AT['lapsed'] as LicenceReply)).not.toBe('expired')
    expect(licenceStateOf(AT['grace'] as LicenceReply)).not.toBe('lapsed')
    expect(licenceStateOf(AT['valid'] as LicenceReply)).not.toBe('grace')
    // And the grace the `grace` state means is the key's own thirty days (B17).
    // The reply is the state, so the console re-derives nothing; this pins the
    // constant that state is defined by, next to the state it defines.
    expect(GRACE_DAYS).toBe(30)
  })
})

describe('the refused class is the table\'s column', () => {
  it('marks exactly the three operations the design takes away, and no other row', () => {
    const marked = OPERATIONS.filter((operation) => operation.refusedAfterLapse === true).map(
      (operation) => operation.name,
    )
    expect([...marked].sort()).toEqual([...THE_REFUSED].sort())
  })

  it('names the target installation on the launch row, and on none of the other two', () => {
    // The second column the gate reads, and the reason the rows are not
    // decided the same way: the launch acts on the installation its payload
    // names, and the settings write and the roadmap move act on the installation
    // the console is driving. `assertReachableSurface` (`ipc.ts`) holds the column's *shape*
    // -- a required `installationName` argument of the same row -- and this
    // holds the *set*: a settings write given an installation argument would ask
    // the store about a name its own payload never carries.
    expect(
      OPERATIONS.filter((operation) => operation.refusedAfterLapse === true).map((operation) => [
        operation.name,
        operation.installationArgument,
      ]),
    ).toEqual([
      ['run.launch', 'installation'],
      ['settings.edit', undefined],
      ['roadmap.move', undefined],
    ])
  })

  it('leaves the key screen unmarked, because a lapse it could not leave is a trap', () => {
    // §5.6: the key screen is never gated -- it is the way out of every state,
    // including the one where nothing can be launched -- and the mechanical half
    // of that rule is that its two rows spawn nothing: a table where a row with
    // no command carries the column is refused at registration, so a lapse can
    // never make the way out unreachable.
    for (const name of ['licence.read', 'licence.save'] as const) {
      const row = OPERATIONS.find((operation) => operation.name === name)
      expect(row?.refusedAfterLapse, name).toBeUndefined()
      expect(row?.command, name).toBeNull()
    }
    // And no `command: null` row anywhere carries it, which is the guard rather
    // than the two rows it was written for.
    for (const operation of OPERATIONS) {
      if (operation.command === null) {
        expect(operation.refusedAfterLapse, operation.name).toBeUndefined()
      }
    }
  })
})

describe('the lapse, at the handler', () => {
  it('refuses the launch, the settings write and the roadmap move in the main process, and reaches no source', async () => {
    const { bridge, reached } = lapsedConsole(() => AT['lapsed'] as LicenceReply)
    for (const name of THE_REFUSED) {
      const reply = await bridgeMethod(bridge, name)(WELL_FORMED[name])
      expect(reply, name).toEqual({ ok: false, reason: LAPSE_REFUSAL })
    }
    // **The point of the gate**: none of the three reached its handler, so no
    // child was spawned, no setting was read or written, and the record the move
    // would have written is untouched. A gate applied after
    // the handler answers the identical sentence and has already spawned.
    expect(reached).toEqual([])
  })

  it('answers a sentence that names the state and the way out, and carries no figure', () => {
    // §5.7: a refusal names the state and the way out, and never a claim read
    // out of a key -- D430 and T22 keep every figure out of the copy, because a
    // number in a lapse message is a number the operator will try to reconcile.
    expect(LAPSE_REFUSAL).toContain('lapsed')
    expect(LAPSE_REFUSAL).toContain('Project tab')
    expect(LAPSE_REFUSAL).toContain('Licence key')
    expect(LAPSE_REFUSAL, 'a figure in a refusal is D430\'s own prohibition').not.toMatch(/\d/)
    // It is the console's sentence and not a key's: the store's own refusal is a
    // different sentence about a different fact, and the two must not be
    // readable as each other.
    expect(LAPSE_REFUSAL).not.toContain('this build does not hold the key id')
    expect(LAPSE_REFUSAL.length).toBeLessThanOrEqual(240)
  })

  it('keeps every read, the console\'s own surface and the key screen', async () => {
    const { bridge, reached, wrapped } = lapsedConsole(() => AT['lapsed'] as LicenceReply)
    // The kept set at the handler: an empty document, and the source reached.
    for (const name of THE_KEPT) {
      expect(await wrapped[name]({}), name).toEqual({})
    }
    expect([...reached].sort()).toEqual([...THE_KEPT].sort())
    // And end to end for every one of them whose payload is empty: the bridge,
    // the registrar and the schema, with no payload at all. That is the half
    // that would catch a gate wired *around* the registrar rather than into it.
    for (const name of EMPTY_PAYLOADS) {
      expect(await bridgeMethod(bridge, name)(), name).toEqual({ ok: true, data: {} })
    }
    // Named, not counted: the paid views the design keeps readable, the kill
    // switch's state, and the two rows of the key screen.
    for (const name of [
      'spend.report',
      'runs.stats',
      'runs.summary',
      'coordinator.status',
      'budget.show',
      'live.watch',
      'licence.read',
      'licence.save',
      'installation.current',
      'startup.compatibility',
    ] as const) {
      expect(reached, name).toContain(name)
    }
    expect(reached).not.toContain('run.launch')
    expect(reached).not.toContain('settings.edit')
    expect(reached).not.toContain('roadmap.move')
  })

  it('works in full within the grace, which is what the grace is for', async () => {
    const { reached, wrapped } = lapsedConsole(() => AT['grace'] as LicenceReply)
    for (const operation of CONSOLE_OPERATIONS) {
      expect(await wrapped[operation.name]({}), operation.name).toEqual({})
    }
    expect([...reached].sort()).toEqual([...CONSOLE_OPERATIONS.map((operation) => operation.name)].sort())
  })

  it('keeps the no-key state working, because nothing refuses without a key', async () => {
    // §5.3: an installation with no key keeps working -- the upgrade from a
    // console that never had one, a fresh install, and an evaluator who has not
    // asked for a key are all this state -- and the absence of a key is not a
    // lapse. §8 item 6 (which views survive) is the owner's open call and is not
    // decided here: nothing in this state is refused at all.
    const { bridge, reached } = lapsedConsole(() => AT['noKey'] as LicenceReply)
    for (const name of THE_REFUSED) {
      expect(await bridgeMethod(bridge, name)(WELL_FORMED[name]), name).toEqual({
        ok: true,
        data: {},
      })
    }
    expect(reached).toContain('run.launch')
    expect(reached).toContain('settings.edit')
  })

  it('treats a key this build cannot verify as it treats no key, and not as a lapse', async () => {
    // §5.4: a key this build cannot verify (a key id a rollback dropped, a key
    // from a newer release) leaves the installation working. A refusal the
    // verifier declined to judge is not a state, so a gate that read the refused
    // reply as the lapse would take a capability away on a fact nothing judged.
    const { bridge, reached } = lapsedConsole(() => AT['unverified'] as LicenceReply)
    for (const name of THE_REFUSED) {
      expect(await bridgeMethod(bridge, name)(WELL_FORMED[name]), name).toEqual({
        ok: true,
        data: {},
      })
    }
    expect(reached).toContain('run.launch')
    expect(reached).toContain('settings.edit')
  })

  it('reads the state at the call, so a renewal lifts the refusal with no restart', async () => {
    // The reason `current` is a function and not a value: the state is the
    // store's answer at the moment of the call, and a gate closed over one copy
    // of it would keep refusing a launch after the operator pasted a renewal.
    let store: LicenceReply = AT['lapsed'] as LicenceReply
    const { bridge, reached } = lapsedConsole(() => store)
    const launch = bridgeMethod(bridge, 'run.launch')
    expect(await launch(WELL_FORMED['run.launch'])).toEqual({ ok: false, reason: LAPSE_REFUSAL })
    expect(reached).toEqual([])
    store = AT['grace'] as LicenceReply
    expect(await launch(WELL_FORMED['run.launch'])).toEqual({ ok: true, data: {} })
    expect(reached).toEqual(['run.launch'])
  })

  it('judges the launch on the installation its payload names, and the settings write on the binding', async () => {
    // **The bug this holds shut.** `run.launch`'s payload names the installation
    // to launch, and the launcher resolves that name against the console's own
    // records and spawns *that* installation -- handing its child that
    // installation's key (`src/main/launch.ts`). A gate that read only the active
    // binding would therefore admit a lapsed installation's launch whenever the
    // console was driving a valid one, and spawn it; the reverse is the milder
    // half: a valid installation's launch refused because the rail had moved.
    //
    // `settings.edit` is the same fact from the other side: its payload carries
    // no installation at all, and its flow reads and writes the settings document
    // of the installation this console is driving, so the binding *is* its
    // target.
    const asked: (string | undefined)[] = []
    const states: Record<string, LicenceReply> = {
      active: AT['lapsed'] as LicenceReply,
      sandbox: AT['valid'] as LicenceReply,
    }
    const { bridge, reached } = lapsedConsole((installation) => {
      asked.push(installation)
      return states[installation ?? 'active'] as LicenceReply
    })
    // The console is driving a lapsed installation and the payload names a valid
    // one: the launch is admitted, and the installation it names is the one the
    // gate asked about.
    expect(await bridgeMethod(bridge, 'run.launch')(WELL_FORMED['run.launch'])).toEqual({
      ok: true,
      data: {},
    })
    expect(reached).toEqual(['run.launch'])
    expect(asked).toEqual(['sandbox'])
    // The settings write names no installation, so it is judged on the binding
    // -- which has lapsed -- and its handler is never reached.
    expect(await bridgeMethod(bridge, 'settings.edit')(WELL_FORMED['settings.edit'])).toEqual({
      ok: false,
      reason: LAPSE_REFUSAL,
    })
    expect(reached).toEqual(['run.launch'])
    expect(asked).toEqual(['sandbox', undefined])
  })

  it('refuses a launch of a lapsed installation while the console drives a valid one', async () => {
    const states: Record<string, LicenceReply> = {
      active: AT['valid'] as LicenceReply,
      sandbox: AT['lapsed'] as LicenceReply,
    }
    const { bridge, reached } = lapsedConsole(
      (installation) => states[installation ?? 'active'] as LicenceReply,
    )
    expect(await bridgeMethod(bridge, 'run.launch')(WELL_FORMED['run.launch'])).toEqual({
      ok: false,
      reason: LAPSE_REFUSAL,
    })
    expect(reached).toEqual([])
  })

  it('wraps nothing else, and answers a frozen surface', async () => {
    // `reducedModeHandlers`' own shape, and its reason: a wrapper left over an
    // operation the lapse keeps is a second place a refusal could be added in
    // silence. `startup.compatibility` is among the rows asserted here, and it
    // is the one the renderer asks first -- the lapse must never be the reason
    // the handshake cannot be read.
    const source = answeredBy()
    const wrapped = lapseHandlers(source.handlers, () => AT['lapsed'] as LicenceReply)
    for (const name of THE_KEPT) {
      expect(wrapped[name], name).toBe(source.handlers[name])
    }
    for (const name of THE_REFUSED) {
      expect(wrapped[name], name).not.toBe(source.handlers[name])
    }
    expect(Object.isFrozen(wrapped)).toBe(true)
    // And a refused operation refuses with the one sentence, whether it is
    // reached through the registrar or called directly: the wrapper refuses by
    // rejecting with the sentence -- the throw `dispatch` turns into the reply --
    // and it does so before the source, which is the whole of the gate.
    await expect(wrapped['run.launch']({})).rejects.toThrow(LAPSE_REFUSAL)
    expect(source.reached).toEqual([])
  })
})

// ---------------------------------------------------------------------------
// The project key's state, as `fugaro licence status --json` answers it
// (licence-key-v2 plan K3.6). What is held here is the reader: which document it
// accepts, what it makes of the state word (D2143 above all: no key is Free and
// not a lapse), what it refuses and by which sentence, and which reader a
// warning window reaches. Nothing here spawns anything -- the argv that prints
// this document is the spawn allow-list's -- and nothing here gates anything:
// the gate is held by the cases above, over the console's own store.
// ---------------------------------------------------------------------------

/** STATUS is one document as the CLI prints one, field for field, with every
 * value the reader uses. A test moves one field of it at a time, so what a case
 * is about is the one key it names rather than a document written out again. */
function statusDocument(overrides: Record<string, unknown> = {}): string {
  return JSON.stringify({
    project: 'sandbox',
    state: 'active',
    tier: 'team',
    seats: 5,
    paid_through: '2027-11-12T00:00:00Z',
    kind: 'paid',
    renewal_mode: 'auto',
    source: 'project',
    revoked: false,
    last_check: null,
    warning: null,
    ...overrides,
  })
}

/** readStatus is the parse of one document that is expected to be read, so a
 * case about a figure does not restate the refusal it is not about. */
function readStatus(overrides: Record<string, unknown> = {}): ProjectLicenceStatus {
  const parsed = parseLicenceStatusDocument(statusDocument(overrides))
  if (!parsed.ok) throw new Error(parsed.reason)
  return parsed.status
}

/** refuseStatus is the parse of one document that is expected to be refused:
 * the sentence, so a case can hold both that it was refused and which field the
 * sentence names. */
function refuseStatus(overrides: Record<string, unknown>): string {
  const parsed = parseLicenceStatusDocument(statusDocument(overrides))
  if (parsed.ok) throw new Error('the document was read, and this case is about a refusal')
  return parsed.reason
}

/** WARNING is K3.5's field as the default policy writes it: the admins from the
 * first moment of the grace, and every role a week in (spec section 7.1). The
 * sentence is the CLI's own and does not vary by window. */
const WARNING = Object.freeze({
  windows: Object.freeze([
    Object.freeze({ opens: '2027-11-12T00:00:00Z', roles: Object.freeze(['admin']) }),
    Object.freeze({ opens: '2027-11-19T00:00:00Z', roles: Object.freeze(['admin', 'operator', 'viewer']) }),
  ]),
  message: 'licence: the paid period ended on 2027-11-12T00:00:00Z and the grace ends on 2027-12-12T00:00:00Z: renew it with `fugaro licence refresh` before then',
})

describe('the project key\'s state as the CLI answers it', () => {
  it('reads the CLI\'s four state words onto this console\'s own', () => {
    // The words are the CLI's (`licenceStateName`, `ee/register/licence.go`) and
    // the states are this file's, so the mapping is the whole of the join.
    expect(readStatus({ state: 'free' }).state).toBe('no-key')
    expect(readStatus({ state: 'active' }).state).toBe('valid')
    expect(readStatus({ state: 'grace' }).state).toBe('grace')
    expect(readStatus({ state: 'expired' }).state).toBe('lapsed')
  })

  it('reads an installation with no key as Free, which keeps working (D2143)', () => {
    // The decision, and the reason it is not `lapsed`: an installation that was
    // never given a key is the ordinary Free state (spec section 2, section 11.1
    // -- the key step is optional), and a reader that turned it into a lapse
    // would take a capability away over a state nothing judged.
    const free = readStatus({
      state: 'free',
      tier: undefined,
      seats: 0,
      paid_through: null,
      kind: undefined,
      renewal_mode: undefined,
      source: undefined,
    })
    expect(free.state).toBe('no-key')
    expect(free.state).not.toBe('lapsed')
    // And nothing beside it pretends to a figure: the CLI leaves the empty words
    // out (`omitempty`) and writes `null` for the stamp, which read as `null`
    // here.
    expect(free.tier).toBeNull()
    expect(free.seats).toBe(0)
    expect(free.paidThrough).toBeNull()
    expect(free.kind).toBeNull()
    expect(free.renewalMode).toBeNull()
    expect(free.source).toBeNull()
    expect(free.revoked).toBe(false)
    expect(free.lastCheck).toBeNull()
    expect(free.warning).toBeNull()
  })

  it('reads a revoked key as the CLI words it, with the flag beside the state', () => {
    // The CLI answers a revoked key `expired` with `revoked: true`: Free in
    // behaviour, and different news. The reader keeps both, because which of the
    // two a screen says is the screen's business and losing one here would lose
    // it everywhere.
    const revoked = readStatus({ state: 'expired', revoked: true })
    expect(revoked.state).toBe('lapsed')
    expect(revoked.revoked).toBe(true)
  })

  it('refuses a state word it does not know, and never reads it as Free', () => {
    // D7: a screen that misreports because it read a shape it did not understand
    // is worse than one that says it cannot read it. A sixth state a later CLI
    // adds is exactly that -- and the fail-open reading (anything unknown is
    // Free) is the one this refuses.
    const reason = refuseStatus({ state: 'suspended' })
    expect(reason).toContain('state')
    expect(reason).not.toContain('suspended')
  })

  it('refuses text that is not a JSON object, and says so', () => {
    expect(parseLicenceStatusDocument('not json at all')).toEqual({
      ok: false,
      reason: 'the CLI’s licence status is not JSON',
    })
    expect(parseLicenceStatusDocument('[1, 2, 3]')).toEqual({
      ok: false,
      reason: 'the CLI’s licence status is not a JSON object',
    })
    expect(parseLicenceStatusDocument('null')).toEqual({
      ok: false,
      reason: 'the CLI’s licence status is not a JSON object',
    })
  })

  it('refuses a document whose field this console uses is missing or the wrong type, by name', () => {
    // Rule 2 of the section's header: the schema is closed on the fields the
    // console uses. Each sentence names its own field, so a reader of the
    // refusal knows which key the CLI and this console disagree about.
    expect(refuseStatus({ project: undefined })).toContain('`project`')
    expect(refuseStatus({ state: undefined })).toContain('`state`')
    expect(refuseStatus({ seats: '5' })).toContain('`seats`')
    expect(refuseStatus({ seats: -1 })).toContain('`seats`')
    expect(refuseStatus({ seats: 1.5 })).toContain('`seats`')
    expect(refuseStatus({ revoked: 'true' })).toContain('`revoked`')
    expect(refuseStatus({ paid_through: 1_800_000_000 })).toContain('`paid_through`')
    expect(refuseStatus({ last_check: 'none' })).toContain('`last_check`')
    expect(refuseStatus({ warning: 'none' })).toContain('`warning`')
    expect(refuseStatus({ tier: 3 })).toContain('`tier`')
    expect(refuseStatus({ kind: 3 })).toContain('`kind`')
    expect(refuseStatus({ renewal_mode: 3 })).toContain('`renewal_mode`')
    expect(refuseStatus({ source: 3 })).toContain('`source`')
  })

  it('refuses a missing key the CLI always writes, and reads a null one as the value it is', () => {
    // `paid_through`, `last_check` and `warning` are the three keys the CLI
    // always writes and that are `null` where there is nothing to say. `null` is
    // a value here; a key that is not there at all is a document this console has
    // not seen, and reading it as `null` would draw "never checked" for a
    // document that never said so.
    for (const key of ['paid_through', 'last_check', 'warning']) {
      const absent: Record<string, unknown> = {}
      absent[key] = undefined
      expect(refuseStatus(absent), key).toContain(`\`${key}\``)
    }
    const bare = readStatus({ paid_through: null, last_check: null, warning: null })
    expect([bare.paidThrough, bare.lastCheck, bare.warning]).toEqual([null, null, null])
  })

  it('ignores a key this console does not know', () => {
    // The CLI's JSON only grows (`shared/schema.ts`): a field a newer CLI adds
    // reaches no screen and no decision, and must not turn the licence section
    // into a skew screen.
    const status = readStatus({ check_interval: 'P1D', seats_used: 2, note: 'design partner' })
    expect(status.state).toBe('valid')
    expect(status.seats).toBe(5)
    expect(Object.keys(status)).not.toContain('check_interval')
  })

  it('reads the figures beside the state, and the words the CLI leaves out as null', () => {
    const status = readStatus()
    expect(status.project).toBe('sandbox')
    expect(status.tier).toBe('team')
    expect(status.seats).toBe(5)
    expect(status.paidThrough).toBe('2027-11-12T00:00:00Z')
    expect(status.kind).toBe('paid')
    expect(status.renewalMode).toBe('auto')
    expect(status.source).toBe('project')
    // The four the CLI writes with `omitempty` are absent rather than empty in a
    // document that has nothing to say about them, and absent is `null` here --
    // the CLI's own meaning, and not the empty string a screen would draw.
    const bare = readStatus({ tier: undefined, kind: undefined, renewal_mode: undefined, source: undefined })
    expect(bare.tier).toBeNull()
    expect(bare.kind).toBeNull()
    expect(bare.renewalMode).toBeNull()
    expect(bare.source).toBeNull()
  })

  it('reads the last check, and null where the installation never ran one', () => {
    const checked = readStatus({
      last_check: {
        key_id: 'lk_01J9Z',
        at: '2027-11-12T00:00:00Z',
        status: 'ok',
        failures: 2,
        last_ok: '2027-11-12T00:00:00Z',
      },
    })
    expect(checked.lastCheck).toEqual({
      at: '2027-11-12T00:00:00Z',
      status: 'ok',
      failures: 2,
      lastOk: '2027-11-12T00:00:00Z',
    })
    // `failures` and `last_ok` are `omitempty`/`omitzero` on the CLI's record: a
    // check that reached the server writes neither, and a missing count is zero
    // checks in a row and not an unreadable figure.
    const first = readStatus({ last_check: { at: '2027-11-12T00:00:00Z', status: 'ok' } })
    expect(first.lastCheck).toEqual({
      at: '2027-11-12T00:00:00Z',
      status: 'ok',
      failures: 0,
      lastOk: null,
    })
    // The key id the record carries is deliberately not read: the console draws
    // nothing from it, and a reader that carried one would be one writer away
    // from a screen.
    expect(Object.keys(checked.lastCheck ?? {})).not.toContain('keyId')
  })

  it('refuses a check it cannot read, rather than drawing half of one', () => {
    expect(refuseStatus({ last_check: { at: '2027-11-12T00:00:00Z' } })).toContain('`last_check`')
    expect(refuseStatus({ last_check: { status: 'ok', failures: -1 } })).toContain('`last_check`')
    expect(refuseStatus({ last_check: 'none' })).toContain('`last_check`')
  })

  it('bounds the document, its strings and its windows', () => {
    // A bound is what stops a child's output deciding how much this process
    // holds: the CLI's own answer is a few hundred bytes, so these are far above
    // it and still finite.
    expect(parseLicenceStatusDocument(' '.repeat(MAX_LICENCE_STATUS_BYTES + 1)).ok).toBe(false)
    expect(refuseStatus({ project: 'a'.repeat(MAX_LICENCE_STATUS_STRING + 1) })).toContain(
      '`project`',
    )
    const window = { opens: '2027-11-12T00:00:00Z', roles: ['admin'] }
    const windows = Array.from({ length: MAX_LICENCE_WARNING_WINDOWS + 1 }, () => window)
    expect(refuseStatus({ warning: { windows, message: WARNING.message } })).toContain('`warning`')
    expect(
      parseLicenceStatusDocument(
        statusDocument({ warning: { windows, message: WARNING.message } }),
      ).ok,
    ).toBe(false)
  })

  it('puts no text out of the document into a refusal', () => {
    // A refusal is drawn on a screen, so it is the console's own sentence: the
    // value that was refused never reaches it, whatever the CLI (or something
    // wearing the CLI's stdout) put there. This is the reason every sentence
    // above names a field and none quotes one.
    const hostile = '<img src=x onerror="alert(1)">'
    // Each refusal is exactly one of the reader's own constants -- so the
    // assertion is equality and not "does not contain": there is no place in a
    // sentence a document's text could reach in the first place.
    const refused: readonly (readonly [Record<string, unknown>, string])[] = [
      [{ state: hostile }, 'the CLI’s licence status names a state this console does not know'],
      [{ seats: hostile }, 'the CLI’s licence status has no usable `seats`'],
      [{ project: { hostile } }, 'the CLI’s licence status has no usable `project`'],
      [{ tier: { hostile } }, 'the CLI’s licence status has a `tier` that is not a string'],
      [{ last_check: { at: hostile } }, 'the CLI’s licence status has no usable `last_check`'],
      [{ warning: hostile }, 'the CLI’s licence status has no usable `warning`'],
    ]
    for (const [overrides, sentence] of refused) {
      expect(parseLicenceStatusDocument(statusDocument(overrides)), sentence).toEqual({
        ok: false,
        reason: sentence,
      })
      expect(sentence).not.toContain(hostile)
    }
  })
})

describe('the warning the key\'s own policy owes one reader', () => {
  it('answers the sentence to every role the open windows reach', () => {
    // The default policy reaches admins from the first moment of the grace and
    // everyone a week in (spec section 7.1), so at the later instant all three
    // are owed the sentence -- and it is the same sentence, because the CLI
    // composes one for the whole warning.
    const status = readStatus({ state: 'grace', warning: WARNING })
    for (const role of ['admin', 'operator', 'viewer'] as const) {
      expect(projectLicenceWarning(status, role), role).toBe(WARNING.message)
    }
  })

  it('answers nobody a warning the key\'s windows do not reach', () => {
    // The first week of the grace warns admins only: an operator is not owed the
    // sentence yet, and a reader that warned everyone from the first window would
    // be telling a developer to renew a licence that is not theirs to renew.
    const adminsOnly = readStatus({
      state: 'grace',
      warning: { windows: [WARNING.windows[0]], message: WARNING.message },
    })
    expect(projectLicenceWarning(adminsOnly, 'admin')).toBe(WARNING.message)
    expect(projectLicenceWarning(adminsOnly, 'operator')).toBeNull()
    expect(projectLicenceWarning(adminsOnly, 'viewer')).toBeNull()
  })

  it('answers the newest window\'s decision when two reach one role', () => {
    // Two windows may reach one role and the later one is the news that has just
    // changed (the CLI's own rule, `ee/register/warn.go`). The windows are listed
    // in the order they open, which is the CLI's promise for the field, so the
    // reader never compares the stamps as text -- a sub-second spelling sorts
    // before a whole second's and would pick the older window.
    const later = { opens: '2027-11-19T00:00:00.5Z', roles: ['admin'] }
    const earlier = { opens: '2027-11-12T00:00:00Z', roles: ['admin'] }
    const status = readStatus({
      state: 'grace',
      warning: { windows: [earlier, later], message: WARNING.message },
    })
    expect(projectLicenceWarning(status, 'admin')).toBe(WARNING.message)
    // A role no window names is reached by nothing, whatever the windows say.
    expect(projectLicenceWarning(status, 'viewer')).toBeNull()
  })

  it('answers no warning at all where the CLI sent none', () => {
    // A key inside its paid term warns nobody (spec section 7.1's "Active:
    // nobody"), and an installation with no key has no policy to speak of: the
    // CLI sends `null` and there is nothing for a role to weigh.
    for (const state of ['active', 'free'] as const) {
      const status = readStatus({ state, warning: null })
      for (const role of ['admin', 'operator', 'viewer'] as const) {
        expect(projectLicenceWarning(status, role), `${state}/${role}`).toBeNull()
      }
    }
  })

  it('reads the windows beside the sentence, roles and all', () => {
    const status = readStatus({ state: 'grace', warning: WARNING })
    expect(status.warning?.windows).toEqual([
      { opens: '2027-11-12T00:00:00Z', roles: ['admin'] },
      { opens: '2027-11-19T00:00:00Z', roles: ['admin', 'operator', 'viewer'] },
    ])
    // The roles are kept as the CLI's own words and are not narrowed to the
    // three this console knows: a window naming a role this build has never
    // heard of reaches no reader here (the role a caller passes is one of the
    // three), and dropping it would be this reader editing the key's policy.
    const unknown = readStatus({
      state: 'grace',
      warning: { windows: [{ opens: '2027-11-12T00:00:00Z', roles: ['owner'] }], message: WARNING.message },
    })
    expect(unknown.warning?.windows[0]?.roles).toEqual(['owner'])
    expect(projectLicenceWarning(unknown, 'admin')).toBeNull()
  })
})
