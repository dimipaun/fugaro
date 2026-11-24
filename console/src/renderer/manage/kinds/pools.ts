// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// People & access: the model pools a project's calls may be served from, and who
// each one belongs to (the model-credentials-routing plan's task VS2; the
// document is C2's `routing.PoolListDoc`, `internal/routing/pools.go`, and its
// prose `docs/model-credentials-routing/contract-pools.md`).
//
// **One row per (pool, member), because that is where the facts are.** The
// document is the project, its pools and each pool's members, and the four
// figures the acceptance asks for -- a member's state, its next reset, its usage
// and its headroom -- are each a member's. So a pool draws one row per member,
// and the pool's own three facts (its name, its principal, its rule) are drawn on
// each of them rather than hoisted into a heading: the shell draws a flat table
// of what a kind decoded and has no heading to put them in (`repos-status` is the
// same shape over a (repository, check) pair). A pool the document lists with no
// member at all is still a pool the screen lists, so it draws one row with the
// member columns empty: a pool whose last member left is a pool an operator can
// see, and a screen that dropped it would be the one place it went missing.
//
// **What a document withholds is an empty cell or an `n/a` and never a missing
// row.** The acceptance has an admin reading another person's personal pool "only
// as a name with usage and headroom", and the console has no way to tell that
// pool from any other: it asks no question about who is reading, and C2's shape
// carries no marker saying which members a viewer was refused. So the withholding
// is the CLI's -- a member whose name an admin may not see is a member the
// document leaves unnamed -- and this kind's part is to keep the row: dropping it
// would take the usage and the headroom with it, which are the two figures that
// clause promises. So a member the document does not name is drawn with an empty
// Member cell and whatever figures it does hold.
//
// **`principal` is the pool's owner, and it is the only owner the document
// has.** C2 keeps `owner` meaning what it always meant -- the display text an
// operator reads in the config -- and gives a pool the `principal` its calls are
// resolved from (D3181): a person's Google subject, a verified service account's
// address, or the project's own name for the shared pool. So the Owner cell is
// the principal's kind and id together. The id alone would read a service
// account's address and a person's subject as the same kind of thing, and a
// subject is a number nobody reads as a person; the class is `label` and not
// `person`, because a principal is that rendered pair and `sanitize.ts`'s
// `person` holds one bare address.
//
// **Nothing here is resolved, and no figure is invented.** A pool that names no
// rule draws an empty Rule cell rather than `failover`: the resolution is
// `RuleOrDefault`'s, which reads the `strategy` the document does not carry from
// the pool's own config (C2), so a console printing `failover` here would be
// naming a rule the pool's owner did not write. A member with no headroom draws
// `n/a`, which is how a screen says there is no figure (`shared/availability.ts`)
// where a `0%` would be one this console made up: the contract's own rule is that
// headroom is null "otherwise: it is never guessed" (D3178, U1).
//
// **Every figure goes through the console's one number door.** The counts and the
// percentage are read with `readNumber` and drawn with `formatReading`
// (`shared/availability.ts`), which is the design's first UI rule -- every number
// carries an availability state, and a missing value is never an implicit zero --
// and the reason none of them is formatted by hand here: `sanitize.test.ts`
// refuses `toFixed` and `toLocaleString` anywhere in the renderer, so a
// percentage this file spelled itself would be the second way to draw a number
// that rule exists to prevent.
//
// **Names and figures, and never a value (F19).** No token, no key and no secret
// is in this document for anyone, an admin included, and this kind's half of that
// rule is that it reads no member that could hold one: a row is built from a
// member's `name`, `state`, `next_reset`, `usage` and `headroom` and from its
// pool's `name`, `rule` and `principal`, and every other key the document carries
// is not read here at all. `manage-pools.test.ts` feeds it a document written
// with the keys a careless decoder would reach for -- a `value`, a `token`, a
// `secret_id` -- each holding a run that must appear in no cell.

import { formatReading, measured, readNumber } from '../../../shared/availability'
import { defineManageKind } from '../types'
import { instantText, stateWordText } from './people'
import { objectAt, objectsAt, textAt } from './read'
import type { Reading } from '../../../shared/availability'
import type { ManageColumn, ManageKind, ManageRow } from '../types'
import type { JsonObject } from './read'

/**
 * POOL_COLUMNS is the pools' table. Every class is `label`, which is the CLI's
 * own short rendered field and what every one of these cells is: a pool's name
 * and a member's name are names a config wrote, and the owner, the rule, the
 * state, the instant, the counts and the percentage are each a rendering this
 * kind makes of a document's member. None of them is free text (`sentence`) and
 * none is a value with a grammar of its own to hold (`repository`, `scope`,
 * `person`): a member's name is a container's, in the project's namespace or the
 * principal's own, and what such a name may be is not this console's to decide.
 */
export const POOL_COLUMNS: readonly ManageColumn[] = Object.freeze([
  { key: 'pool', title: 'Pool', class: 'label' },
  { key: 'owner', title: 'Owner', class: 'label' },
  { key: 'rule', title: 'Rule', class: 'label' },
  { key: 'member', title: 'Member', class: 'label' },
  { key: 'state', title: 'State', class: 'label' },
  { key: 'nextReset', title: 'Next reset', class: 'label' },
  { key: 'usage', title: 'Usage', class: 'label' },
  { key: 'headroom', title: 'Headroom', class: 'label' },
])

/** The three figures' wire paths, as the contract spells them. `readNumber`'s
 * field ends up in the sentence an `n/a` is explained by, so each one is the
 * path the document is read with. */
