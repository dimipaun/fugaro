// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The six new reads' handlers (desktop-console improvements plan, task SRC).
//
// What this file holds, in the order the acceptance states it:
//
//   1. **each read from a stub child** -- all six names, each driven from one
//      child whose bytes the test writes, and the answer decoded by the
//      contract's own reader (`decodeNewOperationAnswer`, `shared/new-ops.ts`),
//      so "SRC's answer is C-OPS's shape" is a fact this file asserts and not a
//      claim in a comment;
//   2. **the caps** -- a line longer than `MAX_FEED_LINE_LENGTH`, more than
//      10,000 rows, and more than 16 MiB of them -- each of which must end the
//      read as an **interrupted** one: the lines that arrived, no `end` line,
//      and a child that was stopped. The red step of this task is the first of
//      them: a reader that accepted an over-long line (a plain `runToCompletion`
//      over a feed's stdout, say) leaves the `end` in the answer and fails here;
//   3. **the floor** -- a CLI below it, and a floor still `null` -- refused with
//      the console's own sentence, and **nothing spawned**;
//   4. **the text classes** -- every field the contract names through its class,
//      a field the class refuses drawn as the console's placeholder rather than
//      dropped, and a field the contract does not name carried as it arrived;
//   5. **the fixture source** -- the same six answers for the e2e specs, from
//      the documents a `FixtureSource` holds.
//
// The stub child is a fake rather than a script on `PATH` for the reason
// `spawn.test.ts` gives: what is under test is the console's own reading of a
// child's bytes, and a real process would only add timing.

import { readFileSync } from 'node:fs'

import { describe, expect, it } from 'vitest'

import type { JSONObject } from '../../src/main/spawn'
import { MAX_FEED_LINE_LENGTH } from '../../src/main/spawn'
import type { ChildSpawner, SpawnOptionsLike, SpawnPlan, SpawnedChildLike } from '../../src/main/spawn'
import { decodeNewOperationAnswer } from '../../src/shared/new-ops'
import { LITERAL_PLACEHOLDER, MAX_TEXT_INPUT_LENGTH } from '../../src/shared/sanitize'
import { UNREADABLE_VERSION } from '../../src/shared/version'
import { FixtureSource, loadFixture } from '../../src/source/fixture'
import {
  NEW_READ_BOUNDS,
  NEW_READ_MAX_BYTES,
  NEW_READ_MAX_ROWS,
  NEW_READ_NAMES,
  NEW_READ_WORDS,
  cliNewReads,
  newReadFloorSentence,
  newReadHandlers,
  throughClasses,
} from '../../src/source/new-reads'
import type { NewReadName, NewReadPlanner, NewReadSource } from '../../src/source/new-reads'
import { SourceRefusal } from '../../src/source/source'
import { SYNTHETIC_WHERE } from './synthetic'

// ---------------------------------------------------------------------------
// A child whose bytes, exit and signals the test writes.
// ---------------------------------------------------------------------------

/** StubChild is the child one read is driven from. */
interface StubChild extends SpawnedChildLike {
  /** `write` is one chunk of the child's stdout. */
  write(text: string): void
  /** `finish` is the child exiting and closing, which is what a reader that
   * settles on `close` waits for. */
  finish(code?: number | null): void
  readonly killed: readonly (NodeJS.Signals | undefined)[]
}

