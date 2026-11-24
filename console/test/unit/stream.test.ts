// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The console's stream read (console-improvements plan RS2; D2906, F18).
//
// `readStream` is one read with two shapes: the opt-in line-delimited read
// (`runs.stream`, C-OPS's contract) whose rows are drawn **as they arrive**, and
// the whole-document read it falls back to when the operation cannot be asked at
// all. Four facts are this suite's subject, and each is a rule rather than a
// behaviour of the module:
//
//   - **the rows arrive in the CLI's order, one paint at a time.** The bridge
//     answers a feed as one promise (`newOpsFrom`), so what makes the rows
//     appear progressively is the chunking: one row per paint, in the document's
//     order, and every paint redraws from the rows read so far rather than
//     appending to a list nobody can re-derive.
//   - **a missing `end` is an interrupted read** and never a list that finished.
//     `StreamFeed.end` is `null` for a CLI that stopped without saying it was
//     done (and for a feed a cap ended), and R1's rule is that such a read draws
//     the console's sentence with a Retry rather than the rows that happened to
//     arrive -- **never a partial list as complete**. This is the case the plan's
//     red step names.
//   - **no answer is the whole-document read.** A console build with no
//     `runs.stream` (the floor is unset, the CLI is older, the channel refused
//     before spawning) reads `runs.list` exactly as it did before this module
//     existed: one answer, one drawing, and no stream at all.
//   - **a view the reduced mode withholds asks neither read.** `withheld` is the
//     caller's `hidesView` answer (`shared/version.ts`), asked **before** either
//     operation is spawned: the read whose figures the mode withholds is never
//     made, so the console cannot draw them by mistake.
//
// The fake bridge is built here from `new-ops.ts`'s types, the way every consumer
// of C-OPS does (`contract-ops.md`: no consumer imports a fixture bridge, which
// is what let I-OPS delete the stub one whole).

import { describe, expect, it } from 'vitest'

import { newOpsFrom } from '../../src/renderer/new-bridge'
import { STREAM_INTERRUPTED_WORD, readStream } from '../../src/renderer/stream'
import type { RunsListRow } from '../../src/shared/schema'

/** aLine is one `row` line of a `runs.stream` feed: the same row `runs ls
 * --json`'s array holds, with the line's own `type` beside it. The four members
 * a row must carry are the ones `decodeRunsList` reads (`schema.ts`). */
function aLine(run: string, status = 'failed'): Record<string, unknown> {
  return { type: 'row', row: { run, repo: 'acme/widgets', status, terminal: true } }
}

/** END_LINE is the feed's own statement that it finished. */
const END_LINE: Record<string, unknown> = { type: 'end', version: 1, count: 3 }

/** aFeedBridge is a bridge that answers `runs.stream` with the lines given, and
 * nothing else: the other eight operations are absent, which is what
 * `newOpsFrom` reads as `not-available`. */
function aFeedBridge(lines: readonly Record<string, unknown>[]) {
  return newOpsFrom({
    streamRuns: () => Promise.resolve({ ok: true, data: { document: { lines: [...lines] } } }),
  })
}

/** theRuns is the ids of the rows a case was handed, in the order it drew them. */
function theRuns(rows: readonly RunsListRow[]): string[] {
  return rows.map((row) => row.run)
}

/** whole is the whole-document read a case falls back to: one answer, and the
 * ids it holds. */
function whole(runs: readonly string[]) {
  return () =>
    Promise.resolve({
      ok: true as const,
      rows: runs.map((run) => ({ run }) as unknown as RunsListRow),
    })
}

/** drawing is the paint the cases drive by hand: the chunk is drawn in the same
 * turn, so the order of the chunks is the order of this list. */
const synchronous = (step: () => void): void => {
  step()
}

