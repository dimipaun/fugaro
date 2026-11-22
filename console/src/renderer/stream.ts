// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// One read in two shapes: the opt-in line-delimited read a screen draws **as its
// rows arrive**, and the whole-document read it falls back to.
//
// **Why this is a module of its own.** D2906 is "measure first; add opt-in
// streaming to the slow reads", and the second half of the owner's rule of
// 2026-10-31 -- "render parts of screen as soon as we get that data, not wait
// for it all before we show anything" -- is about the reads that answer a list:
// `runs ls` (the Coordinator's jobs), `report` and `budget show` (RS5's two
// tabs). The reads themselves are C-OPS's operations (`src/shared/new-ops.ts`:
// `streamRuns`, `streamSpend`, `streamBudget`) and their wire shape is RS1's
// (NDJSON: `row` lines and one `end` line). What is shared is not the reading --
// each screen still owns the rows it draws and the reader it decodes them with
// -- but the **shape of the read around them**: ask, draw the rows one paint at
// a time, say whether the list finished, fall back to the whole document where
// the operation cannot be asked. That is what lives here.
//
// **The rows arrive as lines, and the drawing follows them.** A feed crosses the
// bridge as one promise (`newOpsFrom` answers the parsed lines at once), so what
// makes rows appear progressively is the chunking below and not a callback: each
// chunk is handed to the caller on its own tick (`StreamPaint`), which is what
// lets the browser paint between them. It is the idiom the Coordinator's detail
// drawing already uses (`entry.ts`'s `coordinatorChunk`): one chunk per tick,
// because a drawing the operator can watch arrive is worth a tick each.
//
// **A missing `end` line is an interrupted read, and that is the whole point of
// the line.** The CLI writes `end` when it finished; a feed without one stopped
// -- a cap in main ended it, the child was killed, the socket closed -- and R1
// says what a section does with a read that did not finish: it draws the
// console's own sentence and a Retry, and it **never draws a partial list as a
// complete one** (`AL`'s acceptance: "a feed that reaches a cap is ended ...
// never a partial list as complete"). So `status` is what a caller branches on,
// and it is set from the line and not from whether rows arrived.
//
// **No answer is not a failure: it is the whole-document read.** Every gap
// `newOpsFrom` can answer -- no handler behind the operation, a floor nothing is
// above yet, a channel that threw, an answer this build cannot decode, **and a
// refusal** -- is the console saying it cannot ask, and the read the screen used
// before the stream existed is still there: `runs.list` for the jobs, `report`
// and `budget show` for RS5's tabs. That is `readStream`'s fallback, and it is
// why this module takes two reads and not one.
//
// The refusal half of that is deliberate and it is the floor's own case: the
// operation is registered before the CLI release that carries `--stream`, so
// main (and, later, the real handler below a floor that is still `null`) answers
// the section a refusal rather than spawning a child, and a console that drew
// that sentence would be refusing a list it can still read. The whole-document
// read is the same command line without `--stream`: where it refuses too, its
// sentence is what the section draws, so the fallback can lose nothing but the
// stream's own words about why the *stream* was refused.
//
// Nothing here touches the DOM. A caller draws in `onRows` from the rows it has
// (the Coordinator redraws its queue from the model, so the head's own count
// grows with the rows) -- this module decides what the read *is*, not what it
// looks like, which is `section.ts`'s bargain from the other side.

import type { NewOpsDocument, NewOpsReply, StreamEnd, StreamFeed } from '../shared/new-ops'

/** STREAM_INTERRUPTED_WORD is what the console says when a feed stopped without
 * an `end` line. There is no CLI sentence to draw -- the CLI wrote none, which
 * is the whole of the condition -- so this is the console's own, in the register
 * R1's sentences use: what happened, and the control beside it is the remedy. */
export const STREAM_INTERRUPTED_WORD = 'the read stopped before it wrote the end of the list'

/** STREAM_ROWS_PER_PAINT is how many rows one chunk holds. It is **one**, and
 * that is a decision about the first row rather than about the thousandth: the
 * first row is on screen in the tick the feed landed in, and every row after it
 * costs one tick, which is what the owner's rule asks for. A feed's rows are
 * bounded on main's side (10,000 lines, 16 MiB -- AL's caps), and the rows a
 * screen draws are a selection of them (the Coordinator's queue keeps the jobs a
 * person is waiting on), so a tick each is affordable and a bigger chunk would
 * be rows nobody sees arrive. */
export const STREAM_ROWS_PER_PAINT = 1

/** StreamStatus is what a read of this shape answers with.
 *
 *   - `complete`: the feed wrote its `end` line, or the whole-document read
 *     answered. **This is the only status whose `rows` are the list.**
 *   - `interrupted`: a feed stopped without an `end` line (or a cap ended it).
 *     `sentence` is the console's own (`STREAM_INTERRUPTED_WORD`).
 *   - `failed`: the CLI wrote an `error` line, or the whole-document read
 *     refused. `sentence` is the CLI's.
 *   - `dropped`: the section moved on while the rows were being drawn. Nothing
 *     is answered at all -- not even the rows read so far, which are a list this
 *     read may not draw.
 *   - `withheld`: the reduced mode withholds this view, so **neither read was
 *     made** (`StreamRead.withheld`). It is not a refusal and not a failure:
 *     there is nothing to retry and nothing the CLI said, and the caller draws
 *     the console's own word for the withheld view. */