/** stubChild is a child that starts, says nothing and exits when told. */
function stubChild(): StubChild {
  const out: ((chunk: Buffer | string) => void)[] = []
  const err: ((chunk: Buffer | string) => void)[] = []
  const exits: ((code: number | null, signal: string | null) => void)[] = []
  const closes: ((code: number | null, signal: string | null) => void)[] = []
  const errors: ((error: Error) => void)[] = []
  const killed: (NodeJS.Signals | undefined)[] = []
  // Node emits `exit` and then `close`; the stub fires them together, because
  // `close` is what both readers below settle on.
  const settle = (code: number | null, signal: string | null): void => {
    for (const listener of exits) listener(code, signal)
    for (const listener of closes) listener(code, signal)
  }
  return {
    stdout: {
      on: (_event: string, listener: (chunk: Buffer | string) => void) => {
        out.push(listener)
      },
      destroy: () => undefined,
    },
    stderr: {
      on: (_event: string, listener: (chunk: Buffer | string) => void) => {
        err.push(listener)
      },
      destroy: () => undefined,
    },
    // Node fires `spawn` on the tick after the process exists, before any I/O.
    // This fake fires it at registration instead: the ordering the spawn log
    // depends on is the same (a line exists before the first byte) without a
    // tick for every test to wait out (`spawn.test.ts` does the same).
    on: ((event: string, listener: (...args: never[]) => void) => {
      if (event === 'spawn') (listener as () => void)()
      else if (event === 'error') errors.push(listener as (error: Error) => void)
      else if (event === 'exit') {
        exits.push(listener as (code: number | null, signal: string | null) => void)
      } else closes.push(listener as (code: number | null, signal: string | null) => void)
      return undefined
    }) as SpawnedChildLike['on'],
    kill: (signal?: NodeJS.Signals) => {
      killed.push(signal)
      // A child the console stops is one it must stop *waiting for*: the
      // supervisor settles the read on `close`, so a stub that only recorded the
      // signal would leave every capped read unresolved.
      settle(null, signal ?? 'SIGTERM')
      return true
    },
    write: (text: string) => {
      for (const listener of out) listener(text)
    },
    finish: (code: number | null = 0) => {
      settle(code, null)
    },
    killed,
  }
}

/** SpawnCall is what one read handed the spawner. */
interface SpawnCall {
  readonly command: string
  readonly args: readonly string[]
  readonly options: SpawnOptionsLike
}

/** StubSpawner is the recording spawner: one child, and the calls it saw. */
function stubSpawner(child: StubChild): ChildSpawner & { readonly calls: readonly SpawnCall[] } {
  const calls: SpawnCall[] = []
  const spawner = ((command: string, args: readonly string[], options: SpawnOptionsLike) => {
    calls.push({ command, args, options })
    return child
  }) as unknown as ChildSpawner & { calls: SpawnCall[] }
  Object.defineProperty(spawner, 'calls', { get: () => calls })
  return spawner
}

/** aPlan is one read's plan. Only the fields a child's start reads matter here:
 * the argv, the directory and the environment. */
function aPlan(argv: readonly string[]): SpawnPlan {
  return {
    // The plan's own `operation` is the allow-list's word for the row that built
    // it; the reader below never looks at it, and AL's rows are what makes the
    // real one a `NewReadName`.
    operation: 'runs.list',
    command: '/usr/local/bin/fugaro',
    prefix: [],
    argv,
    wrapper: null,
    cwd: '/work/repo',
    env: {},
    project: 'fugaro',
    backoff: { minMs: 400, maxMs: 8000 },
  }
}

/** plannerFor is a planner that answers one argv for every read, so a test
 * asserts the argv the read was spawned with without the allow-list's rows. */
function plannerFor(argv: readonly string[] = ['runs', 'ls', '--stream', '--json']): NewReadPlanner {
  return () => aPlan(argv)
}

/** floorsAt is every read's floor at one line, for the tests that are about
 * something else than the floor. */
function floorsAt(floor: string | null): Record<NewReadName, string | null> {
  return Object.fromEntries(NEW_READ_NAMES.map((name) => [name, floor])) as Record<
    NewReadName,
    string | null
  >
}

// ---------------------------------------------------------------------------
// The six reads' own documents.
// ---------------------------------------------------------------------------

/** A_RUN_ROW is one `runs ls --json` row, in the shape `decodeRunsList` reads
 * (the stream and the whole-document read share that reader). */
const A_RUN_ROW: JSONObject = {
  run: '20261031-101502-9f3c',
  repo: 'owner/repo-a',
  run_id: '20261031-101502-9f3c-full',
  status: 'done',
  terminal: true,
  reason: '',
  title: 'the runs stream, read from one child',
  plan_task: '',
  created: '2026-10-31T10:15:02Z',
}

