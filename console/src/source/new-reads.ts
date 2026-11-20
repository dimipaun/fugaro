// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The six new reads' handlers (desktop-console improvements plan, task SRC; the
// contract they answer is `docs/console-improvements/contract-ops.md`, and the
// shapes are `src/shared/new-ops.ts`, task C-OPS).
//
// Three feeds and three one-shot reads:
//
//   - `runs.stream`, `spend.stream` and `budget.stream` are the CLI's
//     `--stream` mode, which is NDJSON: a row a line, an `end` line, or an
//     `error` line. A feed has **no document of its own**, so its answer is
//     `{document: {lines: [...]}}` -- the parsed lines, in the order the CLI
//     wrote them -- and everything a screen reads (rows, `end`, failure) is
//     decoded from those lines by `new-ops.ts`, in one place, so the stream and
//     the whole-document read of the same command cannot draw two lists.
//   - `whoami`, `image.catalog.ls` and `recipes.ls` are ordinary one-shot reads,
//     and their answer is the CLI's own document under `document`, unchanged.
//
// **The bounds are the console's, and a feed that reaches one ends as an
// interrupted read.** A stream has no length on the wire, so nothing in a
// child's output says "this is too much": the console stops the read itself at
// `NEW_READ_BOUNDS` -- a line longer than `MAX_FEED_LINE_LENGTH`, more than
// `NEW_READ_MAX_ROWS` rows, or more than `NEW_READ_MAX_BYTES` of them -- and
// answers the lines it kept **without an `end` line**, which is exactly the
// answer the contract reads as interrupted (`runs.stream`'s screen draws Retry).
// The alternative -- a refusal -- would throw away the rows that did arrive and
// tell the operator nothing about how far the read got, and the alternative
// again -- reading on and dropping what does not fit -- would draw a complete
// list that happened to be short, which is the misreading the whole cap exists
// to prevent.
//
// **The child is `spawn.ts`'s, not this module's.** `startSidecar` is the
// console's own supervised-child reader: it frames the lines, holds each one to
// a length, decodes NDJSON documents, and stops by signal -- and on the WSL route
// it is the only thing that starts the wrapper at all. A read here hands it a
// plan and a line ceiling and does none of that itself. What this module adds is
// the three bounds above, and the one thing a *finite* read needs that a
// long-lived feed does not: the child exiting cleanly (exit 0) ends the read
// instead of restarting it (`onRestart`, below).
//
// **Everything here is typed off the contract, and nothing is registered here.**
// The operation names of this set do not exist in `OperationName` until task AL
// adds their rows to `ipc.ts`'s allow-list -- so there is no `handlersFor` row to
// add them to, and their plans cannot be built either (`planFor` throws for a
// name with no row). The wiring task (I-OPS) spreads `newReadHandlers` into the
// `gated` table and passes AL's own rows and floors; what this module promises
// it is the *behaviour* of the six reads, tested against a stub child here.

import {
  MAX_FEED_LINE_LENGTH,
  READ_TIMEOUT_MS,
  cliSentence,
  planFor,
  runToCompletion,
  startSidecar,
} from '../main/spawn'
import type {
  ChildSpawner,
  JSONObject,
  Sidecar,
  SidecarEnd,
  SpawnOperationName,
  SpawnPlan,
  Timers,
} from '../main/spawn'
import type { SpawnLogWriter } from '../main/spawnlog'
import type { ValidatedArguments } from '../main/ipc'
import { NEW_OPERATION_TEXT_CLASSES } from '../shared/new-ops'
import { LITERAL_PLACEHOLDER, sanitizeText } from '../shared/sanitize'
import type { TextClass } from '../shared/sanitize'
import { UNREADABLE_VERSION, meetsCliFloor } from '../shared/version'
import { parseStdout } from './cli'
import { SourceRefusal, refusalFrom } from './source'

// ---------------------------------------------------------------------------
// The names the six reads are, and which half each one is.
// ---------------------------------------------------------------------------

/** NewReadFeedName is the three reads whose answer is a stream of lines. */
export type NewReadFeedName = 'runs.stream' | 'spend.stream' | 'budget.stream'

/** NewReadDocumentName is the three reads whose answer is one document. */
export type NewReadDocumentName = 'whoami' | 'image.catalog.ls' | 'recipes.ls'

/** NewReadName is all six, in the contract's own order. */
export type NewReadName = NewReadFeedName | NewReadDocumentName

