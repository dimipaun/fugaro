// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The bound source: `forget` (desktop-console plan Task 3.4c step 1, wiring
// 3.4b's deferred N1) and the build that is in flight when the console moves.
//
// `createBoundSource` is the join between the console's own state and the data
// plane. Two things about it are not obvious from the happy path, and both are
// about a *build* -- resolving a binary and probing its version -- overlapping
// something else:
//
//   - **`forget`.** The installation the console was reading from is gone, so
//     nothing may stay bound to it. The interesting half is not the close of a
//     source that exists -- it is the build that does not exist *yet*. A build
//     that lands after the removal must not bind, because `bound` is what every
//     later read is handed, and a source bound for an installation the store no
//     longer names is a `fugaro watch` child nobody holds and nobody will ever
//     close.
//   - **A switch.** Moving to another installation closes the previous source
//     first, and a read that arrives while that close is in flight must join the
//     build for the installation the console moved to -- not start a second one
//     for it, which would leave the first refused as a superseded build. And a
//     read whose build *is* superseded is refused for what happened: the console
//     stopped showing that installation. It is **not** D13's no-installation
//     state, which says no binary can be found while one is chosen and running.

import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  NoInstallation,
  GRAPH_READ_MIN_INTERVAL_MS,
  PLAN_STATUS_MIN_INTERVAL_MS,
  FUGARO_INSTALL_COMMAND,
  CliSource,
  createBoundSource,
  noInstallationState,
} from '../../src/source/cli'
import type { BoundInstallation } from '../../src/source/cli'
import {
  NO_GRAPH_SCOPE,
  NO_NAMED_SCOPE,
  NO_ORCHESTRATOR_MAP,
  NO_PLAN_CHECKOUT,
  NO_PLAN_NO_MERGES,
  SourceRefusal,
} from '../../src/source/source'
import type { RoadmapMoveAnswer, RoadmapMoveOutcome, Snapshot } from '../../src/source/source'
import { itHostRoute } from '../support/hostRoute'

/** FIRST and SECOND are the two installations the cases below move between. */
const FIRST: BoundInstallation = {
  id: 'a1',
  project: 'acme',
  binaryPath: '/opt/fugaro/bin/fugaro',
}
const SECOND: BoundInstallation = {
  id: 'b2',
  project: 'other',
  binaryPath: '/opt/fugaro/bin/fugaro',
}

/**
 * floor is the version floor this file's map-read case sets.
 * `ORCHESTRATOR_MAP_CLI_FLOOR` is `null` on this tree -- `orchestrator map
 * --json` is registered from `ee/` and no release carries it -- so the map read
 * is refused for every installation and the teaching it does could not be
 * reached at all. The mock below changes the one thing a case cannot reach: the
 * predicate's default, exactly as `settings-write-floor.test.ts` does for
 * `noApplySupported`. With it unset every other case in this file reads the real
 * constant, which is `null`, and so nothing here behaves differently.
 */
const floor = vi.hoisted(() => ({ value: null as string | null, merges: null as string | null }))

vi.mock('../../src/shared/version', async (importOriginal) => {
  const real = await importOriginal<typeof import('../../src/shared/version')>()
  return {
    ...real,
    orchestratorMapSupported: (version: string, at: string | null = floor.value) =>
      real.orchestratorMapSupported(version, at),
    // IR4's own floor, mocked the same way and for the same reason:
    // `PLAN_STATUS_NO_MERGES_CLI_FLOOR` is `null` on this tree, so a `noMerges`
    // payload is refused for every installation and the branch a release takes
    // -- the argument reaching the child, and its own entry in the cadence --
    // could not be driven at all.
    planStatusNoMergesSupported: (version: string, at: string | null = floor.merges) =>
      real.planStatusNoMergesSupported(version, at),
  }
})

/** MAP_FLOOR is the release that first carries `orchestrator map --json`, and
 * MAP_VERSION the installation's own line: one patch above it, so the read is
 * admitted. Both are spelled rather than imported -- the claim is about the
 * comparison, not about a constant's value. */
const MAP_FLOOR = '1.4.0'
const MAP_VERSION = '1.4.2'

/** MERGES_FLOOR is the release that first carries `plan status --no-merges`
 * (IR4's floor) and MERGES_VERSION the installation's own line: one patch above
 * it, so the argument is admitted. Spelled rather than imported, on the two
 * above's terms. */
const MERGES_FLOOR = '0.33.0'
const MERGES_VERSION = '0.33.1'

afterEach(() => {
  floor.value = null
  floor.merges = null
})

/** MAP is the map document the teaching case is answered with: two active
 * orchestrators of the project `acme`, the project's default one and one named
 * (C-ORCH's id rule). Only `nodes[].id` is read by the surface. */
const MAP = {
  version: 1,
  project: 'acme',
  generated_at: '2026-11-07T12:00:00Z',
  nodes: [
    { id: 'acme', name: '', driver: 'dev@example.com', active_tasks: 1, running_jobs: 0, lease_age_seconds: 3 },
    { id: 'acme~blue', name: 'blue', driver: '', active_tasks: 0, running_jobs: 2, lease_age_seconds: null },
  ],
  edges: [],
  health: { sweep: true, started_at: null, ok_at: null, at: null, note: '' },
}

/** aSource is a `CliSource`-shaped stand-in: it answers a version and records
 * its own close, which is the only thing these cases ask of a source. `hold`
 * delays the close, which is how a case keeps the window a switch opens open. */
function aSource(closed: string[], name: string, hold?: () => Promise<void>): CliSource {
  return {
    version: async () => ({ text: name, receivedAt: '2026-10-15T12:00:00Z' }),
    close: async () => {
      closed.push(name)
      if (hold !== undefined) await hold()
    },
  } as unknown as CliSource
}

/** refusalOf runs a read that is expected to refuse and answers the reason. */
async function refusalOf(reading: Promise<unknown>): Promise<unknown> {
  return reading.then(
    () => null,
    (cause: unknown) => cause,
  )
}