describe('a stream that finishes', () => {
  it('draws the rows in the CLI’s order, one paint at a time', async () => {
    const bridge = aFeedBridge([aLine('run-1'), aLine('run-2'), END_LINE])
    const drawn: string[][] = []
    const chunks: number[] = []
    const result = await readStream<RunsListRow>({
      stream: () => bridge.streamRuns({ since: '24h' }),
      document: whole(['never-read']),
      onRows: (rows) => {
        chunks.push(rows.length)
        drawn.push(theRuns(rows))
      },
      paint: synchronous,
    })
    expect(drawn).toEqual([['run-1'], ['run-1', 'run-2']])
    // Every chunk is one row more than the last: the section redraws from the
    // rows read so far, and a caller that dropped one would draw a list the
    // document does not hold.
    expect(chunks).toEqual([1, 2])
    expect(result.source).toBe('stream')
    expect(result.status).toBe('complete')
    expect(theRuns(result.rows)).toEqual(['run-1', 'run-2'])
    // The `end` line is carried, not just read: it is what says the read
    // finished, and RS5 draws the totals its own streams annotate it with.
    expect(result.end).not.toBeNull()
    expect(result.end?.count).toBe(3)
    expect(result.sentence).toBeNull()
  })

  it('draws nothing for a feed no row arrived in, and still finishes', async () => {
    const bridge = aFeedBridge([{ type: 'end', version: 1, count: 0 }])
    const drawn: string[][] = []
    const result = await readStream<RunsListRow>({
      stream: () => bridge.streamRuns({ since: '24h' }),
      document: whole(['never-read']),
      onRows: (rows) => drawn.push(theRuns(rows)),
      paint: synchronous,
    })
    expect(drawn).toEqual([])
    expect(result.status).toBe('complete')
    expect(result.rows).toEqual([])
  })
})

describe('a stream that stops without an end', () => {
  it('is an interrupted read, not a list that finished', async () => {
    const bridge = aFeedBridge([aLine('run-1'), aLine('run-2')])
    const result = await readStream<RunsListRow>({
      stream: () => bridge.streamRuns({ since: '24h' }),
      // A whole-document read is **not** the answer to an interruption: the CLI
      // was asked and it stopped, and asking a second reader for the same list
      // would be the console covering for the read that failed.
      document: whole(['never-read']),
      paint: synchronous,
    })
    expect(result.status).toBe('interrupted')
    expect(result.source).toBe('stream')
    expect(result.end).toBeNull()
    expect(result.sentence).toBe(STREAM_INTERRUPTED_WORD)
    // What arrived is carried for a caller that wants to draw it beside the
    // sentence; `status` is what says it is not the list.
    expect(theRuns(result.rows)).toEqual(['run-1', 'run-2'])
  })

  it('is interrupted however many rows had arrived', async () => {
    const bridge = aFeedBridge([])
    const result = await readStream<RunsListRow>({
      stream: () => bridge.streamRuns({ since: '24h' }),
      document: whole(['never-read']),
      paint: synchronous,
    })
    expect(result.status).toBe('interrupted')
    expect(result.rows).toEqual([])
    expect(result.sentence).toBe(STREAM_INTERRUPTED_WORD)
  })
})

describe('a stream the CLI failed', () => {
  it('draws the CLI’s own sentence, and never the whole read', async () => {
    const bridge = aFeedBridge([
      aLine('run-1'),
      { type: 'error', error: 'the runs bucket refused the read' },
      END_LINE,
    ])
    const drawn: string[][] = []
    const result = await readStream<RunsListRow>({
      stream: () => bridge.streamRuns({ since: '24h' }),
      document: whole(['never-read']),
      onRows: (rows) => drawn.push(theRuns(rows)),
      paint: synchronous,
    })
    expect(result.status).toBe('failed')
    expect(result.sentence).toBe('the runs bucket refused the read')
    expect(result.end).toBeNull()
    expect(drawn).toEqual([['run-1']])
  })
})