/** ReadCase is one read: the argv it is spawned with and what the child prints.
 *
 * **A feed's lines are the CLI's own line shape**, `{"type":"row","row":{...}}`
 * (`streamRowLine`, `internal/cli/runs_stream.go`; the bytes
 * `internal/cli/testdata/runs_stream.golden.ndjson` pins). A row written as the
 * line itself would be a shape no CLI writes, and a reader that accepted it
 * would answer rows whose members are `type` and `row` -- the mistake this
 * shape is here to catch. */
interface ReadCase {
  readonly name: NewReadName
  readonly argv: readonly string[]
  /** `lines` is a feed's lines in order; a one-shot read answers `document`. */
  readonly lines?: readonly JSONObject[]
  readonly document?: JSONObject
}

const CASES: readonly ReadCase[] = [
  {
    name: 'runs.stream',
    argv: ['runs', 'ls', '--stream', '--json', '--since', '24h'],
    lines: [
      { type: 'row', row: A_RUN_ROW },
      { type: 'end', version: 1, count: 1 },
    ],
  },
  {
    name: 'spend.stream',
    argv: ['report', '--stream', '--json'],
    lines: [
      { type: 'row', row: { key: '2026-10-31', spent_usd: 1.5 } },
      { type: 'end', count: 1 },
    ],
  },
  {
    name: 'budget.stream',
    argv: ['budget', 'show', '--stream', '--json'],
    lines: [
      { type: 'row', row: { slug: 'owner/repo-a', repo: 'owner/repo-a', mode: 'enforce' } },
      { type: 'end', count: 1 },
    ],
  },
  {
    name: 'whoami',
    argv: ['user', 'ls', '--me', '--json'],
    document: { version: 1, role: 'admin', scope: 'project', repos: ['owner/repo-a'] },
  },
  {
    name: 'image.catalog.ls',
    argv: ['image', 'ls', '--json'],
    document: {
      project: 'fugaro',
      entries: [{ name: 'go', source: 'builtin' }],
    },
  },
  {
    name: 'recipes.ls',
    argv: ['recipes', 'ls', '--json'],
    document: { recipes: [{ name: 'standard', source: 'builtin' }], notes: ['a note'] },
  },
]

/** asked is the arguments one read is asked for. The feed's own payload keys are
 * the renderer's; these tests are about the reading, not the validation. */
const ASKED: Record<NewReadName, Record<string, unknown>> = {
  'runs.stream': { since: '24h' },
  'spend.stream': {},
  'budget.stream': {},
  whoami: {},
  'image.catalog.ls': {},
  'recipes.ls': {},
}

/** stdoutOf is what the case's child prints: NDJSON for a feed, one document for
 * a one-shot read. */
function stdoutOf(entry: ReadCase): string {
  if (entry.lines !== undefined) return `${entry.lines.map((line) => JSON.stringify(line)).join('\n')}\n`
  return JSON.stringify(entry.document)
}