describe('the source bound to the active installation', () => {
  it('binds one source per installation, and forget closes it once', async () => {
    let installation: BoundInstallation | null = FIRST
    const closed: string[] = []
    const built: string[] = []
    const source = createBoundSource({
      installation: () => installation,
      create: async (each) => {
        built.push(each.id)
        return aSource(closed, each.id)
      },
    })

    await source.version()
    await source.version()
    expect(built, 'two reads of one installation built two sources').toEqual([FIRST.id])

    installation = null
    await source.forget()
    await source.forget()
    expect(closed, 'forget did not close the source it unbound, exactly once').toEqual([FIRST.id])
    // And the read after the removal is D13's refusal rather than a source for
    // an installation nobody chose.
    const error = await refusalOf(source.version())
    expect(error).toBeInstanceOf(NoInstallation)
    expect(String((error as Error).message), 'the refusal is not D13’s own sentence').toContain(
      'no installation is chosen',
    )
  })

  it('closes a source whose installation was removed while it was being built', async () => {
    let installation: BoundInstallation | null = FIRST
    const closed: string[] = []
    const held: (() => void)[] = []
    const source = createBoundSource({
      installation: () => installation,
      create: () =>
        new Promise((resolve) => {
          held.push(() => resolve(aSource(closed, 'the removed installation')))
        }),
    })

    // A read starts the build and does not answer -- the state the console is in
    // for as long as the probe takes.
    const reading = source.version()
    // The operator removes the installation while that build is outstanding.
    installation = null
    await source.forget()

    // The build finishes after the removal. Nothing holds the source it made, so
    // it closes here or it never closes at all.
    expect(held, 'the build answered before the removal: the case proves nothing').toHaveLength(1)
    held[0]?.()

    const error = await refusalOf(reading)
    // A removal is D13's state, and the sentence is the resolution's own: no
    // installation is chosen, so there is no binary for the console to read.
    expect(error).toBeInstanceOf(NoInstallation)
    expect(String((error as Error).message)).toContain('no installation is chosen')
    expect(closed, 'a source for the removed installation was left running').toEqual([
      'the removed installation',
    ])
  })

  it('joins the build in flight for the installation the console moved to', async () => {
    let installation: BoundInstallation | null = FIRST
    const closed: string[] = []
    const built: string[] = []
    const closing: (() => void)[] = []
    const source = createBoundSource({
      installation: () => installation,
      create: async (each) => {
        built.push(each.id)
        // The first source's close does not answer until the test says so, which
        // is the window a switch leaves open.
        return each.id === FIRST.id
          ? aSource(closed, each.id, () => new Promise<void>((resolve) => closing.push(resolve)))
          : aSource(closed, each.id)
      },
    })

    await source.version()
    installation = SECOND
    // The switch starts: the previous source is closing, and the second read
    // arrives while it is. It is a read for the installation the console moved
    // to, so it is a read for the build already in flight.
    const first = source.version()
    const second = source.version()

    expect(closing, 'the previous source was not closed: the case proves nothing').toHaveLength(1)
    closing[0]?.()

    expect((await first).text, 'the first read was not answered by the new installation').toBe(SECOND.id)
    expect((await second).text).toBe(SECOND.id)
    expect(built, 'the read that arrived during the close started a second build').toEqual([
      FIRST.id,
      SECOND.id,
    ])
  })

  it('rebuilds when the directory the installation runs in changes, so the next read runs there', async () => {
    // A checkout is a field of the record that decides **where the child runs**
    // (`options.checkout ?? privateWorkingDirectory()`), and it is the one field
    // the picker changes on a record it already holds: `setCheckout` deliberately
    // preserves the record's `id` (`console/src/main/installations.ts` -- the id is
    // *which* installation this is, not where it runs). A source cached by that id
    // alone therefore kept the build made for the **previous** directory: the
    // picker ran rule 7's `doctor` from the new one and the row and the header
    // reported it, while every read still spawned with the old cwd -- the
    // dashboard reading the wrong repository, or refused by the CLI's own pairing
    // sentence, under a screen that said the new checkout was in use.
    let installation: BoundInstallation = { id: 'a1', project: 'acme', checkout: '/src/one' }
    const built: (string | undefined)[] = []
    const closed: string[] = []
    const source = createBoundSource({
      installation: () => installation,
      create: async (each) => {
        built.push(each.checkout)
        return aSource(closed, each.checkout ?? 'the private working directory')
      },
    })

    expect((await source.version()).text).toBe('/src/one')
    installation = { ...installation, checkout: '/src/two' }
    // The read after the change is the whole claim: it is answered by a source
    // built from the new checkout, and the source built for the previous one was
    // closed first -- its `watch` child is running in the old directory.
    expect((await source.version()).text, 'the read after a checkout change ran in the previous directory').toBe(
      '/src/two',
    )
    expect(closed, 'the source built for the previous checkout was left running').toEqual(['/src/one'])
    // No churn while the binding is unchanged, which is what makes this a key and
    // not a rebuild-on-everything: the same read twice builds once.
    await source.version()
    expect(built).toEqual(['/src/one', '/src/two'])
    // A clear is a change too: rule 5's ordinary case is an installation with no
    // checkout, and its child runs in the console's own private directory.
    installation = { id: 'a1', project: 'acme' }
    expect((await source.version()).text).toBe('the private working directory')
    expect(built).toEqual(['/src/one', '/src/two', undefined])
    // The ref a screen draws is still read from the binding at call time, so the
    // header and the child never disagree about which directory is in use.
    expect(source.installation?.checkout, 'the ref still names the previous checkout').toBeUndefined()
  })

  it('refuses a read for an installation the console stopped showing, and says so', async () => {
    let installation: BoundInstallation | null = FIRST
    const closed: string[] = []
    const held: (() => void)[] = []
    const source = createBoundSource({
      installation: () => installation,
      create: (each) =>
        each.id === FIRST.id
          ? new Promise((resolve) => held.push(() => resolve(aSource(closed, each.id))))
          : Promise.resolve(aSource(closed, each.id)),
    })

    // A read starts a build for the installation the console is showing, and the
    // operator moves to another one before it lands.
    const reading = source.version()
    installation = SECOND
    expect((await source.version()).text).toBe(SECOND.id)

    expect(held, 'the first build answered before the switch: the case proves nothing').toHaveLength(1)
    held[0]?.()

    const error = await refusalOf(reading)
    // Not "no installation is chosen": one is chosen, and the read that was
    // refused was for the installation the console is no longer showing.
    expect(error).toBeInstanceOf(SourceRefusal)
    expect(
      error,
      'a read for an installation the console left was refused as D13’s no-installation state',
    ).not.toBeInstanceOf(NoInstallation)
    expect(String((error as Error).message)).toContain('no longer showing')
    expect(closed, 'the superseded build’s source was left running').toEqual([FIRST.id])
  })

  it('builds again on the next read after a build that failed, rather than refusing forever', async () => {
    // The probe can fail for a reason that passes: a `version` that did not
    // answer in time on a loaded machine, a binary being replaced by an upgrade.
    // A failed build that stayed cached as the installation's pending build
    // would answer every later read with the same rejection until the operator
    // switched installations or restarted the console.
    const closed: string[] = []
    let attempts = 0
    const source = createBoundSource({
      installation: () => FIRST,
      create: async (each) => {
        attempts += 1
        if (attempts === 1) throw new SourceRefusal('the binary did not answer `version` in time')
        return aSource(closed, each.id)
      },
    })

    const error = await refusalOf(source.version())
    expect(error, 'the failed build was not the read’s refusal').toBeInstanceOf(SourceRefusal)
    expect((await source.version()).text, 'the read after a failed build was not built again').toBe(FIRST.id)
    expect(attempts).toBe(2)
  })

  it('asks the source for the live feed on every poll, so a feed that ended is replaced', async () => {
    // `CliSource.subscribeLive` is idempotent while a feed is live and drops a
    // feed that has ended, so the poll after a `watch` that exited starts a
    // fresh child. A bound feed that kept the first stream it was handed would
    // poll a dead one forever: a refusal on every poll, or the last frame
    // answered as if it were current.
    let started = 0
    let live: { ended: boolean; n: number } | null = null
    const real = {
      version: async () => ({ text: FIRST.id, receivedAt: '2026-10-15T12:00:00Z' }),
      close: async () => undefined,
      subscribeLive: () => {
        if (live === null || live.ended) {
          started += 1
          live = { ended: false, n: started }
        }
        const stream = live
        return {
          latest: null,
          current: () =>
            stream.ended
              ? Promise.reject(new SourceRefusal('watch failed: the feed ended'))
              : Promise.resolve({ document: { child: stream.n }, receivedAt: '2026-10-15T12:00:00Z' }),
          stop: async () => undefined,
          // A feed says it has ended through `onEnded` and not through a
          // rejection -- a `current()` can be refused for a document that did not
          // arrive in time while the child is alive -- and the bound source
          // listens for it. This feed never ends on its own; the case sets
          // `ended` itself, and the `watch` feed holds no floor either way.
          onEnded: () => () => undefined,
        }
      },
    } as unknown as CliSource
    const source = createBoundSource({ installation: () => FIRST, create: async () => real })
    const args = { project: 'acme' }

    expect((await source.subscribeLive(args).current()).document).toEqual({ child: 1 })
    expect((await source.subscribeLive(args).current()).document, 'a live feed was not reused').toEqual({ child: 1 })
    // The `watch` child exits: the stream the source handed out has ended.
    if (live === null) throw new Error('no feed was started: the case proves nothing')
    ;(live as { ended: boolean }).ended = true

    expect(
      (await source.subscribeLive(args).current()).document,
      'the poll after the feed ended was answered by the dead stream',
    ).toEqual({ child: 2 })
    expect(started).toBe(2)
  })

  it('watches the end of a stream once, however many polls a live feed takes', async () => {
    // `current()` asks the source for the feed again on every poll (`lazyFeed`),
    // and a source answers the **same stream** for the same key while it is live:
    // an end listener registered per call would grow that stream's own set by one
    // a second for as long as the Graph tab is open, and every one of them would
    // run when the child finally ended. So the end is watched once per stream --
    // and the floor that reading feeds is still re-armed for the stream that
    // replaces it, which is what the second half of this case holds.
    let started = 0
    let now = Date.parse('2026-11-09T12:00:00Z')
    let live: { ended: boolean; n: number } | null = null
    let listeners: (() => void)[] = []
    const watching: number[] = []
    let stream: unknown = null
    const real = {
      version: async () => ({ text: FIRST.id, receivedAt: '2026-10-15T12:00:00Z' }),
      close: async () => undefined,
      subscribeGraph: () => {
        if (live === null || live.ended) {
          started += 1
          live = { ended: false, n: started }
          listeners = []
          watching.push(0)
          const state = live
          stream = {
            latest: null,
            current: () =>
              state.ended
                ? Promise.reject(new SourceRefusal('graph failed: the feed ended'))
                : Promise.resolve({ document: { child: state.n }, receivedAt: '2026-10-15T12:00:00Z' }),
            stop: async () => undefined,
            onEnded: (listener: () => void) => {
              watching[watching.length - 1] = (watching[watching.length - 1] ?? 0) + 1
              listeners.push(listener)
              if (state.ended) listener()
              return () => undefined
            },
          }
        }
        return stream
      },
    } as unknown as CliSource
    const source = createBoundSource({
      installation: () => FIRST,
      clock: () => new Date(now),
      create: async () => real,
    })
    const args = { source: 'all' }

    for (let poll = 0; poll < 5; poll += 1) {
      expect((await source.subscribeGraph(args).current()).document, 'a poll did not answer the live feed').toEqual({
        child: 1,
      })
    }
    expect(started, 'a poll started a child of its own').toBe(1)
    expect(watching, 'an end listener was registered per poll').toEqual([1])

    // The child exits. A poll inside the floor is answered with what that feed
    // last gave -- the document it had, or the refusal a child that died before
    // its first one left (`graph-watch.test.ts`) -- and starts nothing. The stream
    // that replaces it afterwards is watched in its turn: one listener for the
    // feed that ended, one for the feed that is new.
    if (live === null) throw new Error('no feed was started: the case proves nothing')
    ;(live as { ended: boolean }).ended = true
    for (const listener of listeners) listener()
    expect(
      (await source.subscribeGraph(args).current()).document,
      'a poll inside the floor did not hold the answer the feed left',
    ).toEqual({ child: 1 })
    expect(started, 'a poll inside the floor started a child').toBe(1)

    now += GRAPH_READ_MIN_INTERVAL_MS
    expect((await source.subscribeGraph(args).current()).document, 'the poll past the floor drew a dead feed').toEqual({
      child: 2,
    })
    expect(watching).toEqual([1, 1])
  })

describe('the plan read, whose directory is the binding’s', () => {
  it('refuses an installation with no checkout, and spawns nothing for it', async () => {
    // GV2.1's read walks the graph artifacts under the child's working
    // directory, so an installation that records none has no directory for the
    // question. The refusal has to happen **before** the source is built, and
    // that is the whole point of the case: building one probes `version` first
    // (`CliSource.create`), which is a child, so the operator with no checkout
    // would pay for a spawn whose read could only be refused -- and a console
    // that built anyway would be positioned to read whatever repository it
    // happened to be sitting in. `built` staying empty is the assertion that
    // says no child was started, and not merely that the read failed.
    const built: string[] = []
    const source = createBoundSource({
      installation: () => FIRST,
      create: async (each) => {
        built.push(each.id)
        return aSource([], each.id)
      },
    })

    const error = await refusalOf(source.planStatus({}))
    expect(error, 'a checkoutless installation was not refused').toBeInstanceOf(SourceRefusal)
    expect((error as SourceRefusal).message).toBe(NO_PLAN_CHECKOUT)
    expect(built, 'the refusal spawned a child after all').toEqual([])

    // The read is refused for the missing directory and not for the
    // installation: the same binding answers another read, so the case cannot
    // pass by refusing every op of a broken source.
    const versioned = createBoundSource({ installation: () => FIRST, create: async (each) => aSource([], each.id) })
    expect((await versioned.version()).text).toBe(FIRST.id)
  })

  it('serves the document it read within the minute rather than spawning again', async () => {
    // GV2.1's cadence: on demand, at most once a minute. The cache is keyed by
    // the binding *and* the arguments, so `--all` is its own document rather
    // than the wider one, and the stamp is taken after the child answered, so
    // the interval floors the time between two spawns. Every spawn here is
    // counted, because "the read was served twice from one child" is the claim.
    const spawned: unknown[] = []
    let reads = 0
    const at = Date.parse('2026-10-15T12:00:00Z')
    let now = at
    let installation: BoundInstallation = { ...FIRST, checkout: '/home/operator/src/acme' }
    const source = createBoundSource({
      installation: () => installation,
      clock: () => new Date(now),
      create: async () => {
        return {
          version: async () => ({ text: FIRST.id, receivedAt: '2026-10-15T12:00:00Z' }),
          close: async () => undefined,
          planStatus: async (args: unknown): Promise<Snapshot> => {
            reads += 1
            spawned.push(args)
            return {
              document: { read: reads },
              receivedAt: new Date(now).toISOString().replace(/\.\d{3}Z$/, 'Z'),
              sequence: reads,
              day: null,
              rolled: false,
              notices: [],
            }
          },
        } as unknown as CliSource
      },
    })

    const first = await source.planStatus({})
    expect(first.document, 'the first read did not reach the child').toEqual({ read: 1 })
    now += 1_000
    const again = await source.planStatus({})
    expect(spawned, 'a read inside the minute spawned a second child').toHaveLength(1)
    expect(again, 'the cached answer was not the document that was read').toBe(first)

    // `--all` is a different document, not a wider view of the cached one.
    expect((await source.planStatus({ all: true })).document).toEqual({ read: 2 })
    expect(spawned).toHaveLength(2)

    // At exactly the interval the read runs again: the bound is inclusive, so a
    // one-minute screen is not served a two-minute-old graph.
    now = at + PLAN_STATUS_MIN_INTERVAL_MS
    expect((await source.planStatus({})).document, 'the read at the interval was served the cache').toEqual({ read: 3 })
    expect(spawned).toHaveLength(3)

    // A checkout change is a different binding: the graph belongs to the
    // directory, so the document cached for the previous one must not be
    // answered for the new one.
    now += 1_000
    installation = { ...FIRST, checkout: '/home/operator/src/other' }
    expect((await source.planStatus({})).document, 'the previous directory’s graph was served').toEqual({ read: 4 })
    expect(spawned).toHaveLength(4)
  })
})

// ---------------------------------------------------------------------------
// The plan's `--no-merges` argument and its floor (IR4 of the console-fixes
// plan, D3217 as amended).
// ---------------------------------------------------------------------------

describe('the plan read’s `noMerges` argument', () => {
  /** aPlanSource is the same stand-in as the cadence case's, with the bindings
   * this describe can flip: the installation the console is on, and the child
   * factory's recording of what each read was asked for. */
  function aPlanSource(installation: () => BoundInstallation, spawned: unknown[]): CliSource {
    let reads = 0
    return {
      version: async () => ({ text: FIRST.id, receivedAt: '2026-10-15T12:00:00Z' }),
      close: async () => undefined,
      planStatus: async (args: unknown): Promise<Snapshot> => {
        reads += 1
        spawned.push(args)
        return {
          document: { read: reads },
          receivedAt: '2026-10-15T12:00:00Z',
          sequence: reads,
          day: null,
          rolled: false,
          notices: [],
        }
      },
    } as unknown as CliSource
  }

  it('refuses `--no-merges` below the flag’s floor, before a child exists', async () => {
    // **The floor is asked before `resolve`**, on `orchestratorMap`'s terms: an
    // installation whose CLI predates the release that carries the flag answers
    // `unknown flag: --no-merges` and fails the whole read, which is a failure
    // the operator can do nothing about. The constant names no release on this
    // tree, so this is every installation today -- and the branch a release
    // takes is driven below with the predicate's own argument.
    const built: unknown[] = []
    const spawned: unknown[] = []
    const installation: BoundInstallation = { ...FIRST, checkout: '/home/operator/src/acme', version: '0.32.0' }
    const source = createBoundSource({
      installation: () => installation,
      create: async () => {
        built.push('child')
        return aPlanSource(() => installation, spawned)
      },
    })

    const error = await refusalOf(source.planStatus({ all: true, noMerges: true }))
    expect(error, 'a read below the flag’s floor was not refused').toBeInstanceOf(SourceRefusal)
    expect((error as SourceRefusal).message).toBe(NO_PLAN_NO_MERGES)
    expect(built, 'the refusal spawned a child after all').toEqual([])

    // **The refusal is the argument's and not the read's.** The same binding
    // still answers the plain read -- which is the whole point of the floor: an
    // older CLI gets the single read it has always had rather than a Graph tab
    // with no canvas.
    expect((await source.planStatus({ all: true })).document).toEqual({ read: 1 })
    expect(spawned).toEqual([{ all: true }])
  })

  it('passes the argument above the floor, and caches it as its own document', async () => {
    // The branch a release takes: the constant becomes the version whose flag
    // lands, and the source hands the word to the child. **The cadence is keyed
    // by the argv the arguments become**, so the canvas read and the plain read
    // are two documents and neither is served for the other -- without this the
    // merge section would draw the canvas document, or the first paint would be
    // handed the merge-inclusive one, for up to a minute.
    floor.merges = MERGES_FLOOR
    const spawned: unknown[] = []
    const installation: BoundInstallation = {
      ...FIRST,
      checkout: '/home/operator/src/acme',
      version: MERGES_VERSION,
    }
    const source = createBoundSource({
      installation: () => installation,
      create: async () => aPlanSource(() => installation, spawned),
    })

    // The first paint: `--all --no-merges`, served from the cache on a repeat.
    expect((await source.planStatus({ all: true, noMerges: true })).document).toEqual({ read: 1 })
    expect((await source.planStatus({ all: true, noMerges: true })).document).toEqual({ read: 1 })
    expect(spawned).toHaveLength(1)
    // The merge section: the same command with the lookups left in, and a child
    // of its own.
    expect((await source.planStatus({ all: true })).document).toEqual({ read: 2 })
    expect((await source.planStatus({ all: true })).document).toEqual({ read: 2 })
    expect(spawned).toHaveLength(2)
    expect(spawned).toEqual([{ all: true, noMerges: true }, { all: true }])
    // And `noMerges: false` is the same argv as no `noMerges` at all, which is
    // the plain read above rather than a third document.
    expect((await source.planStatus({ all: true, noMerges: false })).document).toEqual({ read: 2 })
    expect(spawned).toHaveLength(2)
  })
})

describe('the graph read, whose scope a previous answer taught', () => {
  /** aGraphSource is a `CliSource`-shaped stand-in whose graph read answers one
   * document per call: `documents` is what each call returns, in order, and the
   * calls themselves are recorded -- so "no child ran" is a count and not a
   * reading of the refusal's sentence. */
  function aGraphSource(
    documents: readonly Record<string, unknown>[],
    spawned: unknown[],
  ): CliSource {
    let calls = 0
    return {
      version: async () => ({ text: FIRST.id, receivedAt: '2026-11-07T12:00:00Z' }),
      close: async () => undefined,
      graphRead: async (args: unknown): Promise<Snapshot> => {
        spawned.push(args)
        const document = documents[calls] ?? documents[documents.length - 1] ?? {}
        calls += 1
        return {
          document,
          receivedAt: '2026-11-07T12:00:00Z',
          sequence: 1,
          day: null,
          rolled: false,
          notices: [],
        }
      },
    } as unknown as CliSource
  }

  it('refuses a scope no answer of this installation listed, and spawns nothing for it', async () => {
    // **Finding F21's second half, and the reason it is here rather than in the
    // spawn table.** The id grammar admits an id that exists nowhere, so a
    // renderer could name one; the surface closes that by admitting an id only
    // when a previous answer of the same installation listed it. `all` is the
    // one value admitted without being listed, because it is the selector rather
    // than a source (the Graph asks for it when the plan scope refuses for want
    // of a checkout, never as the read it opens on) -- and the refusal is thrown
    // **before** `resolve`, so no binary is probed and no child runs.
    const built: string[] = []
    const spawned: unknown[] = []
    const source = createBoundSource({
      installation: () => FIRST,
      create: async (each) => {
        built.push(each.id)
        return aGraphSource([{ sources: [{ source: 'coordinator' }] }], spawned)
      },
    })

    const error = await refusalOf(source.graphRead({ source: 'orchestrator:o9' }))
    expect(error, 'a scope no answer had listed was not refused').toBeInstanceOf(SourceRefusal)
    expect((error as SourceRefusal).message).toBe(NO_GRAPH_SCOPE)
    expect(built, 'the refusal built a source, which is a child').toEqual([])
    expect(spawned, 'the refusal spawned a read after all').toEqual([])

    // The first answer is the one that teaches the surface: `all` is admitted
    // with nothing listed, and its own `sources[]` are what may be asked for
    // afterwards. `orchestrator:o9` is refused again once they are known.
    const all = await source.graphRead({})
    expect(all.document['sources']).toEqual([{ source: 'coordinator' }])
    expect((await source.graphRead({ source: 'coordinator' })).document['sources']).toEqual([
      { source: 'coordinator' },
    ])
    expect(spawned).toHaveLength(2)
    const still = await refusalOf(source.graphRead({ source: 'orchestrator:o9' }))
    expect((still as SourceRefusal).message).toBe(NO_GRAPH_SCOPE)
    expect(spawned, 'the refused scope reached the source').toHaveLength(2)
  })

  it('keeps every scope an answer listed, serves one inside the minute, and forgets it with the installation', async () => {
    // **What is recorded is a union, and the reading is deliberate.** A scoped
    // read answers about its own scope alone, so a rule that kept only the most
    // recent answer would withdraw every other scope a screen is still drawing
    // the moment one scope was re-read -- and the read is a *lesson* about which
    // scopes exist rather than a snapshot of them. The cadence is
    // `GRAPH_READ_MIN_INTERVAL_MS`, keyed by the binding **and** the scope, on
    // the same terms as the plan read's above.
    const spawned: unknown[] = []
    const at = Date.parse('2026-11-07T12:00:00Z')
    let now = at
    const learned = [
      { sources: [{ source: 'coordinator' }, { source: 'local:o1' }] },
      { sources: [{ source: 'local:o1' }] },
    ]
    let installation: BoundInstallation | null = FIRST
    const source = createBoundSource({
      installation: () => installation,
      clock: () => new Date(now),
      create: async () => aGraphSource(learned, spawned),
    })

    await source.graphRead({})
    expect(spawned).toHaveLength(1)
    // The second call is inside the minute, so it is the first read's answer --
    // and the scoped read that follows is its own key, which is why it spawns.
    expect((await source.graphRead({})).document['sources']).toEqual(learned[0]?.['sources'])
    expect(spawned).toHaveLength(1)
    await source.graphRead({ source: 'local:o1' })
    expect(spawned).toHaveLength(2)
    // The scoped answer listed `local:o1` alone, and `coordinator` is still
    // admitted: nothing withdraws a scope an earlier answer named.
    now = at + GRAPH_READ_MIN_INTERVAL_MS
    expect((await source.graphRead({ source: 'coordinator' })).document['sources']).toEqual([
      { source: 'local:o1' },
    ])
    expect(spawned).toHaveLength(3)

    // A different installation has learned nothing: the scopes are the
    // project's own answers, so a switch is a clean slate rather than a licence
    // for another project's graph.
    installation = SECOND
    const afterSwitch = await refusalOf(source.graphRead({ source: 'coordinator' }))
    expect((afterSwitch as SourceRefusal).message).toBe(NO_GRAPH_SCOPE)
    expect(spawned, 'the previous installation’s scopes were still admitted').toHaveLength(3)
  })

  it('refuses a named orchestrator scope below the floor, before the membership rule and before any child', async () => {
    // **F40's widening sits behind a floor** (`ORCHESTRATOR_MAP_CLI_FLOOR`,
    // `src/shared/version.ts`): `GRAPH_SCOPE_RE` now admits C-ORCH's named id
    // (`orchestrator:<project>~<name>`, `src/main/ipc.ts`), and the release that
    // first reads one is the release that ships `orchestrator map --json`. The
    // constant is `null` today -- the command is registered from `ee/` and no
    // tag carries it -- so every named scope is refused here with the console's
    // own sentence about the CLI being too old, and **not** with `NO_GRAPH_SCOPE`:
    // an older CLI's document can never list a named scope either, so the
    // membership rule would give the same refusal for the wrong reason, and a
    // renderer would be told the scope is missing rather than that this
    // installation cannot read one.
    //
    // The refusal is thrown **before** `resolve`, so no binary is probed and no
    // child exists: `built` and `spawned` are both empty, which is what makes
    // this a fence rather than a sentence.
    const built: string[] = []
    const spawned: unknown[] = []
    const source = createBoundSource({
      installation: () => FIRST,
      create: async (each) => {
        built.push(each.id)
        return aGraphSource([{ sources: [{ source: 'coordinator' }] }], spawned)
      },
    })

    for (const scope of ['orchestrator:acme~blue', 'local:acme~blue']) {
      const error = await refusalOf(source.graphRead({ source: scope }))
      expect(error, scope).toBeInstanceOf(SourceRefusal)
      expect((error as SourceRefusal).message, scope).toBe(NO_NAMED_SCOPE)
    }
    // The feed is refused by the same guard, thrown where the subscription is
    // opened rather than when it is first polled -- so a screen cannot open a
    // live child on a spelling the installation's CLI does not read.
    expect(() => source.subscribeGraph({ source: 'orchestrator:acme~blue' })).toThrow(NO_NAMED_SCOPE)
    expect(built, 'a named scope built a source, which is a child').toEqual([])
    expect(spawned, 'a named scope spawned a read after all').toEqual([])

    // The unnamed scopes are untouched by the floor: `all` and an id-carrying
    // scope with no separator are the grammar the released CLI already reads.
    expect((await source.graphRead({})).document['sources']).toEqual([{ source: 'coordinator' }])
    expect(spawned).toHaveLength(1)
  })

  it('refuses the map read below the floor, and spawns nothing for it', async () => {
    // `orchestrator map --json` is on this tree and in no release
    // (`ORCHESTRATOR_MAP_CLI_FLOOR` is `null`), so the read the Coordinator tab
    // opens with is refused at the source: no binary probed, no child started,
    // and no `unknown command "map"` drawn as a failure the operator cannot act
    // on. The refusal is its own sentence rather than the named scope's, because
    // the states differ -- one is a read this installation cannot make at all,
    // the other a scope it cannot name -- and the two fixes are different lines
    // on the screen.
    const built: string[] = []
    const source = createBoundSource({
      installation: () => FIRST,
      create: async (each) => {
        built.push(each.id)
        return aSource([], each.id)
      },
    })

    const error = await refusalOf(source.orchestratorMap({}))
    expect(error).toBeInstanceOf(SourceRefusal)
    expect((error as SourceRefusal).message).toBe(NO_ORCHESTRATOR_MAP)
    expect(built, 'the refusal built a source, which is a child').toEqual([])

    // ...and with no installation chosen it is D13's state rather than this
    // floor's: the read has no version to ask about.
    const unbound = createBoundSource({ installation: () => null, create: async (each) => aSource([], each.id) })
    expect(await refusalOf(unbound.orchestratorMap({}))).toBeInstanceOf(NoInstallation)
  })

  /** aMapSource is a `CliSource`-shaped stand-in for the two reads the
   * Coordinator makes: `orchestratorMap` answers one document, and `graphRead`
   * answers a document listing the one scope it was asked for -- which is what a
   * scoped read of a real CLI does, and what keeps a later `graphRead`'s own
   * teaching from covering for the map's. Every call is recorded, so "the
   * refusal spawned nothing" is a count. */
  function aMapSource(map: Record<string, unknown>, spawned: unknown[]): CliSource {
    return {
      version: async () => ({ text: FIRST.id, receivedAt: '2026-11-07T12:00:00Z' }),
      close: async () => undefined,
      orchestratorMap: async (args: unknown): Promise<Snapshot> => {
        spawned.push(args)
        return {
          document: map,
          receivedAt: '2026-11-07T12:00:00Z',
          sequence: 1,
          day: null,
          rolled: false,
          notices: [],
        }
      },
      graphRead: async (args: unknown): Promise<Snapshot> => {
        spawned.push(args)
        const source = (args as { readonly source?: unknown }).source
        return {
          document: { sources: [{ source: typeof source === 'string' ? source : 'all' }] },
          receivedAt: '2026-11-07T12:00:00Z',
          sequence: 1,
          day: null,
          rolled: false,
          notices: [],
        }
      },
    } as unknown as CliSource
  }

  it('admits the scope of every box the map drew, and nothing the map did not', async () => {
    // **The drill-down's admission, and the reason the map read teaches at all**
    // (CG7b). The Coordinator draws one box per `nodes[].id` and the press on a
    // box opens that orchestrator through `graph.read --source
    // orchestrator:<id>`; the membership rule above admits a scope only once an
    // answer of this installation named it, and C-ORCH's map carries no
    // `sources[]` -- so the map read records its own nodes, the same union
    // `graphRead` records. Without that, the press the drawing itself invites
    // would be refused `NO_GRAPH_SCOPE`. An id the map did not draw is refused
    // all the same, which is what keeps this a rule rather than a licence.
    floor.value = MAP_FLOOR
    const spawned: unknown[] = []
    const source = createBoundSource({
      installation: () => ({ ...FIRST, version: MAP_VERSION }),
      create: async () => aMapSource(MAP, spawned),
    })

    const map = await source.orchestratorMap({})
    expect(spawned, 'the map read spawned no child').toHaveLength(1)
    expect(map.document['nodes']).toHaveLength(2)

    // One child each, because a scope is its own cadence key: the press is a
    // real read and not the map's own answer handed back.
    await source.graphRead({ source: 'orchestrator:acme' })
    await source.graphRead({ source: 'orchestrator:acme~blue' })
    expect(spawned).toHaveLength(3)

    const refused = await refusalOf(source.graphRead({ source: 'orchestrator:acme~green' }))
    expect((refused as SourceRefusal).message).toBe(NO_GRAPH_SCOPE)
    expect(spawned, 'the scope the map did not draw reached the source').toHaveLength(3)

    // A map with no `nodes[]` teaches nothing, on `graphSourcesOf`'s terms: a
    // malformed answer must not admit every scope rather than none.
    const bare = createBoundSource({
      installation: () => ({ ...FIRST, version: MAP_VERSION }),
      create: async () => aMapSource({}, []),
    })
    await bare.orchestratorMap({})
    const error = await refusalOf(bare.graphRead({ source: 'orchestrator:acme' }))
    expect((error as SourceRefusal).message).toBe(NO_GRAPH_SCOPE)
  })

  /** aMoveSource is `aGraphSource` plus the record's one write: what the next
   * confirmed move answers is read out of `outcome` at the moment it is called,
   * so a case can change it between two moves. */
  function aMoveSource(
    documents: readonly Record<string, unknown>[],
    spawned: unknown[],
    outcome: () => RoadmapMoveOutcome | null,
  ): CliSource {
    let calls = 0
    return {
      version: async () => ({ text: FIRST.id, receivedAt: '2026-11-07T12:00:00Z' }),
      close: async () => undefined,
      graphRead: async (args: unknown): Promise<Snapshot> => {
        spawned.push(args)
        const document = documents[calls] ?? documents[documents.length - 1] ?? {}
        calls += 1
        return {
          document,
          receivedAt: '2026-11-07T12:00:00Z',
          sequence: 1,
          day: null,
          rolled: false,
          notices: [],
        }
      },
      roadmapMove: async (): Promise<RoadmapMoveAnswer> => ({
        refusal: null,
        argv: [],
        task: { key: 'k1', id: 'plan/one', title: 'one' },
        from: null,
        to: null,
        outcome: outcome(),
        sentence: null,
      }),
    } as unknown as CliSource
  }

  it('drops the floor on the two answers that say the record moved under it', async () => {
    // **The events that make a held document wrong.** The floor is a claim that
    // the document has not changed: §6.24's `applied` is the record changed, and
    // a `refused` outcome carrying `current` is GV3.1's compare-and-set saying
    // the record holds something else than the document this source drew from.
    // Either way the screen re-reads before it offers the undo or the next move
    // (design section 5.1 rule 14), and that re-read is what re-teaches the
    // source the release the record holds **now** -- the `--expect` the inverse
    // and every further move are built with. Served from the floor, it would
    // answer the document from before, and the CLI's compare-and-set would refuse
    // the undo and every retry of that move. A refusal that names no `current`
    // changed nothing: the document the floor holds is still what the record says.
    const before = { sources: [{ source: 'coordinator' }], read: 'before' }
    const after = { sources: [{ source: 'coordinator' }], read: 'after' }
    const applied = { sources: [{ source: 'coordinator' }], read: 'after-the-apply' }
    const spawned: unknown[] = []
    const at = Date.parse('2026-11-07T12:00:00Z')
    let outcome: RoadmapMoveOutcome | null = null
    const source = createBoundSource({
      installation: () => FIRST,
      // The clock does not move: the minute the floor holds never runs out, so
      // a spawn after a move can only be the drop.
      clock: () => new Date(at),
      create: async () => aMoveSource([before, after, applied], spawned, () => outcome),
    })

    await source.graphRead({})
    expect(spawned).toHaveLength(1)
    expect((await source.graphRead({})).document).toEqual(before)
    expect(spawned).toHaveLength(1)

    // A refusal with nothing to say about what the record holds changed nothing,
    // so the document the floor holds is still the record's and the floor stays.
    outcome = { status: 'refused', request: 'r1', reason: 'no such release', current: null }
    await source.roadmapMove({ node: 'k1', toRelease: 'rel-a', confirmed: true })
    await source.graphRead({})
    expect(spawned, 'a refusal that named no current dropped the graph read’s floor').toHaveLength(1)

    // The stale `expect` is the other half: someone else moved the task, the
    // record says which release it holds now, and the document in hand is the one
    // from before that move.
    outcome = { status: 'refused', request: 'r2', reason: 'stale expect', current: 'rel-b' }
    await source.roadmapMove({ node: 'k1', toRelease: 'rel-a', confirmed: true })
    const moved = await source.graphRead({})
    expect(spawned, 'a stale-expect refusal was served from the floor').toHaveLength(2)
    expect(
      moved.document,
      'the re-read after the refusal answered the document from before the move it refused',
    ).toEqual(after)
    // And the new answer is the one the floor now holds, on the same terms as any
    // other read.
    expect((await source.graphRead({})).document).toEqual(after)
    expect(spawned).toHaveLength(2)

    outcome = { status: 'applied', request: 'r3', reason: null, current: null }
    await source.roadmapMove({ node: 'k1', toRelease: 'rel-b', confirmed: true })
    const fresh = await source.graphRead({})
    expect(spawned, 'the re-read after an applied move was served from the floor').toHaveLength(3)
    expect(fresh.document, 'the re-read answered the document from before the move').toEqual(applied)
  })
})

  it('says `configured` only for a record that carries an override, and `well-known` otherwise', () => {
    // `InstallationRef.source` is the one word that says *how* the binary was
    // chosen (design section 3.4 rule 4), and the ordinary case -- a record this
    // console wrote, which carries a project and no path, because it never
    // stores the path it resolves -- must not be drawn as the operator's own
    // choice. The binding is where that is decided: it carries the record's
    // **optional override** and nothing else, so an empty `binaryPath` is rule
    // 4's walk and the word says so.
    //
    // Read at call time, which is also asserted here: the ref is asked of
    // `deps.installation` on every read rather than captured when the source was
    // built, so the operator's next choose is what the next read answers. No
    // source is built and none is needed -- the ref is answered from the binding
    // alone, which is exactly the state a console is in before its first read.
    let installation: BoundInstallation = { id: FIRST.id, project: FIRST.project }
    const source = createBoundSource({ installation: () => installation })
    expect(source.installation, 'the ordinary record was not named as one rule 4 resolves').toMatchObject({
      binaryPath: '',
      source: 'well-known',
      project: FIRST.project,
    })

    installation = { id: FIRST.id, project: FIRST.project, binaryPath: '/opt/fugaro/bin/fugaro' }
    expect(source.installation, 'a record that carries an override was not named as the configured one').toMatchObject({
      binaryPath: '/opt/fugaro/bin/fugaro',
      source: 'configured',
    })
  })

  it('holds the once-a-minute floor against concurrent calls, failed reads and equivalent arguments', async () => {
    // Review (a): the floor was a cache of *answered* reads, so ten calls in
    // the same instant spawned ten children, a failed read was spawned again at
    // once, and `{}` and `{ all: false }` -- the same argv -- were two keys.
    let spawned = 0
    let fail = false
    let release: () => void = () => undefined
    const held = new Promise<void>((resolve) => {
      release = resolve
    })
    const at = Date.parse('2026-10-15T12:00:00Z')
    let now = at
    const source = createBoundSource({
      installation: () => ({ ...FIRST, checkout: '/home/operator/src/acme' }),
      clock: () => new Date(now),
      create: async () =>
        ({
          version: async () => ({ text: FIRST.id, receivedAt: '2026-10-15T12:00:00Z' }),
          close: async () => undefined,
          planStatus: async (): Promise<Snapshot> => {
            spawned += 1
            await held
            if (fail) throw new Error('the CLI refused')
            return { document: { read: spawned }, receivedAt: '2026-10-15T12:00:00Z', sequence: spawned, day: null, rolled: false, notices: [] }
          },
        }) as unknown as CliSource,
    })

    const calls = Array.from({ length: 10 }, (_, index) => source.planStatus(index % 2 === 0 ? {} : { all: false }))
    release()
    const answers = await Promise.all(calls)
    expect(spawned, 'concurrent calls each spawned a child').toBe(1)
    for (const answer of answers) expect(answer).toBe(answers[0])

    // A failed read holds the floor too: the next call inside the minute is
    // answered with the same refusal rather than a second child.
    now = at + PLAN_STATUS_MIN_INTERVAL_MS
    fail = true
    await expect(source.planStatus({})).rejects.toThrow()
    expect(spawned).toBe(2)
    now += 1_000
    await expect(source.planStatus({ all: false })).rejects.toThrow()
    expect(spawned, 'a failed read was spawned again inside the minute').toBe(2)
  })
})

