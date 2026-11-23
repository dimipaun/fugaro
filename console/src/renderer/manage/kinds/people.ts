// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// People & access: the installation's members, the roles they hold and the
// Claude accounts it may use (the console-improvements plan's task MO-P; spec
// section 5 as D2903 and D2905 amended it; the prose is
// `docs/console-improvements/manage-kinds.md`).
//
// **Three kinds from two reads.** `members.list` (`fugaro user ls --json`,
// version 2) answers all three questions at once -- this machine's own operator
// and admin lists, the grants node's stored pairs, and the group:, domain: and
// serviceAccount: members a grant cannot hold (F25) -- and this file draws two
// of them from it: the people (`members`) and the stored pairs (`roles`), which
// are the same answer read as a list of one fact per person and as a list of one
// fact per pair. `accounts.inventory` (`fugaro accounts ls --json`) is the third.
//
// **No write control, and every write is a line to copy.** D2903 keeps a write
// that is destructive out of the console: adding a pair, revoking one,
// offboarding a member, adding an account and removing one are each a command the
// operator runs at their own terminal, and the console's whole part is the exact
// line to paste. `fugaro user grant` and `fugaro accounts add` are the kind-level
// commands, because their values are one person's, one role's, one scope's and
// one account's, which only the operator knows; `fugaro user revoke`,
// `fugaro accounts rm` and `fugaro user offboard` are a ROW's commands, because
// the read answered every value they carry (types.ts's `ManageRow.actions`).
//
// The kind-level two carry upper-case placeholders (`MEMBER`, `ROLE`, `SCOPE`,
// `NAME`) on purpose. They are values the CLI refuses -- a member has to be a
// `user:<address>`, the role is one of three words, and a Claude account name is
// `[a-z]` and lower-case throughout -- so a paste that was never edited fails
// with the command's own refusal and writes nothing, which is the direction an
// incomplete line has to fail in. The row-level copies carry the read's own
// values, so an edited line is not a repair they need.
//
// **No secret is drawn (D2905).** `fugaro accounts ls --json` also carries the
// credentials block, the alias map and the secret IDs' absence, and none of it
// becomes a cell here: a credential is not an account (the CLI says so itself),
// the credentials block is the keys kind's read, and this file draws names, roles,
// scopes and states. Nothing below reads a key the CLI could put a value in.
//
// **A destructive copy carries a sentence in the console's own voice, and holds
// the command's own account of what it does** (spec section 8). The sentence is
// this file's (`types.ts`'s `CopyCommandAction`, whose `consequence` is the
// console's rather than a word of the command), and the clauses inside it that
// state what the CLI does are the `export`ed phrases below, copied verbatim out
// of its help -- `REVOKE_RECORD`, `OFFBOARD_SECRETS` and `ACCOUNTS_RM_DELETE`,
// from `internal/cli/user_grant.go`, `offboard.go` and `accounts_rm.go` -- which
// `manage-people.test.ts` holds back to those files. So the console cannot come
// to say about a command something the command does not say, and a rewording of
// either side fails the test. The two adding copies (a grant, `accounts add`)
// have nothing to state and carry `buildCopyCommand`'s empty sentence.

import { buildCopyCommand, commandValue, commandWord } from '../command'
import { defineManageKind } from '../types'
import type { CommandPart } from '../command'
import type { ManageAction, ManageColumn, ManageRow } from '../types'

/** REVOKE_RECORD is the half of a revoke's account that is the command's own: the
 * help in `internal/cli/user_grant.go` names where a stored pair lives -- the
 * grants node at config/admin/access -- and what reads it, and this clause is
 * that sentence's fixed text, copied rather than paraphrased so the claim about
 * where the IAM lists come from is the command's. */
export const REVOKE_RECORD =
  "the grants node at config/admin/access, which is what its next fugaro update derives the installation's IAM lists from"