describe('the six new reads', () => {
  it.each(CASES)('answers $name from one child', async (entry) => {
    const child = stubChild()
    const spawner = stubSpawner(child)
    const handlers = newReadHandlers({
      source: cliNewReads({ plan: plannerFor(entry.argv), spawner }),
      cliVersion: '0.30.0',
      floors: floorsAt('0.30.0'),
    })

    const answered = handlers[entry.name](ASKED[entry.name] as never)
    child.write(stdoutOf(entry))
    child.finish(0)
    const answer = await answered

    // One child, spawned with the plan's own argv and never through a shell.
    expect(spawner.calls).toHaveLength(1)
    expect(spawner.calls[0]?.command).toBe('/usr/local/bin/fugaro')
    expect(spawner.calls[0]?.args).toEqual(entry.argv)
    expect(spawner.calls[0]?.options.shell).toBe(false)

    // The document is the CLI's own, or the feed's lines in order.
    if (entry.lines !== undefined) {
      expect(answer.document).toEqual({ lines: entry.lines })
    } else {
      expect(answer.document).toEqual(entry.document)
    }

    // ...and it is the answer the contract's own reader reads.
    const decoded = decodeNewOperationAnswer(entry.name, answer)
    expect(decoded.ok, JSON.stringify(decoded)).toBe(true)
    if (!decoded.ok) return
    if (entry.name === 'runs.stream') {
      // The stream's own reading of the same lines: one row, and an `end` line
      // -- which is the whole difference between a finished read and an
      // interrupted one.
      expect(decoded.value).toMatchObject({ rows: [{ run: A_RUN_ROW['run'] }], end: { count: 1 } })
    }
    if (entry.name === 'whoami') expect(decoded.value).toMatchObject({ role: 'admin' })
  })

  it('names every read it answers, once', () => {
    expect(new Set(NEW_READ_NAMES).size).toBe(NEW_READ_NAMES.length)
    expect([...NEW_READ_NAMES].sort()).toEqual(Object.keys(NEW_READ_WORDS).sort())
    expect([...NEW_READ_NAMES].sort()).toEqual(
      Object.keys(newReadHandlers({ source: anUnusedSource(), cliVersion: '' })).sort(),
    )
  })
})

// ---------------------------------------------------------------------------
// The bounds.
// ---------------------------------------------------------------------------

/** linesOf is a feed's lines as a child would write them. */
function linesOf(lines: readonly unknown[]): string {
  return `${lines.map((line) => JSON.stringify(line)).join('\n')}\n`
}

/** aRowLine is one row line of a feed, in the line shape the CLI writes, with
 * `pad` text to size it. */
function aRowLine(n: number, pad = ''): JSONObject {
  return { type: 'row', row: { n, pad } }
}

/** feedOver is one feed read from one stub child, with the bounds a test names. */
async function feedOver(
  text: string,
  bounds = NEW_READ_BOUNDS,
): Promise<{ readonly document: JSONObject; readonly child: StubChild; readonly spawner: ChildSpawner & { readonly calls: readonly SpawnCall[] } }> {
  const child = stubChild()
  const spawner = stubSpawner(child)
  // A source, not a handler: the bounds are the reader's, and this is the reader.
  const source = cliNewReads({ plan: plannerFor(), spawner })
  const answered = source.readFeed('runs.stream', {}, bounds)
  child.write(text)
  child.finish(0)
  return { document: await answered, child, spawner }
}

/** lastType is the type of a feed's last line, `''` for an empty feed: an `end`
 * line is what says the CLI finished, so its absence is what "interrupted"
 * means. */
function lastType(document: JSONObject): string {
  const lines = document['lines'] as readonly JSONObject[]
  const last = lines.at(-1)
  return last === undefined ? '' : String(last['type'])
}