export type StreamStatus = 'complete' | 'interrupted' | 'failed' | 'dropped' | 'withheld'

/** StreamSource is which read answered: the opt-in stream, or the whole
 * document it fell back to. A screen that stamps, names or refuses a read the
 * way its operation is named needs the difference (`runs.stream` against
 * `runs.list`). `none` is the withheld read, which asked neither. */
export type StreamSource = 'stream' | 'whole' | 'none'

/** StreamWhole is the whole-document read's answer as this module needs it: the
 * rows the caller's own reader kept, or the sentence it refused with. The caller
 * builds it from its own `documentOf` and its own decoder, because which rows a
 * screen keeps and what a refusal says are the screen's business (`projects
 * .ts`'s `readBudgetStream` and `spend.ts`'s report read are the callers). */
export type StreamWhole<Row> =
  | { readonly ok: true; readonly rows: readonly Row[] }
  | { readonly ok: false; readonly sentence: string }

/** StreamResult is what `readStream` answers.
 *
 * `rows` are the rows this read produced, in the CLI's order -- and they are
 * **the list only where `status` is `complete`**. A caller that draws them for
 * an interrupted or failed read is drawing a partial list as a whole one, which
 * is the reading R1 forbids; what it draws instead is `sentence`, in the
 * section's own place, with the Retry control beside it. */
export interface StreamResult<Row> {
  readonly status: StreamStatus
  readonly source: StreamSource
  readonly rows: readonly Row[]
  readonly sentence: string | null
  readonly end: StreamEnd | null
}

/** StreamPaint is the clock one chunk is drawn on, injected so a case can drive
 * the chunks without waiting for the host's timer (the way `SectionTimers` is).
 * The default is the host's own `setTimeout(step, 0)`. */
export type StreamPaint = (step: () => void) => void

/** NODE_PAINT is that default: the next tick, which is late enough for the
 * browser to have painted the chunk before it. */
export const NODE_PAINT: StreamPaint = (step: () => void) => {
  setTimeout(step, 0)
}

/** StreamRead is one screen's stream read.
 *
 * `stream` is the opt-in operation, typed by C-OPS: the caller closes over the
 * payload (`runs.stream` takes the window and nothing else) **and over the
 * reader of the rows it draws**, so this module never sees a row shape of the
 * contract's choosing -- the Coordinator hands it `QueueRunRow`s it selected
 * with `waitingRuns`, RS5 hands it the report's own rows.
 *
 * `document` is the read that runs where the operation cannot be asked (below).
 *
 * `onRows` is called once per chunk with every row read so far, in the CLI's
 * order -- and **not at all** for the whole-document read, which is one answer
 * and one drawing.
 *
 * `current` is whether this read is still the section's: the caller asks its own
 * sequence token (`section.ts`'s `current`) and its own screen. It is asked
 * before every chunk, so a read the operator has replaced stops drawing rather
 * than drawing under the newer answer, and it answers `dropped`.
 *
 * `withheld` is the reduced mode's own gate, and it is asked **before anything
 * is spawned**: a view this mode withholds (`hidesView`) is one whose figures
 * the console may not draw, and a stream that read the rows anyway would be
 * spawning the very read whose answer it must throw away. The caller asks
 * `hidesView` (`shared/version.ts`) and hands the answer here rather than in
 * each screen's own read, so both of RS5's screens reach the gate through this
 * one door and neither has to remember it. The read answers `withheld` without
 * asking either operation, and the caller draws its own word for the view.
 *
 * `paint` is the clock, and only a case passes one. */
export interface StreamRead<Row> {
  readonly stream: () => Promise<NewOpsReply<StreamFeed<Row>>>
  readonly document: () => Promise<StreamWhole<Row>>
  readonly onRows?: (rows: readonly Row[]) => void
  readonly current?: () => boolean
  readonly withheld?: boolean
  readonly paint?: StreamPaint
}

/** readStream makes one read of the shape this file's header describes, and
 * answers what it read. It draws nothing itself: the chunks go to `onRows`, and
 * every string in the answer is either the CLI's or this file's own.
 *
 * A read the caller marked `withheld` asks neither operation and answers
 * `withheld`: the reduced mode withholds the view, so there is no answer to
 * read and no sentence of the CLI's to carry. */
