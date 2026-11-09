// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The live tab against `fugaro watch` (short-run-ids plan SR10; reconciliation
// F13, D1704, D1706).
//
// "When watch changes, the live tab follows" is a process claim, and D1704 asks
// for it to be a test rather than a promise. The claim is exactly three things
// -- the **order** of the rows, the **short ID** each row is named by, and the
// **words** a row's state is said in -- and the live tab's own columns are the
// approved Flight Deck table's, which is not watch's (D1704: the layout stays).
//
// The fixture is the other half of the check and is not written here:
// `test/fixtures/watch-parity.json` is *recorded* by the Go test
// `internal/watch/console_parity_test.go` from watch's own renderers over one
// fixed view -- the `watch --json` document the CLI would print, beside the
// plain text frame `RenderPlain` draws for the same view. So the two halves of
// each comparison are:
//
//   - **order**: the document's `runs[]` is `watch`'s own order (`sortRuns`),
//     and the plain frame lists the same runs in the same order -- a live model
//     that reordered or regrouped its rows fails against a frame watch drew;
//   - **short IDs**: what the plain frame names each run by is what the live
//     cell must draw, and the ending is the collision rule's answer over the
//     rows of the table (`shared/shortid.ts`), whose cases are held value for
//     value against the Go table by the same Go test;
//   - **words**: the stage the live pipeline line draws is the document's own
//     `stage`, on the line of the plain frame for that same run, and a run the
//     CLI calls lost or queued is said so on both sides.
//
// A change to `internal/watch` fails the Go test until the fixture is recorded
// again; recording it fails this file wherever the live tab no longer agrees
// with what watch now draws. Neither side can drift quietly in the direction
// that matters, which is the console drawing a frame the CLI does not.

import { readFileSync } from 'node:fs'

import { describe, expect, it } from 'vitest'

import { decodeSnapshot } from '../../src/shared/schema'
import type { LiveDocument } from '../../src/shared/schema'
import { displayRuns } from '../../src/shared/shortid'
import { BurnHistory, buildLiveModel, renderLive, runStateOf } from '../../src/renderer/screens/live'
import type { LiveFactory, ScreenElement } from '../../src/renderer/screens/live'

/**
 * DrawNode is the smallest element this file reads: a tag, its classes and
 * attributes, its text and its children, with `append` as the one child write
 * the renderer uses. It is local rather than `test/support/fakeDom.ts`'s
 * `FakeNode` because that fake offers `appendChild` and no `append` -- it is the
 * entry's and the graph screen's fake, built to refuse what those two may not
 * reach -- and the live screen appends. Widening it would loosen the guard three
 * other suites rely on, so the type the renderer asks for is drawn here instead.
 */
class DrawNode implements ScreenElement {
  readonly tag: string
  readonly nodes: ScreenElement[] = []
  readonly attributes = new Map<string, string>()
  textContent: string | null = null

  constructor(tag: string) {
    this.tag = tag
  }

  setAttribute(name: string, value: string): void {
    this.attributes.set(name, value)
  }

  getAttribute(name: string): string | null {
    return this.attributes.get(name) ?? null
  }

  removeAttribute(name: string): void {
    this.attributes.delete(name)
  }

  append(child: ScreenElement): void {
    this.nodes.push(child)
  }

  childNodes(): readonly ScreenElement[] {
    return this.nodes
  }
}

const FIXTURE = 'test/fixtures/watch-parity.json'

/** ParityFixture is the recorded fixture: what it is, the frame `watch --json`
 * writes and the frame the plain renderer draws, both from watch's own code. */
interface ParityFixture {
  readonly note: string
  readonly document: Record<string, unknown>
  readonly plain: string
}

/** fixture is the recorded fixture, read from disk. */
function fixture(): ParityFixture {
  return JSON.parse(readFileSync(FIXTURE, 'utf8')) as ParityFixture
}

/** theDocument is the fixture's frame through the real decoder, which is the
 * door every document the console draws comes through. */
function theDocument(): LiveDocument {
  const decoded = decodeSnapshot(fixture().document, 'the parity fixture')
  if (!decoded.ok) throw new Error(`the parity fixture does not decode: ${decoded.skew.because}`)
  return decoded.value
}

/** factory makes the nodes the live screen draws into. */
const factory: LiveFactory = {
  create: (tag) => new DrawNode(tag),
  createNS: (tag) => new DrawNode(tag),
}