/** OFFBOARD_SECRETS is `internal/cli/offboard.go`'s own sentence about the half
 * of an offboarding a person cannot see from the command's line: the intake
 * deletes the secrets the member stored under their own prefix. */
export const OFFBOARD_SECRETS =
  "The installation's intake deletes the secrets stored under the member's own prefix, found by the owner annotation it wrote (R10)."

/** ACCOUNTS_RM_DELETE is `internal/cli/accounts_rm.go`'s own sentence about the
 * delete behind an account's removal: it cannot be undone, and what is left
 * behind is the token at the provider. */
export const ACCOUNTS_RM_DELETE =
  "That delete cannot be undone — Secret Manager keeps no grace period for a secret — and the copy of the credential left behind is the token at the provider."

/** REVOKE_CONSEQUENCE is the sentence a revoke's Copy draws: this file's words
 * about the pair leaving, carrying the command's own account of where it lived
 * and what reads that record next. */
const REVOKE_CONSEQUENCE = `Revoking this pair takes the role away from this member in the installation's own record of who may do what: ${REVOKE_RECORD}.`

/** OFFBOARD_CONSEQUENCE is the sentence an offboarding's Copy draws: the roster
 * half in this file's words, and the deletion of the member's stored secrets in
 * the command's, which is the half the command's own line does not show. */
const OFFBOARD_CONSEQUENCE = `Offboarding does more than drop this member's grants from the roster: ${OFFBOARD_SECRETS}`

/** ACCOUNTS_RM_CONSEQUENCE is the sentence an account removal's Copy draws: this
 * file's words about the secret going with the declaration, and the command's own
 * words about the delete behind it and the token it leaves at the provider. */
const ACCOUNTS_RM_CONSEQUENCE = `Removing this account also deletes its stored secret through Secret Manager's own delete. ${ACCOUNTS_RM_DELETE}`

/** The `accounts add` argv's placeholder, and the reason it is upper case: a
 * Claude account name the CLI accepts is lower-case throughout, so this word is
 * refused by `accounts add` itself and a paste nobody edited writes nothing. */
const NAME_PLACEHOLDER = 'NAME'

/** The `user grant` argv's placeholders, upper case for the reason above: a member
 * the CLI accepts is a `user:<address>`, a role is one of viewer, operator and
 * admin, and a scope is a scope word or a repository pattern. */
const MEMBER_PLACEHOLDER = 'MEMBER'
const ROLE_PLACEHOLDER = 'ROLE'
const SCOPE_PLACEHOLDER = 'SCOPE'

/**
 * MEMBER_COLUMN, ROLE_COLUMN and SCOPE_COLUMN are the three classes this file
 * could get wrong, and the class each of them takes is a fact about the column
 * rather than a preference:
 *
 * - A **member** is `person`. The grants node, this machine's own lists and this
 *   document all spell a member `user:<address>`, `group:<address>`,
 *   `domain:<domain>` or `serviceAccount:<address>`, and the column says who a
 *   row is -- so the class that draws an identity is the one that belongs here,
 *   and it draws any of those spellings as the read wrote it (D3213,
 *   2026-11-21: the console masks secrets only, so the class no longer refuses a
 *   value that is not a bare login and no longer marks an address). `label`,
 *   which held this column while that class refused those spellings, is left to
 *   the columns that are not a person.
 * - A **role** is `label`: one word out of a closed set, which is what a label is.
 * - A **scope** is `scope`, and no other class of the set holds it: a stored
 *   scope is one string that may be the word `project`, the word `all-repos`, or
 *   a comma-separated list of canonical repository patterns. `repoPattern`
 *   refuses `project`, and `label`, which drew this column before, runs the
 *   free-text rules whose path rule draws `github/acme/app` as `github<path>` --
 *   a redaction the grants node does not carry, and for the scopes the
 *   Copy-command cannot carry (`*` and `,` are not characters the builder copies)
 *   the only place the value appears at all. So `sanitize.ts` gained the class,
 *   and it holds `ParseScope`'s grammar.
 */
