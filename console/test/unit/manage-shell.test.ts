// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Manage section's shell: the four accordion groups, one section per kind,
// and the three rules a kind's independence is made of (the console-improvements
// plan's task MO-SH; reconciliation R3; decisions D2903 and D2909; the prose is
// `docs/console-improvements/manage-kinds.md`).
//
// The file holds the plan's own test list and one case more than it, because the
// rules it pins are the ones a kind added later can only break by accident:
//
//   - **the four groups, in the descriptor's order, collapsed, with the groups
//     no kind landed in left out** -- and nothing read until one is opened, or
//     the whole section is asked for;
//   - **one section per kind**, with the kind's own heading, the columns its
//     descriptor declares and one row per row `decode` answered with;
//   - **every cell through the class its column declared** (`TEXT_CLASSES`), so
//     a value the class refuses is the mark with the reason and never the value;
//   - **the red step the plan names**: one kind's refusal leaves the others
//     drawn. That case fails the moment the shell reads the section with one
//     read, or paints one refusal over the whole section, which is the defect
//     `.claude/rules/console.md`'s parallel-sections rule exists for;
//   - **one busy mark per kind**, in the kind being read and in no other, kept
//     over the rows a re-read already has, and cleared with the section on
//     `end()`;
//   - **the stamp and R1's Retry**, each re-reading its own kind and nothing
//     else;
//   - **the console's own sentence for an empty list**, with the kind's actions
//     beside it;
//   - **the Copy-command copied exactly** (`copyCommandText`, never quoted) and
//     the open-view asked of the caller;
//   - **a `decode` that throws** treated as a refusal of that kind alone, in the
//     console's own words rather than the kind's exception;
//   - **the newest read's answer kept** when an older one lands late.
//
// The harness is `projects.test.ts`'s in this file's own words: a fake element
// that records what a section may draw and **throws on the attributes a section
// may not set**, a `walk` over the drawn tree, and one factory of fresh fakes.
// The one thing this file adds is that the fake carries listeners -- the shell
// owns its own interaction (the group's disclosure, the Refresh, the Retry, the
// Copy), so a case has to be able to press a control, and `fire` is the press.
// The clock is a fake too: a kind's fifteen-second still line is armed on the
// timer the shell was handed, so a case that leaves a read in flight leaves no
// host timer behind.

import { describe, expect, it } from 'vitest'

import { UNAVAILABLE_MARK } from '../../src/shared/availability'
import { TOKEN_PLACEHOLDER } from '../../src/shared/sanitize'
import { gateFrom } from '../../src/renderer/gating'
import {
  BUSY_CLASS,
  BUSY_LABEL_CLASS,
  BUSY_SLOT_CLASS,
  RETRY_CLASS,
  STAMP_AT_CLASS,
  STAMP_CLASS,
  STAMP_LEAD,
  STAMP_REFRESH_CLASS,
  stampOf,
} from '../../src/renderer/section'
import type { SectionTimers } from '../../src/renderer/section'
import { SKELETON_CLASS } from '../../src/renderer/skeleton'
import {
  buildCopyCommand,
  commandValue,
  commandWord,
  copyCommandText,
} from '../../src/renderer/manage/command'
import type { CommandPart } from '../../src/renderer/manage/command'
import {
  MANAGE_ACTIONS_CLASS,
  MANAGE_BUSY_LEAD,
  MANAGE_CELL_CLASS,
  MANAGE_CLASS,
  MANAGE_COLUMN_ATTRIBUTE,
  MANAGE_COLUMN_CLASS,
  MANAGE_COPY_CLASS,
  MANAGE_EMPTY_CLASS,
  MANAGE_EMPTY_SENTENCE,
  MANAGE_GROUP_ATTRIBUTE,
  MANAGE_GROUP_CLASS,
  MANAGE_GROUP_TITLE_CLASS,
  MANAGE_KIND_ATTRIBUTE,
  MANAGE_KIND_CLASS,
  MANAGE_KIND_TITLE_CLASS,
  MANAGE_OPERATION_ATTRIBUTE,
  MANAGE_REFUSAL_CLASS,
  MANAGE_ROW_ATTRIBUTE,
  MANAGE_ROW_CLASS,
  MANAGE_TABLE_CLASS,
  MANAGE_VIEW_CLASS,
  mountManage,
} from '../../src/renderer/manage/shell'
import type {
  ManageDeps,
  ManageElement,
  ManageFactory,
  ManageSection,
} from '../../src/renderer/manage/shell'
import { MANAGE_GROUPS, defineManageKind } from '../../src/renderer/manage/types'
import type {
  CopyCommandAction,
  ManageAction,
  ManageColumn,
  ManageGroup,
  ManageKind,
  ManageRow,
} from '../../src/renderer/manage/types'

import { githubToken } from '../support/fakeSecrets'

