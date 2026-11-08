// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The retirements section: what the last plain `fugaro update` retired, drawn
// from the read that carries it (the managed-screens plan's CM8; F21, D1484,
// D1485).
//
// **One section, one read, and the read is settled here.** The plan left the
// choice to this task -- the record "through the operation CM4 added for the
// marks (`managed.status` carries it) or a row of its own if CM1's record is
// its own document" -- and the module's own header records the settlement and
// its three reasons. The case below that names the read is
// `refuses a document that carries no last-update record at all`: whatever the
// settlement, the section owes the operator the command it read from, and it
// refuses rather than drawing "no update recorded yet" for a document that does
// not carry the record at all (the two are different claims, and only one of
// them is true of a machine that has run an update).
//
// **The two empty states are two sentences** (CM1): `retire: []` is "the last
// update retired nothing", which is a fact about a run that happened, and a
// record with no entry for this project is "no update recorded yet", which is a
// fact about a machine. Merging them is the mutation the plan names, and the
// case below is the one it fails.
//
// **A retirement carries names and no values** (D1485): the Secret Manager ID
// is the secret's *name*, and the record carries no secret value and no
// Terraform plan. The naming case and the plan case below are what hold that:
// the first asserts the name is drawn as a name, and the second feeds the
// section a document wider than the record -- a `plan` member and a row's `job`
// and `region` -- and asserts none of it reaches a cell.
//
// The fake screen is `projects.test.ts`'s, in this file's own words: the
// screens are siblings and each declares its own `ScreenElement`, and it
// **throws** on the attributes a screen may never set rather than recording
// them.

import { describe, expect, it } from 'vitest'

import { UNAVAILABLE_MARK, measured, unavailable } from '../../src/shared/availability'
import { MAX_TEXT_INPUT_LENGTH, TOKEN_PLACEHOLDER } from '../../src/shared/sanitize'
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
  RETIREMENTS_ABSENT_WORD,
  RETIREMENTS_ADDRESS_TERM,
  RETIREMENTS_AT_TERM,
  RETIREMENTS_BUSY,
  RETIREMENTS_CAUSE_TERM,
  RETIREMENTS_CLASS,
  RETIREMENTS_INFLIGHT_TERM,
  RETIREMENTS_KEPT_TERM,
  RETIREMENTS_KIND_TERM,
  RETIREMENTS_LOGICAL_TERM,
  RETIREMENTS_NONE_SENTENCE,
  RETIREMENTS_NO_RECORD_SENTENCE,
  RETIREMENTS_OPERATION,
  RETIREMENTS_PROTECTED_TERM,
  RETIREMENTS_PROTECTED_WORD,
  RETIREMENTS_RECORD_VERSION,
  RETIREMENTS_RECORD_CLASS,
  RETIREMENTS_RECORD_LIST_CLASS,
  RETIREMENTS_ROW_CLASS,
  RETIREMENTS_ROW_FACTS_CLASS,
  RETIREMENTS_SECRET_NOTE,
  RETIREMENTS_SECRET_TERM,
  RETIREMENTS_SET_LIST_CLASS,
  RETIREMENTS_SET_WORD,
  RETIREMENTS_SOURCE_LINE,
  RETIREMENTS_STATE_CLASS,
  RETIREMENTS_TITLE,
  RETIREMENTS_UNPROTECTED_WORD,
  RETIREMENTS_VERSION_WORD,
  buildRetirementsModel,
  fillRetirements,
  renderRetirements,
  renderRetirementsFrame,
} from '../../src/renderer/screens/retirements'
import type { ScreenElement, ScreenFactory } from '../../src/renderer/screens/retirements'
import { githubToken } from '../support/fakeSecrets'

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

