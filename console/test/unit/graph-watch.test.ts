// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The graph's live feed (the graph plan's CG9b): `graph.watch`, the console's
// thirty-seventh operation and its second long-lived child.
//
// Each of the plan's six claims is a case below, in the plan's own order; three
// more hold the rest of this row's contract (the scope is a value and `visible`
// reaches no argv, one child per scope, and a stopped feed is not handed back):
//
//   1. **The argv is exactly the declared row.** §5.1 rule 6 is a fence, not a
//      description: what a real child is handed is `graph --watch --json`, the
//      row's own `--source`, and the installation's two flags -- and no word a
//      caller can spell into one.
//   2. **The latest document wins, and a stale `seq` is ignored.** The rule is
//      the CLI's own (§6.22, H4 and F20): a document whose `seq` is not above
//      the last one seen inside the same `stream_id` is refused **whole** -- not
//      counted, not drawn, and no `current()` answered with it.
//   3. **A new `stream_id` is a new snapshot**, whatever number it carries:
//      the identity is drawn once per child, so a restarted child's `seq` begins
//      again at one, and comparing across the two would throw the first document
//      of every restart away.
//   4. **The child is stopped when the Graph tab is hidden** (H6). `visible:
//      false` is a window saying the tab is off screen, and the handler answers
//      it by stopping the child -- once, and never by starting one. That
//      argument reaches no word of any argv, which is why the stop cannot be
//      spelled by a renderer and is not a flag of the row.
//   5. **The feed counts toward `MAX_LIVE_FEEDS`, and evicts the oldest.** One
//      ceiling for both live subscriptions, or a console that watched the graph
//      and the feed together would hold twice the children the ceiling exists to
//      bound -- and the eviction is least-recently-used over both kinds.
//   6. **`close()` stops the graph child.** The console's own shutdown reaches
//      every child it holds, this one included.
//
// The stub CLI is the only child here (`test/unit/stub.ts`): every case is about
// the argv this console builds and the frames it keeps, and neither needs the
// real binary, a credential or the network. Its `graph --watch` key is the one
// `commandKey` reads off that argv, which is what makes the scenario findable at
// all -- the read's key is `graph`, and the two rows share a command word.

