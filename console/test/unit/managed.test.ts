// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The managed section: the marks and the audit trail, drawn from
// `managed status --json` (the managed-screens plan's CM5; contract section
// 6.21).
//
// **One read and two blocks.** The plan's interface for this module is one
// section, one read (`managed.status`) and two blocks: **the marks** -- managed
// or not, the read's own reason when it is not, the marker version and the
// generation -- and **the trail** -- the day, its count, and the newest
// record's bounded subset. The trail is a *second block of the same read* and
// not a read of its own (D1488), which is why every case below draws both from
// one model, and why the red step the plan names is "with the module present
// and the trail block removed, the second block's case fails".
//
// **The bound is the command's, and this file is what holds the screen to it**
// (D1476). The record itself carries the argv the write ran as, the rules the
// gate fired and the marks that were in force; none of them reaches the
// document, and the case that pins it feeds an `audit` object holding all three
// and asserts that not one of them is drawn. A screen that drew the record's
// raw document would put a path, a root name or a title an operator did not
// mean to publish on a surface anybody at the machine can read.
//
// **No "revoked"** (D1475). A run-time fact of a write gate is persisted
// nowhere, so a project whose marker or label was taken off is *not managed* --
// a different claim from a mark that was revoked -- and the case below asserts
// the word is in no cell of the drawn tree.
//
// The fake screen is `projects.test.ts`'s, in this file's own words: the
// screens are siblings and each declares its own `ScreenElement`, and it
// **throws** on the attributes a screen may never set rather than recording
// them.

import { describe, expect, it } from 'vitest'

import { UNAVAILABLE_MARK, measured } from '../../src/shared/availability'
import {
  ADDRESS_PLACEHOLDER,
  MAX_TEXT_INPUT_LENGTH,
  TOKEN_PLACEHOLDER,
} from '../../src/shared/sanitize'
import { REFUSAL_UNREADABLE } from '../../src/renderer/screens/degraded'
import {
  BUSY_ATTRIBUTE,
  BUSY_BUSY_ATTRIBUTE,
  BUSY_CLASS,
  BUSY_LABEL_CLASS,
  BUSY_SLOT_CLASS,
  sectionRead,
} from '../../src/renderer/section'
import {
  MANAGED_AT_TERM,
  MANAGED_BUSY,
  MANAGED_CLASS,
  MANAGED_COMMAND_TERM,
  MANAGED_COUNT_TERM,
  MANAGED_DAY_TERM,
  MANAGED_DIGEST_NOTE,
  MANAGED_DIGEST_TERM,
  MANAGED_GENERATION_TERM,
  MANAGED_IDENTITY_TERM,
  MANAGED_MANAGED_WORD,
  MANAGED_MARK_TERM,
  MANAGED_MARKS_CLASS,
  MANAGED_MARKS_LIST_CLASS,
  MANAGED_NOT_MANAGED_WORD,
  MANAGED_REASON_TERM,
  MANAGED_SOURCE_LINE,
  MANAGED_TRAIL_CLASS,
  MANAGED_TRAIL_LIST_CLASS,
  MANAGED_TRAIL_STATE_CLASS,
  MANAGED_TRAIL_TITLE,
  MANAGED_VERDICT_TERM,
  MANAGED_VERSION_TERM,
  MANAGED_WORD_CLASS,
  buildManagedModel,
  fillManaged,
  renderManaged,
  renderManagedFrame,
} from '../../src/renderer/screens/managed'
import type { ScreenElement, ScreenFactory } from '../../src/renderer/screens/managed'
import { githubToken, hexDigest } from '../support/fakeSecrets'

// ---------------------------------------------------------------------------
// The harness: a fake element that refuses what a screen may not do.
// ---------------------------------------------------------------------------

/** FakeScreen is the element a screen draws into. It records text, classes and
 * attributes, and it **throws on the attributes a screen must never set**: the
 * navigating attribute, a resource, a style and any handler. */
