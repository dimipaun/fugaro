// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Code group's repositories: what this project admits and what each one
// runs (the console-improvements plan's task MO-C, D2413; the descriptor is
// `../types.ts` and the prose is `docs/console-improvements/manage-kinds.md`).
//
// **Two kinds, one subject, two documents.** The spec groups the Code subject as
// `repos, images, recipes`, and this project answers the first of those with two
// reads rather than one: `repos.ls` is what each repository is made of (its
// branch, its image, its workflows, its accounts and both halves of its
// enrolment) and `repos.status` is a doctor for one repository's enrolment, one
// row per fact that decides whether a run of it can start (contract sections
// 6.26 and 6.27). `ManageKind.source` is one operation, so the subject is two
// kinds and the shell draws two tables -- which is what an operator wants here
// anyway: the listing is what they browse and the status is what they read when
// a run will not start.
//
// **The cells are the CLI's own words.** Every folded cell below ("none", "not
// admitted", "not in this machine's config", the four image sentences) is the
// sentence `internal/cli/project_repos_ls.go` prints for the same member, and
// the column is filled with it rather than with a value the console composed:
// an operator reading the console and an operator reading `fugaro project repos
// ls` are looking at the same repository, and two vocabularies for one fact
// would make them compare screens instead of states. `manage-code.test.ts` reads
// that file as text and holds each of these phrases to it, so a rewording in the
// CLI is a failing test here rather than a drift nobody sees.
//
// **The two Copy-commands, and why their sentences are computed here.** D2903
// keeps `repos add` and `repos rm` out of the console and gives the operator the
// exact command instead, and the console's own reach over this group proves it:
// both words are on `src/main/ipc.ts`'s `NEVER_REACHABLE` table (which
// `manage-code.test.ts` re-reads). What D2909 requires is that a destructive
// action states its consequences, and D2413 that the project tab draws what the
// CLI would print -- so the sentence beside the Copy is worked out from the very
// document the page is drawn from, exactly as the CLI works it out:
// `reposRemovalSentence` reads the same allow-list `removalWarning` reads, and
// the answer is conditional in the same way (`github/acme/*` admits
// `github/acme/app`, so removing the concrete entry refuses nothing). A
// consequence drawn from a fixed sentence would tell an operator their runs stop
// when they do not.

import { buildCopyCommand, commandValue, commandWord } from '../command'
import { boolAt, objectAt, objectsAt, stringsAt, textAt } from './read'
import { defineManageKind } from '../types'
import type { JsonObject } from './read'
import type { ManageAction, ManageColumn, ManageKind, ManageRow } from '../types'

/**
 * Every constant below is a phrase of the CLI's own text, copied verbatim, and
 * each is exported so `manage-code.test.ts` can hold it to the file it came from
 * **and** to the sentence it is drawn in. That pair is the whole claim the
 * sentence beside a Copy makes: a person reads four paragraphs and then runs a
 * command, and a clause this console wrote itself would be its belief about what
 * the command does rather than the command's own account of it.
 *
 * A phrase is chosen as one contiguous run of a Go string literal, which is why
 * the enrolment's writes and its publish are two constants and not one: the CLI
 * composes those two sentences from a `%s` for `--no-apply`, and a copy that
 * spanned the placeholder would stop being verbatim the moment either side was
 * reworded.
 */

/** reposRemovalTakes is the four stores a removal takes away, from the agent
 * refusal in `project_repos_rm.go` -- the sentence that names them as the things
 * "which are what admit a repository to the project and what run it". */
export const reposRemovalTakes =
  "removes the published allow-list entry, this machine's config entry, the repository's published admission and its Cloud Run jobs, which are what admit a repository to the project and what run it"

/**
 * reposLeaves is what `rm` deliberately leaves (D2406), in the order
 * `reposRemovalLeaves` (`project_repos_rm.go`) writes them. The list exists
 * because a person removing a repository otherwise believes the thing they care
 * about went with it: the App installation is theirs on the provider's side, and
 * removing it ends the App's access to every repository it serves.
 */
