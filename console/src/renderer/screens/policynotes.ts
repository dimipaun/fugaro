// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The policy notes: the Project tab's policy section, drawn from two reads
// (managed-project screens plan CM7; reconciliation §5, D1481, D1482).
//
// The ask was *"the policy notes (what the last update changed, runs in flight
// at the time)"*, and the owner's ruling of 2026-11-07 answered what that is:
// **the published node as it is, plus doctor's six policy checks** (D1482). The
// change itself is printed by `fugaro update` and recorded nowhere (F19), and the
// in-flight count is the retirements section's, not this one's (D1483), so this
// section draws neither: it draws the node, the checks that say what is wrong
// with it, and one sentence saying where the change is printed instead.
//
// **Two reads, one section.** `policy.show` is `fugaro project policy show
// --json`, the smallest document the screen needs -- `{project, policy}`, and
// deliberately not `project show --json`, whose `caps` are the Spend screen's
// (D1481, reconciliation §6 rule 3: **these screens show no dollars and no
// caps**). `environment.report` is `fugaro doctor --json`, whose `checks` array
// already carries the six `policy-*` checks structured and worded by the CLI
// (F10). Rule 6's table carries both rows (CM3, CM4), so both are reads some
// other part of the console performs and this module only draws.
//
// **What the node's members are, and what they are not.** The five the plan
// names -- the accounts (name and the repositories each token may be used by),
// `allow_repos`, `base_branch`, the routing switch and the image catalog -- are
// drawn. The node carries more, and this file draws **none** of it: the `budget`
// block's ceilings are the `caps` panel's subject and are drawn by no screen of
// this work (rule 3, D1481 -- the read carries the block, and the section that
// reads it drops it), and `default_claude_account` and `claude_account_stages`
// are the review account keys, which come from `settings --json` and belong to
// the settings panel (D1478, CM6). A member the decoder does not know is ignored
// rather than drawn (reconciliation §5), which is what keeps a newer node's
// additions off this screen rather than into it.
//
// **Each block says which read its lines came from.** Both reads draw a block of
// their own, each with the operation as its `data-source` hook, the console's own
// heading, the read a person can run, and its own state. That is the owner's rule
// of 2026-10-31 for the tab: **every section is independent**, with its own read,
// its own busy state and its own refusal naming its own command -- so a refused
// `doctor` never takes the node's lines down, and a refused node never hides the
// checks.
//
// **This module owns no theme and no frame.** It draws a body, and the tab's own
// frame (`renderProjectsFrame`, CM10) carries `data-theme` on the root the
// section is hosted in: a screen that stamped a theme of its own would be a
// second place the theme is decided, and the theme vocabulary is the styling
// design's, not this file's.
//
// **Why the refusal sentences here are the console's own.** A refusal names the
// command that refused, which is this section's own job (`POLICY_NODE_ABSENT`,
// `POLICY_NODE_SHAPE`). The CLI's own sentence for a read that refused is drawn
// where that read is named -- the panel's `because`, through `projects.ts`'s own
// refusal door -- so this body never speaks for the CLI and never puts a
// subprocess's words in a cell of its own.

import { measured, unavailable } from '../../shared/availability'
import type { Reading } from '../../shared/availability'
import type { TextClass } from '../../shared/sanitize'
import { putReading, putText } from '../app'
import type { ElementLike } from '../app'

// ---------------------------------------------------------------------------
// The two reads, the six checks, and the sentences a reader is owed.
// ---------------------------------------------------------------------------

/** POLICY_NODE_SOURCE is the read the node's block names, and the command its
 * refusal names. `fugaro project policy show --json` is rule 6's own row (CM3):
 * `{project, policy}`, and nothing else (F18). The console's own child carries
 * more -- the two project flags §3.4 rule 3 gives every cloud command -- and the
 * line drawn here is the read a person can run, not that argv. */
export const POLICY_NODE_SOURCE = 'fugaro project policy show --json'

/** POLICY_CHECKS_SOURCE is the read the checks' block names. The console's own
 * child carries `--dir` (`spawn.ts`'s `dirFlag`, and §5.1's `doctor --json` row
 * says why: a GUI app's working directory is not a checkout) and the same two
 * project flags; the line drawn here is the read a person can run, not the argv
 * the console spawns. */
export const POLICY_CHECKS_SOURCE = 'fugaro doctor --json'

/** POLICY_NODE_TITLE is the node block's heading, the console's own words. */
export const POLICY_NODE_TITLE = 'the published policy node'

/** POLICY_CHECKS_TITLE is the checks block's heading. It names the six
 * `policy-*` checks rather than "what doctor says about the published policy":
 * `doctor` warns about that same node from two checks outside the family
 * (`allow-list-empty`, `allow-list-uncovered`), which this section counts and
 * does not draw, and a heading would be claiming coverage the block has not got
 * (see `POLICY_CHECK_IDS`). */
export const POLICY_CHECKS_TITLE = 'the six `policy-*` checks doctor reports'