/** NEW_READ_FEED_NAMES is the feed half as a value. */
export const NEW_READ_FEED_NAMES: readonly NewReadFeedName[] = Object.freeze([
  'runs.stream',
  'spend.stream',
  'budget.stream',
])

/** NEW_READ_DOCUMENT_NAMES is the one-shot half as a value. */
export const NEW_READ_DOCUMENT_NAMES: readonly NewReadDocumentName[] = Object.freeze([
  'whoami',
  'image.catalog.ls',
  'recipes.ls',
])

/** NEW_READ_NAMES is both halves, which is the set of handlers this module
 * answers and the set a test walks. */
export const NEW_READ_NAMES: readonly NewReadName[] = Object.freeze([
  ...NEW_READ_FEED_NAMES,
  ...NEW_READ_DOCUMENT_NAMES,
])

/**
 * NEW_READ_WORDS is how each read names itself in the one sentence the console
 * writes about it (its floor refusal below). The words are the contract's own
 * (`WHERE`, `new-ops.ts`), so a screen that shows one of these sentences and a
 * screen that shows a skew sentence name the same read the same way.
 */
export const NEW_READ_WORDS: Readonly<Record<NewReadName, string>> = Object.freeze({
  'runs.stream': 'the runs stream',
  'spend.stream': 'the spend stream',
  'budget.stream': 'the caps stream',
  whoami: 'the whoami document',
  'image.catalog.ls': 'the image catalog document',
  'recipes.ls': 'the recipes document',
})

// ---------------------------------------------------------------------------
// The bounds, and the floor.
// ---------------------------------------------------------------------------

/**
 * NewReadBounds is what one feed is read under: the longest line the console
 * keeps, how many lines, and how much text in all. The three are one object
 * because they are one decision -- how much of a stream the console holds in
 * memory before it ends the read -- and because the row/byte pair is task AL's
 * (its constants live in `ipc.ts`, beside the table they describe); a caller
 * that has those constants passes them, and the values below are what the read
 * means until then.
 */
export interface NewReadBounds {
  readonly maxLineLength: number
  readonly maxRows: number
  readonly maxBytes: number
}

/** NEW_READ_MAX_ROWS is the most rows one feed may answer with. 10,000 rows is
 * ten rows a second for the longest window the console reads. */
export const NEW_READ_MAX_ROWS = 10_000

/** NEW_READ_MAX_BYTES is how much text one feed may answer with, in all. */
export const NEW_READ_MAX_BYTES = 16 * 1024 * 1024

/** NEW_READ_BOUNDS is the three of them, as a read is bound unless its caller
 * says otherwise. */
export const NEW_READ_BOUNDS: NewReadBounds = Object.freeze({
  maxLineLength: MAX_FEED_LINE_LENGTH,
  maxRows: NEW_READ_MAX_ROWS,
  maxBytes: NEW_READ_MAX_BYTES,
})

/**
 * NEW_READ_FLOOR_UNSET is what a read whose floor is still `null` answers: the
 * floor is unset until a CLI release carries the command (the idiom
 * `LICENCE_KEY_CLI_FLOOR` and `NO_APPLY_CLI_FLOOR` follow, `version.ts`), and a
 * console that read the command anyway would spawn an argv the installed CLI
 * answers with `unknown command`.
 */
export const NEW_READ_FLOOR_UNSET = 'needs a fugaro release no CLI has yet'

/** newReadFloorSentence is the console's own sentence for a read it will not
 * spawn (`screens/seats.ts`'s `SEATS_CLI_FLOOR_WORD` is the same sentence for
 * the seat estimate). A version line the console could not read is named as
 * such rather than drawn as an empty string. */
export function newReadFloorSentence(
  name: NewReadName,
  floor: string | null,
  cliVersion: string,
): string {
  const what = NEW_READ_WORDS[name]
  if (floor === null) return `${what} ${NEW_READ_FLOOR_UNSET}`
  return `${what} needs fugaro ${floor} and this CLI is ${
    cliVersion === '' ? UNREADABLE_VERSION : cliVersion
  }`
}

/** NO_FLOORS is every floor unset, which is the default and the safe direction:
 * a wiring that named none of AL's constants spawns nothing rather than a child
 * whose command may not exist. */
