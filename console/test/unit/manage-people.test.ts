// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// People & access: the members, the roles and the Claude accounts the Manage
// section draws (the console-improvements plan's task MO-P; spec sections 5 and
// 8 as D2903 and D2905 amended them; the prose is
// `docs/console-improvements/manage-kinds.md`).
//
// **What this file is for.** Three claims the task's acceptance makes, and one
// this console's rules make:
//
//   * **three lists with their columns and their status** -- the members with the
//     roles each holds, the stored pairs with the scope each is held at and the
//     repositories this machine knows it matches, and the accounts with the state
//     each one's mark records. Every cell is drawn from
//     `test/fixtures/members.json` (the invented `user ls --json` v2 document) or
//     from the `accounts ls --json` document below, and every cell passes its own
//     column's `TEXT_CLASSES` class -- a wrong class is a cell the shell refuses,
//     so the class is a fact this file checks rather than a note it repeats;
//   * **no write control exists** -- every action of these kinds is a
//     `copy-command` (or, nowhere here, an `open-view`), every one carries exactly
//     the members of a copy-command and nothing else, and the file exports no
//     other member. A control that ran a command itself would be a new action
//     `kind`, a new member of one or a new export, and each of the three fails
//     here;
//   * **every add, revoke, removal and offboarding is one exact line** -- the
//     add commands carry the placeholders only the operator can fill, the row
//     commands carry the values the read answered, and a value the builder refuses
//     (the fixture's comma-separated scope) is a row with no Copy rather than a
//     repaired one;
//   * **each destructive copy carries the command's own sentence** (spec section
//     8), held here to the Go file it is quoted from: the file is read as text and
//     the run is matched against it with whitespace collapsed, as the console's
//     other cross-language pins do (`schema.test.ts`, `fixture.test.ts`).
//
// **No secret is drawn (D2905).** The accounts document below is written with the
// parts of `accounts ls --json` that are not accounts -- the credential block, the
// alias map -- and with keys a careless decoder would read (a `token`, a
// `secret_id`, a state object carrying more than a state), each holding a run this
// file asserts appears in no cell and in no argv.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import { copyCommandText } from '../../src/renderer/manage/command'
import { PEOPLE_KINDS, accounts, members, roles } from '../../src/renderer/manage/kinds/people'
import { sanitizeText } from '../../src/shared/sanitize'
import type { CopyCommandAction, ManageAction, ManageKind, ManageRow } from '../../src/renderer/manage/types'

/** MEMBERS_DOC is `test/fixtures/members.json`: one `user ls --json` version-2
 * answer, invented rather than recorded, and the document the Members view of the
 * roles-and-scopes work already reads (`schema.test.ts` pins its keys against the
 * Go structs). It carries the cases this file needs and a recording would not: a
 * grant whose scope is one pattern, one whose scope is a comma-separated list, one
 * whose scope matches nothing this machine knows, and the group:, domain: and
 * serviceAccount: members a grant cannot hold (F25). */
const MEMBERS_DOC: unknown = JSON.parse(readFileSync(join('test', 'fixtures', 'members.json'), 'utf8'))

/** NOT_DRAWN is a run planted in the accounts document under every key that is
 * not an account, so "this kind draws no secret" is a check and not a reading of
 * the decoder: a cell or an argv that carried one of these keys fails below. */
const NOT_DRAWN = 'this-run-is-not-an-account'

/** ACCOUNTS_DOC is one `accounts ls --json` answer. It is written here rather than
 * added to `test/fixtures/` on purpose: that directory's list is pinned
 * (`fixture.test.ts`), and this document answers one kind's columns where the
 * fixture corpus stands for real CLI answers. */
const ACCOUNTS_DOC: unknown = {
  repo: 'github/example-owner/example-repo',
  accounts: ['default', 'team'],
  complete: true,
  states: {
    default: { state: 'available' },
    team: { state: 'exhausted', until: '2026-11-20T18:30:00Z', token: NOT_DRAWN },
  },
  aliases: { fast: 'team' },
  credentials: [
    { name: 'claude-oauth-token-team', type: 'claude-oauth', serving_path: 'credsvc' },
  ],
  token: NOT_DRAWN,
  secret_id: NOT_DRAWN,
}

/** goSource is one Go file read as text: the revoke, offboarding and account
 * removal sentences below are quoted from those files, and each is read for the
 * reason `schema.test.ts` reads the Go structs -- a test that imported nothing from
 * the CLI cannot drift from it silently, because the pin fails when the Go side's
 * sentence changes. */