/**
 * POLICY_CHECK_IDS is the closed list of the policy checks this section draws,
 * in the order it draws them: the six the owner's ruling names (D1482), which
 * are exactly the `policy-*` checks `doctor` emits.
 *
 * It is a **closed list and not a prefix filter**, and that is the point of the
 * whole block: `doctor --json` returns around twenty checks, a prefix rule would
 * silently grow with the CLI, and the six are a ruling rather than a spelling
 * accident. A check this list names and the document does not carry is drawn as
 * *not in this document* (`POLICY_CHECK_UNDRAWN_WORD`) rather than omitted --
 * absence is never a pass, and a screen that drew five rows for six checks would
 * be hiding the sixth's absence.
 *
 * **The family is the ruling's, and `doctor` warns about this same node from
 * outside it.** `allow-list-empty` and `allow-list-uncovered`
 * (`internal/cli/doctor.go`) are warnings about the *published node's
 * allow-list* -- it admits no repository, or it does not admit one this machine
 * names -- and they are not `policy-*` checks, so they are counted by the
 * section's own figure (`POLICY_OTHERS_LEAD`) and not drawn. A later task that
 * draws them changes D1482's list; widening this one to a prefix or to a
 * severity would be that change made without the ruling.
 */
export const POLICY_CHECK_IDS: readonly string[] = Object.freeze([
  'policy-published',
  'policy-newer-than-binary',
  'policy-older-than-binary',
  'policy-unknown-keys',
  'policy-vs-jobs',
  'policy-rules-skew',
])

/** POLICY_NO_DIFF_NOTE is the sentence that stands where a diff would be. The
 * change the last publish made is printed by `fugaro update` and recorded
 * nowhere (F19), so the section says where a reader gets it and claims nothing
 * (D1482). */
export const POLICY_NO_DIFF_NOTE =
  'this section draws no diff: what the last publish changed is printed by `fugaro update` and recorded nowhere'

/** POLICY_NO_POLICY is the node read's answer when no policy is published: the
 * CLI prints `"policy": null`, and "this project has published none" is a
 * different fact from "the console could not ask". */
export const POLICY_NO_POLICY =
  'no policy is published for this project: the read answered with a null node, and a run of it uses the installation’s own ceilings'

/** POLICY_NODE_ABSENT and POLICY_CHECKS_ABSENT are the two reads' own refusals
 * when no document arrived at all. Each names its own command, which is what
 * lets a person run the read the section could not. */
export const POLICY_NODE_ABSENT = `no document arrived for \`${POLICY_NODE_SOURCE}\``
export const POLICY_CHECKS_ABSENT = `no document arrived for \`${POLICY_CHECKS_SOURCE}\``

/** POLICY_NODE_SHAPE and POLICY_CHECKS_SHAPE are the same refusals for a
 * document that arrived and is not the one the section reads: a decoder that
 * guessed would be a decoder that draws a member out of a document that does not
 * mean it (reconciliation §5). */
export const POLICY_NODE_SHAPE = `the document \`${POLICY_NODE_SOURCE}\` answered with is not the {project, policy} object this section reads`
export const POLICY_CHECKS_SHAPE = `the document \`${POLICY_CHECKS_SOURCE}\` answered with carries no checks array`

/** POLICY_NO_BASE_BRANCH is why the base branch cell reads `n/a`: `base_branch`
 * is `omitempty`, and the node's own reader documents the empty value as "the
 * project sets none, so a repository runs against `main`" -- which is a fact
 * about the project, not a missing figure, so the sentence is drawn with the
 * mark. */
export const POLICY_NO_BASE_BRANCH =
  'the node names no base branch, so a repository of this project runs against `main`'

/** POLICY_DIRECT_WORD is what the routing cell says when the node names no
 * switch: the CLI's own reader spells the absent and the empty value `direct`
 * (D1b), so the screen draws the value the run will use rather than an empty
 * cell. The parenthetical is there because the word is the console's reading of
 * an absent member, and a person comparing the node by hand is owed that. */
export const POLICY_DIRECT_WORD = 'direct (the node names no switch)'

/** POLICY_NONE_WORD and POLICY_NO_REPOSITORY_WORD are the console's own words
 * for an empty list, and they are not a `0`: the node was read, so the count
 * beside them is a measured zero, and a list drawn empty with no word would read
 * as a list that did not load. */
export const POLICY_NONE_WORD = 'none'
export const POLICY_NO_REPOSITORY_WORD = 'no repository'

/** POLICY_DEFAULT_WORD marks the catalog entry a job that names nothing runs
 * (spec.md §Repo configuration item 1). It is drawn only where the entry says
 * so, so a catalog with no default says nothing. */
export const POLICY_DEFAULT_WORD = 'the default'

/** POLICY_CHECK_OK_WORD, POLICY_CHECK_FAILING_WORD, POLICY_CHECK_NOTED_WORD and
 * POLICY_CHECK_UNDRAWN_WORD are a check row's four states, in the console's own
 * words. The last is the one a list of the checks the document carried could not
 * have: a check this section draws and the document does not carry is neither a
 * pass nor a failure, and it says so. */
export const POLICY_CHECK_OK_WORD = 'ok'
export const POLICY_CHECK_FAILING_WORD = 'failing'
export const POLICY_CHECK_NOTED_WORD = 'noted'
export const POLICY_CHECK_UNDRAWN_WORD = 'not in this document'

