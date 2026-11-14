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
  MOVE_OUTCOME_WORDS,
  MOVE_TASK_LABEL,
  MOVE_TARGET_UNCHOSEN,
  MOVE_TARGET_UNCHOSEN_LABEL,
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
import { ROADMAP_MOVE_MOVED } from '../../src/source/source'
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

/** aHeldPort is a port whose answers the case hands over itself: each call is
 * recorded when it is made and its read stays unanswered until `answerWith` is
 * given one. **That is how a read that lands after the gesture which replaced it
 * is written at all** -- a preview the operator has since dragged past, chosen
 * again over, or escaped out of. */
function aHeldPort(): {
  readonly port: MovePort
  readonly calls: readonly (MoveRequest & { confirmed: boolean })[]
  readonly answerWith: (index: number, read: MoveRead) => void
} {
  const calls: (MoveRequest & { confirmed: boolean })[] = []
  const waiting: ((read: MoveRead) => void)[] = []
  return {
    port: {
      move: (args) => {
        calls.push({ ...args })
        return new Promise<MoveRead>((resolve) => waiting.push(resolve))
      },
    },
    calls,
    answerWith: (index, read) => {
      const resolve = waiting[index]
      if (resolve === undefined) throw new Error(`no move has been called ${index + 1} times`)
      resolve(read)
    },
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

/** aSheet is the sheet one preview answered, and a failed case where it answered
 * none: `preview` hands back `null` for a read a newer gesture took the flow
 * away from, which is what the stale-answer cases below assert on purpose and no
 * other case here may do by accident. */
function aSheet(sheet: MoveSheet | null): MoveSheet {
  if (sheet === null) throw new Error('the preview answered no sheet')
  return sheet
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

  it('refuses a confirmation of a move no preview drew, and starts no child for it', async () => {
    // **The compare-and-set is the source's, and it is the whole of what makes a
    // confirmation binding.** Both ids here are in the document the last read
    // holds, so nothing above this line refuses them -- and `--expect` is spelled
    // from the record at confirmation time, so without the guard a renderer that
    // named a pair no preview drew (a second drop, a stale sheet, a console
    // driven by something other than its own drawing) would spawn a write the
    // operator never read. The pair confirmed here is the box the task is
    // already in: a real move, listed by the same read, and not the one the
    // preview spelled.
    const stub = aStub()
    const source = await aSource(stub)
    const preview = await source.roadmapMove({ node: NODE, toRelease: OTHER, confirmed: false })
    const before = stub.calls()
    expect(preview.refusal).toBeNull()

    const moved = await source.roadmapMove({ node: NODE, toRelease: BOX, confirmed: true })
    expect(moved.refusal).toBe(ROADMAP_MOVE_MOVED)
    expect(moved.outcome, 'a refused confirmation answered an outcome').toBeNull()
    expect(stub.calls(), 'a move no preview drew spawned a child').toBe(before)
  })

  it('authorises exactly one write: the line that was previewed, once', async () => {
    // A preview is consumed by the confirmation it authorises -- `roadmapMove`
    // clears it before the comparison -- so the same pair confirmed twice is one
    // child and one refusal, and a second press on a stale button cannot write
    // under a line nobody is looking at any more.
    const stub = aStub()
    const source = await aSource(stub)
    await source.roadmapMove({ node: NODE, toRelease: OTHER, confirmed: false })
    const before = stub.calls()

    const written = await source.roadmapMove({ node: NODE, toRelease: OTHER, confirmed: true })
    expect(written.refusal).toBeNull()
    expect(written.outcome?.status).toBe('applied')
    expect(stub.calls(), 'the confirmation the preview authorised spawned no child').toBe(before + 1)

    const again = await source.roadmapMove({ node: NODE, toRelease: OTHER, confirmed: true })
    expect(again.refusal).toBe(ROADMAP_MOVE_MOVED)
    expect(stub.calls(), 'one preview authorised a second write').toBe(before + 1)
  })

  it('refuses a confirmation with no preview behind it at all, and starts no child', async () => {
    // The cold call: no preview has been drawn since this console read the
    // document, which is what a console that was switched away and back, or a
    // renderer that never showed anything, sends first.
    const stub = aStub()
    const source = await aSource(stub)
    const before = stub.calls()

    const cold = await source.roadmapMove({ node: NODE, toRelease: OTHER, confirmed: true })
    expect(cold.refusal).toBe(ROADMAP_MOVE_MOVED)
    expect(stub.calls(), 'a confirmation with nothing behind it spawned a child').toBe(before)
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

  it('draws what the record said as a value of its own, sanitized and capped, beside the word it is not', () => {
    // §6.24's `reason` is a coordinator's own text -- a refusal interpolates the
    // release's name (`ee/coordinator/roadmap_write.go`) -- so it is a value of
    // the record's like every field of the sheet, and it is drawn the way those
    // are: through `sanitizeText`, cut by this module, and in an element of its
    // own. The status word is the module's own vocabulary and holds none of it,
    // which is what a sheet that spelled `${word}: ${reason}` into one cell
    // would compose.
    const hostile = `the record holds ${ROMAN} ${'x'.repeat(4000)}\u0000\nstill`
    const root = drawn({
      request: { node: NODE, toRelease: OTHER },
      answer: answer({ outcome: { status: 'refused', request: 'req-1', reason: hostile, current: null } }),
      sentence: null,
    })
    const status = ofClass(root, 'graph-move-status')
    expect(status).toHaveLength(1)
    expect(status[0]?.textContent).toBe(MOVE_OUTCOME_WORDS.refused)
    const because = ofClass(root, 'graph-move-status-reason')
    expect(because).toHaveLength(1)
    const text = because[0]?.textContent ?? ''
    // The evidence that the record's sentence went through the sanitizer: the
    // null and the newline a reason can carry are gone, and the sentence is
    // untouched otherwise -- it is text, and it says what the record said.
    expect(text).not.toContain('\u0000')
    expect(text).not.toContain('\n')
    expect(text.startsWith('the record holds')).toBe(true)
    // And the cut is this module's own, so a reason that is a paragraph cannot
    // push the sheet's buttons out of the window.
    expect([...text].length).toBeLessThanOrEqual(MOVE_MAX_FIELD_CODE_POINTS)
    expect(text.endsWith('…')).toBe(true)
    // A record that said nothing draws no cell at all rather than an empty one.
    const quiet = drawn({
      request: { node: NODE, toRelease: OTHER },
      answer: answer({ outcome: { status: 'applied', request: 'req-1', reason: null, current: null } }),
      sentence: null,
    })
    expect(ofClass(quiet, 'graph-move-status')).toHaveLength(1)
    expect(ofClass(quiet, 'graph-move-status-reason')).toHaveLength(0)
  })
})

// ---------------------------------------------------------------------------
// The three gestures
// ---------------------------------------------------------------------------

describe('preview, confirm and decline', () => {
  it('asks for the preview with `confirmed` false, and for nothing else', async () => {
    const { port, calls } = recording(() => ({ ok: true, answer: answer() }))
    const flow = new MoveFlow(port)
    const sheet = aSheet(await flow.preview({ node: NODE, toRelease: OTHER }))

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
    const sheet = aSheet(await flow.preview({ node: NODE, toRelease: OTHER }))
    expect(sheet.answer).toBeNull()
    expect(sheet.sentence).toBe('no such task')
    await expect(flow.confirm()).resolves.toBeNull()
    expect(calls).toHaveLength(1)
  })

  it('drops a preview the next one replaced, and keeps the request the sheet in hand drew', async () => {
    // **The defect this drop exists for.** A preview is a child, so its answer
    // can land after the operator has asked for another move: drawn, the sheet
    // would show one command line while the flow holds the request for another,
    // and the confirming click would run the second -- the move on screen would
    // not be the move that ran.
    const { port, calls, answerWith } = aHeldPort()
    const flow = new MoveFlow(port)
    const first = flow.preview({ node: NODE, toRelease: '' })
    const second = flow.preview({ node: NODE, toRelease: OTHER })
    expect(calls).toEqual([
      { node: NODE, toRelease: '', confirmed: false },
      { node: NODE, toRelease: OTHER, confirmed: false },
    ])

    answerWith(1, { ok: true, answer: answer() })
    const sheet = aSheet(await second)
    expect(sheet.request).toEqual({ node: NODE, toRelease: OTHER })
    expect(flow.pending).toEqual({ node: NODE, toRelease: OTHER })

    // The first answer lands after it, and it is the **refusal** a task the
    // record no longer holds is answered with: the worst of the two, because it
    // is also the shape that would clear the request in hand if it were read as
    // the flow's own state rather than as the answer to a gesture that is over.
    answerWith(0, { ok: false, sentence: 'no such task' })
    await expect(first).resolves.toBeNull()
    expect(flow.pending, 'a stale answer dropped the request the sheet shows').toEqual({
      node: NODE,
      toRelease: OTHER,
    })

    // So the confirmation is the sheet on screen, and nothing else.
    const confirming = flow.confirm()
    answerWith(2, { ok: true, answer: answer({ outcome: { status: 'applied', request: 'r1', reason: null, current: null } }) })
    const confirmed = await confirming
    expect(calls.at(-1)).toEqual({ node: NODE, toRelease: OTHER, confirmed: true })
    expect(confirmed?.answer?.outcome?.status).toBe('applied')
  })

  it('drops a preview the sheet was escaped out of, so no sheet comes back', async () => {
    // The same drop from the other side: the request is gone the moment the
    // person answers with Escape, so the answer that arrives after it is about a
    // sheet that is no longer up -- and a sheet put back over the row the
    // keyboard has just gone back to offers a confirming button for a request
    // the flow has already dropped.
    const { port, answerWith } = aHeldPort()
    const flow = new MoveFlow(port)
    const reading = flow.preview({ node: NODE, toRelease: OTHER })
    expect(flow.pending).toEqual({ node: NODE, toRelease: OTHER })
    expect(flow.decline()).toBeNull()

    answerWith(0, { ok: true, answer: answer() })
    await expect(reading).resolves.toBeNull()
    expect(flow.pending).toBeNull()
  })

  it('never drops a confirmation’s own answer, whichever gesture follows it', async () => {
    // **What a newer gesture makes stale is the sheet, not the record's answer.**
    // A confirmation's request has left this flow and a child ran it, so its
    // answer is a fact about the record rather than a drawing, and the caller
    // (which is what knows the drawing has moved on) is the one that skips it. A
    // flow that dropped it would lose the `applied` the way back is built from.
    const { port, answerWith } = aHeldPort()
    const flow = new MoveFlow(port)
    const previewing = flow.preview({ node: NODE, toRelease: OTHER })
    answerWith(0, { ok: true, answer: answer() })
    await previewing

    const confirming = flow.confirm()
    // The operator asks for another move while the write is in flight.
    const after = flow.preview({ node: NODE, toRelease: '' })
    answerWith(1, { ok: true, answer: answer({ outcome: { status: 'applied', request: 'r1', reason: null, current: null } }) })
    const confirmed = await confirming
    expect(confirmed?.answer?.outcome?.status).toBe('applied')
    expect(confirmed?.request).toEqual({ node: NODE, toRelease: OTHER })

    // And the newer preview's own answer is the sheet in hand: the gesture that
    // asked last is what a confirmation then sends.
    answerWith(2, { ok: true, answer: answer({ to: null }) })
    expect((await after)?.request).toEqual({ node: NODE, toRelease: '' })
    expect(flow.pending).toEqual({ node: NODE, toRelease: '' })
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

    const moved = aSheet(await flow.preview({ node: NODE, toRelease: OTHER }))
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
    // **The control opens on its own not-yet-chosen option and not on a box.** A
    // browser pre-selects a `<select>`'s first option and reports a `change` only
    // when the value differs from the one it holds, so a list drawn on the first
    // box would swallow that box's own choice -- and the unassign's, on a drawing
    // whose boxes are all finished. `selected` says so in the markup too, and the
    // value is one no key can be (a box is 64 lowercase hex, the unassign `''`).
    expect(options[0]?.getAttribute('value')).toBe(MOVE_TARGET_UNCHOSEN)
    expect(options[0]?.getAttribute('selected')).toBe('selected')
    expect(options[0]?.textContent).toBe(MOVE_TARGET_UNCHOSEN_LABEL)
    // Then the values are the record’s keys and the empty string for the
    // unassign: what leaves the renderer is an id, and the unassign declares no
    // box at all.
    expect(options.map((each) => each.getAttribute('value'))).toEqual([MOVE_TARGET_UNCHOSEN, BOX, OTHER, ''])
    expect(options.slice(1).map((each) => each.getAttribute('selected'))).toEqual([null, null, null])
    // The last option is the unassign, in the console’s own words.
    expect(options.at(-1)?.textContent).toBe(MOVE_UNASSIGNED_WORD)
    // **Nothing is confirmed before a request exists**: the choosing sheet is a
    // list, the way to close it and no more. The confirming button is drawn on a
    // preview and never here, so a click on this sheet cannot become a write.
    expect(ofClass(root, 'graph-move-confirm')).toHaveLength(0)
    expect(ofClass(root, 'graph-move-undo')).toHaveLength(0)
    expect(ofClass(root, 'graph-move-cancel')).toHaveLength(1)
  })

  it('offers the unassign alone on a drawing that lists no box, and it is reachable', () => {
    // The degenerate drawing the placeholder exists for: every box of the record
    // is finished, so the list a person gets is the unassign and nothing else --
    // and the control is still drawn on its own option rather than on that one,
    // so choosing it reports a change like any other choice.
    const root = drawn({ request: null, answer: null, sentence: null }, [])
    const options = everyNode(ofClass(root, 'graph-move-target')[0] as FakeNode).filter(
      (each) => each.tagName === 'OPTION',
    )
    expect(options.map((each) => each.getAttribute('value'))).toEqual([MOVE_TARGET_UNCHOSEN, ''])
    expect(options[0]?.getAttribute('selected')).toBe('selected')
  })

  it('offers no empty option for a target the console cannot name', () => {
    const root = drawn({ request: null, answer: null, sentence: null }, [aTarget(BOX, 7)])
    const options = everyNode(root).filter((each) => each.tagName === 'OPTION')
    // The placeholder first, then the box: the target's own option is found by
    // the value it carries rather than by being first in the list.
    const option = options.find((each) => each.getAttribute('value') === BOX)
    // A name the sanitizer refused is drawn as the shared unavailable mark rather
    // than as an empty label, so the row a person picks is never blank.
    expect(option?.textContent).toBe(UNAVAILABLE_MARK)
  })
})