function goSource(path: string): string {
  return readFileSync(fileURLToPath(new URL(`../../../${path}`, import.meta.url)), 'utf8')
}

const USER_GRANT_SOURCE = goSource('internal/cli/user_grant.go')
const OFFBOARD_SOURCE = goSource('internal/cli/offboard.go')
const ACCOUNTS_RM_SOURCE = goSource('internal/cli/accounts_rm.go')

/** collapsed is one text with its whitespace runs collapsed to single spaces: a
 * Go raw string is broken at the help's own margins, so a sentence's words are not
 * on one line in the file, and the sentence is what this file checks. */
function collapsed(text: string): string {
  return text.replace(/\s+/g, ' ')
}

/** actionsOf is every action a kind offers, the kind's own and every row's, over
 * the documents above -- the whole surface a control could be added to. */
function actionsOf(kind: ManageKind, doc: unknown = documentFor(kind)): readonly ManageAction[] {
  const all: ManageAction[] = [...kind.actions]
  for (const row of kind.decode(doc)) {
    all.push(...(row.actions ?? []))
  }
  return all
}

function documentFor(kind: ManageKind): unknown {
  return kind.source === 'accounts.inventory' ? ACCOUNTS_DOC : MEMBERS_DOC
}

function rowsOf(kind: ManageKind, doc: unknown = documentFor(kind)): readonly ManageRow[] {
  return kind.decode(doc)
}

function row(kind: ManageKind, key: string, doc: unknown = documentFor(kind)): ManageRow {
  const found = rowsOf(kind, doc).find((each) => each.key === key)
  if (found === undefined) throw new Error(`${kind.id} drew no row ${key}`)
  return found
}

/** copies is a row's or a kind's copy-commands, refused as the row's own absence
 * rather than as an empty list a caller could read past. */
function copies(actions: readonly ManageAction[] | undefined): readonly CopyCommandAction[] {
  return (actions ?? []).filter((each): each is CopyCommandAction => each.kind === 'copy-command')
}

function lines(actions: readonly ManageAction[] | undefined): readonly string[] {
  return copies(actions).map((each) => copyCommandText(each))
}

/** only is the one command a list holds, or a failure naming how many it held:
 * `noUncheckedIndexedAccess` makes every index a maybe, and a test that checked a
 * copy by index would be asserting on `undefined` the day the list emptied. */
function only(actions: readonly ManageAction[] | undefined): CopyCommandAction {
  const found = copies(actions)
  const [first] = found
  if (found.length !== 1 || first === undefined) {
    throw new Error(`wanted exactly one copy-command, found ${found.length}`)
  }
  return first
}

describe('the three kinds of People & access', () => {
  it('is the members, the roles and the accounts, each in the group’s own order', () => {
    expect(PEOPLE_KINDS.map((kind) => kind.id)).toEqual(['members', 'roles', 'accounts'])
    expect(PEOPLE_KINDS.every((kind) => kind.group === 'People & access')).toBe(true)
  })

  it('reads the two operations the console admits, and no other', () => {
    expect(members.source).toBe('members.list')
    expect(roles.source).toBe('members.list')
    expect(accounts.source).toBe('accounts.inventory')
  })

  it('names its columns and the class each one draws under', () => {
    expect(members.columns).toEqual([
      { key: 'member', title: 'Member', class: 'label' },
      { key: 'roles', title: 'Roles', class: 'label' },
      { key: 'status', title: 'Status', class: 'label' },
    ])
    expect(roles.columns).toEqual([
      { key: 'member', title: 'Member', class: 'label' },
      { key: 'role', title: 'Role', class: 'label' },
      { key: 'scope', title: 'Scope', class: 'label' },
      { key: 'status', title: 'Status', class: 'label' },
    ])
    expect(accounts.columns).toEqual([
      { key: 'account', title: 'Account', class: 'person' },
      { key: 'state', title: 'State', class: 'label' },
    ])
  })
})