/** POLICY_CHECK_ABSENT_BECAUSE is why a check the document does not carry draws
 * no severity, no problem and no fix: the three values are absent, which is what
 * the `n/a` mark and this sentence say. It is the sentence the cells would draw
 * for any absent member, and it is a fact about the *document*, never about the
 * check. */
export const POLICY_CHECK_ABSENT_BECAUSE =
  'the document this section read carries no such check: a check the document does not carry is not a check that passed'

/** POLICY_ENTRY_ABSENT_BECAUSE is why a catalog entry the document carries
 * without its `name` or its `source.kind` draws no value there: the same claim as
 * `POLICY_CHECK_ABSENT_BECAUSE`, about the document rather than about the wire.
 * `catalogOf` keeps such an entry rather than dropping it -- a list drawn one
 * shorter than the count beside it is a list that lies about the document -- so
 * the two cells are what has to say the member is not there. */
export const POLICY_ENTRY_ABSENT_BECAUSE =
  'the document this section read carries this entry without the member: a member the document does not carry is not one the console can draw'

/** POLICY_OTHERS_LEAD says what the figure beside it counts: the checks
 * `doctor` returned that this section does not draw. A filter that said nothing
 * would be a filter a reader cannot see. */
export const POLICY_OTHERS_LEAD = 'checks doctor returned that this section does not draw'

/** POLICY_NOTES_CLASS is the body's own class. The section's frame is the tab's
 * (`projects.ts`'s panel shell, CM10); this is the box a body is drawn into. */
export const POLICY_NOTES_CLASS = 'policy-notes'

// ---------------------------------------------------------------------------
// The model.
// ---------------------------------------------------------------------------

/**
 * PolicyNoteValue is one scalar of the node: the wire field it came from, and
 * either the document's string or a reading with no number.
 *
 * The two are exclusive by construction and neither is optional-with-a-default:
 * a value the document carries is `text` (drawn through `putText` with the class
 * its key declares), and a value it does not carry is a reading with no number
 * (drawn `n/a` with its reason in the title), so no cell can be an empty string
 * that reads like a value the node sent.
 */
export interface PolicyNoteValue {
  readonly field: string
  readonly text: string | null
  readonly textClass: TextClass
  readonly reading: Reading | null
}

/** PolicyAccount is one of the project's Claude accounts as the node carries it:
 * its name (the map key) and the canonical repositories whose runs may use its
 * token. The token itself is not in this document at all, and no screen reads
 * one. */
export interface PolicyAccount {
  readonly name: string
  readonly repos: readonly string[]
}

/** PolicyCatalogEntry is one named image of the project's catalog
 * (spec.md §Catalog entry). `kind` is the source's own word (`dockerfile`,
 * `inline`, `image`), and the pointer members are the ones that kind uses: an
 * inline definition's bytes are the definition itself and are not drawn. */
export interface PolicyCatalogEntry {
  readonly name: string | null
  readonly kind: string | null
  readonly repo: string | null
  readonly path: string | null
  readonly branch: string | null
  readonly image: string | null
  readonly default: boolean
}

/** PolicyRouting is the node's routing switch: the value the run applies and the
 * canonical Claude IDs it opts in. `via` is the empty string when the node
 * carries the block without one, which `POLICY_DIRECT_WORD` covers. */
export interface PolicyRouting {
  readonly via: string
  readonly models: readonly string[]
}

/** PolicyNodeState is where the node's read stands: it answered (`read`), it
 * answered that no policy is published (`no-policy`), or it did not answer at
 * all (`absent` -- no document) or answered with a document this section does not
 * read (`shape`). The two refusals are states of their own because they are
 * different facts and because each names its own command. */
export type PolicyNodeState = 'read' | 'no-policy' | 'absent' | 'shape'

/** PolicyNodeBlock is the node's own block: the read's state and, when it
 * answered, the five members. */
export interface PolicyNodeBlock {
  readonly state: PolicyNodeState
  readonly baseBranch: PolicyNoteValue
  readonly allowRepos: readonly string[]
  readonly accounts: readonly PolicyAccount[]
  readonly routing: PolicyRouting | null
  readonly catalog: readonly PolicyCatalogEntry[]
}

/** PolicyCheckRow is one of the six checks: `ok` is `null` where the document
 * did not carry this check at all, which is not the same fact as a check the
 * document says failed. */
export interface PolicyCheckRow {
  readonly id: string
  readonly severity: string | null
  readonly ok: boolean | null
  readonly problem: string | null
  readonly fix: string | null
}

/** PolicyChecksState is where the checks' read stands, for the reasons
 * `PolicyNodeState` gives. */
export type PolicyChecksState = 'read' | 'absent' | 'shape'

/** PolicyChecksBlock is the checks' block: the read's state, the six rows in
 * `POLICY_CHECK_IDS`' order, and how many checks the document carried that this
 * section does not draw. */
export interface PolicyChecksBlock {
  readonly state: PolicyChecksState
  readonly checks: readonly PolicyCheckRow[]
  readonly others: number
}

/** PolicyNotesModel is the whole section, built and testable with no element. */
export interface PolicyNotesModel {
  readonly project: string | null
  readonly node: PolicyNodeBlock
  readonly checks: PolicyChecksBlock
}