// ---------------------------------------------------------------------------
// The harness: a fake element that refuses what a section may not do, and that
// can be pressed.
// ---------------------------------------------------------------------------

/** FakeManage is the element the shell draws into. It records text, classes,
 * attributes and listeners, and it **throws on the attributes a section must
 * never set**: the navigating attribute, a resource, a style and any handler. A
 * shell that reached for one would fail every case in this file rather than one. */
class FakeManage implements ManageElement {
  readonly attributes = new Map<string, string>()
  readonly nodes: FakeManage[] = []
  readonly classes: string[] = []
  readonly listeners = new Map<string, (() => void)[]>()
  textContent: string | null = null

  static readonly FORBIDDEN_ATTRIBUTES = new Set([
    ['h', 'ref'].join(''),
    'src',
    'srcdoc',
    'style',
    'formaction',
    'action',
  ])

  constructor(readonly tag: string) {}

  setAttribute(name: string, value: string): void {
    const lowered = name.toLowerCase()
    if (FakeManage.FORBIDDEN_ATTRIBUTES.has(lowered) || lowered.startsWith('on')) {
      throw new Error(`a console section may not set the ${name} attribute`)
    }
    if (lowered === 'class') {
      this.classes.length = 0
      this.classes.push(...value.split(/\s+/).filter((one) => one !== ''))
    }
    this.attributes.set(name, value)
  }

  getAttribute(name: string): string | null {
    return this.attributes.get(name) ?? null
  }

  removeAttribute(name: string): void {
    this.attributes.delete(name)
  }

  append(child: ManageElement): void {
    this.nodes.push(child as FakeManage)
  }

  replaceChildren(...children: ManageElement[]): void {
    this.nodes.length = 0
    for (const child of children) this.nodes.push(child as FakeManage)
  }

  addEventListener(type: string, listener: () => void): void {
    const held = this.listeners.get(type) ?? []
    held.push(listener)
    this.listeners.set(type, held)
  }

  /** fire presses the element: every listener the shell wired on one type, in the
   * order it wired them, which is what the document does. */
  fire(type: string): void {
    for (const listener of this.listeners.get(type) ?? []) listener()
  }
}

/** factoryOf makes fresh fakes, in the shape the shell is handed. */
const factory: ManageFactory = { create: (tag) => new FakeManage(tag) }

/** walk visits every node of a tree, the root first. */
function walk(node: ManageElement): readonly FakeManage[] {
  const fake = node as FakeManage
  const out: FakeManage[] = [fake]
  for (const child of fake.nodes) out.push(...walk(child))
  return out
}

/** all is every node carrying a class, in document order. */
function all(node: ManageElement, className: string): readonly FakeManage[] {
  return walk(node).filter((one) => one.classes.includes(className))
}

/** one is the single node carrying a class, asserted to be single: a case that
 * read the first of two would pass over the defect it is written to catch (a
 * second busy mark, two rows where one was expected). */
function one(node: ManageElement, className: string): FakeManage {
  const found = all(node, className)
  expect(found, `expected exactly one .${className}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no .${className}`)
  return first
}

/** texts is the text of every node carrying a class, in document order. */
function texts(node: ManageElement, className: string): readonly (string | null)[] {
  return all(node, className).map((leaf) => leaf.textContent)
}

/** kindSection is the section one kind was drawn in, by the kind's own id. */
function kindSection(root: ManageElement, id: string): FakeManage {
  const found = walk(root).filter((node) => node.getAttribute(MANAGE_KIND_ATTRIBUTE) === id)
  expect(found, `expected exactly one section for the kind ${id}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no section for the kind ${id}`)
  return first
}

/** group is the disclosure one group was drawn as, by the group's own name. */
function group(root: ManageElement, name: string): FakeManage {
  const found = walk(root).filter(
    (node) => node.tag === 'details' && node.getAttribute(MANAGE_GROUP_ATTRIBUTE) === name,
  )
  expect(found, `expected exactly one disclosure for the group ${name}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no disclosure for the group ${name}`)
  return first
}

/** open opens a group the way the document does: the attribute first, then the
 * `toggle` the element fires for it. */
function open(root: ManageElement, name: string): void {
  const disclosure = group(root, name)
  disclosure.setAttribute('open', '')
  disclosure.fire('toggle')
}

// ---------------------------------------------------------------------------
// The reads: one promise per read, so a case decides when each answer lands.
// ---------------------------------------------------------------------------

interface Wait {
  readonly promise: Promise<unknown>
  answer(doc: unknown): void
  refuse(reason: unknown): void
}

function wait(): Wait {
  let answer!: (doc: unknown) => void
  let refuse!: (reason: unknown) => void
  const promise = new Promise<unknown>((onAnswer, onRefuse) => {
    answer = onAnswer
    refuse = onRefuse
  })
  return {
    promise,
    answer: (doc) => {
      answer(doc)
    },
    refuse: (reason) => {
      refuse(reason)
    },
  }
}