const MEMBER_COLUMN: ManageColumn = { key: 'member', title: 'Member', class: 'person' }
const ROLES_COLUMN: ManageColumn = { key: 'roles', title: 'Roles', class: 'label' }
const STATUS_COLUMN: ManageColumn = { key: 'status', title: 'Status', class: 'label' }
const ROLE_COLUMN: ManageColumn = { key: 'role', title: 'Role', class: 'label' }
const SCOPE_COLUMN: ManageColumn = { key: 'scope', title: 'Scope', class: 'scope' }
const ACCOUNT_COLUMN: ManageColumn = { key: 'account', title: 'Account', class: 'person' }
const STATE_COLUMN: ManageColumn = { key: 'state', title: 'State', class: 'label' }

/** The words `members` draws in its Status column. They are this machine's own
 * state of a member, and each one is a sentence the CLI prints somewhere:
 * `in the roster` is a grant of the node, `list only` is `fugaro user`'s "a
 * member written here alone is dropped by that machine's next `fugaro update`",
 * and `legacy` is F25's word for a group:, domain: or serviceAccount: member. */
const STATUS_IN_ROSTER = 'in the roster'
const STATUS_LIST_ONLY = 'list only'
const STATUS_LEGACY = 'legacy'

/** The state of an account whose mark could not be read at all: `accounts ls`
 * omits `states` rather than answer "available" about a node it never read (its
 * own rule: that is a claim it may make only about marks it actually read). */
const STATE_NOT_READ = 'no state read'

type JsonObject = Record<string, unknown>

function asObject(value: unknown): JsonObject | undefined {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
    ? (value as JsonObject)
    : undefined
}

function asArray(value: unknown): readonly unknown[] {
  return Array.isArray(value) ? value : []
}

function asString(value: unknown): string | undefined {
  return typeof value === 'string' && value !== '' ? value : undefined
}

function asStrings(value: unknown): readonly string[] {
  return asArray(value).filter((each): each is string => typeof each === 'string' && each !== '')
}

/** copyAction is a built copy-command, or nothing at all when the builder refused
 * a value. The refusal is the builder's ordinary outcome and this file draws no
 * Copy for it -- the row is drawn without one, as `command.ts` says it should be,
 * and never with a repaired line.
 *
 * The sentence is the builder's second argument and defaults to the empty one:
 * the adding copies have nothing to state, and a copy that states nothing draws
 * nothing (`command.ts`'s rule for a command whose consequence is empty). */
function copyAction(parts: readonly CommandPart[], consequence = ''): ManageAction[] {
  const built = buildCopyCommand(parts, consequence)
  return built.ok ? [built.action] : []
}

/** grantCopy is the kind-level command a pair is added with, and it is the row
 * action's shape with the three values the read would have carried replaced by the
 * placeholders above. */
function grantCopy(): readonly ManageAction[] {
  return copyAction([
    commandWord('fugaro'),
    commandWord('user'),
    commandWord('grant'),
    commandValue(MEMBER_PLACEHOLDER),
    commandWord('--role'),
    commandValue(ROLE_PLACEHOLDER),
    commandWord('--scope'),
    commandValue(SCOPE_PLACEHOLDER),
  ])
}

/** revokeCopy is one stored pair's own command: the member, the role and the scope
 * the read answered, and nothing the operator has to fill in. */
function revokeCopy(member: string, role: string, scope: string): readonly ManageAction[] {
  return copyAction(
    [
      commandWord('fugaro'),
      commandWord('user'),
      commandWord('revoke'),
      commandValue(member),
      commandWord('--role'),
      commandValue(role),
      commandWord('--scope'),
      commandValue(scope),
    ],
    REVOKE_CONSEQUENCE,
  )
}

/** offboardCopy is one member's own command, and it is built only for a member the
 * roster spells `user:<address>`: `fugaro user offboard` names a person by the
 * address they know, refuses the `user:` spelling, and has no address to name for a
 * group:, domain: or serviceAccount: member -- `access.Member` would fold a bare
 * group address into `user:<group address>`, which is a member nobody holds. A row
 * that is not a person therefore carries no action at all. */