/**
 * PolicyNotesInputs is the two documents the section is drawn from.
 *
 * Each is `unknown` for the reason every screen's inputs are: it crossed a
 * subprocess boundary, and this module's readers are the only thing that decides
 * what it holds. `null` means no document arrived.
 */
export interface PolicyNotesInputs {
  readonly document: unknown
  readonly doctor: unknown
}

// ---------------------------------------------------------------------------
// The readers.
// ---------------------------------------------------------------------------

/** objectAt is a member as an object, or null when it is anything else --
 * including an array, which is an object to `typeof` and is not one here. */
function objectAt(raw: unknown): Record<string, unknown> | null {
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return null
  return raw as Record<string, unknown>
}

/** arrayAt is a member as an array, or null when it is anything else. */
function arrayAt(raw: unknown): readonly unknown[] | null {
  return Array.isArray(raw) ? raw : null
}

/** textAt is a member as a string, or null: a number, an object and an absent
 * member are all "the document does not carry this as text", and none of them is
 * coerced into one. */
function textAt(raw: unknown): string | null {
  return typeof raw === 'string' ? raw : null
}

/** textsAt is a member as a list of the strings it holds, and only of those: an
 * entry that is not a string is dropped rather than drawn as `undefined`. */
function textsAt(raw: unknown): readonly string[] {
  const list = arrayAt(raw)
  if (list === null) return []
  return list.filter((one): one is string => typeof one === 'string')
}

// ---------------------------------------------------------------------------
// The builder.
// ---------------------------------------------------------------------------

/**
 * buildPolicyNotes turns the two documents into the section. It touches no
 * element, so every rule below -- which members are drawn, what a missing member
 * says, which checks are the six -- is testable in plain Node.
 */
export function buildPolicyNotes(inputs: PolicyNotesInputs): PolicyNotesModel {
  const document = objectAt(inputs.document)
  return {
    project: document === null ? null : textAt(document['project']),
    node: policyNodeBlock(document),
    checks: policyChecksBlock(inputs.doctor),
  }
}

/** hasOwn is whether an object writes a key at all, which is what tells "the
 * document answers with a null policy" apart from "the document is not this
 * document". */
function hasOwn(object: Record<string, unknown>, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(object, key)
}

/** baseBranchValue is the node's base branch: the document's string, or the
 * reading a cell with no value is drawn from.
 *
 * An **empty** `base_branch` is the absent one: the member is `omitempty` and
 * the node's own reader documents both the empty and the missing value as "the
 * project sets none, so a repository runs against `main`". A cell holding an
 * empty string would say the node named a branch whose name is nothing, which is
 * a third fact the node cannot express. */
function baseBranchValue(raw: unknown): PolicyNoteValue {
  const text = textAt(raw)
  if (text === null || text === '') {
    return {
      field: 'policy.base_branch',
      text: null,
      textClass: 'branch',
      reading: unavailable(POLICY_NO_BASE_BRANCH),
    }
  }
  return { field: 'policy.base_branch', text, textClass: 'branch', reading: null }
}

/** EMPTY_NODE is the node block of a read that did not answer: no member is
 * drawn, and the state says which refusal it was. */
function emptyNode(state: PolicyNodeState): PolicyNodeBlock {
  return { state, baseBranch: baseBranchValue(null), allowRepos: [], accounts: [], routing: null, catalog: [] }
}

/** policyNodeBlock reads the node, member by member. */
function policyNodeBlock(document: Record<string, unknown> | null): PolicyNodeBlock {
  if (document === null) return emptyNode('absent')
  if (!hasOwn(document, 'policy')) return emptyNode('shape')
  const raw = document['policy']
  // A null policy is the CLI's own answer for a project that published none, and
  // it is the one value of this key that is not a document.
  if (raw === null) return emptyNode('no-policy')
  const policy = objectAt(raw)
  if (policy === null) return emptyNode('shape')

  return {
    state: 'read',
    baseBranch: baseBranchValue(policy['base_branch']),
    allowRepos: textsAt(policy['allow_repos']),
    accounts: accountsOf(policy['accounts']),
    routing: routingOf(policy['routing']),
    catalog: catalogOf(policy['image_catalog']),
  }
}

/** accountsOf reads the accounts block, sorted by name: the node holds it as a
 * map, and a drawing whose order came from the decoder would be a drawing that
 * changes for no reason a reader can see. */
function accountsOf(raw: unknown): readonly PolicyAccount[] {
  const block = objectAt(raw)
  if (block === null) return []
  const accounts: PolicyAccount[] = []
  for (const [name, entry] of Object.entries(block)) {
    accounts.push({ name, repos: textsAt(objectAt(entry)?.['allow_repos']) })
  }
  return accounts.sort((left, right) => (left.name < right.name ? -1 : left.name > right.name ? 1 : 0))
}

/** routingOf reads the routing switch, or null when the node carries none:
 * `routing` and `routing.claude` are both `omitempty`, and nil is direct (D1b). */
function routingOf(raw: unknown): PolicyRouting | null {
  const claude = objectAt(objectAt(raw)?.['claude'])
  if (claude === null) return null
  return { via: textAt(claude['via']) ?? '', models: textsAt(claude['models']) }
}