export const reposLeaves = [
  "the provider's App installation (a person's, on the provider's side: removing it ends the App's access to every repository it serves)",
  'the built images (Fugaro never deletes an image, and `fugaro image rm` does not either)',
  "the secrets and their versions (a container is protected against deletion, and no command of Fugaro's deletes one)",
  'the run history in the runs bucket (it is deleted at its own expiry, never by this command)',
  'every pull request a run opened',
  'the remote branch',
] as const

/** reposKeeps is the third part of `reposRemovalWarning`
 * (`reposRemovalOffboarding`): the two things the removal keeps by the owner's
 * ruling of 2026-11-17, and the reason neither is this command's to take away.
 * It is drawn because the sequence that does take them away is not a verb this
 * command has -- naming `--retire` here would send an operator to a command that
 * answers "Nothing to retire" over a repository whose `fugaro.yaml` still
 * declares every address. */
export const reposKeeps = [
  "the repository's Terraform state",
  "and the repository root's own IAM members",
  "Neither is this command's to take away: the state is what an apply of that root plans from, and the grants are members only such an apply deletes, so both go with the sequence docs/how-to/offboarding.md lays out",
] as const

/** reposEnrolmentWrites is the half of an enrolment that changes this machine and
 * the project, from `reposEnrolmentWhat` (`project_enroll_converge.go`): the
 * sentence the CLI's one confirmation is about, which D2413 asks the console to
 * draw beside the Copy rather than behind a question of its own. */
export const reposEnrolmentWrites =
  'write its repos: entry and its image mapping, add it to the published allow-list'

/** reposEnrolmentPublish is the rest of that sentence: the publish and the
 * deploy, which are the two steps a `--no-apply` run saves for `fugaro apply`
 * instead -- the console copies the command without the flag, so it draws the
 * tail that happens. */
export const reposEnrolmentPublish =
  "publish the node from this machine's config, and converge and deploy its own root"

/** reposEnrolmentBuild is what an enrolment does NOT do, from
 * `reposEnrolmentPlan`'s last line: the first image build is billable, and it is
 * the one thing a person reading "the job" would otherwise assume was included. */
export const reposEnrolmentBuild =
  'the first image build is not part of this: it is billable and it waits for `fugaro image build <entry>`'

/**
 * admittedBy is `internal/cli`'s own answer to "does an entry of this list still
 * admit this repository", read here so the removal sentence is the CLI's: the
 * repository's own canonical name wins, a per-segment pattern admits what it
 * matches, and the wildcard admits everything. It is deliberately small -- the
 * console never decides admission anywhere else, and a second matcher that
 * disagreed with `projectpolicy.AdmitRepo` would draw a state the next run does
 * not have.
 */
function admittedBy(entries: readonly string[], canonical: string): string {
  for (const entry of entries) {
    if (entry === canonical) return entry
  }
  for (const entry of entries) {
    if (entry === '*') return entry
  }
  const segments = canonical.split('/')
  for (const entry of entries) {
    const parts = entry.split('/')
    if (parts.length !== segments.length) continue
    if (parts.every((part, index) => part === '*' || part === segments[index])) return entry
  }
  return ''
}

/** reposRemovalConsequence is the sentence drawn beside `repos rm`: what the
 * removal stops (which depends on the rest of the allow-list, `removalWarning`'s
 * own rule), the four stores it takes away, the six things it leaves, and where
 * the two it keeps go. */
export function reposRemovalConsequence(allow: readonly string[], canonical: string): string {
  const still = admittedBy(allow.filter((entry) => entry !== canonical), canonical)
  const stops = still
    ? `removing ${canonical} from the allow-list, but ${still} still admits it: its runs are not refused, and a run in flight keeps the policy until it finishes`
    : `removing ${canonical} from the allow-list: its runs are refused from now on, and a run in flight keeps the policy until it finishes`
  return [
    `${stops}.`,
    `It ${reposRemovalTakes}.`,
    `It leaves, and nothing here touches: ${reposLeaves.join('; ')}.`,
    `It keeps ${reposKeeps[0]} ${reposKeeps[1]}.`,
    `${reposKeeps[2]}.`,
  ].join(' ')
}

/** reposEnrolmentConsequence is the sentence drawn beside `repos add`: the
 * writes the CLI's confirmation is about, and the one thing it leaves for a
 * later command. */