const NO_FLOORS: Readonly<Record<NewReadName, string | null>> = Object.freeze({
  'runs.stream': null,
  'spend.stream': null,
  'budget.stream': null,
  whoami: null,
  'image.catalog.ls': null,
  'recipes.ls': null,
})

// ---------------------------------------------------------------------------
// The text classes.
// ---------------------------------------------------------------------------

/** isText is a value a class applies to: a string, or a list of strings (a
 * feed's `tags`, a recipe's `notes`, the setup feed's `commands`). Anything else
 * is left exactly as it arrived, for the reader that names it -- a `role` that is
 * a number is a document `new-ops.ts` refuses by name, and a class here would
 * turn that refusal into a placeholder nobody could diagnose. */
function isText(raw: unknown): boolean {
  if (typeof raw === 'string') return true
  return Array.isArray(raw) && raw.every((item) => typeof item === 'string')
}

/** drawnThroughClass is one named field through its class. A value the class
 * refuses is drawn as the console's placeholder rather than dropped: the wire
 * reads a missing member as "the CLI has nothing" (the contract's rule 5), so a
 * field that vanished would be one answer and a withheld field would be another.
 * The refused value itself is never carried, which is the class's whole point. */
function drawnThroughClass(raw: unknown, textClass: TextClass, field: string): unknown {
  if (Array.isArray(raw)) {
    return raw.map((item) => drawnThroughClass(item, textClass, field)) as readonly unknown[]
  }
  const drawn = sanitizeText(raw, textClass, field)
  return drawn.ok ? drawn.text : LITERAL_PLACEHOLDER
}

/**
 * throughClasses returns a document with every field the contract names put
 * through its class (`NEW_OPERATION_TEXT_CLASSES`, `new-ops.ts`), and every
 * other value carried as it arrived.
 *
 * The walk is a **work list rather than recursion**, because the document is
 * untrusted text from outside the console: V8's `JSON.parse` is iterative, so a
 * 4 MiB line of nested arrays parses into a document deeper than any call stack,
 * and a recursive walk of one would throw where this must answer. Each container
 * is rebuilt (the answer never shares an object with the parsed document), and a
 * class names a field wherever it appears -- the table is keyed by the field's
 * own wire name, not by a path, so `name` is a label in a recipe step and in an
 * image entry alike.
 */
export function throughClasses(document: JSONObject): JSONObject {
  const root: Record<string, unknown> = {}
  const open: { readonly from: Record<string, unknown> | readonly unknown[]; readonly to: Record<string, unknown> | unknown[] }[] = [
    { from: document, to: root },
  ]
  for (;;) {
    const frame = open.pop()
    if (frame === undefined) break
    const pairs: readonly (readonly [string, unknown])[] = Array.isArray(frame.from)
      ? frame.from.map((item, index) => [String(index), item] as const)
      : Object.entries(frame.from)
    for (const [key, raw] of pairs) {
      const textClass = Array.isArray(frame.from) ? undefined : NEW_OPERATION_TEXT_CLASSES[key]
      const value = textClass !== undefined && isText(raw) ? drawnThroughClass(raw, textClass, key) : raw
      const nested = value !== null && typeof value === 'object'
      const rebuilt: unknown = nested ? (Array.isArray(value) ? [] : {}) : value
      if (Array.isArray(frame.to)) frame.to.push(rebuilt)
      else frame.to[key] = rebuilt
      if (nested) {
        open.push({
          from: value as Record<string, unknown> | readonly unknown[],
          to: rebuilt as Record<string, unknown> | unknown[],
        })
      }
    }
  }
  return root
}

// ---------------------------------------------------------------------------
// Where a read's answer comes from.
// ---------------------------------------------------------------------------

/**
 * NewReadSource is where one read's document comes from, and it is the seam the
 * two sources of this console meet on: the CLI-backed reads below (a child, its
 * stdout, and the three bounds above) and the replaying `FixtureSource`
 * (`fixture.ts`, which answers from the documents a fixture holds). The handlers
 * are written against this and not against a child, so the same handler serves
 * an e2e spec with no installation and no binary behind it.
 */
export interface NewReadSource {
  /** readDocument runs one one-shot read and answers the CLI's own document. */
  readDocument(name: NewReadDocumentName, args: ValidatedArguments): Promise<JSONObject>
  /** readFeed runs one feed and answers its document, `{lines: [...]}`.
   *
   * `bounds` is the reader's, not the answer's: a source that holds a committed
   * recording (a fixture) ignores it, and a source that reads a live pipe stops
   * the read at it. */
  readFeed(name: NewReadFeedName, args: ValidatedArguments, bounds: NewReadBounds): Promise<JSONObject>
}

