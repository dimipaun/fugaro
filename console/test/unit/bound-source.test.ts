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

import { describe, expect, it } from 'vitest'

import { NoInstallation, createBoundSource } from '../../src/source/cli'
import type { BoundInstallation, CliSource } from '../../src/source/cli'
import { SourceRefusal } from '../../src/source/source'

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
})