describe('the caps a feed is read under', () => {
  it('ends the read when a line is longer than the ceiling', async () => {
    const overLong = JSON.stringify(aRowLine(1, 'x'.repeat(MAX_FEED_LINE_LENGTH)))
    expect(overLong.length).toBeGreaterThan(MAX_FEED_LINE_LENGTH)
    const { document, child } = await feedOver(linesOf([JSON.parse(overLong), { type: 'end' }]))

    // The line was dropped, not kept, and no `end` came after it: an interrupted
    // read, and the child was stopped rather than read a little longer.
    expect(document['lines']).toEqual([])
    expect(lastType(document)).not.toBe('end')
    expect(child.killed).toContain('SIGTERM')
  })

  it('ends the read at 10,000 rows', async () => {
    expect(NEW_READ_BOUNDS.maxRows).toBe(NEW_READ_MAX_ROWS)
    const rows = Array.from({ length: NEW_READ_MAX_ROWS + 2 }, (_unused, index) => aRowLine(index))
    const { document, child } = await feedOver(linesOf([...rows, { type: 'end', count: rows.length }]))

    const lines = document['lines'] as readonly JSONObject[]
    expect(lines).toHaveLength(NEW_READ_MAX_ROWS)
    expect(lastType(document)).toBe('row')
    expect(child.killed).toContain('SIGTERM')
  })

  it('ends the read at 16 MiB of lines', async () => {
    expect(NEW_READ_BOUNDS.maxBytes).toBe(NEW_READ_MAX_BYTES)
    // One row a megabyte, so the bound is reached by a handful of them rather
    // than by a quarter of a million small ones. Each line is well under the
    // per-line ceiling, which is what makes this the byte bound's own test.
    const pad = 'x'.repeat(1024 * 1024)
    const each = JSON.stringify(aRowLine(0, pad)).length
    const reaches = Math.ceil(NEW_READ_MAX_BYTES / each)
    expect(each).toBeLessThan(MAX_FEED_LINE_LENGTH)

    const rows = Array.from({ length: reaches + 2 }, (_unused, index) => aRowLine(index, pad))
    const { document, child } = await feedOver(linesOf([...rows, { type: 'end' }]))

    const lines = document['lines'] as readonly JSONObject[]
    expect(lines).toHaveLength(reaches)
    expect(lastType(document)).toBe('row')
    expect(child.killed).toContain('SIGTERM')
  })

  it('ends the read when the child wedges', async () => {
    const child = stubChild()
    const spawner = stubSpawner(child)
    const source = cliNewReads({ plan: plannerFor(), spawner, deadlineMs: 1 })
    const answered = source.readFeed('runs.stream', {}, NEW_READ_BOUNDS)
    child.write(linesOf([aRowLine(1)]))

    const document = await answered
    expect(document['lines']).toEqual([aRowLine(1)])
    expect(lastType(document)).not.toBe('end')
    expect(child.killed).toContain('SIGTERM')
  })

  it('counts a line nested deeper than a recursive serializer can write', async () => {
    // A legal line -- it parses, and it is nowhere near the per-line ceiling --
    // that V8's `JSON.stringify` cannot write at all: it throws a RangeError a
    // few thousand frames down. Counting the byte bound with it must not throw
    // out of the child's stdout listener, where nothing catches it and the
    // process, not the read, is what fails.
    const depth = 20_000
    const nested = `{"type":"row","row":${'['.repeat(depth)}${']'.repeat(depth)}}`
    expect(nested.length).toBeLessThan(MAX_FEED_LINE_LENGTH)
    expect(() => JSON.stringify(JSON.parse(nested))).toThrow(RangeError)

    const { document } = await feedOver(`${nested}\n${JSON.stringify({ type: 'end', count: 1 })}\n`)

    expect(document['lines']).toHaveLength(2)
    expect(lastType(document)).toBe('end')
  })

  it('keeps every line the child wrote before the bound', async () => {
    const { document } = await feedOver(linesOf([aRowLine(1), { type: 'end', count: 1 }]))
    expect(document).toEqual({ lines: [aRowLine(1), { type: 'end', count: 1 }] })
  })
})

// ---------------------------------------------------------------------------
// The floor.
// ---------------------------------------------------------------------------

/** CountingSource is a source that answers nothing and counts being asked. */
function aCountingSource(document: JSONObject = { lines: [] }): NewReadSource & { readonly asked: number } {
  let asked = 0
  const source = {
    readDocument: () => {
      asked += 1
      return Promise.resolve(document)
    },
    readFeed: () => {
      asked += 1
      return Promise.resolve(document)
    },
  } as unknown as NewReadSource & { asked: number }
  Object.defineProperty(source, 'asked', { get: () => asked })
  return source
}

/** anUnusedSource is a source no read is allowed to reach. */
function anUnusedSource(): NewReadSource {
  return {
    readDocument: () => Promise.reject(new Error('a refused read must not be read')),
    readFeed: () => Promise.reject(new Error('a refused read must not be read')),
  }
}