/** rows is every row of the set, in document order. */
function rows(node: ScreenElement): readonly ScreenElement[] {
  return all(node, RETIREMENTS_ROW_CLASS)
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
 * the list carries no such row. Every block is a `dl` of `dt`/`dd` pairs, so a
 * value is found by the word the console gave its row -- which is also the
 * assertion that the row has a term at all: a value with no word for what it is
 * is a figure a reader cannot place. */
function fact(list: ScreenElement, term: string): string | null {
  const cells = (list as FakeScreen).childNodes()
  for (let index = 0; index + 1 < cells.length; index += 2) {
    if (cells[index]?.textContent === term) return cells[index + 1]?.textContent ?? null
  }
  return null
}

/** valueCell is the `dd` node `fact` read the value off, so a case can look at
 * what else the cell carries -- the `title` an explained name goes in. */
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
 * to its slot, which is what the paint after a landed read does. */
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

const AT = '2026-11-08T09:15:00Z'
const SECRET_NAME = 'fugaro-example-cloud-gh-token'

/**
 * ROW is one retired address as CM1's record holds it, **plus the members this
 * section draws none of**: `job` and `region` are the fields the state-removal
 * steps need (F21's own comment on `infra.Retirement`), and D1485's list of
 * what a screen draws is closed at the address, the kind, the cause and the
 * `kept`/`logical`/`protected` flags. They are in this object on purpose,
 * because the section's own reader is what must ignore them.
 */
const ROW = {
  address: 'module.repo.google_secret_manager_secret.gh_token',
  kind: 'secret container',
  cause: 'the repository no longer mounts a GitHub token',
  kept: 'fugaro never deletes a secret or a secret version: this address leaves Terraform’s state',
  logical: 'github-token',
  secret_id: SECRET_NAME,
  protected: true,
  job: 'fugaro-check-example',
  region: 'europe-west1',
}

/** JOB is the second row: no secret, no promise kept, and not protected -- so
 * every optional row of the first is absent here and every flag reads the other
 * way. */
const JOB = {
  address: 'module.repo.google_cloud_run_v2_job.check',
  kind: 'check job',
  cause: 'the repository configures no check job',
  protected: false,
}

/**
 * RECORD is the last update's own document as CM1 writes it, **plus a `plan`
 * member it does not write**: D1485 refuses the Terraform plan by name, and a
 * section that spread the record into its model would draw one. The plan is in
 * this object on purpose, and the case below asserts no cell carries any of it.
 */
const RECORD = {
  version: RETIREMENTS_RECORD_VERSION,
  at: AT,
  retire: [ROW, JOB],
  runs_in_flight: 3,
  plan: {
    terraform: 'a Terraform plan is not this screen’s to draw',
    resource_changes: ['3 to destroy'],
  },
}

/** MANAGED is the read's document: the marks' own members (which this section
 * draws none of) and the record. */
const MANAGED = {
  project: 'example-project',
  gcp_project: 'example-cloud',
  managed: true,
  reason: '',
  version: 3,
  generation: '17',
  audit: null,
  audit_day: '',
  audit_count: 0,
  audit_state: '',
  retirements: RECORD,
}

/** EMPTY is a record that exists and retired nothing: a run happened, and its
 * set is `[]` (CM1 writes the empty set rather than leaving the member out). */
const EMPTY = { ...MANAGED, retirements: { ...RECORD, retire: [], runs_in_flight: 0 } }

/** NO_RECORD is the same read on a machine that has run no update: the record
 * holds no entry for this project, so the member is null. */
const NO_RECORD = { ...MANAGED, retirements: null }

/** UNREAD_STATE is the CLI's own sentence for a record that is there and could
 * not be read (`retirements_state`, contract section 6.21): the file is state a
 * newer build may have written in a shape this one would misread. It is not the
 * section's sentence but the read's. */
const UNREAD_STATE =
  'could not read the last update’s record at /home/operator/.config/fugaro/state/update.json: update record is from another version of fugaro'

/** UNREAD is that read's document: no entry, and the sentence saying why. */
const UNREAD = { ...MANAGED, retirements: null, retirements_state: UNREAD_STATE }

// ---------------------------------------------------------------------------

describe('buildRetirementsModel', () => {
  it('draws the last update’s set, one row per address, with its flags', () => {
    const model = buildRetirementsModel({ raw: MANAGED })
    expect(model.state).toBe('read')
    expect(model.because).toBeNull()
    expect(model.record?.at).toBe(AT)
    expect(model.record?.runsInFlight).toEqual(measured(3))
    expect(model.record?.rows).toHaveLength(2)
    expect(model.record?.rows[0]).toEqual({
      address: ROW.address,
      kind: ROW.kind,
      cause: ROW.cause,
      kept: ROW.kept,
      logical: ROW.logical,
      secretName: ROW.secret_id,
      protected: true,
    })

    const root = renderRetirements(factory, model)
    // The section names the read it made, on every state it draws.
    expect(drawn(root)).toContain(RETIREMENTS_SOURCE_LINE)
    // The panel names the operation it is about, so a reader of the markup can
    // see which read drew it.
    expect(one(root, 'projects-panel').getAttribute('data-operation')).toBe(RETIREMENTS_OPERATION)

    const block = one(root, RETIREMENTS_RECORD_CLASS)
    const facts = one(block, RETIREMENTS_RECORD_LIST_CLASS)
    expect(fact(facts, RETIREMENTS_AT_TERM)).toBe(AT)
    expect(fact(facts, RETIREMENTS_INFLIGHT_TERM)).toBe('3')

    const list = one(root, RETIREMENTS_SET_LIST_CLASS)
    const drawnRows = rows(list)
    expect(drawnRows).toHaveLength(2)
    const first = one(drawnRows[0] as ScreenElement, RETIREMENTS_ROW_FACTS_CLASS)
    expect(fact(first, RETIREMENTS_ADDRESS_TERM)).toBe(ROW.address)
    expect(fact(first, RETIREMENTS_KIND_TERM)).toBe(ROW.kind)
    expect(fact(first, RETIREMENTS_CAUSE_TERM)).toBe(ROW.cause)
    expect(fact(first, RETIREMENTS_KEPT_TERM)).toBe(ROW.kept)
    expect(fact(first, RETIREMENTS_LOGICAL_TERM)).toBe(ROW.logical)
    expect(fact(first, RETIREMENTS_PROTECTED_TERM)).toBe(RETIREMENTS_PROTECTED_WORD)

    // The second row carries no promise and no secret, so those rows are absent
    // rather than empty, and its flag reads the other way.
    const second = one(drawnRows[1] as ScreenElement, RETIREMENTS_ROW_FACTS_CLASS)
    expect(fact(second, RETIREMENTS_ADDRESS_TERM)).toBe(JOB.address)
    expect(fact(second, RETIREMENTS_KEPT_TERM)).toBeNull()
    expect(fact(second, RETIREMENTS_LOGICAL_TERM)).toBeNull()
    expect(fact(second, RETIREMENTS_SECRET_TERM)).toBeNull()
    expect(fact(second, RETIREMENTS_PROTECTED_TERM)).toBe(RETIREMENTS_UNPROTECTED_WORD)

    // The update itself is drawn once, over the whole set, and never per row.
    expect(all(root, RETIREMENTS_RECORD_CLASS)).toHaveLength(1)
    expect(all(root, RETIREMENTS_STATE_CLASS)).toHaveLength(0)
  })

  it('distinguishes “nothing retired by the last update” from “no update recorded yet”', () => {
    // A record that exists with an empty set: the run happened, and it retired
    // nothing. The time it settled and the runs it walked past are facts about
    // that run, so they are drawn beside the sentence.
    const empty = buildRetirementsModel({ raw: EMPTY })
    expect(empty.state).toBe('read')
    expect(empty.record?.rows).toHaveLength(0)
    const emptyRoot = renderRetirements(factory, empty)
    const emptyBlock = one(emptyRoot, RETIREMENTS_RECORD_CLASS)
    expect(one(emptyBlock, RETIREMENTS_STATE_CLASS).textContent).toBe(RETIREMENTS_NONE_SENTENCE)
    const emptyFacts = one(emptyBlock, RETIREMENTS_RECORD_LIST_CLASS)
    expect(fact(emptyFacts, RETIREMENTS_AT_TERM)).toBe(AT)
    expect(fact(emptyFacts, RETIREMENTS_INFLIGHT_TERM)).toBe('0')
    // No set is drawn at all: an empty list would read as a set that was not
    // drawn, and the sentence above is the answer.
    expect(all(emptyRoot, RETIREMENTS_SET_LIST_CLASS)).toHaveLength(0)
    expect(all(emptyRoot, RETIREMENTS_ROW_CLASS)).toHaveLength(0)

    // No record at all: a machine fact, not a run's, and a different sentence.
    const none = buildRetirementsModel({ raw: NO_RECORD })
    expect(none.state).toBe('read')
    expect(none.record).toBeNull()
    const noneRoot = renderRetirements(factory, none)
    expect(drawn(noneRoot)).toContain(RETIREMENTS_NO_RECORD_SENTENCE)
    expect(drawn(noneRoot)).not.toContain(RETIREMENTS_NONE_SENTENCE)
    expect(all(noneRoot, RETIREMENTS_RECORD_CLASS)).toHaveLength(0)
    expect(all(noneRoot, RETIREMENTS_SET_LIST_CLASS)).toHaveLength(0)
    // And the section still names its read, so a person can run the command
    // that would answer.
    expect(drawn(noneRoot)).toContain(RETIREMENTS_SOURCE_LINE)
  })

  it('never draws a zero for a run that was not counted', () => {
    // `runs_in_flight` is absent when the number is not known -- a project with
    // no budget backend, or a read that did not answer -- and absent is never
    // zero: D1483 refuses to draw a smaller number than the runs that were
    // really there.
    const model = buildRetirementsModel({ raw: { ...MANAGED, retirements: { ...RECORD, runs_in_flight: undefined } } })
    expect(model.record?.runsInFlight).toEqual(unavailable('the document has no runs_in_flight'))
    const block = one(renderRetirements(factory, model), RETIREMENTS_RECORD_CLASS)
    const facts = one(block, RETIREMENTS_RECORD_LIST_CLASS)
    expect(fact(facts, RETIREMENTS_INFLIGHT_TERM)).toBe(UNAVAILABLE_MARK)
  })

  it('refuses a record of a version this console’s CLI does not write', () => {
    // The record is state: a newer fugaro may have written a shape this build
    // would misread, and an older one is not the document its reader knows.
    // Both are refused, and the sentence names the two versions rather than a
    // row set this build guessed at. The two compound values are here because
    // the member is a document's and the sentence is the console's: an object
    // or an array is named as what it is rather than quoted back, so no
    // document can size that cell.
    for (const version of [0, 2, '1', null, { version: 2 }, ['2']]) {
      const model = buildRetirementsModel({
        raw: { ...MANAGED, retirements: { ...RECORD, version } },
      })
      expect(model.state, String(version)).toBe('unavailable')
      expect(model.record, String(version)).toBeNull()
      const root = renderRetirements(factory, model)
      expect(drawn(root)).toContain(RETIREMENTS_VERSION_WORD)
      // The refusal names what it read and never the record's rows, and it is
      // a sentence rather than a transcript of the document.
      expect((model.because ?? '').length).toBeLessThan(200)
      // The rows are not drawn at all: a version this build does not read is
      // not a set it half-draws.
      expect(all(root, RETIREMENTS_ROW_CLASS)).toHaveLength(0)
      expect(drawn(root)).not.toContain(ROW.address)
    }
  })

  it('refuses the whole set when one of its rows is not a retirement', () => {
    // The set is read whole or not at all. A reader that skipped a row it could
    // not make sense of would draw a list that reads as **smaller than it is**,
    // and a retirement list is exactly the place where a short list is a false
    // statement about what left the state -- so one unreadable member refuses
    // the whole set, and the good rows that came before it are not drawn
    // either. The mutation this case fails is `rowsOf` skipping the bad row and
    // drawing the rest.
    for (const bad of [7, 'a string', null, true, ['address'], undefined]) {
      const model = buildRetirementsModel({ raw: { ...MANAGED, retirements: { ...RECORD, retire: [ROW, bad] } } })
      expect(model.state, JSON.stringify(bad)).toBe('unavailable')
      expect(model.record, JSON.stringify(bad)).toBeNull()
      const root = renderRetirements(factory, model)
      expect(all(root, RETIREMENTS_SET_LIST_CLASS), JSON.stringify(bad)).toHaveLength(0)
      expect(all(root, RETIREMENTS_ROW_CLASS), JSON.stringify(bad)).toHaveLength(0)
      expect(drawn(root), JSON.stringify(bad)).not.toContain(ROW.address)
      expect(drawn(root), JSON.stringify(bad)).toContain(RETIREMENTS_SET_WORD)
    }

    // The control, so a pass here means the guard is about the bad row and not
    // about the set: the same record with every row readable draws both.
    const ok = buildRetirementsModel({ raw: MANAGED })
    expect(ok.state).toBe('read')
    expect(ok.record?.rows).toHaveLength(2)
  })

  it('refuses a document that carries no last-update record at all', () => {
    // A document with no `retirements` member is not "no update recorded yet":
    // it is a read that does not answer the question at all (an installation
    // whose CLI does not print the record), and the two must not be drawn as
    // one another -- the false one is a machine that has run an update being
    // told it has not.
    const model = buildRetirementsModel({ raw: { ...MANAGED, retirements: undefined } })
    expect(model.state).toBe('unavailable')
    expect(model.record).toBeNull()
    const root = renderRetirements(factory, model)
    expect(drawn(root)).toContain(RETIREMENTS_ABSENT_WORD)
    expect(drawn(root)).toContain(RETIREMENTS_SOURCE_LINE)
    expect(drawn(root)).not.toContain(RETIREMENTS_NO_RECORD_SENTENCE)

    // The same refusal for a document that never arrived, and for a `retire`
    // member of a shape CM1 does not write.
    for (const raw of [null, 'a string', { ...MANAGED, retirements: 7 }, { ...MANAGED, retirements: { ...RECORD, retire: undefined } }]) {
      const refused = buildRetirementsModel({ raw })
      expect(refused.state, String(raw)).toBe('unavailable')
      expect(refused.record, String(raw)).toBeNull()
      expect(drawn(renderRetirements(factory, refused))).toContain(RETIREMENTS_SOURCE_LINE)
    }
  })

  it('draws the read’s own sentence for a record it could not read', () => {
    // `retirements_state` is the CLI's answer for a record that is there and
    // could not be read -- corrupt, over its bound, not a regular file, or of a
    // version this build does not write. It is a THIRD answer, and the one it
    // must never be drawn as is "no update recorded yet": that is a claim about
    // the machine, and this read refused the file rather than learning anything
    // about it. The sentence is the read's, so it is drawn as it stands.
    const model = buildRetirementsModel({ raw: UNREAD })
    expect(model.state).toBe('unavailable')
    expect(model.record).toBeNull()
    const root = renderRetirements(factory, model)
    expect(model.because).toBe(UNREAD_STATE)
    expect(drawn(root)).toContain(UNREAD_STATE)
    expect(drawn(root)).not.toContain(RETIREMENTS_NO_RECORD_SENTENCE)
    expect(drawn(root)).not.toContain(RETIREMENTS_NONE_SENTENCE)
    expect(all(root, RETIREMENTS_RECORD_CLASS)).toHaveLength(0)
    expect(all(root, RETIREMENTS_ROW_CLASS)).toHaveLength(0)
    // Every state of the panel names the read it is about, this one included.
    expect(drawn(root)).toContain(RETIREMENTS_SOURCE_LINE)

    // The two members are exclusive on the wire -- a state is written only
    // where there is no entry to show -- and a document carrying both is read
    // as the state: an entry drawn beside "could not read the record" would be
    // a row from a document the read refused.
    expect(buildRetirementsModel({ raw: { ...UNREAD, retirements: RECORD } }).state).toBe('unavailable')

    // The sentence is a document's, so it goes through the refusal door: a
    // token a child echoed is redacted, and a sentence the sanitizer refuses
    // whole is the console's own rather than a blank section.
    const token = githubToken()
    const echoed = buildRetirementsModel({ raw: { ...UNREAD, retirements_state: `could not read the record: ${token} is not accepted` } })
    expect(drawn(renderRetirements(factory, echoed))).not.toContain(token)
    expect(drawn(renderRetirements(factory, echoed))).toContain(TOKEN_PLACEHOLDER)
    const tooLong = buildRetirementsModel({ raw: { ...UNREAD, retirements_state: 'x'.repeat(MAX_TEXT_INPUT_LENGTH + 1) } })
    expect(drawn(renderRetirements(factory, tooLong))).toContain(REFUSAL_UNREADABLE)
  })

  it('names the secret rather than drawing a value for it', () => {
    // D1485: a retirement carries the secret's **name** where one is named, and
    // never a value. The Secret Manager ID is that name, so the cell draws it
    // as itself -- through the class the secrets inventory draws a name with --
    // under a term that says which name it is. Drawing it as a value (a
    // redaction, or a cell labelled a secret value) is the mutation this case
    // fails.
    const model = buildRetirementsModel({ raw: MANAGED })
    const list = one(renderRetirements(factory, model), RETIREMENTS_SET_LIST_CLASS)
    const first = one(rows(list)[0] as ScreenElement, RETIREMENTS_ROW_FACTS_CLASS)
    expect(model.record?.rows[0]?.secretName).toBe(SECRET_NAME)
    expect(fact(first, RETIREMENTS_SECRET_TERM)).toBe(SECRET_NAME)
    expect(valueCell(first, RETIREMENTS_SECRET_TERM).getAttribute('title')).toBe(RETIREMENTS_SECRET_NOTE)
    expect(drawn(renderRetirements(factory, model))).not.toContain(TOKEN_PLACEHOLDER)

    // A row that names no secret draws no secret row at all, rather than a row
    // saying the secret is empty.
    expect(fact(first, RETIREMENTS_LOGICAL_TERM)).toBe(ROW.logical)
  })

  it('draws no Terraform plan, whatever the record carries', () => {
    // D1485 refuses the plan on the merits: a plan is a document about the
    // operator's whole project and carries values the retirement set does not
    // need. The record's own keys are read one by one, so a document wider than
    // the record -- a `plan` member, a row's `job` and `region` -- contributes
    // nothing to any cell. The fixture's `kept` sentence names Terraform
    // because a promise about Terraform's state is exactly what this row is,
    // so the case looks for the **plan's own** content rather than the word: no
    // member of the plan object, and neither of the two fields the state
    // removal steps need, is in a cell.
    const model = buildRetirementsModel({ raw: MANAGED })
    const text = drawn(renderRetirements(factory, model))
    for (const word of [
      RECORD.plan.terraform,
      RECORD.plan.resource_changes[0] as string,
      'resource_changes',
      'to destroy',
      ROW.job,
      ROW.region,
    ]) {
      expect(text, word).not.toContain(word)
    }
    expect(model.record?.rows[0]).not.toHaveProperty('plan')
    expect(model.record?.rows[0]).not.toHaveProperty('job')
    expect(model.record?.rows[0]).not.toHaveProperty('region')
  })

  it('draws the CLI’s refusal through the refusal door, both ways', () => {
    // A read that refused is drawn as the CLI wrote it -- through
    // `sanitizeRefusal`, so a role name survives and a token does not -- and
    // the console's own sentence stands in only when the child printed none.
    const refused = buildRetirementsModel({
      raw: null,
      because: 'the read needs roles/datastore.viewer on the project',
    })
    expect(drawn(renderRetirements(factory, refused))).toContain('roles/datastore.viewer')

    const token = githubToken()
    const echoed = buildRetirementsModel({ raw: null, because: `the read was refused: ${token} is not accepted` })
    const text = drawn(renderRetirements(factory, echoed))
    expect(text).not.toContain(token)
    expect(text).toContain(TOKEN_PLACEHOLDER)

    // A sentence the sanitizer refuses is the console's own, never the raw text
    // and never an empty section.
    const tooLong = buildRetirementsModel({ raw: null, because: 'x'.repeat(MAX_TEXT_INPUT_LENGTH + 1) })
    expect(drawn(renderRetirements(factory, tooLong))).toContain(REFUSAL_UNREADABLE)
  })
})

describe('the retirements section’s frame', () => {
  it('keeps its busy state to its own section', () => {
    const read = sectionRead({ factory, theme: 'light' })
    const retirements = renderRetirementsFrame(factory)
    expect((retirements.root as FakeScreen).classes).toContain(RETIREMENTS_CLASS)
    expect(drawn(renderRetirements(factory, buildRetirementsModel({ raw: MANAGED })))).toContain(RETIREMENTS_TITLE)
    // The slot is the section's **first** child, so the mark a control change
    // puts up in the same frame sits at the top of this section rather than
    // under a block nobody has scrolled to.
    expect(retirements.root.childNodes()[0]).toBe(retirements.slot)
    expect((retirements.slot as FakeScreen).classes).toContain(BUSY_SLOT_CLASS)

    // A sibling section, drawn the way the tab draws the others: its own host,
    // its own slot and figures already on screen.
    const other = factory.create('div')
    other.setAttribute('class', 'projects-managed')
    const otherSlot = factory.create('div')
    otherSlot.setAttribute('class', BUSY_SLOT_CLASS)
    other.append(otherSlot)
    const otherFigures = factory.create('section')
    otherFigures.textContent = 'the managed section’s own figures'
    other.append(otherFigures)

    fillRetirements(factory, buildRetirementsModel({ raw: MANAGED }), retirements.root)

    const token = read.start()
    read.mark(token, retirements.root, retirements.slot, RETIREMENTS_BUSY)

    // The section that is being read says so, in the frame it happened in...
    expect(retirements.root.getAttribute(BUSY_ATTRIBUTE)).toBe('true')
    expect(retirements.root.getAttribute(BUSY_BUSY_ATTRIBUTE)).toBe('true')
    // ...and no other section wears the claim: one read's busy state is its own
    // section's, which is the owner's rule of 2026-10-31 and the reason the tab
    // has one host per read.
    expect(other.getAttribute(BUSY_ATTRIBUTE)).toBeNull()
    expect(all(other, BUSY_CLASS)).toHaveLength(0)
    expect(drawn(other)).toBe('the managed section’s own figures')
    const mark = one(retirements.slot, BUSY_CLASS)
    expect(one(mark, BUSY_LABEL_CLASS).textContent).toBe(RETIREMENTS_BUSY)
    expect(drawn(retirements.root)).toContain(RETIREMENTS_TITLE)

    expect(read.settle(token)).toBe(true)
    expect(retirements.root.getAttribute(BUSY_ATTRIBUTE)).toBeNull()
    clearSection(retirements)
    fillRetirements(factory, buildRetirementsModel({ raw: MANAGED }), retirements.root)
    expect(all(retirements.slot, BUSY_CLASS)).toHaveLength(0)
    expect(drawn(retirements.root)).toContain(RETIREMENTS_TITLE)
  })
})