/** theTree is the live tab drawn for the fixture's frame. */
function theTree(): ScreenElement {
  return renderLive(factory, buildLiveModel(theDocument(), new BurnHistory()), 'light')
}

/** walk visits every node of a tree, the root first. */
function walk(node: ScreenElement): readonly ScreenElement[] {
  return [node, ...node.childNodes().flatMap((child) => walk(child))]
}

/** cells is every node under `root` carrying a class, in document order. */
function cells(root: ScreenElement, className: string): readonly ScreenElement[] {
  return walk(root).filter((node) => (node.getAttribute('class') ?? '').split(/\s+/).includes(className))
}

/**
 * plainRows is the plain frame's run table as watch drew it: one entry per row,
 * the ending it named the run by and the row's own line. The table is the block
 * whose head is `RUN`, which is `RenderPlain`'s own head
 * (`internal/watch/plain.go`), and its columns are separated by the two spaces
 * its `tabwriter` pads with.
 */
function plainRows(plain: string): readonly { readonly ending: string; readonly line: string }[] {
  const lines = plain.split('\n')
  const head = lines.findIndex((line) => line.trim().startsWith('RUN') && /\s{2,}/.test(line.trim()))
  if (head === -1) throw new Error(`the plain frame has no RUN table:\n${plain}`)
  const rows: { ending: string; line: string }[] = []
  for (const line of lines.slice(head + 1)) {
    if (line.trim() === '') break
    // The first column is the run's ending, and the row's other cells follow it
    // at two or more spaces -- the `tabwriter` padding, which no cell of its own
    // holds (`internal/watch`'s `oneLine` collapses them).
    const ending = line.trim().split(/\s{2,}/)[0]
    if (ending === undefined || ending === '') throw new Error(`a plain row names no run:\n${line}`)
    rows.push({ ending, line })
  }
  return rows
}

/** ids are the whole run IDs the live rows carry in their door attribute, in
 * drawn order (D1707: the ID travels beside the drawn ending). */
function ids(tree: ScreenElement): readonly (string | null)[] {
  return cells(tree, 'live-run-id').map((cell) => cell.getAttribute('data-run-id'))
}

describe('the live tab against watch', () => {
  it('rows follow watch’s order', () => {
    const document = theDocument()
    const tree = theTree()
    // The drawn rows are the frame's runs, in the frame's order: one cell per
    // run, no regrouping and no re-sorting.
    expect(ids(tree)).toEqual(document.runs.map((run) => run.run))
    // And that order is watch's drawing order rather than this file's reading of
    // the document: the plain frame lists the runs in it, named by the collision
    // rule's answer over exactly those rows.
    const refs = document.runs.map((run) => ({ slug: run.slug, runId: run.run }))
    expect(plainRows(fixture().plain).map((row) => row.ending)).toEqual(displayRuns(refs))
  })

  it('short IDs follow watch', () => {
    const rows = plainRows(fixture().plain)
    const tree = theTree()
    // Position by position: the ending watch printed for a row is the ending the
    // live cell draws.
    expect(cells(tree, 'live-run-id').map((cell) => cell.textContent)).toEqual(rows.map((row) => row.ending))
    expect(ids(tree)).toEqual(theDocument().runs.map((run) => run.run))
  })

  it('status words follow watch', () => {
    const document = theDocument()
    const plain = plainRows(fixture().plain)
    const tree = theTree()
    // The stage the pipeline line draws is the document's own, row for row.
    expect(cells(tree, 'live-run-stage').map((cell) => cell.textContent)).toEqual(
      document.runs.map((run) => run.stage),
    )
    // The stage is also on the plain frame's line for the same run, and the two
    // words the CLI can flag a run with are said on both sides: a lost heartbeat
    // and a queued run are the CLI's own conclusions, so the console must not
    // draw one about a run watch calls healthy (or miss one it flags).
    const states = buildLiveModel(document, new BurnHistory()).repos.flatMap((repo) =>
      repo.runs.map((row) => runStateOf(row)),
    )
    states.forEach((state, at) => {
      const row = plain[at]
      if (row === undefined) throw new Error(`no plain row beside live row ${String(at)}`)
      expect(row.line, `the plain row for ${row.ending}`).toContain(document.runs[at]?.stage ?? '')
      expect(row.line.includes('LOST'), `${row.ending} and LOST`).toBe(state === 'lost')
      expect(row.line.includes('QUEUED'), `${row.ending} and QUEUED`).toBe(state === 'queued')
    })
  })
})