export function reposEnrolmentConsequence(canonical: string): string {
  return `enrolling ${canonical} in the project will ${reposEnrolmentWrites}, ${reposEnrolmentPublish}. And ${reposEnrolmentBuild}.`
}

/** reposImageCell is a row's image cell: what its job runs and which of the
 * seven answers chose it. It is the CLI's own rendering of the same member
 * (`reposLsImageCell`), including its fall-through: the three sources whose
 * image is not a catalog entry's name their own sentence, and the rest draw
 * `image <entry> (<source>)`. A document that stated no source draws no cell --
 * an empty `image_source` is a member this kind did not read, and `image  ()`
 * would be a value nobody wrote. */
function reposImageCell(source: string, entry: string): string {
  switch (source) {
    case '':
      return ''
    case 'repo':
      return 'image this project builds for the repository (repo)'
    case 'base':
      return 'image the installation ships as its base (base)'
    case 'shared':
      return 'image the shared dependency layer (shared)'
    case 'unresolved':
      return 'image unresolved (no answer chose one)'
    default:
      return `image ${entry} (${source})`
  }
}

/** reposListCell is a cell that holds a list: the values as the document spells
 * them, and the CLI's own word for none, so an empty list is not a column a
 * reader has to notice is empty. */
function reposListCell(values: readonly string[]): string {
  return values.length === 0 ? 'none' : values.join(', ')
}

/** reposPublishedCell is a row's published half: the entry that admits it, or
 * the CLI's word for an allow-list that does not. */
function reposPublishedCell(published: JsonObject | null): string {
  if (published === null || !boolAt(published, 'admitted')) return 'not admitted'
  return textAt(published, 'pattern') || 'not admitted'
}

/** reposLocalCell is a row's local half: the key this machine's config holds the
 * repository under, or the CLI's word for a machine that does not hold it. */
function reposLocalCell(local: JsonObject | null): string {
  if (local === null || !boolAt(local, 'present')) return "not in this machine's config"
  const key = textAt(local, 'key')
  return key === '' ? "not in this machine's config" : `repos: ${key}`
}

/** reposRowActions is one repository's two Copy-commands, and each is dropped
 * when `buildCopyCommand` refuses it: a canonical name this console will not put
 * in a shell draws a row without a Copy rather than a command it repaired. Both
 * verbs are built from the row's own canonical name, which is why they are the
 * row's actions and not the kind's (`../types.ts`, `ManageRow.actions`). */
function reposRowActions(canonical: string, allow: readonly string[]): readonly ManageAction[] {
  const verbs: readonly (readonly [string, string])[] = [
    ['add', reposEnrolmentConsequence(canonical)],
    ['rm', reposRemovalConsequence(allow, canonical)],
  ]
  const actions: ManageAction[] = []
  for (const [verb, consequence] of verbs) {
    const answer = buildCopyCommand(
      [
        commandWord('fugaro'),
        commandWord('project'),
        commandWord('repos'),
        commandWord(verb),
        commandValue(canonical),
      ],
      consequence,
    )
    if (answer.ok) actions.push(answer.action)
  }
  return actions
}

/** REPOS_COLUMNS is the listing's table, in the order an operator asks the
 * questions: what the repository is, what its job runs, and then both halves of
 * its enrolment. The two halves are two columns and never one word (D2409): a
 * repository the allow-list admits that this machine has never configured is a
 * different state from one configured here the list does not admit -- the first
 * runs, the second does not. */
export const REPOS_COLUMNS: readonly ManageColumn[] = Object.freeze([
  { key: 'repo', title: 'Repository', class: 'repoPattern' },
  { key: 'provider', title: 'Provider', class: 'label' },
  { key: 'branch', title: 'Base branch', class: 'branch' },
  { key: 'image', title: 'Image', class: 'label' },
  { key: 'workflows', title: 'Workflows', class: 'label' },
  { key: 'accounts', title: 'Claude accounts', class: 'label' },
  { key: 'published', title: 'Published', class: 'label' },
  { key: 'local', title: 'This machine', class: 'label' },
])