describe('the floor a read is offered at', () => {
  it.each(NEW_READ_NAMES)('refuses %s below its floor, and spawns nothing', async (name) => {
    const source = aCountingSource()
    const handlers = newReadHandlers({ source, cliVersion: '0.29.1', floors: floorsAt('0.30.0') })

    await expect(handlers[name]({})).rejects.toBeInstanceOf(SourceRefusal)
    await expect(handlers[name]({})).rejects.toThrow(
      `${NEW_READ_WORDS[name]} needs fugaro 0.30.0 and this CLI is 0.29.1`,
    )
    expect(source.asked).toBe(0)
  })

  it.each(NEW_READ_NAMES)('refuses %s while its floor is still null', async (name) => {
    const source = aCountingSource()
    const handlers = newReadHandlers({ source, cliVersion: '0.30.0', floors: floorsAt(null) })

    await expect(handlers[name]({})).rejects.toThrow(newReadFloorSentence(name, null, '0.30.0'))
    // ...and the same refusal is the default: a wiring that named no floor
    // spawns nothing.
    const unwired = newReadHandlers({ source, cliVersion: '0.30.0' })
    await expect(unwired[name]({})).rejects.toBeInstanceOf(SourceRefusal)
    expect(source.asked).toBe(0)
  })

  it('names a version line it could not read rather than drawing an empty one', async () => {
    const handlers = newReadHandlers({
      source: aCountingSource(),
      cliVersion: '',
      floors: floorsAt('0.30.0'),
    })
    await expect(handlers.whoami({})).rejects.toThrow(
      `needs fugaro 0.30.0 and this CLI is ${UNREADABLE_VERSION}`,
    )
  })

  it('reads at or above the floor', async () => {
    const source = aCountingSource({ version: 1, role: 'admin', scope: 'project', repos: [] })
    const handlers = newReadHandlers({ source, cliVersion: '0.30.0', floors: floorsAt('0.30.0') })
    await expect(handlers.whoami({})).resolves.toEqual({
      document: { version: 1, role: 'admin', scope: 'project', repos: [] },
    })
  })
})

// ---------------------------------------------------------------------------
// The text classes.
// ---------------------------------------------------------------------------

describe('the fields the contract names', () => {
  it('puts a named field through its class wherever it appears', () => {
    // `title` is a sentence and `name` a label, at any depth: the table is keyed
    // by the field's own wire name, and a feed's row is one level further down
    // than the document around it.
    const document: JSONObject = {
      type: 'row',
      row: {
        title: 'a​title',
        entries: [{ name: 'a​name', other: 'a​other' }],
        tags: ['a​tag'],
      },
    }
    expect(throughClasses(document)).toEqual({
      type: 'row',
      row: {
        title: 'atitle',
        entries: [{ name: 'aname', other: 'a​other' }],
        tags: ['atag'],
      },
    })
  })

  it('draws a value its class refuses as the placeholder, never as the value', () => {
    const tooLong = 'x'.repeat(MAX_TEXT_INPUT_LENGTH + 1)
    const drawn = throughClasses({ type: 'row', row: { title: tooLong } })
    expect((drawn['row'] as JSONObject)['title']).toBe(LITERAL_PLACEHOLDER)
    expect(JSON.stringify(drawn)).not.toContain(tooLong)
  })

  it('lets the class, not this module, decide what a field may carry', () => {
    // An image entry's `source` is a label, and a label is a name: a path in one
    // is drawn as it came (D3213), so this pins that the read put the value
    // through the class and not through a rule of its own.
    const drawn = throughClasses({ entries: [{ name: 'go', source: 'images/catalog/go/Dockerfile' }] })
    expect(drawn).toEqual({ entries: [{ name: 'go', source: 'images/catalog/go/Dockerfile' }] })
  })

  it('answers the sanitized document through a handler, not the raw one', async () => {
    const raw = { type: 'row', row: { title: 'a​title' } }
    const handlers = newReadHandlers({
      source: aCountingSource({ lines: [raw] }),
      cliVersion: '0.30.0',
      floors: floorsAt('0.30.0'),
    })
    const answer = await handlers['runs.stream']({})
    expect(answer.document).toEqual({ lines: [{ type: 'row', row: { title: 'atitle' } }] })
  })

  it('rebuilds the document rather than sharing it', () => {
    const nested: JSONObject = { name: 'a name' }
    const document: JSONObject = { entries: [nested] }
    const drawn = throughClasses(document)
    expect(drawn).not.toBe(document)
    expect(drawn['entries']).not.toBe(document['entries'])
    expect((drawn['entries'] as readonly JSONObject[])[0]).not.toBe(nested)
  })
})