export async function readStream<Row>(options: StreamRead<Row>): Promise<StreamResult<Row>> {
  const still = options.current ?? ((): boolean => true)
  const paint = options.paint ?? NODE_PAINT
  if (options.withheld === true) {
    return { status: 'withheld', source: 'none', rows: [], sentence: null, end: null }
  }
  const answer = await options.stream()
  if (!answer.ok) return wholeDocument(options)
  const feed = answer.value
  const rows: Row[] = []
  let pending = 0
  for (const row of feed.rows) {
    if (!still()) return dropped()
    rows.push(row)
    pending += 1
    if (pending < STREAM_ROWS_PER_PAINT) continue
    pending = 0
    options.onRows?.([...rows])
    await painted(paint)
  }
  if (pending > 0) options.onRows?.([...rows])
  if (feed.failure !== null) {
    return { status: 'failed', source: 'stream', rows, sentence: feed.failure, end: null }
  }
  if (feed.end === null) {
    return { status: 'interrupted', source: 'stream', rows, sentence: STREAM_INTERRUPTED_WORD, end: null }
  }
  return { status: 'complete', source: 'stream', rows, sentence: null, end: feed.end }
}

/** wholeDocument is the read that runs where the stream cannot be asked, and it
 * is the screen's own read: the caller's `document` closure asks it, reads it
 * with the screen's own reader and answers its rows or its refusal. */
async function wholeDocument<Row>(options: StreamRead<Row>): Promise<StreamResult<Row>> {
  const answer = await options.document()
  if (!answer.ok) {
    return { status: 'failed', source: 'whole', rows: [], sentence: answer.sentence, end: null }
  }
  return { status: 'complete', source: 'whole', rows: answer.rows, sentence: null, end: null }
}

/** dropped is the read the section has moved on from. It answers no rows at all,
 * which is the point: a list this read may not draw is not handed back for a
 * caller to draw by mistake. */
function dropped<Row>(): StreamResult<Row> {
  return { status: 'dropped', source: 'stream', rows: [], sentence: null, end: null }
}

/** BUDGET_KILL_ROW_SLUG is the feed's own marker for the project's kill switch:
 * the row `budget show --stream` writes in place of the document's `global`
 * block, whose slug is the literal `kill` (`budgetKillRowSlug`,
 * `internal/cli/budget_stream.go`). The word is a scope no repository's own slug
 * is -- the writer says so where it defines the sentinel, and a repository's row
 * is the document's own element, slug and all -- which is the whole of how a
 * reader tells the two kinds of row apart, and it is why the split below is a
 * rule of the wire and not a screen's guess. */
export const BUDGET_KILL_ROW_SLUG = 'kill'

/** BudgetFeedRows is a streamed caps feed's rows, split into the two things
 * they are: the repositories' rows, in the feed's order, and the project's kill
 * switch.
 *
 * **Why this lives here and not in either screen.** `budget.stream` is read by
 * both of RS5's tabs -- the Spend screen's caps section and the Project tab --
 * and the sentinel above is one rule with one spelling: a second reader that
 * spelled `kill` itself would be a copy that drifts from the CLI's. It is *not*
 * a document: what each screen wraps these rows in (`capsFromFeed`'s caps panel,
 * `budgetFromFeed`'s document) is that screen's own, and this module knows no
 * screen. `killed` is separate from `kill` because the row's presence is the
 * fact "this is the project's switch" and its `kill: null` the fact "it is not
 * on" (`budgetKillRow`, `internal/cli/budget_stream.go`): a feed that carried no
 * such row leaves the switch **unknown**, which is not the same as off. */
export interface BudgetFeedRows {
  readonly repos: readonly NewOpsDocument[]
  readonly kill: unknown
  readonly killed: boolean
}

export function budgetFeedRows(rows: readonly NewOpsDocument[]): BudgetFeedRows {
  const repos: NewOpsDocument[] = []
  let kill: unknown = null
  let killed = false
  for (const row of rows) {
    if (row === null || typeof row !== 'object') continue
    if (row['slug'] === BUDGET_KILL_ROW_SLUG) {
      killed = true
      kill = row['kill'] ?? null
      continue
    }
    repos.push(row)
  }
  return { repos, kill, killed }
}

/** feedRows is one of a document's arrays as the feed's own row type: the
 * elements that are JSON objects, which is every row of a document this console
 * reads (`new-ops.ts`'s `rowOf` refuses the rest one line at a time, where the
 * rows are drawn). It is how a fallback read answers `StreamWhole.rows`: the
 * whole document's array is the list a feed would have written as `row` lines,
 * and a `row` line carries an object by the contract's own reading.
 *
 * **Why it lives here.** Both of RS5's caps readers fall back to a document's
 * array (`report`'s `rows` and `budget show`'s `repos`) through this module, and
 * the filter is one rule with one spelling: a screen that kept an element the
 * other dropped would answer a different list for the same document. */
export function feedRows(array: unknown): readonly NewOpsDocument[] {
  if (!Array.isArray(array)) return []
  return array.flatMap((row) =>
    row !== null && typeof row === 'object' && !Array.isArray(row) ? [row as NewOpsDocument] : [],
  )
}

/** painted is one chunk's tick, as a promise the loop can await. */
function painted(paint: StreamPaint): Promise<void> {
  return new Promise<void>((resolve) => {
    paint(resolve)
  })
}