/** reposKind decodes `repos.ls` (contract section 6.27): the document's own
 * `repos` array, one row each, with the published allow-list the removal
 * sentence is computed from. `named` and `credentials` are members this kind
 * does not read -- the first is absent on this command's paths by construction,
 * and the second needs a Secret Manager call per repository that a listing has
 * no business making. */
export const reposKind: ManageKind = defineManageKind({
  id: 'repos',
  group: 'Code',
  title: 'Repositories',
  source: 'repos.ls',
  columns: REPOS_COLUMNS,
  actions: [],
  decode(doc: unknown): readonly ManageRow[] {
    if (doc === null || typeof doc !== 'object' || Array.isArray(doc)) return []
    const document = doc as JsonObject
    const allow = stringsAt(objectAt(document, 'allow') ?? {}, 'entries')
    const rows: ManageRow[] = []
    for (const repo of objectsAt(document, 'repos')) {
      const canonical = textAt(repo, 'canonical')
      if (canonical === '') continue
      rows.push({
        key: canonical,
        cells: {
          repo: canonical,
          provider: textAt(repo, 'provider'),
          branch: textAt(repo, 'base_branch'),
          image: reposImageCell(textAt(repo, 'image_source'), textAt(repo, 'image_entry')),
          workflows: reposListCell(
            objectsAt(repo, 'workflows').map((workflow) => textAt(workflow, 'name')),
          ),
          accounts: reposListCell(stringsAt(repo, 'claude_accounts')),
          published: reposPublishedCell(objectAt(repo, 'published')),
          local: reposLocalCell(objectAt(repo, 'local')),
        },
        actions: reposRowActions(canonical, allow),
      })
    }
    return rows
  },
})

/** STATUS_COLUMNS is the doctor's table. `enrolled` carries the CLI's own
 * sentence where the enrolment could not be decided (`state`), which is a fact
 * about the read: the console's word for that is not a verdict, and the CLI has
 * already written the reason. */
export const STATUS_COLUMNS: readonly ManageColumn[] = Object.freeze([
  { key: 'repo', title: 'Repository', class: 'repoPattern' },
  { key: 'enrolled', title: 'Enrolment', class: 'sentence' },
  { key: 'check', title: 'Check', class: 'label' },
  { key: 'result', title: 'Result', class: 'label' },
  { key: 'problem', title: 'Problem', class: 'sentence' },
  { key: 'fix', title: 'Fix', class: 'sentence' },
])

/** reposStatusKind decodes `repos.status` (contract section 6.26). One row per
 * (repository, check) pair, because that is what the document is: nine rows per
 * repository, each naming one fact and its one fix. A repository's `enrolled`
 * and `state` are per-repository facts and are drawn on each of its rows rather
 * than hoisted into a group heading -- the shell draws a flat table of what a
 * kind decoded, so a heading of the kind's own would be a shape it does not
 * have. A row this build does not evaluate (`skipped`) says so instead of
 * reading as a check that passed. */
export const reposStatusKind: ManageKind = defineManageKind({
  id: 'repos-status',
  group: 'Code',
  title: 'Repository status',
  source: 'repos.status',
  columns: STATUS_COLUMNS,
  actions: [],
  decode(doc: unknown): readonly ManageRow[] {
    if (doc === null || typeof doc !== 'object' || Array.isArray(doc)) return []
    const document = doc as JsonObject
    const rows: ManageRow[] = []
    for (const repo of objectsAt(document, 'repos')) {
      const canonical = textAt(repo, 'canonical')
      if (canonical === '') continue
      const state = textAt(repo, 'state')
      const enrolled = state !== '' ? state : boolAt(repo, 'enrolled') ? 'enrolled' : 'not enrolled'
      for (const check of objectsAt(repo, 'rows')) {
        const id = textAt(check, 'id')
        if (id === '') continue
        rows.push({
          key: `${canonical}/${id}`,
          cells: {
            repo: canonical,
            enrolled,
            check: id,
            result: boolAt(check, 'skipped') ? 'not checked' : boolAt(check, 'ok') ? 'ok' : 'failed',
            problem: textAt(check, 'problem'),
            fix: textAt(check, 'fix'),
          },
        })
      }
    }
    return rows
  },
})
