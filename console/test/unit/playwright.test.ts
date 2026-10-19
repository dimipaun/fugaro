// playwright.config.ts's parallelism, held to the invariant the e2e spec is
// written against (desktop-console plan Task 1.1, review round on PR #437).
//
// `test/e2e/security.spec.ts` launches Electron once, in `beforeAll`, and the
// whole file is written against the instance that comes back: one app and one
// page, shared by every test, with the `activate` test last because it is the
// one that takes the window down to zero windows and builds a new one. That is
// an assumption about the runner, not about the app, and the runner only gives
// it when `fullyParallel` is false -- Playwright's default, which is why the
// config names it rather than leaving it out.
//
// With `fullyParallel: true`, Playwright spreads ONE file's tests across
// workers and runs that file's `beforeAll` once per worker, so the file's eight
// tests become up to eight concurrent Electron apps, each with its own page,
// and the "last test in the file" ordering that the `activate` test depends on
// stops meaning anything. Measured on the pinned Playwright 1.64.1 with a
// scratch spec of four tests in one file: `false` ran all four in worker 0
// (one pid); `true` split them over two workers (two pids) on a two-worker
// machine, and would use more where more workers are free.
//
// Nothing asserted in the spec breaks under `true` today -- it is a claim about
// how many Electron processes the run spawns and in what order, so no
// assertion in that file can see it. This is the file that can: it reads the
// config the way Playwright does, so the flag is a checked contract rather than
// a comment, and flipping it back to `true` fails here.
//
// It is a unit test rather than a fifth e2e test on purpose: it is a property
// of the config file, and reading it needs no app, no display and no Electron
// runtime -- which is exactly the environment where the e2e file cannot run at
// all.

import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

// The config as Playwright loads it: `defineConfig` returns the object it is
// given, so the default export is the value the runner reads. Importing it (as
// opposed to parsing the file's text) is what keeps this test honest about
// conditionals inside the config, of which it has two (`forbidOnly` and
// `retries` both branch on `process.env.CI`).
import config from '../../playwright.config'

/** E2E_DIR is where the specs live, from this file's location. */
const E2E_DIR = fileURLToPath(new URL('../e2e', import.meta.url))

/**
 * The two ways a spec can start Electron without going through
 * `test/support/launch.ts`, and therefore without the runtime being resolved
 * before the spawn and without the ETXTBSY retry.
 *
 * `_electron` is matched as a bare token rather than as `_electron as electron`
 * (review of this PR): Playwright exports the launcher under the underscore
 * name, so `import { _electron } from '@playwright/test'` followed by
 * `_electron.launch(...)` is the *likeliest* way a spec goes back to launching
 * itself -- and `\b` does not match between `_` and `e`, so a call pattern of
 * `\belectron\.launch\(` reads that call as an ordinary member call and the
 * guard stays green. `DIRECT_LAUNCH_CALL` accepts the optional underscore for
 * the same reason. Both are asserted against the spellings in this file's own
 * test, so loosening one is not a silent change.
 */