const HEADROOM_FIELD = 'pools[].members[].headroom'
const CALLS_FIELD = 'pools[].members[].usage.calls'
const TOKENS_FIELD = 'pools[].members[].usage.tokens'

/**
 * ownerText is a pool's principal as its kind and its id, and the empty string
 * where the document holds neither. An id with no kind is drawn alone rather than
 * dropped: the id is what the acceptance asks the Owner column for, and a
 * document that carried it without saying what it names is still a pool whose
 * owner a reader can see.
 */
function ownerText(pool: JsonObject): string {
  const principal = objectAt(pool, 'principal')
  if (principal === null) {
    return ''
  }
  const id = textAt(principal, 'id')
  if (id === '') {
    return ''
  }
  const kind = textAt(principal, 'kind')
  return kind === '' ? id : `${kind} ${id}`
}

/** countText is one count and the noun it counts: the figure through the
 * console's own formatter -- grouped, never by a locale -- and the noun in the
 * singular only where the figure really is one. `n/a` takes the plural, which is
 * the safe direction: it is not a count of one. */
function countText(reading: Reading, noun: string): string {
  return `${formatReading(reading, 'count')} ${reading.value === 1 ? noun : `${noun}s`}`
}

/**
 * usageText is what a member has spent in the window the service counts: its
 * calls and its tokens, each read on its own so that a count the document does
 * not hold is `n/a` and never a zero -- "a missing value is never an implicit
 * zero" is the design's first UI rule, and a member the service counted nothing
 * for (`0`) and one it did not count at all (`null`, C2) are different facts. A
 * member the document holds no usage object for draws the same `n/a` twice, which
 * is what its two counts are.
 */
function usageText(usage: JsonObject | null): string {
  const calls = readNumber(usage === null ? undefined : usage.calls, CALLS_FIELD)
  const tokens = readNumber(usage === null ? undefined : usage.tokens, TOKENS_FIELD)
  return `${countText(calls, 'call')}, ${countText(tokens, 'token')}`
}

/**
 * headroomText is a member's headroom -- the fraction of its limit that is left --
 * as a percentage, or `n/a` where the document holds no figure for it (the
 * contract's rule: it is never guessed, D3178 and U1).
 *
 * The reading is scaled by a hundred before it is drawn because the two sides
 * speak different units: `headroom` is a fraction of a whole (C2, and
 * `routing.Threshold`'s own unit) and the console's `percent` takes a percentage
 * already (`entry.ts` draws a share the same way, `measured(share * 100)`).
 */
function headroomText(headroom: unknown): string {
  const reading = readNumber(headroom, HEADROOM_FIELD)
  if (reading.value === null) {
    return formatReading(reading, 'percent')
  }
  return formatReading(measured(reading.value * 100), 'percent')
}

/**
 * memberKey identifies a member among its pool's rows. A named member's key is
 * its name, which is what the CLI itself identifies one by; a member the document
 * left unnamed -- which is what a withheld name looks like, see the header -- has
 * none to give, so its position stands in for it. The position is the document's
 * own order, which C2 fixes ("the order its owner listed them", the order
 * `ordered` walks), so the key of an unnamed member is stable across two reads of
 * one document.
 */
function memberKey(pool: string, name: string, index: number): string {
  return name === '' ? `${pool}/#${index}` : `${pool}/${name}`
}

/**
 * decodePools draws one row per member of every pool `fugaro accounts pool ls
 * --json` lists, and one row with the member columns empty for a pool it lists
 * with no member in it.
 *
 * The `project` member is not drawn: every pool of the document belongs to it, so
 * it is the document's own heading and not a fact that tells two rows apart, the
 * same reason a read's `version` is no column. `threshold` is not drawn either:
 * the acceptance asks for the rule and the figures, and a pool's threshold is its
 * own policy rather than one of them.
 *
 * A row for a pool with no member draws its figure cells empty and not `n/a`:
 * there is no member for the document to hold a figure about, and `n/a` would
 * read as an answer about one.
 */
function decodePools(doc: unknown): readonly ManageRow[] {
  if (doc === null || typeof doc !== 'object' || Array.isArray(doc)) {
    return []
  }
  const document = doc as JsonObject
  const rows: ManageRow[] = []
  for (const pool of objectsAt(document, 'pools')) {
    const name = textAt(pool, 'name')
    const owner = ownerText(pool)
    const rule = textAt(pool, 'rule')
    const members = objectsAt(pool, 'members')
    if (members.length === 0) {
      rows.push({
        key: `${name}/`,
        cells: { pool: name, owner, rule, member: '', state: '', nextReset: '', usage: '', headroom: '' },
      })
      continue
    }
    for (const [index, member] of members.entries()) {
      const memberName = textAt(member, 'name')
      rows.push({
        key: memberKey(name, memberName, index),
        cells: {
          pool: name,
          owner,
          rule,
          member: memberName,
          state: stateWordText(textAt(member, 'state')),
          nextReset: instantText(textAt(member, 'next_reset')),
          usage: usageText(objectAt(member, 'usage')),
          headroom: headroomText(member.headroom),
        },
      })
    }
  }
  return rows
}

/** pools is the project's model pools: who owns each one, how the service picks
 * among its members, and what each member has spent and has left. */
export const poolsKind: ManageKind = defineManageKind({
  id: 'pools',
  group: 'People & access',
  title: 'Pools',
  source: 'accounts.pools',
  columns: POOL_COLUMNS,
  decode: decodePools,
  actions: [],
})