/** catalogOf reads the image catalog's entries, in the order the node holds
 * them: the order is the operator's own (the catalog is a hand-curated list),
 * and nothing here sorts it. */
function catalogOf(raw: unknown): readonly PolicyCatalogEntry[] {
  const entries = arrayAt(objectAt(raw)?.['entries']) ?? []
  const catalog: PolicyCatalogEntry[] = []
  for (const entry of entries) {
    const object = objectAt(entry)
    if (object === null) continue
    const source = objectAt(object['source'])
    catalog.push({
      name: textAt(object['name']),
      kind: textAt(source?.['kind']),
      repo: textAt(source?.['repo']),
      path: textAt(source?.['path']),
      branch: textAt(source?.['branch']),
      image: textAt(source?.['image']),
      default: object['default'] === true,
    })
  }
  return catalog
}

/** policyChecksBlock reads the six checks out of `doctor --json`.
 *
 * The document's `checks` array is read in one pass and the drawn list is built
 * from `POLICY_CHECK_IDS`, so the order a reader sees is the ruling's and not the
 * CLI's, a check the document carries twice is drawn once, and a check the
 * document does not carry is drawn as the undrawn state rather than dropped. */
function policyChecksBlock(raw: unknown): PolicyChecksBlock {
  const document = objectAt(raw)
  if (document === null) return { state: 'absent', checks: [], others: 0 }
  const list = arrayAt(document['checks'])
  if (list === null) return { state: 'shape', checks: [], others: 0 }

  const found = new Map<string, PolicyCheckRow>()
  let others = 0
  for (const entry of list) {
    const object = objectAt(entry)
    if (object === null) continue
    const id = textAt(object['id'])
    if (id === null) continue
    if (!POLICY_CHECK_IDS.includes(id)) {
      others += 1
      continue
    }
    if (found.has(id)) continue
    found.set(id, {
      id,
      severity: textAt(object['severity']),
      ok: typeof object['ok'] === 'boolean' ? object['ok'] : null,
      problem: textAt(object['problem']),
      fix: textAt(object['fix']),
    })
  }
  const checks = POLICY_CHECK_IDS.map(
    (id): PolicyCheckRow => found.get(id) ?? { id, severity: null, ok: null, problem: null, fix: null },
  )
  return { state: 'read', checks, others }
}

// ---------------------------------------------------------------------------
// The drawing.
// ---------------------------------------------------------------------------

/** ScreenElement is the element a screen draws into. It is declared here rather
 * than imported from `renderer/app.ts` for the reason the four sibling screens
 * declare it: the file the interface belongs in is not a console task's to edit,
 * the declarations are structurally identical, and the day `app.ts` holds them
 * all the screens import them. */
export interface ScreenElement extends ElementLike {
  readonly tag: string
  append(child: ScreenElement): void
  childNodes(): readonly ScreenElement[]
}

/** ScreenFactory is where elements come from: an argument rather than a
 * `document` reference, so the section draws in a test with no window. */
export interface ScreenFactory {
  create(tag: string): ScreenElement
}

/** make is a fresh element with its classes. */
function make(factory: ScreenFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = factory.create(tag)
  element.setAttribute('class', classes.join(' '))
  return element
}

/** literal writes the console's own vocabulary -- never a document's string,
 * which comes through `putText` or not at all. */
function literal(element: ScreenElement, text: string): ScreenElement {
  element.textContent = text
  return element
}

/** cell is a text cell of the console's own making, appended to a parent. */
function cell(parent: ScreenElement, factory: ScreenFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = make(factory, tag, ...classes)
  parent.append(element)
  return element
}

/** textCell is `cell` with a document's string written into it through `putText`,
 * with the class the wire key declares. */
function textCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  tag: string,
  raw: unknown,
  textClass: TextClass,
  field: string,
  ...classes: string[]
): ScreenElement {
  const element = cell(parent, factory, tag, ...classes)
  putText(element, raw, textClass, field)
  return element
}

/** numberCell is `cell` with a reading written through `putReading`, so no
 * number can be drawn without its state. */
function numberCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  tag: string,
  reading: Reading,
  ...classes: string[]
): ScreenElement {
  const element = cell(parent, factory, tag, 'policy-notes-number', ...classes)
  putReading(element, reading, 'count')
  return element
}

/** valueCell is one member's value: the document's string through `putText`, or
 * the reading a member the document does not carry is drawn from. The two doors
 * are exclusive, which is what keeps an absent member from being an empty cell. */
function valueCell(parent: ScreenElement, factory: ScreenFactory, value: PolicyNoteValue, ...classes: string[]): ScreenElement {
  const element = cell(parent, factory, 'span', 'policy-notes-value', ...classes)
  if (value.reading === null) putText(element, value.text, value.textClass, value.field)
  else putReading(element, value.reading, 'count')
  return element
}

/**
 * policyNotesBody draws the section's body: the project line, the node's block
 * and the checks' block, in that order.
 *
 * The body wears no `data-theme`: the tab's frame stamps it on the root this
 * body is hosted in, and a second place the theme is decided is a second place a
 * golden can disagree with.
 */
