// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The console's read-only state after the grace period (subscriptions plan
// S6.2; the design is `docs/design/lapse-read-only.md`, D436 and T12).
//
// **What this file is.** One mapping from the verifier's answer about the key an
// installation holds to the five states of the design's table, and the gate that
// takes exactly two operations away in the state the design calls the lapse: the
// launch and the configuration change. Everything else -- every read, the
// console's own surface, the kill switch's state, the key screen -- answers
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
