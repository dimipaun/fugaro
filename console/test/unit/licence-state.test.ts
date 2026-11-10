// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The console's lapse state, at the handler (subscriptions plan S6.2; the
// design is `docs/design/lapse-read-only.md`, D436 and T12).
//
// What this file holds, and why each part is a test rather than an argument:
//
//   - **the refused class is the table's, not a list of names.** The lapse takes
//     two operations away -- `run.launch` and `settings.edit` -- and it must
//     classify them by the column their own rows carry (`refusedAfterLapse`),
//     because the design's rule is that a later cancel, whose row differs from
//     the launch's in exactly that field, must not be refused the day it is
//     added. A gate built from a list beside the table would pass every case
//     below and fail that one, so the set is asserted here twice: as the two
//     names, and as the rows the column marks;
//   - **the refusal happens in the main process, before any child exists.** The
//     renderer is not the gate: the channels come from the operation table and
//     from nothing else, so the refusal tests go through the real registrar and
//     the real bridge, and assert that the source behind the handler was never
//     reached -- a gate applied after the handler would answer the same sentence
//     and have spawned the launch anyway;
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
  ConsoleReply,
  OperationName,
} from '../../src/main/ipc'
import { GRACE_DAYS } from '../../src/main/licence'
import { LAPSE_REFUSAL, lapseHandlers, licenceStateOf } from '../../src/main/licence-state'
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

/** THE_REFUSED is the lapsed state's whole class, written out as a literal so
 * the column is checked against the design rather than against itself: T12 takes
 * the console's launch and its configuration change away, and nothing else. */
const THE_REFUSED: readonly OperationName[] = ['run.launch', 'settings.edit']

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
 * WELL_FORMED is what the registrar's own schema accepts for the two refused
 * operations. The tests that go through the bridge for a refusal are about the
 * gate, and a payload the schema refuses would answer a refusal this file is not
 * about; `ipc.test.ts` holds the same two payloads for the same reason.
 */
const WELL_FORMED: Readonly<Record<string, Record<string, unknown>>> = Object.freeze({
  'run.launch': { installation: 'sandbox', repository: 'acme/app', task: 'do the thing' },
  'settings.edit': { op: 'set', key: 'budget.cap_usd', value: '25' },
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
 * bridge. `current` is a function, so a test can move the state under the gate;
 * `wrapped` is what `lapseHandlers` answered, for the whole-surface cases.
 */
function lapsedConsole(current: () => LicenceReply) {
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
  it('marks exactly the two operations the design takes away, and no other row', () => {
    const marked = CONSOLE_OPERATIONS.filter(
      (operation) => operation.refusedAfterLapse === true,
    ).map((operation) => operation.name)
    expect([...marked].sort()).toEqual([...THE_REFUSED].sort())
  })

  it('leaves the key screen unmarked, because a lapse it could not leave is a trap', () => {
    // §5.6: the key screen is never gated -- it is the way out of every state,
    // including the one where nothing can be launched -- and the mechanical half
    // of that rule is that its two rows spawn nothing: a table where a row with
    // no command carries the column is refused at registration, so a lapse can
    // never make the way out unreachable.
    for (const name of ['licence.read', 'licence.save'] as const) {
      const row = CONSOLE_OPERATIONS.find((operation) => operation.name === name)
      expect(row?.refusedAfterLapse, name).toBeUndefined()
      expect(row?.command, name).toBeNull()
    }
    // And no `command: null` row anywhere carries it, which is the guard rather
    // than the two rows it was written for.
    for (const operation of CONSOLE_OPERATIONS) {
      if (operation.command === null) {
        expect(operation.refusedAfterLapse, operation.name).toBeUndefined()
      }
    }
  })
})

describe('the lapse, at the handler', () => {
  it('refuses the launch and the settings write in the main process, and reaches no source', async () => {
    const { bridge, reached } = lapsedConsole(() => AT['lapsed'] as LicenceReply)
    for (const name of THE_REFUSED) {
      const reply = await bridgeMethod(bridge, name)(WELL_FORMED[name])
      expect(reply, name).toEqual({ ok: false, reason: LAPSE_REFUSAL })
    }
    // **The point of the gate**: neither operation reached its handler, so no
    // child was spawned and no setting was read or written. A gate applied after
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

  it('wraps nothing else, and answers a frozen surface', () => {
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
    // And a refused operation throws the one sentence the bridge answers, so the
    // wrapper is the same gate however it is reached.
    expect(() => wrapped['run.launch']({})).toThrow(LAPSE_REFUSAL)
    expect(source.reached).toEqual([])
  })
})