class FakeScreen implements ScreenElement {
  readonly attributes = new Map<string, string>()
  readonly nodes: FakeScreen[] = []
  readonly classes: string[] = []
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
    if (FakeScreen.FORBIDDEN_ATTRIBUTES.has(lowered) || lowered.startsWith('on')) {
      throw new Error(`a console screen may not set the ${name} attribute`)
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

  append(child: ScreenElement): void {
    this.nodes.push(child as FakeScreen)
  }

  childNodes(): readonly ScreenElement[] {
    return this.nodes
  }
}

/** factory makes fresh fakes, in the one-member shape every screen declares. */
const factory: ScreenFactory = { create: (tag) => new FakeScreen(tag) }

/** walk visits every node of a tree, the root first. */
function walk(node: ScreenElement): readonly FakeScreen[] {
  const fake = node as FakeScreen
  const out: FakeScreen[] = [fake]
  for (const child of fake.childNodes()) out.push(...walk(child))
  return out
}

/** all is every node carrying a class, in document order. */
function all(node: ScreenElement, className: string): readonly FakeScreen[] {
  return walk(node).filter((one) => one.classes.includes(className))
}

/** one is the single node carrying a class, asserted to be single. */
function one(node: ScreenElement, className: string): FakeScreen {
  const found = all(node, className)
  expect(found, `expected exactly one .${className}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no .${className}`)
  return first
}

/** drawn is the whole tree's text, in document order: what a person reading the
 * screen would read if they read every cell. */
function drawn(node: ScreenElement): string {
  return walk(node)
    .map((leaf) => leaf.textContent ?? '')
    .filter((text) => text !== '')
    .join(' ')
}

/** fact is the value drawn beside one term of a definition list, or null when
 * the list carries no such row. The two blocks are `dl`s of `dt`/`dd` pairs, so
 * a value is found by the word the console gave its row -- which is also the
 * assertion that the row has a term at all: a value with no word for what it is
 * is a figure a reader cannot place. */
function fact(list: ScreenElement, term: string): string | null {
  const cells = (list as FakeScreen).childNodes()
  for (let index = 0; index + 1 < cells.length; index += 2) {
    if (cells[index]?.textContent === term) return cells[index + 1]?.textContent ?? null
  }
  return null
}

/** valueCell is the `dd` node `fact` read the value off, so a test can look at
 * what else the cell carries -- the `title` an explained marker goes in. */
function valueCell(list: ScreenElement, term: string): FakeScreen {
  const cells = (list as FakeScreen).childNodes()
  for (let index = 0; index + 1 < cells.length; index += 2) {
    if (cells[index]?.textContent === term) {
      const value = cells[index + 1]
      if (value === undefined) break
      return value as FakeScreen
    }
  }
  throw new Error(`no row for the term ${term}`)
}

/** clearSection is `entry.ts`'s own, in this file's words: empty a section back
 * to its slot, which is what the paint after a landed read does. `settle`
 * clears the section's busy **state** and leaves the mark element for the
 * caller's refill to remove -- the mark is drawn into a slot the section owns,
 * and the section is emptied and filled in the same frame. */
function clearSection(frame: { readonly root: ScreenElement; readonly slot: ScreenElement }): void {
  const slot = frame.slot as FakeScreen
  slot.nodes.length = 0
  const root = frame.root as FakeScreen
  root.nodes.length = 0
  root.append(frame.slot)
}

// ---------------------------------------------------------------------------
// The documents.
// ---------------------------------------------------------------------------

const DAY = '2026-11-07'

/**
 * RECORD is the bounded subset the document carries **plus the members it does
 * not**: `argv`, `rules` and `marker_version` are what the record itself holds
 * (F12) and what section 6.21's bound keeps off the wire. They are in this
 * object on purpose, because the console's own decoder is what must ignore
 * them: a screen handed a document wider than the contract draws the five
 * members it knows and nothing else.
 */
const RECORD = {
  at: '2026-11-07T09:15:00Z',
  command: 'fugaro config publish',
  verdict: 'refused',
  identity: 'deployer@example.test',
  plan_digest: hexDigest(),
  argv: ['fugaro', 'config', 'publish', '--title', 'a-title-nobody-meant-to-publish'],
  rules: ['managed-marker-agrees'],
  marker_version: 3,
}

/** MANAGED is `managed status --json` on a managed project with a record. */
const MANAGED = {
  project: 'example-project',
  gcp_project: 'example-cloud',
  managed: true,
  reason: '',
  version: 3,
  generation: '17',
  audit: RECORD,
  audit_day: DAY,
  audit_count: 4,
  audit_state: '',
}

/** UNMANAGED is the same read on a project whose marks do not agree: the read's
 * own reason, and **no trail at all** -- `audit` null, the day and the count
 * empty, the state empty. A project that is not managed files no record
 * (D1025), so there is no trail to read. */
const UNMANAGED = {
  project: 'example-project',
  gcp_project: 'example-cloud',
  managed: false,
  reason: 'the project has no creation fact in the runs bucket',
  version: 0,
  generation: '',
  audit: null,
  audit_day: '',
  audit_count: 0,
  audit_state: '',
}

// ---------------------------------------------------------------------------

describe('buildManagedModel', () => {
  it('reads the marks’ four fields', () => {
    const model = buildManagedModel({ raw: MANAGED })
    expect(model.state).toBe('read')
    expect(model.because).toBeNull()
    expect(model.marks).toEqual({
      managed: true,
      // The document's `reason` is `""` exactly when the marks agree, and an
      // empty sentence is no reason: it is read as absent rather than drawn as
      // an empty cell.
      reason: null,
      version: measured(3),
      generation: '17',
    })

    const root = renderManaged(factory, model)
    const list = one(one(root, MANAGED_MARKS_CLASS), MANAGED_MARKS_LIST_CLASS)
    expect(fact(list, MANAGED_MARK_TERM)).toBe(MANAGED_MANAGED_WORD)
    expect(one(root, MANAGED_WORD_CLASS).textContent).toBe(MANAGED_MANAGED_WORD)
    expect(fact(list, MANAGED_VERSION_TERM)).toBe('3')
    expect(fact(list, MANAGED_GENERATION_TERM)).toBe('17')
    // No reason row at all, rather than a row with an empty value.
    expect(fact(list, MANAGED_REASON_TERM)).toBeNull()
  })

  it('draws the reason and no trail when the project is not managed', () => {
    const model = buildManagedModel({ raw: UNMANAGED })
    expect(model.state).toBe('read')
    expect(model.marks?.managed).toBe(false)
    expect(model.marks?.reason).toBe('the project has no creation fact in the runs bucket')
    // The whole of the trail is withheld: the document's four trail members are
    // null, `""`, `0` and `""`, and none of them is a fact about this project.
    expect(model.trail).toBeNull()

    const root = renderManaged(factory, model)
    const list = one(one(root, MANAGED_MARKS_CLASS), MANAGED_MARKS_LIST_CLASS)
    expect(fact(list, MANAGED_MARK_TERM)).toBe(MANAGED_NOT_MANAGED_WORD)
    expect(fact(list, MANAGED_REASON_TERM)).toBe('the project has no creation fact in the runs bucket')
    expect(all(root, MANAGED_TRAIL_CLASS)).toHaveLength(0)
    // The version is a fact about the project -- an unmanaged one has no
    // creation fact -- and it is drawn as the `0` the document carries rather
    // than withheld: `0` here is not an unknown about the read (D1474).
    expect(fact(list, MANAGED_VERSION_TERM)).toBe('0')
    // "Revoked" is a state the CLI does not have and persists nowhere, so it is
    // in no cell of any state this screen draws (D1475).
    expect(drawn(root)).not.toContain('revoked')
  })

  it('reads the trail’s bounded fields', () => {
    const model = buildManagedModel({ raw: MANAGED })
    expect(model.trail?.day).toBe(DAY)
    expect(model.trail?.count).toEqual(measured(4))
    expect(model.trail?.state).toBeNull()
    expect(model.trail?.record).toEqual({
      at: RECORD.at,
      command: RECORD.command,
      verdict: RECORD.verdict,
      identity: RECORD.identity,
      planDigest: RECORD.plan_digest,
    })

    const root = renderManaged(factory, model)
    const trail = one(root, MANAGED_TRAIL_CLASS)
    expect(drawn(root)).toContain(MANAGED_TRAIL_TITLE)
    const list = one(trail, MANAGED_TRAIL_LIST_CLASS)
    expect(fact(list, MANAGED_DAY_TERM)).toBe(DAY)
    expect(fact(list, MANAGED_COUNT_TERM)).toBe('4')
    // The record's five members, each drawn through the class its key declares.
    // `identity` is a person -- the recorder's own class for the account a write
    // ran as -- so an address loses its local part.
    expect(fact(list, MANAGED_AT_TERM)).toBe(RECORD.at)
    expect(fact(list, MANAGED_COMMAND_TERM)).toBe(RECORD.command)
    expect(fact(list, MANAGED_VERDICT_TERM)).toBe(RECORD.verdict)
    expect(fact(list, MANAGED_IDENTITY_TERM)).toBe(ADDRESS_PLACEHOLDER)
    // `plan_digest` is a digest, and the free-text rules take a long hex run as
    // a credential -- which is the marker the recorder writes for the same
    // value, so the two layers agree about what a digest is worth drawing. The
    // console has no class that keeps a digest, so the cell explains itself in
    // its `title` instead of drawing the value (this is the half review found
    // missing: a marker with no reason drawn anywhere).
    expect(fact(list, MANAGED_DIGEST_TERM)).toBe(TOKEN_PLACEHOLDER)
    expect(valueCell(list, MANAGED_DIGEST_TERM).getAttribute('title')).toBe(MANAGED_DIGEST_NOTE)
    expect(drawn(root)).not.toContain(RECORD.plan_digest)

    // The note explains a marker, so it is drawn only when the marker is what
    // the cell shows. `putText`'s ok path keeps a value the free-text rules do
    // not read as a credential, and the cell then draws that value as itself
    // with no note: a cell claiming a redaction it did not make is worse than an
    // unexplained one.
    for (const [what, planDigest] of [
      ['a short run the rules keep', 'abc123'],
      ['the empty string a document with no digest spells', ''],
    ] as const) {
      const kept = buildManagedModel({
        raw: { ...MANAGED, audit: { ...RECORD, plan_digest: planDigest } },
      })
      const keptList = one(one(renderManaged(factory, kept), MANAGED_TRAIL_CLASS), MANAGED_TRAIL_LIST_CLASS)
      expect(fact(keptList, MANAGED_DIGEST_TERM), what).toBe(planDigest)
      expect(valueCell(keptList, MANAGED_DIGEST_TERM).getAttribute('title'), what).toBeNull()
    }

    // And a document that carried no digest at all draws `putText`'s own mark,
    // whose own reason stands in the attribute instead.
    const missing = buildManagedModel({
      raw: { ...MANAGED, audit: { ...RECORD, plan_digest: null } },
    })
    const missingList = one(one(renderManaged(factory, missing), MANAGED_TRAIL_CLASS), MANAGED_TRAIL_LIST_CLASS)
    expect(fact(missingList, MANAGED_DIGEST_TERM)).toBe(UNAVAILABLE_MARK)
    expect(valueCell(missingList, MANAGED_DIGEST_TERM).getAttribute('title')).not.toBe(MANAGED_DIGEST_NOTE)
  })

  it('never draws the record’s argv', () => {
    const model = buildManagedModel({ raw: MANAGED })
    const text = drawn(renderManaged(factory, model))
    for (const word of [
      'argv',
      'a-title-nobody-meant-to-publish',
      'managed-marker-agrees',
      'rules',
      'marker_version',
    ]) {
      expect(text, word).not.toContain(word)
    }
    // The bound is the five members the contract names and no others: the
    // record's own keys are read one by one, so a document wider than section
    // 6.21 contributes nothing to the screen.
    expect(model.trail?.record).toEqual({
      at: RECORD.at,
      command: RECORD.command,
      verdict: RECORD.verdict,
      identity: RECORD.identity,
      planDigest: RECORD.plan_digest,
    })
  })

  it('refuses on its own read and names its own command', () => {
    const model = buildManagedModel({ raw: null })
    expect(model.state).toBe('unavailable')
    expect(model.marks).toBeNull()
    expect(model.trail).toBeNull()

    const root = renderManaged(factory, model)
    // The section's own refusal names the command it made, so an operator
    // reading a refusal knows which read did not answer.
    expect(drawn(root)).toContain(MANAGED_SOURCE_LINE)
    expect(drawn(root)).toContain('no document arrived for')
    expect(all(root, MANAGED_MARKS_CLASS)).toHaveLength(0)
    expect(all(root, MANAGED_TRAIL_CLASS)).toHaveLength(0)

    // A refusal the CLI printed is drawn as the CLI wrote it -- through
    // `sanitizeRefusal`, so a role name survives and a token does not -- and
    // the console's own sentence stands in only when the child printed none.
    const refused = buildManagedModel({
      raw: null,
      because: 'the read needs roles/datastore.viewer on the project',
    })
    expect(drawn(renderManaged(factory, refused))).toContain('roles/datastore.viewer')
    expect(drawn(renderManaged(factory, refused))).not.toContain('no document arrived for')
  })

  it('puts the CLI’s refusal through the refusal door, both ways', () => {
    // `drawnRefusal` is the section's one path where text a child wrote is
    // drawn without `putText`, so both halves of the door are pinned here and
    // not just the one that keeps the remedy (`projects.test.ts` has the same
    // pair, from review round 5 of PR 478: the two screens drew one channel two
    // ways). The first half is the redaction -- a token a child echoed into its
    // stderr never reaches the Project tab -- and reducing the door to
    // `return because` must fail this case and not only the model shape.
    const token = githubToken()
    const echoed = buildManagedModel({
      raw: null,
      because: `the read was refused: ${token} is not accepted`,
    })
    const text = drawn(renderManaged(factory, echoed))
    expect(text).not.toContain(token)
    expect(text).toContain(TOKEN_PLACEHOLDER)

    // The second half is the fallback: a sentence `sanitizeRefusal` refuses --
    // here one longer than the console reads -- is the console's own sentence
    // naming what happened, never the raw text and never an empty section.
    const tooLong = buildManagedModel({
      raw: null,
      because: 'x'.repeat(MAX_TEXT_INPUT_LENGTH + 1),
    })
    expect(drawn(renderManaged(factory, tooLong))).toContain(REFUSAL_UNREADABLE)
  })

  it('withholds a document whose marks are not a boolean', () => {
    // Types are strict and a member of another type is never coerced: a
    // `managed` spelled `"true"` would decide the whole section against a
    // reading of the document the CLI never made.
    for (const managed of ['true', 1, null]) {
      const model = buildManagedModel({ raw: { ...MANAGED, managed } })
      expect(model.state, String(managed)).toBe('unavailable')
      expect(model.marks).toBeNull()
      expect(model.trail).toBeNull()
    }
  })

  it('draws the console’s own mark for a number the document does not carry', () => {
    // The version is a reading like every other number: a document that leaves
    // it out draws the console's own mark, never a `0` a reader would take for
    // a version.
    const model = buildManagedModel({
      raw: { ...MANAGED, version: undefined, audit_count: undefined },
    })
    const root = renderManaged(factory, model)
    const marks = one(one(root, MANAGED_MARKS_CLASS), MANAGED_MARKS_LIST_CLASS)
    expect(fact(marks, MANAGED_VERSION_TERM)).toBe(UNAVAILABLE_MARK)
    const trail = one(one(root, MANAGED_TRAIL_CLASS), MANAGED_TRAIL_LIST_CLASS)
    expect(fact(trail, MANAGED_COUNT_TERM)).toBe(UNAVAILABLE_MARK)
  })

  it('draws the trail’s unread state as a sentence and no figure beside it', () => {
    // A listing that failed is an unknown, and an unknown drawn as
    // `audit_count: 0` is the silent pass: "nothing was recorded" and "the
    // records could not be read" look identical in a count (section 6.21). The
    // state is drawn in its own cell, and the day and the record the document
    // does not carry are not drawn at all.
    const model = buildManagedModel({
      raw: {
        ...MANAGED,
        audit: null,
        audit_day: '',
        audit_count: 0,
        audit_state: 'the audit listing was refused: the grant is missing',
      },
    })
    expect(model.trail?.state).toBe('the audit listing was refused: the grant is missing')
    expect(model.trail?.record).toBeNull()

    const root = renderManaged(factory, model)
    const trail = one(root, MANAGED_TRAIL_CLASS)
    expect(one(trail, MANAGED_TRAIL_STATE_CLASS).textContent).toBe(
      'the audit listing was refused: the grant is missing',
    )
    const list = one(trail, MANAGED_TRAIL_LIST_CLASS)
    expect(fact(list, MANAGED_DAY_TERM)).toBeNull()
    expect(fact(list, MANAGED_AT_TERM)).toBeNull()
    // The count is not drawn beside the state: a figure beside a state is read
    // as an answer, and the state is that there is none.
    expect(fact(list, MANAGED_COUNT_TERM)).toBeNull()
  })
})

describe('the managed section’s frame', () => {
  it('keeps its busy state to its own section', () => {
    const read = sectionRead({ factory, theme: 'light' })
    const managed = renderManagedFrame(factory)
    expect((managed.root as FakeScreen).classes).toContain(MANAGED_CLASS)
    // The slot is the section's **first** child, so the mark a control change
    // puts up in the same frame sits at the top of this section rather than
    // under a block nobody has scrolled to.
    expect(managed.root.childNodes()[0]).toBe(managed.slot)
    expect((managed.slot as FakeScreen).classes).toContain(BUSY_SLOT_CLASS)

    // A sibling section, drawn the way the tab draws the others: its own host,
    // its own slot and figures already on screen.
    const other = factory.create('div')
    other.setAttribute('class', 'projects-settings')
    const otherSlot = factory.create('div')
    otherSlot.setAttribute('class', BUSY_SLOT_CLASS)
    other.append(otherSlot)
    const otherFigures = factory.create('section')
    otherFigures.textContent = 'the settings section’s own figures'
    other.append(otherFigures)

    fillManaged(factory, buildManagedModel({ raw: MANAGED }), managed.root)

    const token = read.start()
    read.mark(token, managed.root, managed.slot, MANAGED_BUSY)

    // The section that is being read says so, in the frame it happened in...
    expect(managed.root.getAttribute(BUSY_ATTRIBUTE)).toBe('true')
    expect(managed.root.getAttribute(BUSY_BUSY_ATTRIBUTE)).toBe('true')
    // ...and no other section wears the claim: one read's busy state is its own
    // section's, which is the owner's rule of 2026-10-31 and the reason the tab
    // has one host per read.
    expect(other.getAttribute(BUSY_ATTRIBUTE)).toBeNull()
    expect(other.getAttribute(BUSY_BUSY_ATTRIBUTE)).toBeNull()
    expect(all(other, BUSY_CLASS)).toHaveLength(0)
    expect(drawn(other)).toBe('the settings section’s own figures')
    // The mark goes in this section's own slot, names the read it is making,
    // and the figures this section already drew are kept under it.
    const mark = one(managed.slot, BUSY_CLASS)
    expect(one(mark, BUSY_LABEL_CLASS).textContent).toBe(MANAGED_BUSY)
    expect(drawn(managed.root)).toContain(MANAGED_TRAIL_TITLE)

    expect(read.settle(token)).toBe(true)
    expect(managed.root.getAttribute(BUSY_ATTRIBUTE)).toBeNull()
    expect(managed.root.getAttribute(BUSY_BUSY_ATTRIBUTE)).toBeNull()
    // And the section the read landed for is refilled in the same frame: the
    // mark element goes with the empty, and the figures come back.
    clearSection(managed)
    fillManaged(factory, buildManagedModel({ raw: MANAGED }), managed.root)
    expect(all(managed.slot, BUSY_CLASS)).toHaveLength(0)
    expect(drawn(managed.root)).toContain(MANAGED_TRAIL_TITLE)
  })
})
