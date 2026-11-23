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
//   * **each destructive copy carries a sentence, and the command's own account
//     inside it** (spec section 8): the sentence is the kind's own, and every
//     clause of it that states what the CLI does is a phrase held here to the Go
//     file it was copied from -- the file is read as text and the phrase is
//     matched against it with whitespace collapsed, as the console's other
//     cross-language pins do (`schema.test.ts`, `fixture.test.ts`).
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
import {
  ACCOUNTS_RM_DELETE,
  OFFBOARD_SECRETS,
  PEOPLE_KINDS,
  REVOKE_RECORD,
  accounts,
  members,
  roles,
} from '../../src/renderer/manage/kinds/people'
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

/** goSource is one Go file read as text: the phrases the kind's three
 * consequences carry are copied out of those files, and each is read for the
 * reason `schema.test.ts` reads the Go structs -- a test that imported nothing
 * from the CLI cannot drift from it silently, because the pin fails when the Go
 * side's sentence changes. */
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
    // A member is a `person`: the column says who a row is, and the class that
    // draws an identity draws the whole `user:<address>` the roster spells
    // (D3213, 2026-11-21 -- it no longer refuses a value that is not a bare login
    // and no longer marks an address). The two lists that carry a member draw it
    // the same way, because they are the same fact about the same person.
    expect(members.columns).toEqual([
      { key: 'member', title: 'Member', class: 'person' },
      { key: 'roles', title: 'Roles', class: 'label' },
      { key: 'status', title: 'Status', class: 'label' },
    ])
    expect(roles.columns).toEqual([
      { key: 'member', title: 'Member', class: 'person' },
      { key: 'role', title: 'Role', class: 'label' },
      { key: 'scope', title: 'Scope', class: 'scope' },
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
          action.kind === 'copy-command' ? ['argv', 'consequence', 'kind'] : ['kind', 'view'],
        )
        for (const value of Object.values(action)) {
          expect(typeof value).not.toBe('function')
        }
      }
    }
  })

  it('exports the three kinds, their list and the phrases they quote, and no control of its own', async () => {
    // `stateWordText` and `instantText` are the two the pools kind draws a
    // member's state and its next reset with (the model-credentials-routing
    // plan's VS2): one table of `internal/creds`' words, spelled once, rather
    // than a second copy in `pools.ts` that would drift from this one.
    const module = await import('../../src/renderer/manage/kinds/people')
    expect(Object.keys(module).sort()).toEqual([
      'ACCOUNTS_RM_DELETE',
      'OFFBOARD_SECRETS',
      'PEOPLE_KINDS',
      'REVOKE_RECORD',
      'accounts',
      'instantText',
      'members',
      'roles',
      'stateWordText',
    ])
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

describe('the sentence a destructive copy states', () => {
  /** Each case is one destructive copy, the phrase of the command's own text that
   * its sentence carries, the clause the kind wrote around that phrase, and the Go
   * file the phrase was copied out of. The phrase is checked both ways -- the
   * sentence contains it, and (whitespace collapsed) the file still says it -- and
   * the lead is checked against the file too, the other way round: the kind's own
   * words must NOT be a run of the command's help, or the sentence would be the
   * help quoted whole rather than a sentence in the console's voice carrying the
   * command's account (`types.ts`'s `CopyCommandAction.consequence`). */
  const CASES: readonly {
    readonly what: string
    readonly source: string
    readonly file: string
    readonly lead: string
    readonly phrase: string
    readonly action: CopyCommandAction
  }[] = [
    {
      what: 'fugaro user revoke',
      source: USER_GRANT_SOURCE,
      file: 'internal/cli/user_grant.go',
      lead: 'Revoking this pair takes the role away from this member',
      phrase: REVOKE_RECORD,
      action: only(row(roles, '["user:admin@example.com","admin","project"]').actions),
    },
    {
      what: 'fugaro user offboard',
      source: OFFBOARD_SOURCE,
      file: 'internal/cli/offboard.go',
      lead: "Offboarding does more than drop this member's grants from the roster",
      phrase: OFFBOARD_SECRETS,
      action: only(row(members, 'user:admin@example.com').actions),
    },
    {
      what: 'fugaro accounts rm',
      source: ACCOUNTS_RM_SOURCE,
      file: 'internal/cli/accounts_rm.go',
      lead: 'Removing this account also deletes its stored secret',
      phrase: ACCOUNTS_RM_DELETE,
      action: only(row(accounts, 'team').actions),
    },
  ]

  it('states a sentence for each destructive command, and the empty one for an adding command', () => {
    for (const { what, action } of CASES) {
      expect(action.consequence, what).not.toBe('')
    }
    // The two adds destroy nothing, so their Copy draws no sentence at all --
    // which is the empty string and never an absent member (`types.ts`).
    expect(only(roles.actions).consequence).toBe('')
    expect(only(accounts.actions).consequence).toBe('')
  })

  it('holds the account of the command inside each sentence to the file it came from', () => {
    for (const { source, file, lead, phrase } of CASES) {
      expect(collapsed(source), `${file} no longer says: ${phrase}`).toContain(collapsed(phrase))
      expect(collapsed(source), `${file} now says the kind's own words: ${lead}`).not.toContain(
        collapsed(lead),
      )
    }
  })

  it('carries that account inside a sentence the kind wrote', () => {
    for (const { what, lead, phrase, action } of CASES) {
      expect(action.consequence, `${what} omits: ${phrase}`).toContain(phrase)
      expect(action.consequence, `${what} opens with something else`).toContain(lead)
    }
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

  it('draws a member as the address the node spells, and never as a shorter one', () => {
    // The member column carries every spelling an installation's roster holds --
    // a person's, a group's, a domain's, a service account's -- and each is drawn
    // whole through the class the column declares (D3213, 2026-11-21). The
    // address is what the cell is for: a console that cut it at the `@`, as the
    // Started-by column did until ID2, would hide the answer behind a name.
    const column = members.columns.find((each) => each.key === 'member')
    if (column === undefined) throw new Error('the members kind declares no Member column')
    const cells = rowsOf(members).map((each) => each.cells.member)
    expect(cells).toContain('user:admin@example.com')
    expect(cells).toContain('group:fugaro-operators@example.com')
    expect(cells).toContain('serviceAccount:fugaro-admin@example-project.iam.gserviceaccount.com')
    for (const cell of cells) {
      if (cell === undefined) throw new Error('a members row drew no Member cell')
      const drawn = sanitizeText(cell, column.class, 'members.member')
      if (!drawn.ok) throw new Error(`members.member refused ${cell}: ${drawn.reason}`)
      // The whole spelling, domain and all: the four examples above are drawn
      // with the address they hold, and the one that is a bare domain is drawn
      // as that domain.
      expect(drawn.text, cell).toBe(cell)
    }
  })

  it('draws a stored scope as the node spells it, and not as a redaction of it', () => {
    // The Scope column's class is validating, so the text drawn is the cell
    // itself, and the equality is asserted through the class the column
    // declares rather than through the class named here. `label`, which held
    // this column before MO-P's review, runs the free-text rules whose path
    // rule draws `github/example-owner/example-repo` as `github<path>` -- a
    // value the grants node does not hold -- and for the scopes whose Copy the
    // builder refuses (`*` and `,` are not characters `command.ts` copies) the
    // cell is the only place the value appears at all.
    const column = roles.columns.find((each) => each.key === 'scope')
    if (column === undefined) throw new Error('the roles kind declares no Scope column')
    const scopes = rowsOf(roles).map((each) => each.cells.scope)
    expect(scopes).toContain('github/example-owner/example-repo')
    for (const scope of scopes) {
      if (scope === undefined) throw new Error('a roles row drew no Scope cell')
      const drawn = sanitizeText(scope, column.class, 'roles.scope')
      if (!drawn.ok) throw new Error(`roles.scope refused ${scope}: ${drawn.reason}`)
      expect(drawn.text, scope).toBe(scope)
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