/** Timers is the clock the shell was handed. A kind's still line is armed here
 * rather than on the host's clock, so a case that leaves a read in flight leaves
 * no real timer behind. */
class Timers implements SectionTimers {
  readonly armed: number[] = []
  readonly cleared: number[] = []
  private next = 1

  setTimeout(_callback: () => void, ms: number): ReturnType<typeof setTimeout> {
    this.armed.push(ms)
    const handle = this.next
    this.next += 1
    return handle as unknown as ReturnType<typeof setTimeout>
  }

  clearTimeout(handle: ReturnType<typeof setTimeout>): void {
    this.cleared.push(handle as unknown as number)
  }
}

/** AT is the console clock every case reads the stamp against: the shell is
 * handed `now`, so the stamp is the same string wherever a case runs (a case
 * computes its expectation with `stampOf`, the same function, rather than
 * writing an hour into a file that runs in every time zone). */
const AT = Date.UTC(2026, 10, 20, 14, 5)

/** Handles is the caller's side of the shell: the reads it was asked for, the
 * answers nobody has given yet, what was copied and which view was asked for. */
class Handles {
  readonly calls: string[] = []
  readonly waits: Wait[] = []
  readonly copied: string[] = []
  readonly views: string[] = []
  readonly timers = new Timers()
  readonly deps: ManageDeps

  constructor() {
    this.deps = {
      factory,
      theme: 'dark',
      read: (operation) => {
        this.calls.push(operation)
        const held = wait()
        this.waits.push(held)
        return held.promise
      },
      copy: (text) => {
        this.copied.push(text)
      },
      openView: (view) => {
        this.views.push(view)
      },
      // These cases are about the shell's own tree and read no role: the gate
      // they hand it is `gating.ts`'s no-answer value, which allows everything
      // (`ManageDeps.can` is required).
      can: gateFrom(null).can,
      now: () => AT,
      timers: this.timers,
    }
  }

  /** answer settles the read at one index with a document, and refuse rejects it
   * with a reason. The index is the read's place in `calls`, which is the order
   * the shell asked for them in. */
  answer(index: number, doc: unknown): void {
    this.held(index).answer(doc)
  }

  refuse(index: number, reason: unknown): void {
    this.held(index).refuse(reason)
  }

  private held(index: number): Wait {
    const found = this.waits[index]
    if (found === undefined) throw new Error(`no read was made at index ${String(index)}`)
    return found
  }
}

/** mounted draws the shell over the kinds and hands back the section, its tree
 * and the caller's side of it. */
function mounted(kinds: readonly ManageKind[]): {
  section: ManageSection
  root: ManageElement
  handles: Handles
} {
  const handles = new Handles()
  const section = mountManage(kinds).draw(handles.deps)
  return { section, root: section.root, handles }
}

/** settle yields to the microtask queue, which is where a landed read paints. */
const settle = (): Promise<void> => Promise.resolve()

// ---------------------------------------------------------------------------
// The fixture kinds. They are fixtures and not the real kinds (MO-P, MO-C, MO-K
// and MO-B write those under `manage/kinds/`): what this file holds is the shell,
// and a fixture kind is a descriptor the shell has no opinion about.
// ---------------------------------------------------------------------------

/** rowsOf is a fixture `decode`: the document is the rows, and anything else is
 * an empty list. A real kind reads the keys it knows by name. */
function rowsOf(doc: unknown): readonly ManageRow[] {
  return Array.isArray(doc) ? (doc as readonly ManageRow[]) : []
}

const PERSON_COLUMNS: readonly ManageColumn[] = Object.freeze([
  { key: 'person', title: 'Person', class: 'person' },
  { key: 'role', title: 'Role', class: 'label' },
])

/** REPOSITORY_COLUMNS carries the pair the text-class case needs: an `owner/name`
 * passes the `repository` class and a value with a second slash does not, so one
 * document proves both halves of the rule. */
const REPOSITORY_COLUMNS: readonly ManageColumn[] = Object.freeze([
  { key: 'repository', title: 'Repository', class: 'repository' },
  { key: 'status', title: 'Status', class: 'label' },
])

interface Fixture {
  readonly id: string
  readonly group: ManageGroup
  readonly source: string
  readonly title?: string
  readonly columns?: readonly ManageColumn[]
  readonly decode?: (doc: unknown) => readonly ManageRow[]
  readonly actions?: readonly ManageAction[]
}

function kindOf(fixture: Fixture): ManageKind {
  return defineManageKind({
    id: fixture.id,
    group: fixture.group,
    title: fixture.title ?? fixture.id,
    source: fixture.source,
    columns: fixture.columns ?? PERSON_COLUMNS,
    decode: fixture.decode ?? rowsOf,
    actions: fixture.actions ?? [],
  })
}