describe('the members list', () => {
  it('draws one row per member any of the document’s three lists names', () => {
    expect(rowsOf(members).map((each) => each.cells.member)).toEqual([
      'user:operator@example.com',
      'user:second-operator@example.com',
      'group:fugaro-operators@example.com',
      'domain:example.com',
      'user:admin@example.com',
      'group:fugaro-admins@example.com',
      'serviceAccount:fugaro-admin@example-project.iam.gserviceaccount.com',
      'user:pipeline@example.com',
      'user:reviewer@example.com',
      'user:elsewhere@example.com',
    ])
  })

  it('gives each member the roles the roster and the lists place on them', () => {
    expect(row(members, 'user:admin@example.com').cells.roles).toBe('admin')
    expect(row(members, 'user:pipeline@example.com').cells.roles).toBe('operator')
    expect(row(members, 'user:reviewer@example.com').cells.roles).toBe('viewer')
  })

  it('says whether a member is still placed, only on a list, or legacy', () => {
    expect(row(members, 'user:admin@example.com').cells.status).toBe('in the roster')
    expect(row(members, 'group:fugaro-operators@example.com').cells.status).toBe('legacy')
    expect(row(members, 'user:elsewhere@example.com').cells.status).toBe('in the roster')
  })

  it('tells a member a grant places apart from one only this machine’s lists hold', () => {
    const onlyOnAList = rowsOf(members, {
      version: 2,
      run_operators: ['user:operator@example.com'],
      admins: [],
      grants: [],
      effective: [],
      legacy: [],
    })
    expect(onlyOnAList).toHaveLength(1)
    expect(onlyOnAList.map((each) => each.cells.status)).toEqual(['list only'])
  })

  it('does not draw the effective list, which answers about one repository', () => {
    expect(rowsOf(members).every((each) => !('repo' in each.cells))).toBe(true)
    expect(rowsOf(members).some((each) => Object.values(each.cells).includes('github/example-owner/example-repo'))).toBe(false)
  })
})

describe('the roles list', () => {
  it('draws one row per stored pair, in the node’s own order', () => {
    expect(rowsOf(roles).map((each) => [each.cells.member, each.cells.role, each.cells.scope])).toEqual([
      ['user:admin@example.com', 'admin', 'project'],
      ['user:operator@example.com', 'operator', 'github/example-owner/*'],
      ['user:second-operator@example.com', 'operator', 'github/example-owner/example-repo'],
      ['user:pipeline@example.com', 'operator', 'github/example-owner/example-repo,github/example-owner/other-repo'],
      ['user:reviewer@example.com', 'viewer', 'all-repos'],
      ['user:elsewhere@example.com', 'viewer', 'bitbucket/another-owner/*'],
    ])
  })

  it('gives a pair the repositories this machine knows its scope matches (F27)', () => {
    expect(row(roles, '["user:admin@example.com","admin","project"]').cells.status).toBe('2 known repositories')
    expect(row(roles, '["user:second-operator@example.com","operator","github/example-owner/example-repo"]').cells.status).toBe('1 known repository')
    expect(row(roles, '["user:elsewhere@example.com","viewer","bitbucket/another-owner/*"]').cells.status).toBe('no known repository')
  })

  it('keys a row by its whole pair, so two pairs of one member cannot collide', () => {
    const keys = rowsOf(roles).map((each) => each.key)
    expect(new Set(keys).size).toBe(keys.length)
  })
})

describe('the accounts list', () => {
  it('draws one row per name, with the state its mark records', () => {
    expect(rowsOf(accounts).map((each) => [each.cells.account, each.cells.state])).toEqual([
      ['default', 'available'],
      ['team', 'exhausted until 2026-11-20 18:30Z'],
    ])
  })

  it('mirrors the CLI’s own words, and leaves a word it does not know alone', () => {
    const doc = {
      accounts: ['a', 'b', 'c', 'd', 'e'],
      states: {
        a: { state: 'backed_off' },
        b: { state: 'broken' },
        c: { state: 'exhausted' },
        d: { state: 'exhausted', until: 'not-an-instant' },
        e: { state: 'a-word-of-a-newer-release' },
      },
    }
    expect(rowsOf(accounts, doc).map((each) => each.cells.state)).toEqual([
      'backed off',
      'broken',
      'exhausted',
      'exhausted until not-an-instant',
      'a-word-of-a-newer-release',
    ])
  })

  it('says no state was read rather than claiming every account is available', () => {
    const doc = { accounts: ['default'], complete: false, state_note: 'this project has no budget database' }
    expect(rowsOf(accounts, doc).map((each) => each.cells.state)).toEqual(['no state read'])
  })

  it('draws no part of the document that is not an account', () => {
    const cells = rowsOf(accounts).flatMap((each) => Object.values(each.cells))
    expect(cells.some((each) => each.includes(NOT_DRAWN))).toBe(false)
    expect(cells.some((each) => each.includes('claude-oauth-token'))).toBe(false)
    expect(cells.some((each) => each.includes('credsvc'))).toBe(false)
    expect(cells.some((each) => each === 'fast')).toBe(false)
  })

  it('draws nothing at all from a document with no accounts in it', () => {
    expect(rowsOf(accounts, { accounts: [] })).toEqual([])
    expect(rowsOf(accounts, {})).toEqual([])
    expect(accounts.decode(undefined)).toEqual([])
    expect(rowsOf(accounts, 'not a document')).toEqual([])
  })
})

