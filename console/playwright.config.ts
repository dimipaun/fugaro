// The e2e harness's boundary (desktop-console plan Task 1.0; design section
// 9.2, Playwright driving the packaged app with `electron.launch`).
//
// Task 1.0 writes the two scripts every later task's command uses, and
// `npm run test:e2e` is one of them: `playwright test --pass-with-no-tests`, so
// it runs whatever specs exist and passes while none do (Task 1.1's
// `test/e2e/security.spec.ts` is the first, and Task 1.4's staged workflow
// depends on this script staying runnable before that spec lands).
//
// This file is what makes that true, and it exists for one reason worth writing
// down: Playwright's default `testMatch` accepts `*.test.ts` as well as
// `*.spec.ts`, so without a config it collects the VITEST unit tests under
// test/unit/ and fails on their ESM `import.meta` -- an e2e script that is red
// on a tree with no e2e spec at all. `testDir` keeps the two runners on their
// own files, permanently, rather than by naming a directory in the script.
import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: 'test/e2e',
  testMatch: /\.spec\.ts$/,
  // The Electron runtime is fetched lazily -- `require('electron')` spawns the
  // package's `install.js`, which unpacks the archive into
  // `node_modules/electron/dist` -- and Playwright's `electron.launch` resolves
  // that module immediately before it spawns the executable. On a cold
  // `node_modules` (every `npm ci --ignore-scripts`) two workers therefore
  // unpack the same archive at once, and one of them execs a file the other is
  // still writing: `Error: electron.launch: spawn ETXTBSY`, or ENOENT when the
  // file is not there yet. Both were in the same failed CI job (run
  // 39174336894, attempt 1, `console (ubuntu-latest)`), and the second is what
  // failed the release gate's required check.
  //
  // `globalSetup` runs once, in this process, before any worker is created, so
  // resolving the runtime there finishes the download -- writer process gone,
  // every descriptor closed -- before a launch exists to race it.
  // `test/support/launch.ts` resolves it again on the way into each launch and
  // retries ETXTBSY three times, 250 ms apart, as the smaller safety net.
  globalSetup: './test/support/globalSetup.ts',
  // `false` is Playwright's default, and it is named here because it is a
  // contract with the specs rather than a tuning knob: with `true`, Playwright
  // spreads ONE file's tests across workers, which runs that file's
  // `beforeAll` once per worker. `test/e2e/security.spec.ts` launches Electron
  // in `beforeAll` and states the opposite invariant -- one shared app and page
  // for the file, tests in order in a worker, the `activate` test last because
  // it is the one that takes the window down -- so `true` would launch one
  // Electron per worker and quietly break that ordering. Parallelism across
  // files is unaffected: it is workers, not this flag, that decides how many
  // spec files run at once.
  fullyParallel: false,
  // Task 2.1's goldens (`test/e2e/__screenshots__/live-{light,dark}.png`) are
  // compared, never written by a run. `'none'` writes nothing and FAILS a
  // screenshot whose baseline is missing; `'missing'` would write the baseline
  // and pass, so a golden nobody committed would compare nothing and no run
  // could ever be red for it. Recording is an explicit act, once on each
  // platform the comparison runs on (pixels differ between macOS and Linux):
  //   cd console && npm run build && npx playwright test live.spec.ts --update-snapshots
  // (the build is part of it -- the specs read `dist/`, and `npx playwright test`
  // does not build; on Linux it takes `xvfb-run --auto-servernum`).
  // The command-line flag overrides this option for that one run, which is what
  // `test/support/goldens.ts` reads to tell a recording run from a comparison
  // one: the freshness preflight stands aside for a recording, because the
  // matcher is what writes the baseline.
  updateSnapshots: 'none',
  // One PNG per theme PER PLATFORM (`live-light-darwin.png`,
  // `live-light-linux.png`): macOS and Linux draw different pixels, and the CI
  // CI runs the job on both legs (GitHub reports them as the check runs
  // `console (ubuntu-latest)` and `console (macos-latest)`, the two names
  // scripts/release-gate.sh requires), so a single unsuffixed file could only
  // ever be green on the platform that recorded it
  // (review round 2 of PR 494). `{platform}` is `process.platform`; no project
  // name is added, since there is one project.
  snapshotPathTemplate: '{testDir}/__screenshots__/{arg}-{platform}{ext}',
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? 'line' : 'list',
  // The app under test is an Electron binary, launched per spec with
  // `electron.launch` (Task 1.1), not a browser on a server: there is no
  // webServer to start, no baseURL, and connect-src 'none' in the app means the
  // specs assert what does NOT leave the window.
})
