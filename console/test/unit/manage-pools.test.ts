// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// People & access: the pools the Manage section draws -- who owns each one, how
// the service picks among its members, and what each member has spent and has
// left (the model-credentials-routing plan's task VS2; the document is C2's
// `routing.PoolListDoc`, `internal/routing/pools.go`, and its prose
// `docs/model-credentials-routing/contract-pools.md`).
//
// **What this file is for.** The acceptance's own claims, and the three rules
// this console makes about a document:
//
//   * **each pool with its owner, its rule, its members by name, each member's
//     state and next reset, and headroom where a figure exists** -- one row per
//     (pool, member), the pool's own facts repeated on each, and every cell drawn
//     from `test/fixtures/accounts-pool-ls.json` (the invented `accounts pool ls
//     --json` document) or from a document written below;
//   * **it never shows a value** -- the document below is written with the keys a
//     careless decoder would reach for (a `value`, a `token`, a `secret_id`, on
//     the pool, on the member and inside the usage object), each holding a run
//     this file asserts appears in no cell, and every row's cells are asserted to
//     be exactly its kind's columns;
//   * **another person's personal pool is a name with usage and headroom** -- the
//     console cannot know who is reading, so what a viewer may not see is what the
//     CLI left out, and this kind's part is to keep the row: a member the
//     document does not name still draws its usage and its headroom, which is the
//     clause that would break if a nameless member were dropped;
//   * **nothing is resolved and no figure is invented** -- a pool that names no
//     rule draws no rule rather than `failover` (the resolution reads a config
//     this document does not carry), and a figure the document does not hold is
//     `n/a` rather than a zero or a percentage of this console's own making
//     (`shared/availability.ts`: a missing value is never an implicit zero), which
//     is also why headroom -- null "otherwise: it is never guessed" (D3178, U1) --
//     is the case the acceptance names.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { KINDS } from '../../src/renderer/manage/kinds/index'
import { POOL_COLUMNS, poolsKind } from '../../src/renderer/manage/kinds/pools'
import { UNAVAILABLE_MARK } from '../../src/shared/availability'
import { sanitizeText } from '../../src/shared/sanitize'
import { CLI_VERSION_BUILT_AGAINST, classifyCliVersion } from '../../src/shared/version'
import type { ManageRow } from '../../src/renderer/manage/types'

/** POOLS_DOC is `test/fixtures/accounts-pool-ls.json`: one `accounts pool ls
 * --json` answer, invented from the structs rather than recorded, and the three
 * cases the kind's columns turn on -- the project's shared pool with two members
 * (one available and counted, one exhausted with a reset and no headroom), a
 * person's own pool whose config names no rule, and a service account's pool
 * whose member the service counts nothing for. */
const POOLS_DOC: unknown = JSON.parse(readFileSync(join('test', 'fixtures', 'accounts-pool-ls.json'), 'utf8'))

/** NOT_DRAWN is a run planted under every key of the document below that is not
 * one of the five members this kind reads, so "this kind draws no value" is a
 * check on the decoder and not a reading of it: a cell that carried one of these
 * fails below. */
const NOT_DRAWN = 'this-run-is-not-a-pool-member'

function rowsOf(doc: unknown = POOLS_DOC): readonly ManageRow[] {
  return poolsKind.decode(doc)
}

function row(key: string, doc: unknown = POOLS_DOC): ManageRow {
  const found = rowsOf(doc).find((each) => each.key === key)
  if (found === undefined) throw new Error(`the pools kind drew no row ${key}`)
  return found
}

/** cellsOf is one row's cells as a list of `[column, cell]` pairs, in the order
 * the kind declares them. */
function cellsOf(each: ManageRow): readonly (readonly [string, string])[] {
  return poolsKind.columns.map((column) => [column.key, each.cells[column.key] ?? ''] as const)
}

/** ONE_POOL is the document below with one pool in it, so a case about a member
 * or a pool writes the smallest document that has one. */
function onePool(pool: Record<string, unknown>): unknown {
  return { project: 'example-project', pools: [pool] }
}