describe('no write control', () => {
  it('offers a copy-command or a view, and nothing else', () => {
    for (const kind of PEOPLE_KINDS) {
      for (const action of actionsOf(kind)) {
        expect(['copy-command', 'open-view']).toContain(action.kind)
      }
    }
  })

  it('carries exactly the members of a copy-command, so nothing can be executed', () => {
    for (const kind of PEOPLE_KINDS) {
      for (const action of actionsOf(kind)) {
        expect(Object.keys(action).sort()).toEqual(
          action.kind === 'copy-command' && action.consequence !== undefined
            ? ['argv', 'consequence', 'kind']
            : action.kind === 'copy-command'
              ? ['argv', 'kind']
              : ['kind', 'view'],
        )
        for (const value of Object.values(action)) {
          expect(typeof value).not.toBe('function')
        }
      }
    }
  })

  it('exports the three kinds and their list, and no control of its own', async () => {
    const module = await import('../../src/renderer/manage/kinds/people')
    expect(Object.keys(module).sort()).toEqual(['PEOPLE_KINDS', 'accounts', 'members', 'roles'])
  })
})

describe('every write is a line to copy', () => {
  it('copies the pair a member is given, with the values only the operator knows', () => {
    expect(lines(members.actions)).toEqual([])
    expect(lines(roles.actions)).toEqual(['fugaro user grant MEMBER --role ROLE --scope SCOPE'])
    expect(lines(accounts.actions)).toEqual(['fugaro accounts add NAME'])
  })

  it('copies a revoke with the pair’s own member, role and scope', () => {
    expect(lines(row(roles, '["user:admin@example.com","admin","project"]').actions)).toEqual([
      'fugaro user revoke user:admin@example.com --role admin --scope project',
    ])
    expect(lines(row(roles, '["user:second-operator@example.com","operator","github/example-owner/example-repo"]').actions)).toEqual([
      'fugaro user revoke user:second-operator@example.com --role operator --scope github/example-owner/example-repo',
    ])
    expect(lines(row(roles, '["user:reviewer@example.com","viewer","all-repos"]').actions)).toEqual([
      'fugaro user revoke user:reviewer@example.com --role viewer --scope all-repos',
    ])
  })

  it('draws a pair’s row without a Copy when the builder refuses its scope, never a repaired line', () => {
    // `command.ts` refuses a `*` (a shell glob) and a `,` (outside the alphabet),
    // and a value that does not fit refuses the whole action: the row keeps its
    // scope in the Scope column and the operator has the command's name to type.
    const glob = '["user:operator@example.com","operator","github/example-owner/*"]'
    expect(row(roles, glob).cells.scope).toBe('github/example-owner/*')
    expect(row(roles, glob).actions).toBeUndefined()

    const comma = '["user:pipeline@example.com","operator","github/example-owner/example-repo,github/example-owner/other-repo"]'
    expect(row(roles, comma).cells.scope).toBe('github/example-owner/example-repo,github/example-owner/other-repo')
    expect(row(roles, comma).actions).toBeUndefined()
  })

  it('copies an offboarding for a person, and offers none for a member no address names', () => {
    expect(lines(row(members, 'user:admin@example.com').actions)).toEqual(['fugaro user offboard admin@example.com'])
    expect(lines(row(members, 'group:fugaro-operators@example.com').actions)).toEqual([])
    expect(row(members, 'group:fugaro-operators@example.com').actions).toBeUndefined()
    expect(row(members, 'domain:example.com').actions).toBeUndefined()
    expect(row(members, 'serviceAccount:fugaro-admin@example-project.iam.gserviceaccount.com').actions).toBeUndefined()
  })

  it('copies an account’s removal with its own name', () => {
    expect(lines(row(accounts, 'team').actions)).toEqual(['fugaro accounts rm team'])
    expect(lines(row(accounts, 'default').actions)).toEqual(['fugaro accounts rm default'])
  })
})