export function policyNotesBody(factory: ScreenFactory, model: PolicyNotesModel): ScreenElement {
  const element = make(factory, 'div', POLICY_NOTES_CLASS)
  if (model.project !== null) {
    textCell(element, factory, 'p', model.project, 'project', 'project', 'policy-notes-project')
  }
  element.append(nodeBlock(factory, model.node))
  element.append(checksBlock(factory, model.checks))
  // The one line about a diff, and it is the sentence saying there is none
  // (D1482): the change is printed by `fugaro update` and recorded nowhere, so
  // the section says where a reader gets it instead of drawing a claim.
  literal(cell(element, factory, 'p', 'policy-notes-note'), POLICY_NO_DIFF_NOTE)
  return element
}

/** nodeBlock draws the node's read: its heading, its source, its state, and --
 * when it answered with a policy -- the five members. */
function nodeBlock(factory: ScreenFactory, node: PolicyNodeBlock): ScreenElement {
  const element = make(factory, 'div', 'policy-notes-block')
  element.setAttribute('data-source', 'policy.show')
  literal(cell(element, factory, 'h2', 'policy-notes-heading'), POLICY_NODE_TITLE)
  literal(cell(element, factory, 'p', 'policy-notes-source'), POLICY_NODE_SOURCE)
  const state = cell(element, factory, 'span', 'policy-notes-state')
  if (node.state === 'read') {
    element.setAttribute('data-state', 'read')
    literal(state, 'read')
    element.append(nodeMembers(factory, node))
    return element
  }
  if (node.state === 'no-policy') {
    element.setAttribute('data-state', 'no-policy')
    literal(state, 'no policy')
    literal(cell(element, factory, 'p', 'policy-notes-because'), POLICY_NO_POLICY)
    return element
  }
  element.setAttribute('data-state', 'unavailable')
  literal(state, 'not read')
  if (node.state === 'absent') {
    literal(cell(element, factory, 'p', 'policy-notes-because'), POLICY_NODE_ABSENT)
    return element
  }
  literal(cell(element, factory, 'p', 'policy-notes-because'), POLICY_NODE_SHAPE)
  return element
}

/** nodeMembers draws the five members the plan names, each in a block of its
 * own. Each member's title is written by its own draw function rather than
 * passed in: the title is the console's own word, and a parameter would be a
 * string this file cannot vouch for. */
function nodeMembers(factory: ScreenFactory, node: PolicyNodeBlock): ScreenElement {
  const element = make(factory, 'div', 'policy-notes-members')
  element.append(baseBranchMember(factory, node.baseBranch))
  element.append(allowReposMember(factory, node.allowRepos))
  element.append(accountsMember(factory, node.accounts))
  element.append(routingMember(factory, node.routing))
  element.append(catalogMember(factory, node.catalog))
  return element
}

/** listOrNone draws the console's own word for a list the document answered
 * empty. The word matters: the count beside it is a measured zero, and a box
 * with a `0` and nothing else reads as a list that did not load. */
function listOrNone(factory: ScreenFactory, parent: ScreenElement, empty: boolean): ScreenElement | null {
  if (!empty) return null
  return literal(cell(parent, factory, 'span', 'policy-notes-empty'), POLICY_NONE_WORD)
}

/** baseBranchMember draws the project's default base branch. */
function baseBranchMember(factory: ScreenFactory, value: PolicyNoteValue): ScreenElement {
  const element = make(factory, 'div', 'policy-notes-member')
  element.setAttribute('data-member', 'base_branch')
  literal(cell(element, factory, 'h3', 'policy-notes-member-title'), 'base branch')
  valueCell(element, factory, value, 'policy-notes-branch')
  return element
}

/** allowReposMember draws the project's allow-list: the repositories the
 * project's runs are admitted to, with the list drawn as its own rows so a
 * reader can count them against the figure beside the heading.
 *
 * Each entry is drawn through the **`repoPattern`** class, not `repository`:
 * an entry is `provider/owner/name` or a `*` pattern
 * (`projectpolicy.MatchRepo`), and `repository` is `owner/name`, one slash --
 * it refuses every value the CLI writes, so the whole list drew `n/a` beside a
 * count of its entries (review of 2026-11-07). */
function allowReposMember(factory: ScreenFactory, repos: readonly string[]): ScreenElement {
  const element = make(factory, 'div', 'policy-notes-member')
  element.setAttribute('data-member', 'allow_repos')
  literal(cell(element, factory, 'h3', 'policy-notes-member-title'), 'allow repositories')
  numberCell(element, factory, 'span', measured(repos.length), 'policy-notes-count')
  if (listOrNone(factory, element, repos.length === 0) !== null) return element
  const list = cell(element, factory, 'ul', 'policy-notes-list')
  for (const repo of repos) {
    textCell(list, factory, 'li', repo, 'repoPattern', 'policy.allow_repos[]', 'policy-notes-repo')
  }
  return element
}

/** accountsMember draws the project's Claude accounts: each account's name and
 * the repositories its token may be used by. **Names only** -- the document
 * carries no token and no screen asks for one. */
