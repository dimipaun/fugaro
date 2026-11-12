// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The console's move of one task of the record into another release (the
// dependency-graph plan's GV3.2; design section 5.1 rule 14, contract section
// 6.24 and 6.25).
//
// **The fence is the subject of the first block.** A drag names a node key and a
// release key out of the graph document this installation last read, and the
// console's own `roadmapMove` is what turns them into a command line: the
// positional it spells is the document's `id` for the node the key named, and
// the release word is the document's own key for the box -- so a payload that
// named a task the record does not hold, or a box no read listed, is refused
// **before a child exists**, which is what `stub.calls()` counts here. A console
// that forwarded the renderer's ids would spawn a child for a task the drawing
// never drew, and this is the case that fails when it does.
//
// **The rest is the sheet.** The confirmation is a function of the answer and
// nothing else: the named fields, the exact argv, and the three gestures a
// person makes on it. Every value in it comes from the record and is untrusted
// text, so the fields are `Sanitized` and capped -- a title that carries "→"
// cannot forge the from/to line because it is drawn in a slot of its own, which
// is the case `moveFields` is held to below.
//
// Hermetic: the stub binary is a file written into a temporary directory, no
// network is reached and no real name is written down.

import { mkdtempSync, realpathSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import {
  MOVE_CANCEL_LABEL,
  MOVE_CONFIRM_LABEL,
  MOVE_FROM_LABEL,
  MOVE_MAX_FIELD_CODE_POINTS,
  MOVE_TASK_LABEL,
  MOVE_TO_LABEL,
  MOVE_UNASSIGNED_WORD,
  MOVE_UNDO_LABEL,
  MoveFlow,
  inverseOf,
  moveAnswerOf,
  moveFields,
  moveSheet,
  type MovePort,
  type MoveRead,
  type MoveRequest,
  type MoveSheet,
  type MoveTarget,
} from '../../src/renderer/graph/move'
import { UNAVAILABLE_MARK } from '../../src/shared/availability'
import { MAX_TEXT_LENGTH, sanitizeText } from '../../src/shared/sanitize'
import { GRAPH_DOCUMENT_VERSION } from '../../src/shared/schema'
import type { RoadmapMoveAnswer } from '../../src/source/source'
import { CliSource } from '../../src/source/cli'
import { everyNode } from '../support/fakeDom'
import { FakeDocument } from '../support/fakeDom'
import { NodeSurface, Surface } from '../support/graphHarness'
import type { FakeNode } from '../support/fakeDom'
import { installStub } from './stub'
import type { Stub } from './stub'

const PROJECT = 'fugaro'

/** BOX and OTHER are two release boxes as the record writes them: the 64
 * lowercase hex characters a `releases[].key` is (§6.22). */
const BOX = '1a'.repeat(32)
const OTHER = '2b'.repeat(32)

/** NODE is the document key of the task, and TASK the `id` the record holds for
 * it -- the two are different spellings of the same task, which is the whole
 * reason the fence is a check and not a formatting step: `NODE` is what a
 * renderer may name, and `TASK` is what the CLI reads. */
const NODE = 'coordinator:GV/GV3.2'
const TASK = 'GV/GV3.2'
const TITLE = 'drag a task to another release'

/** words is the argv a move is expected to spawn, **without the program**: the
 * answer's own argv carries the resolved binary as its first word (`plan.argv`,
 * which is what rule 11's and rule 12's confirmation windows show too), and the
 * child's own report carries the words it was handed. */
function wordsOf(to: string, expect: string): readonly string[] {
  return ['coordinator', 'roadmap', 'move', '--json', TASK, '--to', to, '--expect', expect, '--project', PROJECT]
}

/** ROMAN is the title a hostile document writes: an arrow, and the from/to line
 * a confirmation draws, so a field that dropped the title into one string would
 * read as the sheet's own words. */
const ROMAN = '0.11.0 → 0.12.0 the task'

function freshDirectory(): string {
  return realpathSync(mkdtempSync(join(tmpdir(), 'fugaro-console-graph-move-')))
}

/** graphDocument is the graph read's document with the record's own members
 * (GV4.1): two release boxes and one task placed in the first of them. */
function graphDocument(options: { readonly release?: string; readonly title?: string } = {}): string {
  return `${JSON.stringify({
    version: GRAPH_DOCUMENT_VERSION,
    scopes: [{ source: 'coordinator', state: 'up', as_of: null, reason: '' }],
    releases: [
      { key: BOX, stream: 'gv', name: '0.12.0', state: 'open', order: 1 },
      { key: OTHER, stream: 'gv', name: '0.13.0', state: 'planned', order: 2 },
    ],
    nodes: [
      {
        key: NODE,
        source: 'coordinator',
        id: TASK,
        kind: 'task',
        title: options.title ?? TITLE,
        repo: '',
        state: 'ready',
        plan: 'GV/GV.md',
        stream: 'gv',
        release: options.release ?? BOX,
      },
    ],
    edges: [],
    warnings: [],
  })}\n`
}

/** outcomeDocument is §6.24's answer to a move that reached the queue. */
function outcomeDocument(status: string): string {
  return `${JSON.stringify({ version: 1, request: 'req-1', kind: 'move', status })}\n`
}

/** aStub is the fake CLI with the three answers a move needs. */
function aStub(scenario: { readonly outcome?: string; readonly release?: string; readonly title?: string } = {}): Stub {
  return installStub(freshDirectory(), {
    commands: {
      version: { stdout: '1.4.2\n' },
      graph: { stdout: graphDocument(scenario) },
      'coordinator roadmap move': { stdout: outcomeDocument(scenario.outcome ?? 'applied') },
    },
  })
}

/** aSource is a real source over the stub, with the graph read already made so
 * the record's facts are the document's. */
async function aSource(stub: Stub): Promise<CliSource> {
  const source = await CliSource.create({
    target: { project: PROJECT },
    configuredBinaryPath: stub.binary,
    appEnv: {},
  })
  await source.graphRead({})
  return source
}

/** aPort is the entry's own door in a test: the source's answer, and a thrown
 * refusal read the way the main process reads one into the envelope. */
function aPort(source: CliSource): MovePort {
  return {
    move: async (args): Promise<MoveRead> => {
      try {
        const { node, toRelease, confirmed } = args
        return { ok: true, answer: await source.roadmapMove({ node, toRelease, confirmed }) }
      } catch (error) {
        return { ok: false, sentence: error instanceof Error ? error.message : String(error) }
      }
    },
  }
}

/** recording is a port that remembers every call and answers the one answer each
 * call is given, so "nothing was sent" and "this exact request was sent" are
 * claims about the flow rather than about a source. */
function recording(read: (args: MoveRequest & { confirmed: boolean }) => MoveRead): {
  readonly port: MovePort
  readonly calls: readonly (MoveRequest & { confirmed: boolean })[]
} {
  const calls: (MoveRequest & { confirmed: boolean })[] = []
  return {
    port: {
      move: (args) => {
        calls.push({ ...args })
        return Promise.resolve(read(args))
      },
    },
    calls,
  }
}

/** answer is one `roadmap.move` answer as the surface writes one, for the cases
 * that are about the sheet and not about the child. */
function answer(over: Partial<RoadmapMoveAnswer> = {}): RoadmapMoveAnswer {
  return {
    refusal: null,
    argv: ['coordinator', 'roadmap', 'move', '--json', TASK, '--to', OTHER, '--expect', BOX, '--project', PROJECT],
    task: { key: NODE, id: TASK, title: TITLE },
    from: { key: BOX, name: '0.12.0' },
    to: { key: OTHER, name: '0.13.0' },
    outcome: null,
    sentence: null,
    ...over,
  }
}

/** drawn is one sheet drawn into a document of its own, as the tree a case reads
 * its text and its hooks off -- with the targets the screen's own button hands
 * it, for the sheet a keyboard opens. */
function drawn(sheet: MoveSheet, targets: readonly MoveTarget[] = []): FakeNode {
  const document = new FakeDocument()
  const node = (moveSheet(new Surface(document), sheet, targets) as NodeSurface).node
  document.body.appendChild(node)
  return node
}

/** aTarget is one release box as the screen hands it to the sheet: the record's
 * key, and the box's name through the class everything drawn goes through. */
function aTarget(key: string, name: unknown): MoveTarget {
  return { key, name: sanitizeText(name, 'label', 'a release name') }
}

/** ofClass is every element under a node carrying one class, in document
 * order -- the reader the sheet's own hooks are held to. */
function ofClass(node: FakeNode, name: string): readonly FakeNode[] {
  return everyNode(node).filter((each) => (each.getAttribute('class') ?? '').split(/\s+/).includes(name))
}

/** words is every argv word the sheet draws, in document order. */
function words(sheet: MoveSheet): readonly string[] {
  return ofClass(drawn(sheet), 'graph-move-word').map((each) => each.textContent ?? '')
}

// ---------------------------------------------------------------------------
// The fence: the ids are checked, and a check that fails spawns nothing
// ---------------------------------------------------------------------------

describe('a move names two ids, and the console checks them', () => {
  it('refuses a node the last read never listed, and starts no child for it', async () => {
    const stub = aStub()
    const source = await aSource(stub)
    const before = stub.calls()

    await expect(
      source.roadmapMove({ node: 'coordinator:GV/GV9.9', toRelease: OTHER, confirmed: true }),
    ).rejects.toThrow()
    // **The claim is about the child and not about this file's count.** A source
    // that forwarded the renderer's node key would spell whatever it was handed
    // into the positional and run it -- which is the child this count would see.
    expect(stub.calls(), 'a move for a task the drawing never drew spawned a child').toBe(before)
  })

  it('refuses a release the last read never listed, and starts no child for it', async () => {
    const stub = aStub()
    const source = await aSource(stub)
    const before = stub.calls()

    await expect(source.roadmapMove({ node: NODE, toRelease: 'f'.repeat(64), confirmed: true })).rejects.toThrow()
    expect(stub.calls(), 'a move into a box no read listed spawned a child').toBe(before)
  })

  it('spells the document’s own id for the task, never the key the caller sent', async () => {
    const stub = aStub()
    const source = await aSource(stub)
    const preview = await source.roadmapMove({ node: NODE, toRelease: OTHER, confirmed: false })

    expect(preview.argv).toEqual([stub.binary, ...wordsOf(OTHER, BOX)])
    expect(preview.argv).not.toContain(NODE)
  })
})

// ---------------------------------------------------------------------------
// The confirmation's fields
// ---------------------------------------------------------------------------

describe('the confirmation is composed from named fields', () => {
  it('puts the task, the box it is in and the box it goes to in three fields of their own', () => {
    const fields = moveFields(answer())
    expect(fields.map((field) => field.label)).toEqual([MOVE_TASK_LABEL, MOVE_FROM_LABEL, MOVE_TO_LABEL])
    const value = (label: string): string => {
      const field = fields.find((each) => each.label === label)
      return field !== undefined && field.value.ok ? field.value.text : ''
    }
    expect(value(MOVE_TASK_LABEL)).toBe(TITLE)
    expect(value(MOVE_FROM_LABEL)).toBe('0.12.0')
    expect(value(MOVE_TO_LABEL)).toBe('0.13.0')
  })

  it('keeps a title that spells the from/to line in its own field, arrow and all', () => {
    const fields = moveFields(answer({ task: { key: NODE, id: TASK, title: ROMAN } }))
    const task = fields.find((field) => field.label === MOVE_TASK_LABEL)
    const from = fields.find((field) => field.label === MOVE_FROM_LABEL)
    const to = fields.find((field) => field.label === MOVE_TO_LABEL)
    expect(task?.value.ok && task.value.text).toBe(ROMAN)
    // The two other fields are the record's own names and carry nothing of the
    // title: one string composed of the three would read as a from/to line the
    // title wrote.
    expect(from?.value.ok && from.value.text).toBe('0.12.0')
    expect(to?.value.ok && to.value.text).toBe('0.13.0')
  })

  it('caps every field, so a long title cannot push the from/to line out of the sheet', () => {
    const long = 'x'.repeat(4000)
    const fields = moveFields(answer({ task: { key: NODE, id: TASK, title: long } }))
    for (const field of fields) {
      expect(field.value.ok, field.label).toBe(true)
      const text = field.value.ok ? field.value.text : ''
      expect([...text].length, field.label).toBeLessThanOrEqual(MOVE_MAX_FIELD_CODE_POINTS)
    }
    const task = fields.find((field) => field.label === MOVE_TASK_LABEL)
    expect(task?.value.ok && task.value.text.endsWith('…')).toBe(true)
    // The sheet's cut is its own and tighter than the sanitizer's: a field is a
    // row of a sheet and not a paragraph, and the bound is what keeps the three
    // rows of a confirmation the same size whatever a title holds.
    expect(MOVE_MAX_FIELD_CODE_POINTS).toBeLessThan(MAX_TEXT_LENGTH)
  })

  it('names the unassigned side in the console’s own words rather than an empty cell', () => {
    const fields = moveFields(answer({ from: null, to: { key: OTHER, name: '0.13.0' } }))
    const from = fields.find((field) => field.label === MOVE_FROM_LABEL)
    expect(from?.value.ok && from.value.text).toBe(MOVE_UNASSIGNED_WORD)
  })
})

// ---------------------------------------------------------------------------
// The sheet
// ---------------------------------------------------------------------------

describe('the sheet the screen draws', () => {
  it('draws every named field, and the exact argv word by word', () => {
    const sheet: MoveSheet = { request: { node: NODE, toRelease: OTHER }, answer: answer(), sentence: null }
    const drawn = words(sheet)
    expect(drawn).toEqual(answer().argv)
  })

  it('draws the refusal as a sentence and offers no confirming button', () => {
    const root = drawn({ request: { node: NODE, toRelease: OTHER }, answer: null, sentence: 'no such task' })
    expect(ofClass(root, 'graph-move-confirm')).toHaveLength(0)
    expect(ofClass(root, 'graph-move-cancel')).toHaveLength(1)
  })
})

// ---------------------------------------------------------------------------
// The three gestures
// ---------------------------------------------------------------------------

describe('preview, confirm and decline', () => {
  it('asks for the preview with `confirmed` false, and for nothing else', async () => {
    const { port, calls } = recording(() => ({ ok: true, answer: answer() }))
    const flow = new MoveFlow(port)
    const sheet = await flow.preview({ node: NODE, toRelease: OTHER })

    expect(calls).toEqual([{ node: NODE, toRelease: OTHER, confirmed: false }])
    expect(sheet.answer?.outcome).toBeNull()
  })

  it('sends nothing at all when the confirmation is declined', async () => {
    const { port, calls } = recording(() => ({ ok: true, answer: answer() }))
    const flow = new MoveFlow(port)
    await flow.preview({ node: NODE, toRelease: OTHER })
    expect(calls).toHaveLength(1)

    expect(flow.decline()).toBeNull()
    // A decline is the closed sheet: the request is dropped, so a confirm that
    // arrives from a stale key or a second click has nothing to confirm.
    await expect(flow.confirm()).resolves.toBeNull()
    expect(calls, 'a declined confirmation spawned something').toHaveLength(1)
  })

  it('confirms the request the preview drew, and not a later one', async () => {
    const { port, calls } = recording((args) => ({ ok: true, answer: answer() }))
    const flow = new MoveFlow(port)
    await flow.preview({ node: NODE, toRelease: OTHER })
    await flow.confirm()
    expect(calls).toEqual([
      { node: NODE, toRelease: OTHER, confirmed: false },
      { node: NODE, toRelease: OTHER, confirmed: true },
    ])
  })

  it('keeps a refused call as a sentence, and spawns nothing on it', async () => {
    const { port, calls } = recording(() => ({ ok: false, sentence: 'no such task' }))
    const flow = new MoveFlow(port)
    const sheet = await flow.preview({ node: NODE, toRelease: OTHER })
    expect(sheet.answer).toBeNull()
    expect(sheet.sentence).toBe('no such task')
    await expect(flow.confirm()).resolves.toBeNull()
    expect(calls).toHaveLength(1)
  })
})

describe('undo is the inverse move', () => {
  it('names the box the task came from, and no box at all when it had none', () => {
    expect(inverseOf(answer())).toEqual({ node: NODE, toRelease: BOX })
    expect(inverseOf(answer({ from: null }))).toEqual({ node: NODE, toRelease: '' })
    // A move that put a task into no box is undone by putting it back where it
    // was: the inverse of `to: null` is the box the answer named for `from`.
    expect(inverseOf(answer({ to: null }))).toEqual({ node: NODE, toRelease: BOX })
  })

  it('spawns the argv back, against the record the move left behind', async () => {
    const stub = aStub()
    const source = await aSource(stub)
    const flow = new MoveFlow(aPort(source))

    const moved = await flow.preview({ node: NODE, toRelease: OTHER })
    expect(moved.answer?.outcome, 'the record did not apply the move').toBeNull()
    const applied = await flow.confirm()
    expect(applied?.answer?.outcome?.status).toBe('applied')
    // **What the confirmation drew is what the child got**, held against the
    // child's own account of its argv rather than against this file's two
    // spellings of one command.
    expect(stub.reports().at(-1)?.argv).toEqual((moved.answer?.argv ?? []).slice(1))
    expect(stub.reports().at(-1)?.argv).toEqual(wordsOf(OTHER, BOX))

    // The console re-reads the document the move changed before it offers the
    // way back: `--expect` is the release the record holds **now**, and a
    // console still holding the read from before the move would run an inverse
    // that the record refuses as stale.
    stub.write({
      commands: {
        version: { stdout: '1.4.2\n' },
        graph: { stdout: graphDocument({ release: OTHER }) },
        'coordinator roadmap move': { stdout: outcomeDocument('applied') },
      },
    })
    await source.graphRead({})

    const back = await flow.undo()
    expect(back?.request).toEqual({ node: NODE, toRelease: BOX })
    await flow.confirm()
    // The way back is the same command with the two boxes exchanged, and the
    // `--expect` is the box the record holds **now**: an undo built from the
    // read before the move is a request the record refuses as stale.
    expect(stub.reports().at(-1)?.argv).toEqual(wordsOf(BOX, OTHER))
    expect(MOVE_UNDO_LABEL).not.toBe(MOVE_CONFIRM_LABEL)
    expect(MOVE_CANCEL_LABEL).not.toBe(MOVE_CONFIRM_LABEL)
  })
})

// ---------------------------------------------------------------------------
// The answer the bridge hands over, and the sheet a keyboard opens
// ---------------------------------------------------------------------------

describe('the answer is read closed, because a confirmation promises what it spawns', () => {
  it('reads §6.24’s answer whole, the two sides included', () => {
    const read = moveAnswerOf({
      ...answer(),
      outcome: { status: 'applied', request: 'req-1', reason: null, current: null },
    })
    expect(read?.task).toEqual({ key: NODE, id: TASK, title: TITLE })
    expect(read?.from).toEqual({ key: BOX, name: '0.12.0' })
    expect(read?.to).toEqual({ key: OTHER, name: '0.13.0' })
    expect(read?.outcome?.status).toBe('applied')
    expect(read?.argv).toEqual(answer().argv)
    // A task the record holds in no box is the contract's `null` and not a
    // missing member: the sheet names it in the console's own words.
    expect(moveAnswerOf({ ...answer(), from: null })?.from).toBeNull()
  })

  it('refuses the whole answer for a status this build does not hold', () => {
    // `MOVE_STATUSES` is the **whole** vocabulary: a newer CLI's status is a word
    // whose meaning this build cannot read, and a sheet that drew the rest of the
    // answer would draw a command line beside a status it guessed. The whole
    // answer is refused rather than the one member, which is what the sheet's
    // "could not read" sentence is for (`MOVE_UNREADABLE_WORD`).
    const newer = {
      ...answer(),
      outcome: { status: 'queued-elsewhere', request: 'req-1', reason: null, current: null },
    }
    expect(moveAnswerOf(newer)).toBeNull()
    // A member of the wrong type is not a member either -- the argv is the array
    // the answer carried or nothing, so no word of a command line is rebuilt here.
    expect(moveAnswerOf({ ...answer(), argv: ['coordinator', 7] })).toBeNull()
    expect(moveAnswerOf({ ...answer(), sentence: 7 })).toBeNull()
    expect(moveAnswerOf('a sentence where an answer goes')).toBeNull()
    expect(moveAnswerOf(null)).toBeNull()
  })
})

describe('the sheet a row’s own button opens', () => {
  it('offers one option per target and the unassign, and no confirming button', () => {
    const root = drawn({ request: null, answer: null, sentence: null }, [
      aTarget(BOX, '0.12.0'),
      aTarget(OTHER, '0.13.0'),
    ])
    const select = ofClass(root, 'graph-move-target')
    expect(select).toHaveLength(1)
    const options = everyNode(select[0] as FakeNode).filter((each) => each.tagName === 'OPTION')
    // The values are the record’s keys and the empty string for the unassign:
    // what leaves the renderer is an id, and the unassign declares no box at all.
    expect(options.map((each) => each.getAttribute('value'))).toEqual([BOX, OTHER, ''])
    // The last option is the unassign, in the console’s own words.
    expect(options.at(-1)?.textContent).toBe(MOVE_UNASSIGNED_WORD)
    // **Nothing is confirmed before a request exists**: the choosing sheet is a
    // list, the way to close it and no more. The confirming button is drawn on a
    // preview and never here, so a click on this sheet cannot become a write.
    expect(ofClass(root, 'graph-move-confirm')).toHaveLength(0)
    expect(ofClass(root, 'graph-move-undo')).toHaveLength(0)
    expect(ofClass(root, 'graph-move-cancel')).toHaveLength(1)
  })

  it('offers no empty option for a target the console cannot name', () => {
    const root = drawn({ request: null, answer: null, sentence: null }, [aTarget(BOX, 7)])
    const option = everyNode(root).find((each) => each.tagName === 'OPTION')
    expect(option?.getAttribute('value')).toBe(BOX)
    // A name the sanitizer refused is drawn as the shared unavailable mark rather
    // than as an empty label, so the row a person picks is never blank.
    expect(option?.textContent).toBe(UNAVAILABLE_MARK)
  })
})