/**
 * The four ways an installation can have no `fugaro` to read -- the resolution's
 * own `BinaryRefusalKind` (`src/main/spawn.ts`) -- and the one thing all four
 * share on this side of the seam: **no sidecar**. The refusal is thrown by
 * `CliSource.create` before `new CliSource(...)`, which is the object a
 * `fugaro watch` child lives in, so a console with no installation to read holds
 * no source, runs no child and has nothing to close.
 *
 * The three WSL kinds are the siblings of the absent-binary state (Part G, T-G1):
 * they reach this path the same way, and the build here is the shipped one's
 * shape exactly -- it throws the state `noInstallationState` builds from the
 * resolution's `kind`, at the point `CliSource.create` throws it.
 *
 * The second case drives `CliSource.create` itself, through the `resolver` seam,
 * because the *kind* a refusal carries is the one thing no build over this
 * machine's filesystem can pin: the three WSL kinds are produced by
 * `resolveInstalledBinary` and every refusal `resolveBinary` makes is
 * `no-binary`. Flattening that carry-through back to `'no-binary'` -- the defect
 * F26 exists to name -- would otherwise leave every test in this suite green.
 */
describe('a read that finds no installation to run', () => {
  it('refuses each of the four kinds as a NoInstallation, and runs no sidecar for any of them', async () => {
    for (const kind of ['no-binary', 'no-wsl', 'no-distro', 'no-fugaro-in-distro'] as const) {
      let attempts = 0
      const source = createBoundSource({
        installation: () => FIRST,
        create: () => {
          attempts += 1
          throw new NoInstallation(
            noInstallationState({ kind, reason: `the resolution failed as ${kind}`, home: '/nonexistent-home' }),
          )
        },
      })

      const error = await refusalOf(source.version())
      expect(error, kind).toBeInstanceOf(NoInstallation)
      expect((error as NoInstallation).state.kind, `${kind} lost its kind on the way to the screen`).toBe(kind)

      // **No sidecar.** The feed is the one thing this source starts a child
      // for, and on this path it hands out no stream at all: `latest` is null
      // before and after the read, so no `fugaro watch` was ever attached --
      // there is nothing running and nothing to stop.
      const feed = source.subscribeLive({})
      expect(feed.latest, `${kind} drew a feed for a console with nothing to read`).toBeNull()
      await expect(feed.current(), kind).rejects.toBeInstanceOf(NoInstallation)
      expect(feed.latest, `${kind} kept a feed and its child`).toBeNull()
      await feed.stop()

      // And the refused build is not cached as this installation's source: the
      // next read resolves again rather than being handed what was never made.
      expect(await refusalOf(source.version()), kind).toBeInstanceOf(NoInstallation)
      expect(attempts, `${kind}: the refused build was cached`).toBe(3)
    }
  })

  itHostRoute('refuses through `create` with the resolution’s own kind, whichever of the four it failed as', async () => {
    // The pin on the one line that carries the kind: `resolveBinary` answers
    // `no-binary` for every refusal it makes -- the WSL route is
    // `resolveInstalledBinary` and nothing in `resolveBinary` reaches it -- so a
    // build over this machine's filesystem can never hand `create` a resolution
    // that failed as a WSL kind. The seam is the only way to ask, and asking is
    // the point: `kind: resolution.kind` written as `'no-binary'` would send a
    // machine with no WSL to install a CLI, and no test that resolves for real
    // can tell the two apart.
    for (const kind of ['no-binary', 'no-wsl', 'no-distro', 'no-fugaro-in-distro'] as const) {
      const refused = await CliSource.create({
        target: { project: 'acme' },
        appEnv: {},
        home: '/nonexistent-home',
        resolver: async () => ({ ok: false, path: null, kind, reason: `the resolution failed as ${kind}` }),
      }).then(
        () => null,
        (cause: unknown) => cause,
      )
      expect(refused, kind).toBeInstanceOf(NoInstallation)
      const state = (refused as NoInstallation).state
      expect(state.kind, `${kind} was flattened on the way to the screen`).toBe(kind)
      expect(state.reason, kind).toBe(`the resolution failed as ${kind}`)
      // And the two fields the page draws beside the kind follow it: the install
      // line for the kind whose remedy is a command this machine can run, and
      // neither it nor a count of host places for the three the WSL route answers
      // -- that route asked `wsl.exe` and the distribution and walked no
      // directory here (F30's rule: the count, and never the paths).
      expect(state.installCommand, `${kind} offers the wrong remedy`).toBe(
        kind === 'no-binary' ? FUGARO_INSTALL_COMMAND : '',
      )
      if (kind === 'no-binary') {
        expect(state.searched.length, 'the walk the resolution made was not re-derived').toBeGreaterThan(0)
      } else {
        expect(state.searched, `${kind} carries a walk this route never made`).toEqual([])
      }
    }
  })
})