// ---------------------------------------------------------------------------
// The fixture source.
// ---------------------------------------------------------------------------

/** FIXTURE_CLI is the version the fixture tests offer the six reads. A synthetic
 * fixture carries no `cli` (it was not recorded anywhere), so the source's own
 * `installation.version` is the case name; a floor is a release line, so these
 * tests name one rather than ask a case name to be one. */
const FIXTURE_CLI = '0.30.0'

/** FIXTURE_DOCUMENTS is what the fixture holds, one entry per read, in the
 * contract's own keying. The values are the source's, not the CLI stubs': a test
 * that compared the fixture's answer with the stub child's own document would
 * pass with a fixture that answered the wrong read. */
const FIXTURE_DOCUMENTS: Readonly<Record<NewReadName, JSONObject>> = Object.freeze({
  'runs.stream': {
    lines: [
      { type: 'row', row: { ...A_RUN_ROW, title: 'the fixture runs stream' } },
      { type: 'end', version: 1, count: 1 },
    ],
  },
  'spend.stream': {
    lines: [{ type: 'row', row: { key: '2026-10-31' } }, { type: 'end' }],
  },
  'budget.stream': {
    lines: [
      { type: 'row', row: { slug: 'owner/repo-a', repo: 'owner/repo-a', mode: 'enforce' } },
      { type: 'end', count: 1 },
    ],
  },
  whoami: { version: 1, role: 'viewer', scope: 'repos', repos: ['owner/repo-a'] },
  'image.catalog.ls': { project: 'fugaro', entries: [] },
  'recipes.ls': { recipes: [], notes: [] },
})

/** fixtureSource is a `FixtureSource` over the committed synthetic fixture,
 * holding the six reads a spec would drive the new screens from. */
function fixtureSource(): FixtureSource {
  return new FixtureSource({
    fixture: loadFixture(readFileSync(SYNTHETIC_WHERE, 'utf8'), SYNTHETIC_WHERE),
    documents: FIXTURE_DOCUMENTS,
  })
}

describe('the fixture source answers the same six reads', () => {
  it.each(NEW_READ_NAMES)('answers %s from a fixture, for the e2e specs', async (name) => {
    const fixture = fixtureSource()
    const handlers = newReadHandlers({
      source: fixture.newReads(),
      cliVersion: FIXTURE_CLI,
      floors: floorsAt(FIXTURE_CLI),
    })

    // The answer is the fixture's document, through the same classes a child's
    // would go through -- the two sources meet at one handler, which is the
    // whole point of the seam.
    const answer = await handlers[name](ASKED[name] as never)
    expect(answer.document).toEqual(throughClasses(FIXTURE_DOCUMENTS[name]))
    const decoded = decodeNewOperationAnswer(name, answer)
    expect(decoded.ok, JSON.stringify(decoded)).toBe(true)
  })

  it('refuses a read the fixture holds no document for, by name', async () => {
    const fixture = new FixtureSource({
      fixture: loadFixture(readFileSync(SYNTHETIC_WHERE, 'utf8'), SYNTHETIC_WHERE),
    })
    const handlers = newReadHandlers({
      source: fixture.newReads(),
      cliVersion: FIXTURE_CLI,
      floors: floorsAt(FIXTURE_CLI),
    })
    await expect(handlers['recipes.ls']({})).rejects.toThrow(/holds no recipes\.ls document/)
  })
})