/** copyAction builds the action a kind copies a command with, through MG0's own
 * builder: a fixture that assembled an argv by hand could carry a word the
 * builder would have refused, and this file is about what the shell does with an
 * admitted action. */
function copyAction(parts: readonly CommandPart[]): CopyCommandAction {
  const answer = buildCopyCommand(parts)
  if (!answer.ok) throw new Error(`the fixture command was refused: ${answer.reason}`)
  return answer.action
}

const ROW_ADA: ManageRow = Object.freeze({
  key: 'ada',
  cells: Object.freeze({ person: 'ada', role: 'operator' }),
})

const MEMBERS = kindOf({
  id: 'members',
  group: 'People & access',
  source: 'members.list',
  title: 'Members',
})

const ACCOUNTS = kindOf({
  id: 'accounts',
  group: 'People & access',
  source: 'accounts.inventory',
  title: 'Accounts',
})

const REPOS = kindOf({
  id: 'repos',
  group: 'Code',
  source: 'repos.ls',
  title: 'Repositories',
  columns: REPOSITORY_COLUMNS,
  actions: [
    copyAction([commandWord('fugaro'), commandWord('repos'), commandWord('add'), commandValue('acme/app')]),
    { kind: 'open-view', view: 'spend' },
  ],
})

const CAPS = kindOf({ id: 'budget', group: 'Budget', source: 'budget.show', title: 'Caps' })

/** REFUSAL is a CLI sentence of the shape a permissions refusal really has: it
 * names a role, which is exactly the part `sanitizeRefusal` keeps where the
 * free-text rules would redact it. A kind's refusal is the CLI's line and not the
 * console's, and a case pinned to a plain sentence would not tell the two passes
 * apart. */
const REFUSAL = 'fugaro: the accounts inventory needs roles/run.viewer'

// ---------------------------------------------------------------------------
// The groups.
// ---------------------------------------------------------------------------