describe('the pools kind', () => {
  it('is one kind of People & access, reading the operation the pools screen names', () => {
    expect(poolsKind.id).toBe('pools')
    expect(poolsKind.group).toBe('People & access')
    expect(poolsKind.title).toBe('Pools')
    expect(poolsKind.source).toBe('accounts.pools')
  })

  it('names its columns and the class each one draws under', () => {
    expect(poolsKind.columns).toEqual([
      { key: 'pool', title: 'Pool', class: 'label' },
      { key: 'owner', title: 'Owner', class: 'label' },
      { key: 'rule', title: 'Rule', class: 'label' },
      { key: 'member', title: 'Member', class: 'label' },
      { key: 'state', title: 'State', class: 'label' },
      { key: 'nextReset', title: 'Next reset', class: 'label' },
      { key: 'usage', title: 'Usage', class: 'label' },
      { key: 'headroom', title: 'Headroom', class: 'label' },
    ])
    expect(POOL_COLUMNS).toEqual(poolsKind.columns)
  })

  it('is in the list the Project tab draws, after the accounts of its own group', () => {
    const kinds = KINDS(classifyCliVersion(CLI_VERSION_BUILT_AGAINST))
    expect(kinds.filter((kind) => kind.group === 'People & access').map((kind) => kind.id)).toEqual([
      'members',
      'roles',
      'accounts',
      'pools',
    ])
  })

  it('offers no control of its own, so nothing can be executed from this screen', () => {
    expect(poolsKind.actions).toEqual([])
  })

  it('passes the class its own column declared', () => {
    for (const each of rowsOf()) {
      for (const column of poolsKind.columns) {
        const cell = each.cells[column.key]
        if (cell === undefined) throw new Error(`a pools row drew no ${column.key} cell`)
        const drawn = sanitizeText(cell, column.class, `pools.${column.key}`)
        expect(drawn.ok, `pools.${column.key} refused ${cell}`).toBe(true)
      }
    }
  })
})

describe('the listing', () => {
  it('draws one row per member of every pool the document lists', () => {
    expect(rowsOf().map((each) => [each.cells.pool, each.cells.member])).toEqual([
      ['launcher', 'fugaro-launcher-primary'],
      ['launcher', 'fugaro-launcher-secondary'],
      ['ana', 'ana-claude-max'],
      ['ci', 'ci-team'],
    ])
  })

  it('draws the project’s own pool with its owner, its rule and its figures', () => {
    expect(row('launcher/fugaro-launcher-primary').cells).toEqual({
      pool: 'launcher',
      owner: 'project example-project',
      rule: 'ordered',
      member: 'fugaro-launcher-primary',
      state: 'available',
      nextReset: '',
      usage: '128 calls, 4,820,000 tokens',
      headroom: '42.0%',
    })
  })

  it('repeats a pool’s own facts on each of its members’ rows', () => {
    expect(cellsOf(row('launcher/fugaro-launcher-primary')).slice(0, 3)).toEqual(
      cellsOf(row('launcher/fugaro-launcher-secondary')).slice(0, 3),
    )
  })

  it('draws a member’s exhaustion with the instant it lapses, and nothing where it is held by nothing', () => {
    expect(row('launcher/fugaro-launcher-secondary').cells.state).toBe('exhausted')
    expect(row('launcher/fugaro-launcher-secondary').cells.nextReset).toBe('2026-11-22 18:30Z')
    expect(row('launcher/fugaro-launcher-primary').cells.nextReset).toBe('')
  })

  it('draws a member with no headroom as no figure rather than as a full one', () => {
    expect(row('launcher/fugaro-launcher-secondary').cells.headroom).toBe(UNAVAILABLE_MARK)
    expect(row('launcher/fugaro-launcher-secondary').cells.usage).toBe('41 calls, 1,500,000 tokens')
  })

  it('draws a person’s pool as whose it is, and the rule its config names nowhere as none', () => {
    expect(row('ana/ana-claude-max').cells).toEqual({
      pool: 'ana',
      owner: 'person 104293871120293847',
      rule: '',
      member: 'ana-claude-max',
      state: 'backed off',
      nextReset: '2026-11-22 13:45Z',
      usage: '3 calls, 90,000 tokens',
      headroom: '80.0%',
    })
  })

  it('draws a service account’s pool, and a member nothing is counted for with no figures at all', () => {
    expect(row('ci/ci-team').cells).toEqual({
      pool: 'ci',
      owner: 'service-account fugaro-ci@example-project.iam.gserviceaccount.com',
      rule: 'round-robin',
      member: 'ci-team',
      state: 'broken',
      nextReset: '',
      usage: `${UNAVAILABLE_MARK} calls, ${UNAVAILABLE_MARK} tokens`,
      headroom: UNAVAILABLE_MARK,
    })
  })

  it('draws a pool the document lists with no member as a row of its own', () => {
    const rows = rowsOf(onePool({ name: 'empty', rule: 'failover', principal: { kind: 'project', id: 'example-project' }, members: [] }))
    expect(rows).toHaveLength(1)
    expect(rows[0]?.cells).toEqual({
      pool: 'empty',
      owner: 'project example-project',
      rule: 'failover',
      member: '',
      state: '',
      nextReset: '',
      usage: '',
      headroom: '',
    })
  })
})