function accountsMember(factory: ScreenFactory, accounts: readonly PolicyAccount[]): ScreenElement {
  const element = make(factory, 'div', 'policy-notes-member')
  element.setAttribute('data-member', 'accounts')
  literal(cell(element, factory, 'h3', 'policy-notes-member-title'), 'accounts')
  numberCell(element, factory, 'span', measured(accounts.length), 'policy-notes-count')
  if (listOrNone(factory, element, accounts.length === 0) !== null) return element
  const list = cell(element, factory, 'ul', 'policy-notes-list')
  for (const account of accounts) {
    const row = cell(list, factory, 'li', 'policy-notes-account')
    textCell(row, factory, 'span', account.name, 'label', 'policy.accounts{}.name', 'policy-notes-account-name')
    if (account.repos.length === 0) {
      // An account whose list the node left empty is drawn in the console's own
      // words rather than as nothing: the key is what bounds the token, and an
      // account row with no repositories beside it would read as a row that
      // loaded without its list.
      literal(cell(row, factory, 'span', 'policy-notes-empty'), POLICY_NO_REPOSITORY_WORD)
      continue
    }
    for (const repo of account.repos) {
      textCell(row, factory, 'span', repo, 'repoPattern', 'policy.accounts{}.allow_repos[]', 'policy-notes-repo')
    }
  }
  return element
}

/** routingMember draws the routing switch: the value the run applies, and the
 * models it opts in. An absent switch (or one the node wrote empty) is the
 * `direct` the CLI's own reader answers with, drawn as a value rather than as an
 * `n/a`, because the run's destination is known and not missing (D1b). */
function routingMember(factory: ScreenFactory, routing: PolicyRouting | null): ScreenElement {
  const element = make(factory, 'div', 'policy-notes-member')
  element.setAttribute('data-member', 'routing')
  literal(cell(element, factory, 'h3', 'policy-notes-member-title'), 'routing')
  if (routing === null || routing.via === '') {
    literal(cell(element, factory, 'span', 'policy-notes-value'), POLICY_DIRECT_WORD)
    return element
  }
  textCell(element, factory, 'span', routing.via, 'label', 'policy.routing.claude.via', 'policy-notes-value')
  for (const model of routing.models) {
    textCell(element, factory, 'span', model, 'label', 'policy.routing.claude.models[]', 'policy-notes-model')
  }
  return element
}

/** catalogMember draws the image catalog: each entry's name, the kind of source
 * it is built from, and that source's pointer.
 *
 * A `dockerfile` entry's path is drawn through the `path` class, which this
 * console replaces with its own placeholder: the class exists because a path is
 * where the operator's own machine shows through, and an entry's repository and
 * branch are what a reader needs to find the file. A `dockerfile` entry's
 * `repo` is an `owner/name` and goes through `repository`; an `image` entry's
 * reference goes through **`imageRef`**, the class that holds
 * `internal/config`'s `catalogImageRE` -- `label` drew `ghcr.io/acme/agent:1.2`
 * as `<host><path>:1.2`, a redaction the document does not contain (review of
 * 2026-11-07). An `inline` entry's bytes are the definition itself and are not
 * drawn -- the node holds them, and a screen that printed a Dockerfile would be
 * a screen printing a document rather than a fact about the project.
 *
 * `name` and the source's `kind` are drawn through `absentValueCell` rather than
 * `textCell`: both readers answer `null` for a member the document does not
 * carry, and the entry is still an entry (the count above the list counts it). */
function catalogMember(factory: ScreenFactory, catalog: readonly PolicyCatalogEntry[]): ScreenElement {
  const element = make(factory, 'div', 'policy-notes-member')
  element.setAttribute('data-member', 'image_catalog')
  literal(cell(element, factory, 'h3', 'policy-notes-member-title'), 'image catalog')
  numberCell(element, factory, 'span', measured(catalog.length), 'policy-notes-count')
  if (listOrNone(factory, element, catalog.length === 0) !== null) return element
  const list = cell(element, factory, 'ul', 'policy-notes-list')
  for (const entry of catalog) {
    const row = cell(list, factory, 'li', 'policy-notes-entry')
    if (entry.default) literal(cell(row, factory, 'span', 'policy-notes-default'), POLICY_DEFAULT_WORD)
    absentValueCell(row, factory, entry.name, POLICY_ENTRY_ABSENT_BECAUSE, 'label', 'policy.image_catalog.entries[].name', 'policy-notes-entry-name')
    absentValueCell(row, factory, entry.kind, POLICY_ENTRY_ABSENT_BECAUSE, 'label', 'policy.image_catalog.entries[].source.kind', 'policy-notes-entry-kind')
    if (entry.image !== null) {
      textCell(row, factory, 'span', entry.image, 'imageRef', 'policy.image_catalog.entries[].source.image', 'policy-notes-entry-image')
    }
    if (entry.repo !== null) {
      textCell(row, factory, 'span', entry.repo, 'repository', 'policy.image_catalog.entries[].source.repo', 'policy-notes-entry-repo')
    }
    if (entry.path !== null) {
      textCell(row, factory, 'span', entry.path, 'path', 'policy.image_catalog.entries[].source.path', 'policy-notes-entry-path')
    }
    if (entry.branch !== null) {
      textCell(row, factory, 'span', entry.branch, 'branch', 'policy.image_catalog.entries[].source.branch', 'policy-notes-entry-branch')
    }
  }
  return element
}