function offboardCopy(member: string): readonly ManageAction[] {
  const prefix = 'user:'
  if (!member.startsWith(prefix)) {
    return []
  }
  return copyAction(
    [
      commandWord('fugaro'),
      commandWord('user'),
      commandWord('offboard'),
      commandValue(member.slice(prefix.length)),
    ],
    OFFBOARD_CONSEQUENCE,
  )
}

/** addAccountCopy is the kind-level command an account is added with. The token is
 * not this command's to carry: `accounts add` reads it from the terminal, which is
 * exactly why the console can offer the line and not the write (D2905). */
function addAccountCopy(): readonly ManageAction[] {
  return copyAction([
    commandWord('fugaro'),
    commandWord('accounts'),
    commandWord('add'),
    commandValue(NAME_PLACEHOLDER),
  ])
}

/** removeAccountCopy is one account's own command: the name the read answered. */
function removeAccountCopy(name: string): readonly ManageAction[] {
  return copyAction(
    [commandWord('fugaro'), commandWord('accounts'), commandWord('rm'), commandValue(name)],
    ACCOUNTS_RM_CONSEQUENCE,
  )
}

interface MemberFacts {
  readonly roles: string[]
  inRoster: boolean
  legacy: boolean
  onAList: boolean
}

function rememberMember(
  facts: Map<string, MemberFacts>,
  member: string,
  role: string,
  place: (facts: MemberFacts) => void,
): void {
  let entry = facts.get(member)
  if (entry === undefined) {
    entry = { roles: [], inRoster: false, legacy: false, onAList: false }
    facts.set(member, entry)
  }
  if (!entry.roles.includes(role)) {
    entry.roles.push(role)
  }
  place(entry)
}

/**
 * decodeMembers draws one row per member any of `user ls --json`'s three member
 * lists names: this machine's own run_operators and admins, the grants node's
 * stored pairs, and F25's legacy members.
 *
 * The `effective` list is deliberately not drawn. It answers the role each person
 * holds in ONE repository, and a kind that is not about a repository has no column
 * to put that in: a person who holds `operator` over `github/acme/api` and `viewer`
 * over everything else is one row with two roles here, and which of them applies is
 * `roles`' own question about that pair's scope.
 */
function decodeMembers(doc: unknown): readonly ManageRow[] {
  const document = asObject(doc)
  if (document === undefined) {
    return []
  }
  const facts = new Map<string, MemberFacts>()
  for (const member of asStrings(document.run_operators)) {
    rememberMember(facts, member, 'operator', (entry) => {
      entry.onAList = true
    })
  }
  for (const member of asStrings(document.admins)) {
    rememberMember(facts, member, 'admin', (entry) => {
      entry.onAList = true
    })
  }
  for (const grant of asArray(document.grants)) {
    const pair = asObject(grant)
    const member = pair === undefined ? undefined : asString(pair.user)
    const role = pair === undefined ? undefined : asString(pair.role)
    if (member !== undefined && role !== undefined) {
      rememberMember(facts, member, role, (entry) => {
        entry.inRoster = true
      })
    }
  }
  for (const legacy of asArray(document.legacy)) {
    const entry = asObject(legacy)
    const member = entry === undefined ? undefined : asString(entry.member)
    const role = entry === undefined ? undefined : asString(entry.role)
    if (member !== undefined && role !== undefined) {
      rememberMember(facts, member, role, (each) => {
        each.legacy = true
      })
    }
  }
  const rows: ManageRow[] = []
  for (const [member, entry] of facts) {
    const actions = offboardCopy(member)
    rows.push({
      key: member,
      cells: {
        member,
        roles: entry.roles.join(', '),
        status: memberStatus(entry),
      },
      ...(actions.length === 0 ? {} : { actions }),
    })
  }
  return rows
}

