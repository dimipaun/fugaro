// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The settings write's one newer word is held behind a CLI floor, and this file
// is where that wiring is asserted at the level it lives (the review of AP8,
// round 2).
//
// `CliSource`'s `#plan` is the **one** builder of a settings edit's plan, so the
// single line
//
//   noApply: noApplySupported(this.installation.version)
//
// is the whole of what decides whether `settings set|unset` is handed
// `--no-apply`. Nothing else in the console asks that question, so a regression
// there -- the fact dropped, or written `true` -- would send the word to every
// installed CLI, and every confirmed settings edit would fail whole with
// `fugaro: unknown flag: --no-apply`. No test saw it: the argv tables
// (`spawn.test.ts`) and the confirmation flow (`confirm.test.ts`) both assert
// through `argvFor` with the fact handed in, which is the level **below** the
// decision.
//
// **The floor is 0.26.0 today** (`NO_APPLY_CLI_FLOOR`: AP5/AP6 of the
// composite-commands plan are what register the word), and the test installation
// below runs a stub CLI of its own version, so the branch above the floor is
// reached only by overriding the predicate's own default and the branch below it
// by leaving it unset. That is the one thing this file mocks -- the module is the real
// one with `noApplySupported`'s default floor read from a holder a case sets,
// which is the same parameter `version.test.ts` passes by hand -- and it is what
// makes both directions of the line assertions rather than one.

import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { SettingsEditPhase } from '../../src/main/spawn'
import { CliSource } from '../../src/source/cli'
import { SETTINGS_EDIT_KEY, installStub } from './stub'
import type { Stub } from './stub'
import { itHostRoute } from '../support/hostRoute'

/** floor is the floor this file's cases set: `null` stands for a CLI no floor
 * answers for, and a version line stands for the release that carries
 * `--no-apply` on the CLI's own side. */
const floor = vi.hoisted(() => ({ value: null as string | null }))

vi.mock('../../src/shared/version', async (importOriginal) => {
  const real = await importOriginal<typeof import('../../src/shared/version')>()
  return {
    ...real,
    // The real predicate with the one thing changed that a test cannot reach
    // otherwise: its default. `real.noApplySupported(version, null)` is `false`
    // and `real.noApplySupported(version, '1.4.0')` is the version comparison
    // every other floor makes, so a case sets a floor and the source's own call
    // -- which passes no floor at all -- reads it.
    noApplySupported: (version: string, at: string | null = floor.value) => real.noApplySupported(version, at),
  }
})

const PROJECT = 'fugaro'
/** VERSION is the stub's own `version` line: the version every case below asks
 * the floor about. */
const VERSION = '1.4.2'
const KEY = 'agent.max_output_tokens'
const VALUE = '8000'
/** LAYER is one of `SETTINGS_EDIT_LAYERS`, which is what the plan accepts. */
const LAYER = 'project'
/** WORD is spelled rather than imported: the claim is about this argv word, and
 * a case that read the constant would follow a rename of it silently. */
const WORD = '--no-apply'

const SET = { op: 'set', key: KEY, value: VALUE }

function freshDirectory(): string {
  return mkdtempSync(join(tmpdir(), 'fugaro-settings-floor-'))
}

/** aSource is a real `CliSource` over the stub, built the way the app builds
 * one: the binary is the stub, so the version line the floor is asked about is
 * the stub's own answer. */
async function aSource(version = VERSION): Promise<{ readonly source: CliSource; readonly stub: Stub }> {
  const stub = installStub(freshDirectory(), {
    commands: { version: { stdout: `${version}\n` }, [SETTINGS_EDIT_KEY]: { stdout: 'saved\n' } },
  })
  const source = await CliSource.create({
    target: { project: PROJECT },
    configuredBinaryPath: stub.binary,
    appEnv: {},
  })
  return { source, stub }
}

/** asChild is a plan's argv as the child itself reports it: the stub writes its
 * own `process.argv`, which is the plan's words without the program at
 * `plan.argv[0]` (the binary the plan starts). Every comparison below is
 * between the two readings of **one** argv, so a word the console added between
 * the plan and the spawn shows up here. */
