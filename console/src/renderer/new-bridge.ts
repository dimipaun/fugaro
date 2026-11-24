// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The renderer's typed read of the nine new operations (C-OPS).
//
// **Why a second door.** `src/renderer/entry.ts` reaches the bridge through
// `readBridgeFrom`, which names one member per read and one read per screen: it
// is the whole of the old surface, and a task that added an operation to it
// would put a new screen's name in the file every screen shares. `newOpsFrom`
// is the same idea, one step out: it takes the structural bridge and answers the
// nine operations of `src/shared/new-ops.ts` typed, so the consumer that reaches
// `runs.stream` is the screen that draws it and not this file. A consumer
// therefore imports this module and the contract, and edits neither `entry.ts`
// nor `ipc.ts`.
//
// **Its three answers, and why they are three.** The bridge crosses a process
// boundary twice -- the preload's channel and, behind it, the CLI -- so a read
// that answers nothing can be three different facts, and a screen draws a
// different thing for each:
//
//   - **not available**: the bridge has no such method, or the call threw or
//     rejected. The console's own channel answers a refusal rather than a
//     rejection (`ipc.ts`'s sender gate and its handler wrapper), so a rejection
//     is the preload's own failure -- no handler behind the channel, a bridge
//     from an older preload -- and it is the same fact as a missing method: this
//     build cannot ask. AL's stub handler answers this build's sentence, so a
//     screen shows one sentence for "not wired yet" wherever the read came back
//     empty.
//   - **refused**: the console answered its ordinary refusal (`{ok: false,
//     reason}`), a sentence a person reads and that names what to do. It is the
//     CLI's own words where the CLI refused, and the operation's own sentence
//     where the allow-list refused the payload before spawning anything.
//   - **unreadable**: something answered, and it is not a shape this build can
//     read -- a reply that is not the console's envelope, or a document the
//     contract's decoders refused. A skew is carried as a gap and never as an
//     empty value, for `replyData`'s reason: a screen handed `{}` draws zeros
//     nobody measured.
//
// Nothing here rejects: a method missing, a channel that throws and a reply that
// will not decode all answer a resolved gap, so a screen's own `try`/`catch` is
// for its own bugs and never for the bridge's (`src/renderer/section.ts`).

import {
  NEW_OPS_NOT_AVAILABLE,
  NEW_OPS_UNREADABLE,
  NEW_OPERATION_METHODS,
  decodeNewOperationAnswer,
} from '../shared/new-ops'
import type {
  BudgetStreamRequest,
  NewOperationAnswers,
  NewOperationName,
  NewOps,
  NewOpsGapName,
  NewOpsReply,
  NoArgumentRequest,
  RunsStreamRequest,
  SettingsEditorOpenRequest,
  SetupPlanRequest,
  SpendStreamRequest,
} from '../shared/new-ops'

/** ConsoleMethod is a member of the structural bridge: the shape
 * `readBridgeFrom` binds one of, and the only thing this module assumes about
 * the preload. Its argument and its answer are `unknown` because they cross a
 * channel: the payload is the contract's request type, and the answer is checked
 * here rather than trusted. */
type ConsoleMethod = (payload: unknown) => unknown

/** methodOf binds one bridge member, or answers `null` for a bridge that has no
 * such member (or is not an object at all). The member is bound to the bridge
 * because the preload's methods are plain functions of their argument, and an
 * unbound `this` would make a future one that reads its own object answer
 * `undefined` instead of failing loudly. */
function methodOf(fugaro: unknown, name: string): ConsoleMethod | null {
  if (fugaro === null || typeof fugaro !== 'object') return null
  const member = (fugaro as Record<string, unknown>)[name]
  if (typeof member !== 'function') return null
  return (member as ConsoleMethod).bind(fugaro)
}

/** Reply is the console's answer, read: the data of an accepted call, or the
 * sentence of a refusal. */
type Reply =
  | { readonly ok: true; readonly data: unknown }
  | { readonly ok: false; readonly sentence: string }

/** replyOf reads an answer as the console's envelope, or answers `null` for
 * something that is not one. A refusal with no string reason is not read as a
 * refusal with an empty sentence: it is a reply nobody can draw, and the gap for
 * that is `unreadable` rather than `refused`. */
function replyOf(raw: unknown): Reply | null {
  if (raw === null || typeof raw !== 'object') return null
  const record = raw as Record<string, unknown>
  if (record['ok'] === true) return { ok: true, data: record['data'] }
  const reason = record['reason']
  if (record['ok'] === false && typeof reason === 'string') return { ok: false, sentence: reason }
  return null
}

/** gap is one of the three answers, typed by the caller. */
function gap<Answer>(gapName: NewOpsGapName, sentence: string): NewOpsReply<Answer> {
  return { ok: false, gap: { gap: gapName, sentence } }
}

/** ask is one operation, end to end: the member, the call, the envelope, the
 * decode. Its answer type is the contract's own, so the object below cannot
 * answer a shape the type does not name. */
async function ask<Name extends NewOperationName>(
  fugaro: unknown,
  name: Name,
  payload: unknown,
): Promise<NewOpsReply<NewOperationAnswers[Name]>> {
  const method = methodOf(fugaro, NEW_OPERATION_METHODS[name])
  if (method === null) return gap('not-available', NEW_OPS_NOT_AVAILABLE)
  let raw: unknown
  try {
    raw = await method(payload)
  } catch {
    return gap('not-available', NEW_OPS_NOT_AVAILABLE)
  }
  const reply = replyOf(raw)
  if (reply === null) return gap('unreadable', NEW_OPS_UNREADABLE)
  if (!reply.ok) return gap('refused', reply.sentence)
  const decoded = decodeNewOperationAnswer(name, reply.data)
  if (!decoded.ok) return gap('unreadable', `${NEW_OPS_UNREADABLE}: ${decoded.skew.because}`)
  return { ok: true, value: decoded.value }
}

/**
 * newOpsFrom is the renderer's typed read of the nine operations, over the
 * structural bridge (`window.fugaro`, or a fake of it -- which is what a
 * screen's own test hands it, built from the contract's types rather than from
 * a fixture bridge).
 *
 * The argument is `unknown` on purpose: this module is the boundary that checks
 * a bridge, so a caller that hands it a wrong object gets nine gaps and not a
 * crash, and a screen's test can hand it `{}` and read `not-available` for every
 * one of them.
 */
export function newOpsFrom(fugaro: unknown): NewOps {
  return {
    streamRuns: (request: RunsStreamRequest) => ask(fugaro, 'runs.stream', request),
    streamSpend: (request: SpendStreamRequest) => ask(fugaro, 'spend.stream', request),
    streamBudget: (request: BudgetStreamRequest) => ask(fugaro, 'budget.stream', request),
    whoAmI: (request: NoArgumentRequest) => ask(fugaro, 'whoami', request),
    listImageCatalog: (request: NoArgumentRequest) => ask(fugaro, 'image.catalog.ls', request),
    listRecipes: (request: NoArgumentRequest) => ask(fugaro, 'recipes.ls', request),
    planSetup: (request: SetupPlanRequest) => ask(fugaro, 'setup.plan', request),
    applySetup: (request: NoArgumentRequest) => ask(fugaro, 'setup.apply', request),
    openSettingsEditor: (request: SettingsEditorOpenRequest) =>
      ask(fugaro, 'settings.editor.open', request),
  }
}