/** memberStatus is one word per member, and the order it is decided in is the
 * order of how much the member keeps: a stored grant places them on the
 * installation until it is revoked, a legacy entry keeps the IAM grant the
 * installation derives (F25), and a member only on this machine's own lists is
 * dropped by that machine's next `fugaro update`. */
function memberStatus(entry: MemberFacts): string {
  if (entry.inRoster) {
    return STATUS_IN_ROSTER
  }
  if (entry.legacy) {
    return STATUS_LEGACY
  }
  return STATUS_LIST_ONLY
}

/**
 * decodeRoles draws one row per stored pair of the grants node -- the roster as
 * pairs rather than as people, which is the shape `fugaro user revoke` takes.
 *
 * The Status column is `known`: the repositories the pair's scope matches among
 * those THIS MACHINE knows (F27). It is a status and not a count of repositories
 * because that is all the document can answer: the CLI has no directory to ask and
 * a pattern may admit repositories nothing here enumerates, so `no known
 * repository` is what an installation says about a pattern whose matches it cannot
 * see, and never "this pair reaches nothing".
 */
function decodeRoles(doc: unknown): readonly ManageRow[] {
  const document = asObject(doc)
  if (document === undefined) {
    return []
  }
  const rows: ManageRow[] = []
  for (const grant of asArray(document.grants)) {
    const pair = asObject(grant)
    const member = pair === undefined ? undefined : asString(pair.user)
    const role = pair === undefined ? undefined : asString(pair.role)
    const scope = pair === undefined ? undefined : asString(pair.scope)
    if (member === undefined || role === undefined || scope === undefined) {
      continue
    }
    const known = pair === undefined ? [] : asStrings(pair.known)
    const actions = revokeCopy(member, role, scope)
    rows.push({
      key: JSON.stringify([member, role, scope]),
      cells: {
        member,
        role,
        scope,
        status: knownStatus(known.length),
      },
      ...(actions.length === 0 ? {} : { actions }),
    })
  }
  return rows
}

function knownStatus(known: number): string {
  if (known === 0) {
    return 'no known repository'
  }
  return known === 1 ? '1 known repository' : `${known} known repositories`
}

/**
 * decodeAccounts draws one row per name `fugaro accounts ls --json` lists, with the
 * state its own mark records.
 *
 * The credentials block, the alias map and the `complete`/`note` caveats are not
 * drawn. A credential is not an account -- the CLI drops a key credential's name
 * from this list for exactly that reason, and a console that drew the block beside
 * the accounts would be reading one as the other -- and the two caveats are
 * sentences about the READ rather than about a row, which a kind's columns have no
 * place for; the CLI's own table is silent about them too, and its `--json` reader
 * is where they are answered.
 */
function decodeAccounts(doc: unknown): readonly ManageRow[] {
  const document = asObject(doc)
  if (document === undefined) {
    return []
  }
  const states = asObject(document.states)
  const rows: ManageRow[] = []
  for (const name of asStrings(document.accounts)) {
    const state = states === undefined ? undefined : asObject(states[name])
    const actions = removeAccountCopy(name)
    rows.push({
      key: name,
      cells: {
        account: name,
        state: state === undefined ? STATE_NOT_READ : accountStateText(state),
      },
      ...(actions.length === 0 ? {} : { actions }),
    })
  }
  return rows
}

/**
 * accountStateText mirrors `internal/cli/accounts.go`'s `stateText`: the four words
 * `internal/creds` stores, an exhaustion with the instant its hold lapses, and any
 * word a newer release wrote left standing rather than guessed at.
 *
 * The console does not import the CLI's formatter and cannot: the four words are a
 * Go constant and this is a renderer, so the words are spelled once -- in
 * `stateWordText`, which the pools kind draws from too -- and
 * `manage-people.test.ts` holds them to the same table. What is this file's and
 * not the pools kind's is the `until` clause: `accounts ls --json` carries the
 * instant beside the word, and this column is the one with room for the sentence.
 */
