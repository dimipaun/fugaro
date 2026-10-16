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
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? 'line' : 'list',
  // The app under test is an Electron binary, launched per spec with
  // `electron.launch` (Task 1.1), not a browser on a server: there is no
  // webServer to start, no baseURL, and connect-src 'none' in the app means the
  // specs assert what does NOT leave the window.
})
