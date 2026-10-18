// The bound source's `forget` (desktop-console plan Task 3.4c step 1, wiring
// 3.4b's deferred N1).
//
// `createBoundSource` is the join between the console's own state and the data
// plane, and `forget` is the one operation the IPC surface has no name for: the
// installation the console was reading from is gone, so nothing may stay bound
// to it. The interesting half is not the close of a source that exists -- it is
// the build that does not exist *yet*. Building a source resolves a binary and
// probes its version, and a removal can land in the middle of that: the console
// is reading from an installation at the moment the operator removes it.
//
// A build that finishes after the removal must not bind. `bound` is what every
// later read is handed, and a source bound for an installation the store no
// longer names is a `fugaro watch` child nobody holds and nobody will ever
// close -- which is the exact thing `forget` exists to prevent.

import { describe, expect, it } from 'vitest'

import { NoInstallation, createBoundSource } from '../../src/source/cli'
import type { BoundInstallation, CliSource } from '../../src/source/cli'

/** INSTALLATION is the installation the cases below read from. */
const INSTALLATION: BoundInstallation = {
  id: 'a1',
  project: 'acme',
  binaryPath: '/opt/fugaro/bin/fugaro',
}

/** aSource is a `CliSource`-shaped stand-in: it answers a version and records
 * its own close, which is the only thing these cases ask of a source. */
function aSource(closed: string[], name: string): CliSource {
  return {
    version: async () => ({ text: name, receivedAt: '2026-10-15T12:00:00Z' }),
    close: async () => {
      closed.push(name)
    },
  } as unknown as CliSource
}

describe('the source bound to the active installation', () => {
  it('binds one source per installation, and forget closes it once', async () => {
    let installation: BoundInstallation | null = INSTALLATION
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
    expect(built, 'two reads of one installation built two sources').toEqual(['a1'])

    installation = null
    await source.forget()
    await source.forget()
    expect(closed, 'forget did not close the source it unbound, exactly once').toEqual(['a1'])
    // And the read after the removal is D13's refusal rather than a source for
    // an installation nobody chose.
    await expect(source.version()).rejects.toBeInstanceOf(NoInstallation)
  })

  it('closes a source whose installation was removed while it was being built', async () => {
    let installation: BoundInstallation | null = INSTALLATION
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
    await expect(reading).rejects.toBeInstanceOf(NoInstallation)
    expect(closed, 'a source for the removed installation was left running').toEqual([
      'the removed installation',
    ])
  })
})