function accountStateText(state: JsonObject): string {
  const word = asString(state.state)
  if (word === undefined) {
    return STATE_NOT_READ
  }
  const text = stateWordText(word)
  if (word !== 'exhausted') {
    return text
  }
  const until = asString(state.until)
  return until === undefined ? text : `${text} until ${instantText(until)}`
}

/**
 * stateWordText is one of `internal/creds`' state words as this console draws it:
 * `available` and `exhausted` as the service spells them, `backed_off` as `backed
 * off` -- the CLI's own `stateText` is where the space comes from -- and any word a
 * newer release wrote left standing rather than guessed at.
 *
 * It is exported because two kinds draw the same words about the same marks: a pool
 * member's `state` is `internal/creds`' own, "which `fugaro accounts ls --json`
 * already prints" (C2, `routing.PoolMember.State`), and the pools kind
 * (`pools.ts`) draws it in a column of its own. A second copy of this table would
 * be a second place `backed off` is spelled, and the two would part the day one of
 * them learned a word.
 */
export function stateWordText(word: string): string {
  switch (word) {
    case 'available':
      return 'available'
    case 'exhausted':
      return 'exhausted'
    case 'backed_off':
      return 'backed off'
    case 'broken':
      return 'broken'
    default:
      return oneLine(word)
  }
}

const RFC3339 = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/

/**
 * instantText is one instant as the CLI prints it -- `2006-01-02 15:04Z`, in UTC --
 * and a string that is not an RFC 3339 instant left as it stands rather than
 * rendered as `Invalid Date`. It is spelled rather than localised because the
 * console draws what the CLI prints, and it is exported for the pools kind's
 * `next_reset`, which is the same kind of instant about the same kind of mark.
 */
export function instantText(instant: string): string {
  if (!RFC3339.test(instant)) {
    return oneLine(instant)
  }
  const at = new Date(instant)
  if (Number.isNaN(at.getTime())) {
    return oneLine(instant)
  }
  return (
    `${at.getUTCFullYear()}-${pad(at.getUTCMonth() + 1)}-${pad(at.getUTCDate())}` +
    ` ${pad(at.getUTCHours())}:${pad(at.getUTCMinutes())}Z`
  )
}

function pad(value: number): string {
  return String(value).padStart(2, '0')
}

/** oneLine is `internal/cli`'s own `cellOneLine` in the one place this file needs
 * it: a state word is drawn in a table cell, and a word a document put line
 * structure in must not become two cells. */
function oneLine(text: string): string {
  return text.replace(/[\r\n\t]+/g, ' ')
}

/** members is the people: who the installation's lists and its roster name, the
 * roles each of them holds and whether they are still placed. */
export const members = defineManageKind({
  id: 'members',
  group: 'People & access',
  title: 'Members',
  source: 'members.list',
  columns: [MEMBER_COLUMN, ROLES_COLUMN, STATUS_COLUMN],
  decode: decodeMembers,
  actions: [],
})

/** roles is the grants node read as pairs: one row per stored role and scope pair,
 * which is the unit `fugaro user revoke` takes away. */
export const roles = defineManageKind({
  id: 'roles',
  group: 'People & access',
  title: 'Roles',
  source: 'members.list',
  columns: [MEMBER_COLUMN, ROLE_COLUMN, SCOPE_COLUMN, STATUS_COLUMN],
  decode: decodeRoles,
  actions: grantCopy(),
})

/** accounts is the named Claude accounts the repository may choose among, with the
 * state each one's mark records. */
export const accounts = defineManageKind({
  id: 'accounts',
  group: 'People & access',
  title: 'Claude accounts',
  source: 'accounts.inventory',
  columns: [ACCOUNT_COLUMN, STATE_COLUMN],
  decode: decodeAccounts,
  actions: addAccountCopy(),
})

export const PEOPLE_KINDS = [members, roles, accounts] as const
