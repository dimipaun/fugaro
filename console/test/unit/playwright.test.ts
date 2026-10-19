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

import { describe, expect, it } from 'vitest'

// The config as Playwright loads it: `defineConfig` returns the object it is
// given, so the default export is the value the runner reads. Importing it (as
// opposed to parsing the file's text) is what keeps this test honest about
// conditionals inside the config, of which it has two (`forbidOnly` and
// `retries` both branch on `process.env.CI`).
import config from '../../playwright.config'

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

  it('keeps one golden per theme per platform, in test/e2e/__screenshots__', () => {
    // CI runs the e2e pass on macOS and on Linux under one check name, and the
    // two draw different pixels: the file name carries the platform, so each
    // leg compares against its own recording (review round 2 of PR 494).
    expect(config.snapshotPathTemplate).toBe('{testDir}/__screenshots__/{arg}-{platform}{ext}')
  })
})