/** NewReadPlan is one read as a child process: AL's argv row (or the `wsl.exe`
 * route), the environment, and the backoff a supervised feed is restarted on. */
export type NewReadPlanner = (name: NewReadName, args: ValidatedArguments) => SpawnPlan

/**
 * newReadPlanner is the production planner: `planFor` over the rows task AL adds
 * to the allow-list. The cast is what stands in for those rows while they do not
 * exist -- a name without one throws inside `planFor`, before any process
 * exists, which is the allow-list's own refusal and not a hole in it.
 */
export function newReadPlanner(context: Parameters<typeof planFor>[2]): NewReadPlanner {
  return (name, args) => planFor(name as SpawnOperationName, args, context)
}

/** CliNewReadsOptions is the CLI-backed source's own. Everything but `plan` is a
 * seam for a test: a stub `spawner` is how a read is driven from a child whose
 * bytes the test writes. */
export interface CliNewReadsOptions {
  readonly plan: NewReadPlanner
  readonly spawner?: ChildSpawner | undefined
  readonly log?: SpawnLogWriter | undefined
  readonly timers?: Timers | undefined
  /** `deadlineMs` is how long one feed may take in all, `README_TIMEOUT_MS`
   * unless a caller says otherwise. A read of a finite command must end: a
   * supervisor that waited forever on a wedged child would leave a screen busy
   * with no Retry. `runToCompletion`'s deadline is the same one, for the
   * one-shot half. */
  readonly deadlineMs?: number | undefined
}

const NODE_TIMERS: Timers = {
  setTimeout: (handler, ms) => setTimeout(handler, ms),
  clearTimeout: (handle) => {
    clearTimeout(handle)
  },
}

/** lineLength is how much text one kept line is, which is what the byte bound
 * counts: the answer carries parsed lines, so the text the console holds for a
 * line is the JSON its document is written back as. */
function lineLength(document: JSONObject): number {
  return JSON.stringify(document).length
}

/** cliNewReads is the six reads over a `fugaro` child. */
export function cliNewReads(options: CliNewReadsOptions): NewReadSource {
  const timers = options.timers ?? NODE_TIMERS
  const deadlineMs = options.deadlineMs ?? READ_TIMEOUT_MS

  const readDocument = async (
    name: NewReadDocumentName,
    args: ValidatedArguments,
  ): Promise<JSONObject> => {
    const plan = options.plan(name, args)
    const result = await runToCompletion(plan, {
      ...(options.log === undefined ? {} : { log: options.log }),
      ...(options.spawner === undefined ? {} : { spawner: options.spawner }),
    })
    if (result.code !== 0) throw refusalFrom(name, cliSentence(result.stderr))
    const read = parseStdout(result.stdout)
    if (read.document === null) {
      throw refusalFrom(name, cliSentence(result.stderr) ?? `${name} printed no JSON document`)
    }
    return read.document
  }

  const readFeed = (
    name: NewReadFeedName,
    args: ValidatedArguments,
    bounds: NewReadBounds,
  ): Promise<JSONObject> =>
    new Promise<JSONObject>((resolve, reject) => {
      const lines: JSONObject[] = []
      let held = 0
      let settled = false
      let stopping = false
      let sidecar: Sidecar | null = null
      let deadline: ReturnType<typeof setTimeout> | null = null

      const settle = (action: () => void): void => {
        if (settled) return
        settled = true
        if (deadline !== null) {
          timers.clearTimeout(deadline)
          deadline = null
        }
        action()
      }

      // atABound stops the read where it is: the answer is what arrived before
      // the bound, and no `end` line can arrive after it. The stop is
      // `startSidecar`'s own (SIGTERM, then the grace and the kill inside a
      // distribution), so a capped read leaves no child behind.
      const atABound = (): void => {
        if (stopping) return
        stopping = true
        void sidecar?.stop()
      }

      const end = (result: SidecarEnd): void => {
        settle(() => {
          if (result.reason === 'refused' || result.reason === 'unstartable') {
            reject(refusalFrom(name, result.sentence))
            return
          }
          resolve({ lines })
        })
      }

      sidecar = startSidecar(
        options.plan(name, args),
        {
          onFrame: (frame) => {
            if (settled || stopping) return
            lines.push(frame.document)
            held += lineLength(frame.document)
            if (lines.length >= bounds.maxRows || held >= bounds.maxBytes) atABound()
          },
          // A notice is a line the console could not read: one longer than the
          // ceiling (dropped by the supervisor) or one that is not a JSON
          // object. Either way the feed is not one this console read in full, so
          // it ends here as an interrupted read -- never as a complete list with
          // a row quietly missing.
          onNotice: () => {
            atABound()
          },
          // A finite read does not restart: the child exiting (exit 0 included)
          // ends it, which is the one place this pump differs from the
          // long-lived feeds the supervisor was written for.
          onRestart: () => {
            atABound()
          },
          onEnded: end,
        },
        {
          maxLineLength: bounds.maxLineLength,
          timers,
          ...(options.log === undefined ? {} : { log: options.log }),
          ...(options.spawner === undefined ? {} : { spawner: options.spawner }),
        },
      )
      deadline = timers.setTimeout(() => {
        atABound()
      }, deadlineMs)
    })

  return { readDocument, readFeed }
}