describe('the Manage section’s shell', () => {
  it('draws the four groups in the descriptor’s order, collapsed, and skips a group with no kind', () => {
    // The caller lists the kinds worst-first on purpose: the order drawn is
    // `MANAGE_GROUPS`', and a shell that drew them in the caller's order would
    // pass a case whose fixtures happened to be listed in the right one.
    const { root, handles } = mounted([CAPS, MEMBERS])
    one(root, MANAGE_CLASS)
    expect(
      walk(root)
        .filter((node) => node.tag === 'details')
        .map((node) => node.getAttribute(MANAGE_GROUP_ATTRIBUTE)),
    ).toEqual(['People & access', 'Budget'])
    expect(texts(root, MANAGE_GROUP_TITLE_CLASS)).toEqual(['People & access', 'Budget'])
    // Collapsed by default: the attribute is absent, and nothing has been read
    // for a group nobody opened.
    for (const disclosure of all(root, MANAGE_GROUP_CLASS)) {
      expect(disclosure.getAttribute('open')).toBe(null)
    }
    expect(handles.calls).toEqual([])
    // Each kind is inside its own group's disclosure and not loose in the
    // section: the group is what an operator folds, and a kind drawn outside one
    // could not be folded away.
    expect(
      all(group(root, 'People & access'), MANAGE_KIND_CLASS).map((node) =>
        node.getAttribute(MANAGE_KIND_ATTRIBUTE),
      ),
    ).toEqual(['members'])
    expect(all(group(root, 'Budget'), MANAGE_KIND_CLASS).length).toBe(1)
    // Code and Models & keys have no kind here, so they are not drawn at all: a
    // heading with nothing under it would claim this console manages something it
    // does not. The four names are the descriptor's, and this count is what a
    // fifth group would change.
    const names = walk(root).map((node) => node.getAttribute(MANAGE_GROUP_ATTRIBUTE))
    expect(names).not.toContain('Code')
    expect(names).not.toContain('Models & keys')
    expect(MANAGE_GROUPS.length).toBe(4)
  })

  it('reads a group’s kinds when it is opened, and once only', async () => {
    const { root, handles } = mounted([MEMBERS, ACCOUNTS, CAPS])
    open(root, 'People & access')
    expect(handles.calls).toEqual(['members.list', 'accounts.inventory'])
    // Folding and opening it again is not a second round of children: the group
    // holds the rows it already read.
    const disclosure = group(root, 'People & access')
    disclosure.removeAttribute('open')
    disclosure.fire('toggle')
    disclosure.setAttribute('open', '')
    disclosure.fire('toggle')
    expect(handles.calls).toEqual(['members.list', 'accounts.inventory'])
    // And opening this one read nothing of the other group's kind.
    expect(handles.calls).not.toContain('budget.show')
    handles.answer(0, [ROW_ADA])
    handles.answer(1, [])
    await settle()
  })

  it('reads every kind when read() is asked, whatever group it is in, and clears the marks on end()', async () => {
    const { section, root, handles } = mounted([MEMBERS, CAPS])
    section.read()
    expect(handles.calls).toEqual(['members.list', 'budget.show'])
    expect(all(root, BUSY_CLASS).length).toBe(2)
    // The clock is the shell's: one still line armed per kind being read
    // (`sectionRead`'s fifteen seconds), and no host timer anywhere.
    expect(handles.timers.armed.length).toBe(2)

    section.end()
    // The mark goes with the state, and the still line is disarmed: a section the
    // operator has left writes nothing into a tree nobody is looking at.
    expect(all(root, BUSY_CLASS).length).toBe(0)
    expect(handles.timers.cleared.length).toBe(2)
    for (const kind of all(root, MANAGE_KIND_CLASS)) {
      expect(kind.getAttribute('data-busy')).toBe(null)
      expect(kind.getAttribute('aria-busy')).toBe(null)
    }
    // A read nobody answered is still the caller's, and its answer is still drawn
    // when it lands -- `end` clears the busy state, it does not unsay a read --
    // but no mark comes back with it.
    handles.answer(0, [ROW_ADA])
    await settle()
    expect(all(kindSection(root, 'members'), MANAGE_TABLE_CLASS).length).toBe(1)
    expect(all(root, BUSY_CLASS).length).toBe(0)
  })

  // -------------------------------------------------------------------------
  // One kind's own section: its heading, its columns, its rows.
  // -------------------------------------------------------------------------

  it('draws a section per kind, with the kind’s own heading, columns and rows', async () => {
    const { root, handles } = mounted([MEMBERS, ACCOUNTS])
    open(root, 'People & access')
    handles.answer(0, [ROW_ADA])
    handles.answer(1, [])
    await settle()

    const drawn = kindSection(root, 'members')
    expect(drawn.getAttribute(MANAGE_OPERATION_ATTRIBUTE)).toBe('members.list')
    expect(texts(drawn, MANAGE_KIND_TITLE_CLASS)).toEqual(['Members'])
    // The columns are the kind's own, in its own order, and every heading carries
    // the key its cells are keyed by.
    expect(texts(drawn, MANAGE_COLUMN_CLASS)).toEqual(['Person', 'Role'])
    expect(
      all(drawn, MANAGE_COLUMN_CLASS).map((node) => node.getAttribute(MANAGE_COLUMN_ATTRIBUTE)),
    ).toEqual(['person', 'role'])
    expect(one(drawn, MANAGE_TABLE_CLASS).tag).toBe('table')
    // One row, one cell per column in the descriptor's order -- and the row keeps
    // its own identity among the rows of this read.
    expect(all(drawn, MANAGE_ROW_CLASS).length).toBe(1)
    expect(one(drawn, MANAGE_ROW_CLASS).getAttribute(MANAGE_ROW_ATTRIBUTE)).toBe('ada')
    expect(texts(drawn, MANAGE_CELL_CLASS)).toEqual(['ada', 'operator'])
    // The neighbour section answered with nothing, and it is drawn as such: the
    // other half of one section per kind.
    expect(texts(kindSection(root, 'accounts'), MANAGE_EMPTY_CLASS)).toEqual([MANAGE_EMPTY_SENTENCE])
  })

  it('puts every cell through the class its column declared, so a refused value is the mark with the reason', async () => {
    const { root, handles } = mounted([REPOS])
    open(root, 'Code')
    handles.answer(0, [
      { key: 'entry-one', cells: { repository: 'acme/app', status: 'ready' } },
      { key: 'entry-two', cells: { repository: 'acme/apps/extra', status: 'ready' } },
    ])
    await settle()

    const cells = all(kindSection(root, 'repos'), MANAGE_CELL_CLASS).filter(
      (cell) => cell.getAttribute(MANAGE_COLUMN_ATTRIBUTE) === 'repository',
    )
    expect(cells.map((cell) => cell.textContent)).toEqual(['acme/app', UNAVAILABLE_MARK])
    // The reason is the console's own sentence in `title`, and it never quotes
    // the value it refused (`putText`: a refusal that echoed its input would put
    // the input on screen through the back door).
    const refused = cells[1]
    if (refused === undefined) throw new Error('no refused cell')
    const reason = refused.getAttribute('title')
    expect(reason).not.toBe(null)
    expect(reason).not.toContain('acme/apps/extra')
  })

  it('keeps a document key out of the row attribute, and draws the row without one when the key is refused', async () => {
    const { root, handles } = mounted([MEMBERS])
    open(root, 'People & access')
    // The key is a *document* string -- the CLI's own id, address or name -- and
    // it is drawn as an attribute, so it goes through the sanitizer like every
    // other document string (`rowKey`). A token-shaped key is redacted; a key the
    // class refuses loses the attribute. The row itself is drawn either way: the
    // cells are what the operator reads and the key is bookkeeping.
    const token = githubToken()
    handles.answer(0, [
      { key: 'ada', cells: { person: 'ada', role: 'operator' } },
      { key: token, cells: { person: 'grace', role: 'operator' } },
      { key: 'two\nlines', cells: { person: 'linus', role: 'operator' } },
    ])
    await settle()

    const drawn = kindSection(root, 'members')
    expect(all(drawn, MANAGE_ROW_CLASS).map((row) => row.getAttribute(MANAGE_ROW_ATTRIBUTE))).toEqual([
      'ada',
      TOKEN_PLACEHOLDER,
      null,
    ])
    expect(texts(drawn, MANAGE_CELL_CLASS)).toEqual([
      'ada',
      'operator',
      'grace',
      'operator',
      'linus',
      'operator',
    ])
    // And nothing the tree carries as an attribute holds the token: a key drawn
    // as written would put a credential on screen through the markup, where the
    // cell's own class would never see it (`projects.test.ts`'s guard, over this
    // section's tree).
    const values = walk(drawn).flatMap((node) => [...node.attributes.values()])
    for (const value of values) expect(value).not.toContain(token)
  })

  it('says the console’s own sentence for a kind that answered with nothing, and draws its actions beside it', async () => {
    const { root, handles } = mounted([REPOS])
    open(root, 'Code')
    handles.answer(0, [])
    await settle()

    const drawn = kindSection(root, 'repos')
    expect(texts(drawn, MANAGE_EMPTY_CLASS)).toEqual([MANAGE_EMPTY_SENTENCE])
    expect(all(drawn, MANAGE_TABLE_CLASS).length).toBe(0)
    // The commands a kind offers are about the kind and not about the rows a read
    // happened to answer with: an operator looking at an empty list is exactly
    // the one who wants the command that fills it.
    expect(all(drawn, MANAGE_ACTIONS_CLASS).length).toBe(1)
    expect(all(drawn, MANAGE_COPY_CLASS).length).toBe(1)
    expect(all(drawn, MANAGE_VIEW_CLASS).length).toBe(1)
  })

  // -------------------------------------------------------------------------
  // The red step: one kind's refusal leaves the others drawn.
  // -------------------------------------------------------------------------

  it('reads each kind on its own: one kind’s refusal leaves the others drawn', async () => {
    const { root, handles } = mounted([MEMBERS, ACCOUNTS])
    open(root, 'People & access')
    // Two reads, not one: a section's own read per kind is what makes the two
    // answers independent (`.claude/rules/console.md`'s parallel sections). A
    // shell that read the section with one read fails here, before the refusal
    // below is read at all -- this is the red step the plan names.
    expect(handles.calls).toEqual(['members.list', 'accounts.inventory'])

    handles.answer(0, [ROW_ADA])
    handles.refuse(1, new Error(REFUSAL))
    await settle()

    // The kind that answered keeps its rows and wears no refusal.
    const answered = kindSection(root, 'members')
    expect(texts(answered, MANAGE_CELL_CLASS)).toEqual(['ada', 'operator'])
    expect(all(answered, MANAGE_REFUSAL_CLASS).length).toBe(0)
    // The kind that refused draws the CLI's own sentence, under its own heading,
    // with its own Retry -- and no rows at all.
    const refused = kindSection(root, 'accounts')
    expect(texts(refused, MANAGE_REFUSAL_CLASS)).toEqual([REFUSAL])
    expect(all(refused, MANAGE_TABLE_CLASS).length).toBe(0)
    expect(all(refused, MANAGE_EMPTY_CLASS).length).toBe(0)
    expect(texts(refused, MANAGE_KIND_TITLE_CLASS)).toEqual(['Accounts'])
    // The refusal names its own read, on the section and on the control.
    expect(refused.getAttribute(MANAGE_OPERATION_ATTRIBUTE)).toBe('accounts.inventory')
    expect(one(refused, RETRY_CLASS).getAttribute(MANAGE_OPERATION_ATTRIBUTE)).toBe('accounts.inventory')
  })

  it('draws the CLI’s sentence when the read refuses in the shape the entry throws', async () => {
    const { root, handles } = mounted([MEMBERS])
    open(root, 'People & access')
    // The shape a refused read really arrives in: the caller's read is
    // `entry.ts`'s bridge, whose readers throw the CLI's reply as a plain
    // `{ sentence }` and not as an `Error` (`readSpend`'s `refusalSentence`
    // reads the same member). A shell that only read `Error.message` would drop
    // the remedy and draw its own sentence instead.
    handles.refuse(0, { sentence: REFUSAL })
    await settle()

    expect(texts(kindSection(root, 'members'), MANAGE_REFUSAL_CLASS)).toEqual([REFUSAL])
  })

  it('redacts a credential a child echoed into its refusal', async () => {
    const { root, handles } = mounted([MEMBERS])
    open(root, 'People & access')
    // The refusal is the one line of this section the CLI writes, so it goes
    // through `sanitizeRefusal` like every sibling screen's: a sentence is not a
    // document, but it is also not the console's own text, and a child that
    // prints the token it failed with must not put it on a screen. The role name
    // `REFUSAL` carries is kept in the same pass (the case above).
    const token = githubToken()
    handles.refuse(0, { sentence: `permission denied for ${token}` })
    await settle()

    const drawn = texts(kindSection(root, 'members'), MANAGE_REFUSAL_CLASS)
    expect(drawn).toEqual([`permission denied for ${TOKEN_PLACEHOLDER}`])
    expect(drawn.join('')).not.toContain(token)
  })

  it('says the console’s own sentence naming the read when the refusal carried nothing readable', async () => {
    const { root, handles } = mounted([MEMBERS])
    open(root, 'People & access')
    // Not an `Error` and no `sentence`, so there is no sentence in it:
    // `String(it)` would draw whatever a hostile object's `toString` returned.
    handles.refuse(0, { not: 'an error' })
    await settle()

    expect(texts(kindSection(root, 'members'), MANAGE_REFUSAL_CLASS)).toEqual([
      'no document arrived for the read `members.list`',
    ])
  })

  it('treats a decode that throws as a refusal of that kind alone', async () => {
    const broken = kindOf({
      id: 'repos',
      group: 'Code',
      source: 'repos.ls',
      decode: () => {
        throw new Error('the fixture kind cannot read this document')
      },
    })
    const { root, handles } = mounted([broken, MEMBERS])
    open(root, 'Code')
    handles.answer(0, {})
    await settle()

    const drawn = kindSection(root, 'repos')
    // The kind's own exception is not the CLI's refusal and is not drawn as one:
    // what the operator is told is the console's sentence about the read.
    expect(texts(drawn, MANAGE_REFUSAL_CLASS)).toEqual(['no document arrived for the read `repos.ls`'])
    expect(all(drawn, RETRY_CLASS).length).toBe(1)
    expect(all(drawn, MANAGE_TABLE_CLASS).length).toBe(0)
  })

  // -------------------------------------------------------------------------
  // The busy mark, the stamp and the Retry.
  // -------------------------------------------------------------------------

  it('puts a busy mark in the kind being read and nowhere else, and keeps the rows a re-read already has', async () => {
    const { root, handles } = mounted([MEMBERS, ACCOUNTS])
    open(root, 'People & access')

    for (const [id, title, source] of [
      ['members', 'Members', 'members.list'],
      ['accounts', 'Accounts', 'accounts.inventory'],
    ] as const) {
      const drawn = kindSection(root, id)
      expect(drawn.getAttribute('data-busy')).toBe('true')
      expect(drawn.getAttribute('aria-busy')).toBe('true')
      // The mark names what is being read, and the section says which read it is.
      expect(texts(drawn, BUSY_LABEL_CLASS)).toEqual([`${MANAGE_BUSY_LEAD} ${title}…`])
      expect(drawn.getAttribute(MANAGE_OPERATION_ATTRIBUTE)).toBe(source)
      one(drawn, BUSY_SLOT_CLASS)
      // A kind that has never been filled holds the shape of what is coming, and
      // the shape names the kind it is for.
      expect(all(drawn, SKELETON_CLASS).length).toBe(1)
      expect(one(drawn, SKELETON_CLASS).getAttribute('data-section')).toBe(`manage-${id}`)
    }

    handles.answer(0, [ROW_ADA])
    await settle()
    const answered = kindSection(root, 'members')
    expect(all(answered, BUSY_CLASS).length).toBe(0)
    expect(answered.getAttribute('data-busy')).toBe(null)
    // The mark of the kind still in flight is untouched by the one that landed:
    // the two sections did not wait for each other.
    expect(all(kindSection(root, 'accounts'), BUSY_CLASS).length).toBe(1)
  })

  it('stamps a kind that landed and reads that kind again when its Refresh is pressed', async () => {
    const { root, handles } = mounted([MEMBERS])
    open(root, 'People & access')
    handles.answer(0, [ROW_ADA])
    await settle()

    const drawn = kindSection(root, 'members')
    expect(texts(drawn, STAMP_AT_CLASS)).toEqual([`${STAMP_LEAD} ${stampOf(AT)}`])
    one(drawn, STAMP_CLASS)
    // A kind that landed wears the stamp and no Retry: R1's control is the door
    // back from a refusal, and a Retry beside a list that is drawn would offer to
    // repeat a read that has already answered.
    expect(all(drawn, RETRY_CLASS).length).toBe(0)
    // Pressing Refresh reads **this** kind and nothing else -- and the rows it
    // already has stay drawn under the mark (dimmed, never blanked), with no
    // second skeleton: this section has been filled.
    one(drawn, STAMP_REFRESH_CLASS).fire('click')
    expect(handles.calls).toEqual(['members.list', 'members.list'])
    expect(all(drawn, MANAGE_TABLE_CLASS).length).toBe(1)
    expect(all(drawn, BUSY_CLASS).length).toBe(1)
    expect(all(drawn, SKELETON_CLASS).length).toBe(0)
    handles.answer(1, [])
    await settle()
    expect(all(drawn, BUSY_CLASS).length).toBe(0)
    expect(all(drawn, MANAGE_TABLE_CLASS).length).toBe(0)
  })

  it('reads a kind again from its Retry, and only from the refusal it belongs to', async () => {
    const { root, handles } = mounted([MEMBERS, ACCOUNTS])
    open(root, 'People & access')
    handles.answer(0, [])
    handles.refuse(1, new Error(REFUSAL))
    await settle()

    one(kindSection(root, 'accounts'), RETRY_CLASS).fire('click')
    expect(handles.calls).toEqual(['members.list', 'accounts.inventory', 'accounts.inventory'])
    handles.answer(2, [])
    await settle()
    // The refusal is gone with the answer, that kind alone re-landed, and its
    // neighbour's read was never asked for again.
    const drawn = kindSection(root, 'accounts')
    expect(all(drawn, MANAGE_REFUSAL_CLASS).length).toBe(0)
    expect(texts(drawn, MANAGE_EMPTY_CLASS)).toEqual([MANAGE_EMPTY_SENTENCE])
    expect(all(drawn, BUSY_CLASS).length).toBe(0)
  })

  it('keeps the newest read’s answer when an older one lands late', async () => {
    const { section, root, handles } = mounted([MEMBERS])
    section.readKind('members')
    section.readKind('members')
    expect(handles.calls).toEqual(['members.list', 'members.list'])

    // The second read answers first and the first lands after it: the older
    // answer must not overwrite the newer one (the sequence token covers each
    // section).
    handles.answer(1, [{ key: 'newer', cells: { person: 'newer', role: 'operator' } }])
    await settle()
    handles.answer(0, [{ key: 'older', cells: { person: 'older', role: 'operator' } }])
    await settle()

    const drawn = kindSection(root, 'members')
    expect(texts(drawn, MANAGE_CELL_CLASS)).toEqual(['newer', 'operator'])
    expect(one(drawn, MANAGE_ROW_CLASS).getAttribute(MANAGE_ROW_ATTRIBUTE)).toBe('newer')
  })

  it('ignores a kind id the section does not hold rather than throwing at its caller', () => {
    const { section, handles } = mounted([MEMBERS])
    section.readKind('nothing-like-this')
    expect(handles.calls).toEqual([])
  })

  // -------------------------------------------------------------------------
  // The actions.
  // -------------------------------------------------------------------------

  it('copies the exact argv of a copy-command, and asks the caller for a view', async () => {
    const { root, handles } = mounted([REPOS])
    open(root, 'Code')
    handles.answer(0, [])
    await settle()

    const command = copyCommandText(
      copyAction([commandWord('fugaro'), commandWord('repos'), commandWord('add'), commandValue('acme/app')]),
    )
    const copy = one(kindSection(root, 'repos'), MANAGE_COPY_CLASS)
    // The label is the command, so what the operator reads is what is copied, and
    // the argv is joined with single spaces and never quoted again.
    expect(command).toBe('fugaro repos add acme/app')
    expect(copy.textContent).toBe(command)
    copy.fire('click')
    expect(handles.copied).toEqual([command])

    one(kindSection(root, 'repos'), MANAGE_VIEW_CLASS).fire('click')
    expect(handles.views).toEqual(['spend'])
  })

  it('draws no control for a kind that offers none, and none at all for a read that refused', async () => {
    const { root, handles } = mounted([MEMBERS, REPOS])
    open(root, 'People & access')
    open(root, 'Code')
    handles.answer(0, [])
    handles.refuse(1, new Error(REFUSAL))
    await settle()

    expect(all(kindSection(root, 'members'), MANAGE_ACTIONS_CLASS).length).toBe(0)
    // Nothing about the list is known, so no control reaches the clipboard: a
    // Copy drawn over a refusal is an offer made on nothing.
    const refused = kindSection(root, 'repos')
    expect(all(refused, MANAGE_ACTIONS_CLASS).length).toBe(0)
    expect(all(refused, MANAGE_COPY_CLASS).length).toBe(0)
    expect(all(refused, MANAGE_VIEW_CLASS).length).toBe(0)
    expect(all(refused, MANAGE_EMPTY_CLASS).length).toBe(0)
    expect(all(refused, MANAGE_REFUSAL_CLASS).length).toBe(1)
  })
})