describe('the rule is the pool’s own, and the figures are the document’s', () => {
  it('never resolves an absent rule into failover', () => {
    const rows = rowsOf(
      onePool({
        name: 'p',
        rule: '',
        principal: { kind: 'project', id: 'example-project' },
        members: [{ name: 'm', state: 'available' }],
      }),
    )
    expect(rows[0]?.cells.rule).toBe('')
  })

  it('draws a rule the pool did write exactly as it wrote it', () => {
    for (const rule of ['ordered', 'round-robin', 'failover']) {
      const rows = rowsOf(onePool({ name: 'p', rule, members: [{ name: 'm' }] }))
      expect(rows[0]?.cells.rule).toBe(rule)
    }
  })

  it('draws headroom as the console’s own percentage of the fraction the document carries', () => {
    const rows = rowsOf(
      onePool({
        name: 'p',
        members: [
          { name: 'a', headroom: 0.75 },
          { name: 'b', headroom: 0.8 },
          { name: 'c', headroom: 0.4237 },
          { name: 'd', headroom: 1 },
          { name: 'e', headroom: 0 },
        ],
      }),
    )
    expect(rows.map((each) => each.cells.headroom)).toEqual(['75.0%', '80.0%', '42.4%', '100.0%', '0.0%'])
  })

  it('draws no figure for a headroom the document does not hold, and never a zero of its own', () => {
    const rows = rowsOf(
      onePool({
        name: 'p',
        members: [{ name: 'a', headroom: null }, { name: 'b' }, { name: 'c', headroom: 'half' }],
      }),
    )
    expect(rows.map((each) => each.cells.headroom)).toEqual([
      UNAVAILABLE_MARK,
      UNAVAILABLE_MARK,
      UNAVAILABLE_MARK,
    ])
  })

  it('draws the counts the document states, and no zero for a count it does not', () => {
    const rows = rowsOf(
      onePool({
        name: 'p',
        members: [
          { name: 'a', usage: { calls: 1, tokens: 1 } },
          { name: 'b', usage: { calls: 2 } },
          { name: 'c', usage: { tokens: 3 } },
          { name: 'd', usage: null },
          { name: 'e', usage: {} },
        ],
      }),
    )
    expect(rows.map((each) => each.cells.usage)).toEqual([
      '1 call, 1 token',
      `2 calls, ${UNAVAILABLE_MARK} tokens`,
      `${UNAVAILABLE_MARK} calls, 3 tokens`,
      `${UNAVAILABLE_MARK} calls, ${UNAVAILABLE_MARK} tokens`,
      `${UNAVAILABLE_MARK} calls, ${UNAVAILABLE_MARK} tokens`,
    ])
  })

  it('leaves a state word of a newer release standing rather than guessing at it', () => {
    const rows = rowsOf(onePool({ name: 'p', members: [{ name: 'a', state: 'a-word-of-a-newer-release' }] }))
    expect(rows[0]?.cells.state).toBe('a-word-of-a-newer-release')
  })

  it('draws an instant it cannot read as it stands, and on one line', () => {
    const rows = rowsOf(
      onePool({
        name: 'p',
        members: [
          { name: 'a', next_reset: 'not-an-instant' },
          { name: 'b', next_reset: '2026-11-22\n18:30:00Z' },
        ],
      }),
    )
    expect(rows.map((each) => each.cells.nextReset)).toEqual(['not-an-instant', '2026-11-22 18:30:00Z'])
  })
})