// ---------------------------------------------------------------------------
// The handlers.
// ---------------------------------------------------------------------------

/** NewReadAnswer is one read's answer: the document, which is the `data` half
 * of the console's reply (`registerConsoleHandlers` wraps it). */
export interface NewReadAnswer {
  readonly document: JSONObject
}

/** NewReadHandler is one read's handler, `ConsoleHandler` for the six names
 * `ipc.ts` does not carry yet. It answers asynchronously and throws a
 * `SourceRefusal` for anything the CLI refused. */
export type NewReadHandler = (args: ValidatedArguments) => Promise<NewReadAnswer>

/** NewReadHandlers is the six of them by name, the shape I-OPS spreads into the
 * `gated` table. */
export type NewReadHandlers = { readonly [Name in NewReadName]: NewReadHandler }

/** NewReadHandlerDeps is what the six handlers are built from. */
export interface NewReadHandlerDeps {
  readonly source: NewReadSource
  /** `cliVersion` is the resolved binary's own version line -- what the floor is
   * asked of -- or `''` when the console could not read one. */
  readonly cliVersion: string
  /** `floors` is task AL's one floor constant per read, `null` while no release
   * carries the command. Unset by default: every read refuses its own sentence
   * rather than spawning an argv nothing answers. */
  readonly floors?: Readonly<Record<NewReadName, string | null>> | undefined
  readonly bounds?: NewReadBounds | undefined
}

/** refuseBelowFloor throws the console's own sentence for a read this CLI cannot
 * answer, before anything is spawned. */
function refuseBelowFloor(name: NewReadName, floor: string | null, cliVersion: string): void {
  if (floor !== null && meetsCliFloor(cliVersion, floor)) return
  throw new SourceRefusal(newReadFloorSentence(name, floor, cliVersion))
}

/**
 * newReadHandlers answers the six reads: the floor first (a CLI below it, or a
 * floor no release names yet, is refused with the console's sentence and Retry,
 * and **no child is spawned**), then the read, then every field the contract
 * names through its class.
 */
export function newReadHandlers(deps: NewReadHandlerDeps): NewReadHandlers {
  const bounds = deps.bounds ?? NEW_READ_BOUNDS
  const floors = deps.floors ?? NO_FLOORS
  const answer = async (name: NewReadName, read: () => Promise<JSONObject>): Promise<NewReadAnswer> => {
    refuseBelowFloor(name, floors[name], deps.cliVersion)
    return { document: throughClasses(await read()) }
  }
  return {
    'runs.stream': (args) => answer('runs.stream', () => deps.source.readFeed('runs.stream', args, bounds)),
    'spend.stream': (args) => answer('spend.stream', () => deps.source.readFeed('spend.stream', args, bounds)),
    'budget.stream': (args) => answer('budget.stream', () => deps.source.readFeed('budget.stream', args, bounds)),
    whoami: (args) => answer('whoami', () => deps.source.readDocument('whoami', args)),
    'image.catalog.ls': (args) =>
      answer('image.catalog.ls', () => deps.source.readDocument('image.catalog.ls', args)),
    'recipes.ls': (args) => answer('recipes.ls', () => deps.source.readDocument('recipes.ls', args)),
  }
}