const DIRECT_LAUNCH_IMPORT = /_electron/
const DIRECT_LAUNCH_CALL = /_?electron\.launch\(/

describe('playwright.config.ts', () => {
  it('is the config the e2e run uses', () => {
    // The anchor for the assertion below: a config reached through the wrong
    // path, or one whose fields had been emptied, would satisfy
    // "fullyParallel is false" as easily as the real one does.
    expect(config.testDir, 'this is not the e2e config file').toBe('test/e2e')
    expect(config.testMatch).toEqual(/\.spec\.ts$/)
  })

  it('keeps a spec file in one worker, in order: fullyParallel must be false', () => {
    expect(
      config.fullyParallel,
      'fullyParallel: true spreads one file\'s tests across workers and runs its beforeAll once per worker, so test/e2e/security.spec.ts would launch one Electron per worker and lose the shared app/page instance its tests and its "last test in the file" ordering (the activate test) depend on. Playwright\'s default is false; this config sets it to false explicitly, and a change back to true is a deliberate edit to the spec\'s premises, which has to change that file too.',
    ).toBe(false)
  })

  it('fails on a missing screenshot baseline: updateSnapshots must be "none"', () => {
    // `'missing'` makes toHaveScreenshot write its own baseline and PASS, so a
    // golden that was never committed compares nothing and no run can ever be
    // red for it (the Task 2.1 review's high finding). `'none'` never writes
    // and fails a missing baseline; recording is an explicit act
    // (`playwright test --update-snapshots`), not a side effect of a run.
    expect(
      config.updateSnapshots,
      'a missing golden must fail the run: use "none" (recording is `npx playwright test --update-snapshots`, run on the CI platform)',
    ).toBe('none')
  })

  it('resolves the Electron runtime before any worker exists: globalSetup must be set', () => {
    // The intermittent `Error: electron.launch: spawn ETXTBSY` (and its ENOENT
    // twin: the same failed CI job, run 39174336894 attempt 1) is the electron
    // package unpacking its runtime into `node_modules/electron/dist` while
    // another Playwright worker execs the same file. `globalSetup` runs once,
    // in the main process, BEFORE the first worker is forked, so resolving the
    // binary there is what removes the race rather than papering over it.
    // Removing this line reintroduces it; changing it is a deliberate edit that
    // has to explain where the runtime is fetched instead.
    expect(
      config.globalSetup,
      'the Electron runtime must be resolved once, before any worker can launch it: without globalSetup two workers unpack node_modules/electron/dist at the same time and one of them execs a file the other is writing (ETXTBSY), or execs it before it exists (ENOENT)',
    ).toBe('./test/support/globalSetup.ts')
  })

  it('keeps one golden per theme per platform, in test/e2e/__screenshots__', () => {
    // CI runs the e2e pass on macOS and on Linux under one check name, and the
    // two draw different pixels: the file name carries the platform, so each
    // leg compares against its own recording (review round 2 of PR 494).
    expect(config.snapshotPathTemplate).toBe('{testDir}/__screenshots__/{arg}-{platform}{ext}')
  })

  it('is the file Playwright calls, and not an empty stand-in', async () => {
    // The path above is asserted as a string; this is the other half -- the
    // module at that path exports the one function Playwright's loader looks
    // for. Importing it here is safe and deliberate: the module resolves the
    // binary only when the function is *called*, and a unit suite that
    // downloaded an Electron runtime would be a unit suite that cannot run on a
    // machine with no Chromium libraries at all.
    const setup: { default?: unknown } = await import('../support/globalSetup')
    expect(typeof setup.default, 'test/support/globalSetup.ts must default-export the setup function').toBe('function')
  })

  it('launches every spec through test/support/launch.ts, never electron.launch directly', () => {
    // The helper is where the runtime is resolved before the spawn and where
    // ETXTBSY is retried. A spec that went back to calling `electron.launch`
    // itself would look correct, pass, and reintroduce the flake on a cold
    // `node_modules` -- the failure mode this guard exists for, since only the
    // worker that spawns the binary can see it.
    const specs = readdirSync(E2E_DIR)
      .filter((name) => name.endsWith('.spec.ts'))
      .sort()
    expect(specs.length, 'there are specs to check, and this guard covers every one of them').toBeGreaterThan(0)
    for (const spec of specs) {
      const text = readFileSync(join(E2E_DIR, spec), 'utf8')
      expect(text, `${spec} imports Playwright's Electron launcher directly; use launchConsole from ../support/launch`).not.toMatch(
        DIRECT_LAUNCH_IMPORT,
      )
      expect(text, `${spec} calls electron.launch directly; use launchConsole from ../support/launch`).not.toMatch(
        DIRECT_LAUNCH_CALL,
      )
    }
  })

  it('recognises every spelling of a direct launch, the `_electron` alias included', () => {
    // The patterns above are the guard, so they get their own test: the first
    // spelling is the one this PR rewrote, and the second is the one that would
    // slip past a guard written as `\belectron\.launch\(` -- `\b` does not match
    // between `_` and `e`, so an aliased import called as `_electron.launch(`
    // reads as an ordinary member call to that pattern. A guard with that hole
    // would be green while a spec raced itself again.
    const direct = [
      "import { _electron as electron } from '@playwright/test'\nelectron.launch({ args: [] })",
      "import { _electron } from '@playwright/test'\n_electron.launch({ args: [] })",
      "import { _electron as e } from '@playwright/test'\ne.launch({ args: [] })",
      "const { _electron } = require('@playwright/test')\n_electron.launch({ args: [] })",
    ]
    for (const source of direct) {
      expect(source, 'a direct launch must match one of the two patterns').toMatch(DIRECT_LAUNCH_IMPORT)
    }
    expect(direct[0], 'the rewritten spelling, called through the alias').toMatch(DIRECT_LAUNCH_CALL)
    expect(direct[1], 'the rewritten spelling, called under its own name').toMatch(DIRECT_LAUNCH_CALL)

    // And the spelling every spec uses now is not a false positive: the guard
    // must not fail the helper it exists to point specs at.
    const throughHelper = "import { launchConsole } from '../support/launch'\nlaunchConsole({ args: [], env: {} })"
    expect(throughHelper, 'launchConsole is not a direct launch').not.toMatch(DIRECT_LAUNCH_IMPORT)
    expect(throughHelper).not.toMatch(DIRECT_LAUNCH_CALL)
  })
})