describe('what a document withholds is a blank cell and never a missing row', () => {
  it('keeps an unnamed member’s row, with the usage and the headroom an admin may see', () => {
    const rows = rowsOf(
      onePool({
        name: 'ana',
        principal: { kind: 'person', id: '104293871120293847' },
        members: [{ name: '', state: '', usage: { calls: 4, tokens: 100 }, headroom: 0.5 }],
      }),
    )
    expect(rows).toHaveLength(1)
    expect(rows[0]?.cells).toEqual({
      pool: 'ana',
      owner: 'person 104293871120293847',
      rule: '',
      member: '',
      state: '',
      nextReset: '',
      usage: '4 calls, 100 tokens',
      headroom: '50.0%',
    })
  })

  it('keys two unnamed members of one pool apart, so neither takes the other’s row', () => {
    const rows = rowsOf(
      onePool({ name: 'p', members: [{ name: '', usage: { calls: 1 } }, { name: '', usage: { calls: 2 } }] }),
    )
    expect(rows.map((each) => each.key)).toEqual(['p/#0', 'p/#1'])
    expect(rows.map((each) => each.cells.usage)).toEqual([
      `1 call, ${UNAVAILABLE_MARK} tokens`,
      `2 calls, ${UNAVAILABLE_MARK} tokens`,
    ])
  })

  it('draws no row for a document that is not one, or for a pool list that is not a list', () => {
    expect(rowsOf(null)).toEqual([])
    expect(rowsOf('not-a-document')).toEqual([])
    expect(rowsOf([])).toEqual([])
    expect(rowsOf({ project: 'example-project' })).toEqual([])
    expect(rowsOf({ pools: 'not-a-list' })).toEqual([])
  })

  it('draws a member with no name at all rather than a repaired one', () => {
    const rows = rowsOf(onePool({ name: 'p', members: [{ state: 'available' }] }))
    expect(rows).toHaveLength(1)
    expect(rows[0]?.cells.member).toBe('')
    expect(rows[0]?.cells.state).toBe('available')
  })

  it('draws a pool whose member list holds no object as the pool it is, with no member row', () => {
    const rows = rowsOf(onePool({ name: 'p', members: ['not-an-object'] }))
    expect(rows.map((each) => each.cells.member)).toEqual([''])
  })
})

describe('it draws no value, and no member that is not its own', () => {
  it('draws no member of the document that is not one of its own columns', () => {
    const doc = {
      project: 'example-project',
      version: 1,
      note: NOT_DRAWN,
      pools: [
        {
          name: 'p',
          rule: 'ordered',
          principal: { kind: 'project', id: 'example-project', token: NOT_DRAWN },
          threshold: 0.9,
          strategy: NOT_DRAWN,
          value: NOT_DRAWN,
          secret_id: NOT_DRAWN,
          members: [
            {
              name: 'm',
              state: 'available',
              next_reset: null,
              usage: { calls: 1, tokens: 2, value: NOT_DRAWN, cost_usd: 3 },
              headroom: 0.5,
              token: NOT_DRAWN,
              secret_id: NOT_DRAWN,
              value: NOT_DRAWN,
            },
          ],
        },
      ],
    }
    const cells = rowsOf(doc).flatMap((each) => Object.values(each.cells))
    expect(cells.some((each) => each.includes(NOT_DRAWN))).toBe(false)
    expect(cells.some((each) => each.includes('3'))).toBe(false)
    expect(rowsOf(doc)[0]?.cells.usage).toBe('1 call, 2 tokens')
  })

  it('draws exactly its columns’ cells on every row, and nothing else', () => {
    const keys = poolsKind.columns.map((column) => column.key).sort()
    for (const each of rowsOf()) {
      expect(Object.keys(each.cells).sort()).toEqual(keys)
    }
  })

  it('holds no member a control could run: every row is cells and a key', () => {
    for (const each of rowsOf()) {
      expect(Object.keys(each).sort()).toEqual(['cells', 'key'])
    }
  })
})
