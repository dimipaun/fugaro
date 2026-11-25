// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
//
// The **pinned copy of the last released console's reader** for a finished run's
// `status`: console-v0.125.0 ("Watermelon"), whose `src/shared/schema.ts`
// `finishedAt` read the key with the one expression below. It lives under
// `test/fixtures/` and not beside the live decoder because it is a *fixture*:
// what a person already has on their machine, checked in so a test can ask it a
// question about a document this build is only now learning to write
// (model-credentials-routing VS3, its acceptance; O8/F54's "an older console and
// an older CLI still parse what a newer run writes, or the owner decides a
// version bump").
//
// **Do not update this file when the live decoder changes.** It is a copy of a
// released file and it moves the day that release does not: a later console
// keeps it, and a second copy takes its place beside this one when the next
// release is the one being asked about. `run-paused.test.ts` is what compares
// the two, and the comparison is the whole point -- a rule imported from
// `src/shared/schema.ts` would answer for today's decoder and prove nothing
// about the release.
//
// The three helpers are the released file's own (`objectAt`, `required`,
// `stringAt`), copied verbatim, and so is the call: the release refused a value
// by throwing, as the live file still does, so the copy throws too and a caller
// reads the two answers the same way. The released `finishedAt` read the rest of
// a finished row the same way and **read no other key of it at all** -- it had
// no rule that an unknown key is a refusal -- which is why the copy's input
// below carries a paused row's `resume_at` and `resumed_by` beside the `status`:
// the release never looked at them, and accepting them is what it did.

/** RELEASED_WHERE is the sentence the released decoder named itself with, which
 * `decodeSnapshot` passed it ("the live document"): the release's own words,
 * kept so the copy refuses in the release's voice. */
export const RELEASED_WHERE = 'the live document'

/** ReleasedRefusal is the released file's `Refusal`, thrown for the same reason
 * and carrying the same two facts -- a `refusal` record rather than a parameter
 * property per fact, because the release's own class is written that way and
 * `name` would shadow `Error`'s. */
export class ReleasedRefusal extends Error {
  constructor(readonly refusal: { readonly name: string; readonly field: string }) {
    super(`${RELEASED_WHERE}: ${refusal.field} is ${refusal.name}`)
  }
}

function refuse(name: string, field: string): never {
  throw new ReleasedRefusal({ name, field })
}

function objectAt(raw: unknown, field: string): Record<string, unknown> {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return refuse('not-an-object', field)
  return raw as Record<string, unknown>
}

function required(raw: Record<string, unknown>, key: string, field: string): unknown {
  if (!(key in raw)) return refuse('missing', field === '' ? key : `${field}.${key}`)
  return raw[key]
}

function stringAt(raw: unknown, field: string): string {
  if (typeof raw !== 'string') return refuse('not-a-string', field)
  return raw
}

/**
 * releasedFinishedStatus is console-v0.125.0's reading of one finished run's
 * `status`: the released `finishedAt`'s own line, with the `where` argument the
 * release threaded through every reader left out because the copy names its
 * document itself. It answers the string, or throws `ReleasedRefusal`.
 *
 * **A word the release did not know is answered exactly as a word it did.** The
 * release had no table of statuses: `required` asks only that the key be there
 * and `stringAt` only that its value be a string, so a document carrying
 * `paused` -- or any other word a newer CLI writes -- comes back with that word
 * rather than as a refusal. That is the answer this copy exists to give, and it
 * is the answer O8 turns on.
 */
export function releasedFinishedStatus(
  row: unknown,
  list = 'finished',
  index = 0,
): string {
  const field = `${list}[${String(index)}]`
  return stringAt(required(objectAt(row, field), 'status', field), `${field}.status`)
}