import { mkdtempSync, realpathSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { SPAWN_COMMANDS, SPAWN_FLAGS, argvFor } from '../../src/main/spawn'
import { CliSource, MAX_LIVE_FEEDS } from '../../src/source/cli'
import { handlersFor } from '../../src/source/source'
import type { LiveFeed, Snapshot } from '../../src/source/source'
import { GRAPH_DOCUMENT_VERSION } from '../../src/shared/schema'
import { installStub } from './stub'
import type { Stub, StubAnswer } from './stub'

/** PROJECT is the installation every child here is spawned for. */
const PROJECT = 'fugaro'

/** freshDirectory is a temporary directory this file owns, spelled the canonical
 * way: on macOS the temporary directory is reached through a link and the
 * child's own cwd is the realpath, so a comparison needs both sides resolved. */
function freshDirectory(): string {
  return realpathSync(mkdtempSync(join(tmpdir(), 'fugaro-console-graph-watch-')))
}

/** waitFor waits for a condition the child produces out of band -- a report line,
 * a kill -- by polling, because none of those crosses a promise. */
async function waitFor(predicate: () => boolean, ms = 15_000): Promise<void> {
  const deadline = Date.now() + ms
  while (!predicate()) {
    if (Date.now() > deadline) throw new Error('waitFor timed out')
    await new Promise((resolve) => setTimeout(resolve, 10))
  }
}

/** graphSource is a source over the stub binary, with the version answer every
 * case needs before a read is made. */
const graphSource = (stub: Stub): Promise<CliSource> =>
  CliSource.create({ target: { project: PROJECT }, configuredBinaryPath: stub.binary, appEnv: {} })

/** graphSpawns counts the graph children the stub has seen, so "no child was
 * started" is a claim about the child and not about this file's own count. */
const graphSpawns = (stub: Stub): number =>
  stub.reports().filter((report) => report.argv[0] === 'graph' && report.argv.includes('--watch')).length

/** document is one stream document as `graph --watch --json` prints one: the
 * whole graph every time, with only the identity and the counter differing
 * between two of them (contract §6.22). */
function document(streamId: string, seq: number): Record<string, unknown> {
  return {
    version: GRAPH_DOCUMENT_VERSION,
    stream_id: streamId,
    seq,
    generated_at: '2026-11-09T10:30:00Z',
    sources: [{ source: 'coordinator', state: 'up', as_of: null, reason: '' }],
    nodes: [
      {
        key: 'coordinator:acme/widgets#42',
        source: 'coordinator',
        kind: 'pull_request',
        id: 'acme/widgets#42',
        title: 'Tidy the widget loader',
        repo: 'acme/widgets',
        state: 'open',
        release: 'v1.4.0',
        updated: '2026-11-06T18:00:00Z',
      },
    ],
    edges: [],
    warnings: [],
  }
}

/** aStream is a long-lived child printing these documents and then holding
 * still, which is what a subscription with nothing changing looks like. */
const aStream = (...documents: Record<string, unknown>[]): StubAnswer => ({
  lines: documents.map((each) => JSON.stringify(each)),
  delayMs: 20,
  hold: true,
})

const WATCHING = { version: { stdout: '1.4.2\n' }, 'graph --watch': aStream(document('s1', 1)) }
const LIVE = { lines: ['{"day":"2026-11-09"}'], hold: true }

/** seqsOf is the `seq` of every document a feed drew, in the order it drew
 * them: the assertion "a stale document arrived" is about this list. */
function seqsOf(drawn: readonly Record<string, unknown>[]): readonly unknown[] {
  return drawn.map((each) => each['seq'])
}

describe('the graph feed’s argv', () => {
  it('spawns exactly the declared row, and no word a caller can spell', async () => {
    // §5.1 rule 6's row is the whole of what this console may say to the command
    // (`graph --watch --json [--source <v>]`), so the row's shape is written out
    // here rather than read back off the table, and then checked against a real
    // child's own argv: the report file is the child's account, not this file's.
    const row = SPAWN_COMMANDS.find((candidate) => candidate.operation === 'graph.watch')
    expect(row?.words.join(' ')).toBe('graph --watch --json')
    expect(row?.takesProject).toBe(true)
    expect(row?.dirFlag).toBe(false)
    expect(row?.args).toEqual([
      { key: 'source', flag: '--source', kind: 'value' },
      { key: 'visible', flag: null, kind: 'boolean' },
    ])
    // `--watch` is a word of the command path, and the module's flag list is
    // what `assertArgvFlags` holds every emitted word to: a word of `words` is
    // not a flag, so nothing a caller sends can be an extra flag either.
    expect(SPAWN_FLAGS).toContain('--watch')

    const stub = installStub(freshDirectory(), { commands: WATCHING })
    const source = await graphSource(stub)
    const feed = source.subscribeGraph({ source: 'all' })
    await feed.current()
    await waitFor(() => graphSpawns(stub) === 1)
    expect(stub.reports().filter((report) => report.argv.includes('--watch')).map((report) => report.argv)).toEqual([
      ['graph', '--watch', '--json', '--source', 'all', '--project', PROJECT],
    ])
    await source.close()
    await waitFor(() => stub.kills().length === 1)
  })

  it('builds the scope as a value, never as a word of the command', () => {
    // The scope is the only thing a renderer chooses about this argv, and it is
    // a value behind the row's own `--source`: a scope spelled as a flag, or as
    // a flag and a value, is refused by the argv builder's own whitelist before
    // any child exists. (`ipc.ts` refuses it earlier still, against the
    // command's closed pattern -- `graph-read.test.ts`'s sibling cases.)
    const where = { target: { project: PROJECT }, directory: undefined }
    // With no scope the command's own default is left to the CLI.
    expect([...argvFor('graph.watch', {}, where)]).toEqual([
      'graph',
      '--watch',
      '--json',
      '--project',
      PROJECT,
    ])
    for (const value of ['--watch', '-x', 'all --source local:o1', 'coordinator\n--json']) {
      expect(() => argvFor('graph.watch', { source: value }, where), value).toThrow()
    }
    // And `visible` — the argument that is not a word — reaches no argv at all:
    // the two spellings of it build the same command line.
    expect([...argvFor('graph.watch', { source: 'all', visible: false }, where)]).toEqual([
      ...argvFor('graph.watch', { source: 'all' }, where),
    ])
  })
})

describe('the graph feed’s documents', () => {
  it('keeps the latest document, and refuses a stale seq whole', async () => {
    // The child prints a document and then an older one -- what a reader sees
    // when the CLI reconnects and replays, or when two children's lines meet on
    // one pipe. The second is not a state change: it is not counted, it reaches
    // no listener, and `current()` answers the newest document this feed kept.
    const stub = installStub(freshDirectory(), {
      commands: { version: { stdout: '1.4.2\n' }, 'graph --watch': aStream(document('s1', 1), document('s1', 3), document('s1', 2)) },
    })
    const source = await graphSource(stub)
    const feed = source.subscribeGraph({ source: 'all' })
    const drawn: Record<string, unknown>[] = []
    feed.onChange((snapshot) => drawn.push(snapshot.document as Record<string, unknown>))
    await waitFor(() => drawn.length >= 2)
    await feed.current()
    // Nothing more arrives: the third document was refused rather than drawn.
    await new Promise((resolve) => setTimeout(resolve, 200))
    expect(seqsOf(drawn), 'a document older than the feed’s own was drawn').toEqual([1, 3])
    const latest = feed.latest as Snapshot | null
    expect((latest?.document as Record<string, unknown>)['seq']).toBe(3)
    // The console's own counter counts snapshots, and a refused document is not
    // one: two documents were kept, so the newest snapshot is the second.
    expect(latest?.sequence).toBe(drawn.length)
    expect((await feed.current()).sequence).toBe(2)
    await source.close()
    await waitFor(() => stub.kills().length === 1)
  })

  it('treats a new stream id as a new snapshot, whatever seq it carries', async () => {
    // `stream_id` is drawn once per child and `seq` rises within it, so the two
    // children behind one feed's lifetime -- a restart after a killed CLI, the
    // reconnect the child does itself -- are told apart by the id alone. A rule
    // that compared the numbers across the two would refuse the first document
    // of every restart, and a screen would hold a graph that no longer exists.
    const stub = installStub(freshDirectory(), {
      commands: {
        version: { stdout: '1.4.2\n' },
        'graph --watch': aStream(document('s1', 4), document('s1', 4), document('s2', 1)),
      },
    })
    const source = await graphSource(stub)
    const feed = source.subscribeGraph({ source: 'all' })
    const drawn: Record<string, unknown>[] = []
    feed.onChange((snapshot) => drawn.push(snapshot.document as Record<string, unknown>))
    await waitFor(() => drawn.length >= 2)
    await new Promise((resolve) => setTimeout(resolve, 200))
    // The repeated `(s1, 4)` is the stale one and the lower `(s2, 1)` is not.
    expect(drawn.map((each) => [each['stream_id'], each['seq']])).toEqual([
      ['s1', 4],
      ['s2', 1],
    ])
    await source.close()
    await waitFor(() => stub.kills().length === 1)
  })
})

describe('the graph feed’s lifetime', () => {
  it('stops the child when the tab is hidden, and starts none', async () => {
    // H6: the CLI holds a subscription on the project's scope subtree for as
    // long as this child lives, so a Graph tab that left the screen gives it up.
    // The handler is the door: `visible: false` stops the feed and answers
    // nothing, and the same call twice is the state the caller asked for.
    const stub = installStub(freshDirectory(), { commands: WATCHING })
    const source = await graphSource(stub)
    const handlers = handlersFor(source, { openExternal: async () => {} })
    const subscribed = (await handlers['graph.watch']?.({ source: 'all' })) as Snapshot
    expect(
      (subscribed.document as Record<string, unknown>)['stream_id'],
      'the subscription did not answer a first document',
    ).toBe('s1')
    await waitFor(() => graphSpawns(stub) === 1)

    expect(await handlers['graph.watch']?.({ source: 'all', visible: false })).toBeNull()
    await waitFor(() => stub.kills().length === 1)
    expect(stub.kills()[0]?.argv).toContain('--watch')
    expect(graphSpawns(stub), 'the stop started a child of its own').toBe(1)

    // Idempotent: the child the first stop ended is not stopped twice, and the
    // call answers nothing rather than a refusal.
    expect(await handlers['graph.watch']?.({ source: 'all', visible: false })).toBeNull()
    expect(stub.kills()).toHaveLength(1)

    // And the tab's return is a fresh child: the stopped feed is not handed
    // back, because a screen that comes back to the graph has to be reading.
    await handlers['graph.watch']?.({ source: 'all' })
    await waitFor(() => graphSpawns(stub) === 2)
    await source.close()
    await waitFor(() => stub.kills().length === 2)
  })

  it('counts toward MAX_LIVE_FEEDS with the live feeds, and evicts the oldest', async () => {
    // One ceiling for both kinds, over one map: the graph's child is as
    // expensive as the watch's -- each is a long-lived CLI process -- so a
    // second ceiling would be twice the bound the first exists to be.
    expect(MAX_LIVE_FEEDS).toBe(4)
    const stub = installStub(freshDirectory(), {
      commands: { version: { stdout: '1.4.2\n' }, watch: LIVE, 'graph --watch': aStream(document('s1', 1)) },
    })
    const source = await graphSource(stub)
    const feeds: readonly LiveFeed[] = [
      source.subscribeLive({ repo: 'acme/a' }),
      source.subscribeGraph({ source: 'all' }),
      source.subscribeLive({ repo: 'acme/b' }),
      source.subscribeGraph({ source: 'coordinator' }),
    ]
    await Promise.all(feeds.map((feed) => feed.current()))
    // A graph feed used again moves to the newest end of the order, so the
    // live feed asked for first is now the least recently used.
    expect(source.subscribeGraph({ source: 'all' })).toBe(feeds[1])

    let evicted: string | null = null
    feeds[0]?.onEnded((end) => {
      evicted = end.reason
    })
    const fifth = source.subscribeLive({ repo: 'acme/c' })
    await fifth.current()
    await waitFor(() => evicted !== null)
    expect(evicted, 'the fifth subscription evicted nothing').toBe('stopped')
    await waitFor(() => stub.kills().length === 1)
    expect(stub.kills()[0]?.argv).toContain('acme/a')
    // The three that were not least recently used are the same feeds, the
    // graph's included: the eviction is not a kind of subscription.
    expect(source.subscribeGraph({ source: 'coordinator' })).toBe(feeds[3])
    expect(source.subscribeLive({ repo: 'acme/b' })).toBe(feeds[2])
    // Four children are held at this point -- the fifth subscription evicted the
    // one above -- and `close()` ends every one of them.
    await source.close()
    await waitFor(() => stub.kills().length === 5)
  })

  it('stops the graph child when the source closes', async () => {
    // `close()` is the console's own shutdown path, and it is the backstop
    // behind H6: a child a screen forgot to stop is one this ends.
    const stub = installStub(freshDirectory(), {
      commands: { version: { stdout: '1.4.2\n' }, 'graph --watch': aStream(document('s1', 1)) },
    })
    const source = await graphSource(stub)
    const feed = source.subscribeGraph({ source: 'all' })
    await feed.current()
    await waitFor(() => graphSpawns(stub) === 1)
    await source.close()
    await waitFor(() => stub.kills().length === 1)
    expect(stub.kills()[0]?.signal).toBe('SIGTERM')
    expect(stub.kills()[0]?.argv).toContain('--watch')
  })

  it('keeps one child for one scope, and a second scope is a second child', async () => {
    // `subscribeLive`'s contract word for word (the interface's own doc): the
    // surface is `invoke`-only, so a screen reaches `graph.watch` once per poll,
    // and a subscription per call would leave one `fugaro graph --watch` child
    // per poll against the operator's project.
    const stub = installStub(freshDirectory(), {
      commands: { version: { stdout: '1.4.2\n' }, 'graph --watch': aStream(document('s1', 1)) },
    })
    const source = await graphSource(stub)
    const first = source.subscribeGraph({ source: 'all' })
    expect(source.subscribeGraph({ source: 'all' })).toBe(first)
    await first.current()
    await waitFor(() => graphSpawns(stub) === 1)
    const second = source.subscribeGraph({ source: 'coordinator' })
    expect(second).not.toBe(first)
    await second.current()
    await waitFor(() => graphSpawns(stub) === 2)
    expect(graphSpawns(stub)).toBe(2)
    await source.close()
    await waitFor(() => stub.kills().length === 2)
  })

  it('answers the same arguments with the same feed, and a stopped one with a new child', async () => {
    // A feed the console stopped is not a live feed: the next poll of the tab
    // has to start a child again rather than hand back the child it ended.
    const stub = installStub(freshDirectory(), {
      commands: { version: { stdout: '1.4.2\n' }, 'graph --watch': aStream(document('s1', 1)) },
    })
    const source = await graphSource(stub)
    const stopped = source.subscribeGraph({ source: 'all' })
    await stopped.current()
    await source.stopGraphWatch({ source: 'all' })
    await waitFor(() => stub.kills().length === 1)
    const next = source.subscribeGraph({ source: 'all' })
    expect(next, 'the stopped feed was handed back').not.toBe(stopped)
    await next.current()
    await waitFor(() => graphSpawns(stub) === 2)
    expect(graphSpawns(stub)).toBe(2)
    await source.close()
    await waitFor(() => stub.kills().length === 2)
  })
})