/** checksBlock draws the checks' read: its heading, its source, its state, the
 * six rows and the count of the checks it does not draw. */
function checksBlock(factory: ScreenFactory, checks: PolicyChecksBlock): ScreenElement {
  const element = make(factory, 'div', 'policy-notes-block')
  element.setAttribute('data-source', 'environment.report')
  literal(cell(element, factory, 'h2', 'policy-notes-heading'), POLICY_CHECKS_TITLE)
  literal(cell(element, factory, 'p', 'policy-notes-source'), POLICY_CHECKS_SOURCE)
  const state = cell(element, factory, 'span', 'policy-notes-state')
  if (checks.state !== 'read') {
    element.setAttribute('data-state', 'unavailable')
    literal(state, 'not read')
    if (checks.state === 'absent') {
      literal(cell(element, factory, 'p', 'policy-notes-because'), POLICY_CHECKS_ABSENT)
      return element
    }
    literal(cell(element, factory, 'p', 'policy-notes-because'), POLICY_CHECKS_SHAPE)
    return element
  }
  element.setAttribute('data-state', 'read')
  literal(state, 'read')
  numberCell(element, factory, 'span', measured(checks.others), 'policy-notes-others')
  literal(cell(element, factory, 'span', 'policy-notes-others-lead'), POLICY_OTHERS_LEAD)
  const list = cell(element, factory, 'ul', 'policy-notes-checks')
  for (const row of checks.checks) list.append(checkRow(factory, row))
  return element
}

/** absentValueCell draws one member that a document may carry or not: the
 * document's string through `putText` with the class its key declares, or -- where
 * the document does not carry the member -- the door this file's other absent
 * members go through (`valueCell`), the `n/a` mark with the caller's own sentence
 * for why.
 *
 * A member the document does not carry may not be handed to `putText` as a
 * `null`: it drew `n/a` titled "checks[].severity is a object, not text", which is
 * ungrammatical and claims the wire carried a member of the wrong type when it
 * carried none (review of 2026-11-07). The same defect reached a catalog entry's
 * `name` and `source.kind`, which is why the sentence is a parameter and not the
 * checks' one. */
function absentValueCell(
  parent: ScreenElement,
  factory: ScreenFactory,
  value: string | null,
  because: string,
  textClass: TextClass,
  field: string,
  ...classes: string[]
): ScreenElement {
  if (value === null) {
    const element = cell(parent, factory, 'span', ...classes)
    putReading(element, unavailable(because), 'count')
    return element
  }
  return textCell(parent, factory, 'span', value, textClass, field, ...classes)
}

/** checkRow draws one of the six: its state, its id, its severity, and the
 * CLI's own two sentences.
 *
 * The state word is the **CLI's own** classification read out of the document
 * rather than a second one the console makes: `ok` for a check the document says
 * passed, and otherwise the weight the document gives it -- `info` is a state
 * `doctor` prints and does not fail on, and any other severity is a finding it
 * does (`checksFail` and `doctorClassOf`, `internal/cli/doctor_render.go`;
 * design `m11-setup-and-skills.md` §4.5: "an informational state never fails
 * doctor; a warning one fails it only with `--strict`").
 *
 * `ok` alone is not that rule. Read alone it drew **`failing` for
 * `policy-published`**, which is the one check of the six nearly every
 * installation carries -- the CLI writes it `"ok": false` with
 * `"severity": "info"` and files it as a measurement (`doctorMeasureIDs`) --
 * so a healthy installation read as a failing one (review of 2026-11-07). The
 * `data-ok` attribute stays the document's own boolean, which is a different
 * fact from the state word beside it. */
function checkRow(factory: ScreenFactory, row: PolicyCheckRow): ScreenElement {
  const element = make(factory, 'li', 'policy-notes-check')
  const state = cell(element, factory, 'span', 'policy-notes-check-state')
  if (row.ok === null) {
    element.setAttribute('data-ok', 'unstated')
    literal(state, POLICY_CHECK_UNDRAWN_WORD)
  } else if (row.ok) {
    element.setAttribute('data-ok', 'true')
    literal(state, POLICY_CHECK_OK_WORD)
  } else if (row.severity === 'info') {
    element.setAttribute('data-ok', 'false')
    literal(state, POLICY_CHECK_NOTED_WORD)
  } else {
    element.setAttribute('data-ok', 'false')
    literal(state, POLICY_CHECK_FAILING_WORD)
  }
  textCell(element, factory, 'span', row.id, 'label', 'checks[].id', 'policy-notes-check-id')
  absentValueCell(element, factory, row.severity, POLICY_CHECK_ABSENT_BECAUSE, 'label', 'checks[].severity', 'policy-notes-check-severity')
  absentValueCell(element, factory, row.problem, POLICY_CHECK_ABSENT_BECAUSE, 'sentence', 'checks[].problem', 'policy-notes-check-problem')
  absentValueCell(element, factory, row.fix, POLICY_CHECK_ABSENT_BECAUSE, 'sentence', 'checks[].fix', 'policy-notes-check-fix')
  return element
}