describe('a bridge that cannot ask', () => {
  it('reads the whole document instead, and draws nothing as it arrives', async () => {
    const drawn: string[][] = []
    const result = await readStream<RunsListRow>({
      // No `streamRuns` member at all: what `newOpsFrom` answers as
      // `not-available` -- the floor is unset, or this build has no handler.
      stream: () => newOpsFrom({}).streamRuns({ since: '24h' }),
      document: whole(['run-1', 'run-2']),
      onRows: (rows) => drawn.push(theRuns(rows)),
      paint: synchronous,
    })
    expect(result.source).toBe('whole')
    expect(result.status).toBe('complete')
    expect(theRuns(result.rows)).toEqual(['run-1', 'run-2'])
    // The fallback is one answer and one drawing: there are no lines arriving,
    // so nothing is drawn through `onRows` on the way.
    expect(drawn).toEqual([])
    expect(result.sentence).toBeNull()
    expect(result.end).toBeNull()
  })

  it('reads the whole document when the operation refused too', async () => {
    const refusing = newOpsFrom({
      streamRuns: () => Promise.resolve({ ok: false, reason: 'the CLI refused the stream' }),
    })
    const result = await readStream<RunsListRow>({
      stream: () => refusing.streamRuns({ since: '24h' }),
      document: whole(['run-1']),
      paint: synchronous,
    })
    expect(result.source).toBe('whole')
    expect(result.status).toBe('complete')
    expect(theRuns(result.rows)).toEqual(['run-1'])
  })

  it('draws the whole document’s own refusal as the read’s sentence', async () => {
    const result = await readStream<RunsListRow>({
      stream: () => newOpsFrom({}).streamRuns({ since: '24h' }),
      document: () => Promise.resolve({ ok: false as const, sentence: 'no project is configured' }),
      paint: synchronous,
    })
    expect(result.source).toBe('whole')
    expect(result.status).toBe('failed')
    expect(result.sentence).toBe('no project is configured')
    expect(result.rows).toEqual([])
  })
})

describe('a view the reduced mode withholds', () => {
  it('asks neither read, and answers the withheld view rather than a list', async () => {
    let asked = 0
    let fellBack = false
    const result = await readStream<RunsListRow>({
      withheld: true,
      stream: () => {
        asked += 1
        return aFeedBridge([aLine('run-1'), END_LINE]).streamRuns({ since: '24h' })
      },
      document: () => {
        fellBack = true
        return Promise.resolve({ ok: true as const, rows: [] })
      },
      paint: synchronous,
    })
    expect(result.status).toBe('withheld')
    expect(result.source).toBe('none')
    expect(result.rows).toEqual([])
    // No sentence: the console's own word for the withheld view is the caller's
    // (each screen names the view), and nothing here is the CLI's to draw.
    expect(result.sentence).toBeNull()
    expect(result.end).toBeNull()
    // **Neither read was made.** A withheld view whose figures may not be drawn
    // must not have the console spawn the read that would produce them: this is
    // what `hidesView` is for, and it is checked before anything else.
    expect(asked).toBe(0)
    expect(fellBack).toBe(false)
  })
})

describe('a read the section has moved on from', () => {
  it('stops drawing between chunks and answers that it was dropped', async () => {
    const bridge = aFeedBridge([aLine('run-1'), aLine('run-2'), aLine('run-3'), END_LINE])
    const drawn: string[][] = []
    let current = true
    const result = await readStream<RunsListRow>({
      stream: () => bridge.streamRuns({ since: '24h' }),
      document: whole(['never-read']),
      onRows: (rows) => {
        drawn.push(theRuns(rows))
        // The operator's own press can take the section while the rows are
        // still being drawn: the token is what says whose answer stays.
        current = false
      },
      current: () => current,
      paint: synchronous,
    })
    expect(drawn).toEqual([['run-1']])
    expect(result.status).toBe('dropped')
    expect(result.rows).toEqual([])
  })
})