describe('the consequence a destructive copy states', () => {
  /** Each case is a command's copy, the file its own help is written in, and the
   * run of that help the copy must carry. The run is checked both ways: the copy
   * contains it, and the file contains it (whitespace collapsed), so a sentence
   * that was composed here rather than quoted cannot pass. */
  const CASES: readonly {
    readonly what: string
    readonly source: string
    readonly file: string
    readonly run: string
  }[] = [
    {
      what: 'fugaro user revoke',
      source: USER_GRANT_SOURCE,
      file: 'internal/cli/user_grant.go',
      run: "one person, in the installation's own record of who may do what",
    },
    {
      what: 'fugaro user revoke',
      source: USER_GRANT_SOURCE,
      file: 'internal/cli/user_grant.go',
      run: "which is what its next fugaro update derives the installation's IAM lists from",
    },
    {
      what: 'fugaro user offboard',
      source: OFFBOARD_SOURCE,
      file: 'internal/cli/offboard.go',
      run: "The installation's intake deletes the secrets stored under the member's own prefix, found by the owner annotation it wrote (R10).",
    },
    {
      what: 'fugaro accounts rm',
      source: ACCOUNTS_RM_SOURCE,
      file: 'internal/cli/accounts_rm.go',
      run: "Removing the declaration also deletes the secret claude-oauth-token-<name> through Secret Manager's own delete.",
    },
    {
      what: 'fugaro accounts rm',
      source: ACCOUNTS_RM_SOURCE,
      file: 'internal/cli/accounts_rm.go',
      run: "That delete cannot be undone — Secret Manager keeps no grace period for a secret — and the copy of the credential left behind is the token at the provider.",
    },
  ]

  const CONSEQUENCES: readonly { readonly conjunction: string; readonly action: CopyCommandAction }[] = [
    {
      conjunction: 'fugaro user revoke',
      action: only(row(roles, '["user:admin@example.com","admin","project"]').actions),
    },
    {
      conjunction: 'fugaro user offboard',
      action: only(row(members, 'user:admin@example.com').actions),
    },
    { conjunction: 'fugaro accounts rm', action: only(row(accounts, 'team').actions) },
  ]

  it('states a sentence for each destructive command, and none for an adding one', () => {
    for (const { conjunction, action } of CONSEQUENCES) {
      expect(action.consequence, conjunction).toBeTruthy()
    }
    expect(only(roles.actions).consequence).toBeUndefined()
    expect(only(accounts.actions).consequence).toBeUndefined()
  })

  it('quotes each sentence out of the command’s own help text', () => {
    for (const { what, source, file, run } of CASES) {
      expect(collapsed(source), `${file} no longer says: ${run}`).toContain(collapsed(run))
      const carried = CONSEQUENCES.filter((each) => each.conjunction === what)
      expect(carried.length, `no copy for ${what}`).toBeGreaterThan(0)
      for (const { action } of carried) {
        const consequence = action.consequence
        if (consequence === undefined) throw new Error(`${what} carries no sentence at all`)
        expect(consequence, `${what} does not carry: ${run}`).toContain(run)
      }
    }
  })

  it('carries no sentence on a copy whose command states none', () => {
    const adding = [...copies(roles.actions), ...copies(accounts.actions)]
    expect(adding).toHaveLength(2)
    expect(adding.every((each) => each.consequence === undefined)).toBe(true)
  })
})

describe('the cells of every row', () => {
  it('fills exactly the columns the kind declared', () => {
    for (const kind of PEOPLE_KINDS) {
      const keys = kind.columns.map((each) => each.key).sort()
      for (const each of rowsOf(kind)) {
        expect(Object.keys(each.cells).sort()).toEqual(keys)
      }
    }
  })

  it('passes the class its own column declared', () => {
    for (const kind of PEOPLE_KINDS) {
      for (const each of rowsOf(kind)) {
        for (const column of kind.columns) {
          const drawn = sanitizeText(each.cells[column.key], column.class, `${kind.id}.${column.key}`)
          expect(drawn.ok, `${kind.id}.${column.key} refused ${each.cells[column.key]}`).toBe(true)
        }
      }
    }
  })

  it('skips a row a field is missing from, rather than drawing it empty', () => {
    expect(rowsOf(roles, { grants: [{ user: 'user:x@example.com', role: 'viewer' }] })).toEqual([])
    expect(rowsOf(members, { grants: [{ role: 'viewer', scope: 'project' }] })).toEqual([])
  })

  it('draws nothing from a document that is not one', () => {
    for (const kind of PEOPLE_KINDS) {
      expect(rowsOf(kind, {})).toEqual([])
      expect(rowsOf(kind, 'not a document')).toEqual([])
      expect(rowsOf(kind, [])).toEqual([])
      expect(kind.decode(undefined)).toEqual([])
      expect(kind.decode(null)).toEqual([])
    }
  })
})