function asChild(argv: readonly string[]): readonly string[] {
  return argv.slice(1)
}

/** spawned is one write's own argv as the child saw it, and it is what left the
 * console rather than what the console believes it sent. The plan the window was
 * shown is the source's own, so a difference between the two is
 * `SETTINGS_EDIT_MOVED` and no child at all -- which the assertion on the
 * refusal below is here to catch. */
async function spawned(
  source: CliSource,
  stub: Stub,
  phase: SettingsEditPhase = 'narrowing',
): Promise<readonly string[]> {
  const shown = await source.settingsEditArgv(SET, phase, LAYER)
  const answer = await source.settingsEdit(SET, phase, LAYER, shown)
  expect(answer.refusal).toBeNull()
  return stub.reports().at(-1)?.argv ?? []
}

describe('the settings write’s floor for `--no-apply`', () => {
  beforeEach(() => {
    floor.value = null
  })

  itHostRoute('hands the word to no child while the floor names no release', async () => {
    // The shipped state, and the one that must hold until AP5/AP6 land the
    // flag: the floor is unset, so no version line is above it.
    const { source, stub } = await aSource()

    // The dry run never carries it: it runs no step, so it owes no skip word.
    const dryRun = await source.settingsEditArgv(SET, 'dry-run', LAYER)
    expect(dryRun).toContain('--dry-run')
    expect(dryRun).not.toContain(WORD)

    // Neither the plan the window is shown nor the argv the child is started
    // with, which are the same argv (`settingsEdit` refuses a difference).
    const shown = await source.settingsEditArgv(SET, 'narrowing', LAYER)
    expect(shown).not.toContain(WORD)
    expect(await spawned(source, stub)).toEqual(asChild(shown))

    // **No version line is above an unset floor**, which is the fact the
    // constant's own shape states (`version.test.ts`): a second installation
    // whose binary answers a version no release has reached is handed nothing
    // either, so the console cannot be made to send the word by a `fugaro` that
    // claims to be new.
    const future = await aSource('99.99.99')
    expect(await future.source.settingsEditArgv(SET, 'narrowing', LAYER)).not.toContain(WORD)
    expect(await spawned(future.source, future.stub)).not.toContain(WORD)
  })

  itHostRoute('keeps the word off a version below the floor a release will set', async () => {
    // The other side of the comparison: a floor a release has named, and a
    // binary that is not at it yet. The write goes out exactly as it does today.
    floor.value = '1.5.0'
    const { source, stub } = await aSource()
    // The pair this case compares: the floor names 1.5.0, and the binary's own
    // answer is the line the stub printed.
    expect(source.installation.version).toBe(VERSION)

    const shown = await source.settingsEditArgv(SET, 'narrowing', LAYER)
    expect(shown).not.toContain(WORD)
    expect(await spawned(source, stub)).not.toContain(WORD)
  })

  itHostRoute('hands the word to the write once the version line is at the floor', async () => {
    // And the branch a release takes: the floor names a version this binary is
    // at, so the source's own call -- with no floor argument of its own -- has
    // to answer for it. A `#plan` that wrote the fact as a literal `false`, or
    // dropped it, fails here; one that wrote it as `true` fails the two cases
    // above.
    floor.value = '1.4.0'
    const { source, stub } = await aSource()

    const shown = await source.settingsEditArgv(SET, 'widening', LAYER)
    expect(shown).toContain(WORD)
    // A widening and a narrowing spell the same write argv (D1572): what the
    // classification decides is the window's own tick, not a word the CLI
    // reads.
    expect(await spawned(source, stub, 'widening')).toEqual(asChild(shown))
    expect(await spawned(source, stub, 'narrowing')).toEqual(asChild(shown))

    // **The word is the write's alone**, even at the floor: the phase table's
    // dry run is `--dry-run --json` and nothing else, so a floor that leaked
    // into the first spawn would be a dry run that skipped steps it does not
    // run at all.
    const dryRun = await source.settingsEditArgv(SET, 'dry-run', LAYER)
    expect(dryRun).not.toContain(WORD)
    expect(dryRun).toContain('--dry-run')
    expect(dryRun).toContain('--json')
  })
})
