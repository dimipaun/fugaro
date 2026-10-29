// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// Playwright's global setup: the one thing the console's e2e run does before
// any worker exists.
//
// It resolves the Electron runtime (see `test/support/electronRuntime.ts` for
// what that means and for the CI failure it fixes). This file runs once, in
// Playwright's main process, after the config is read and **before the first
// worker is forked**, so the download the electron package does on a cold
// `node_modules` happens exactly once and finishes -- writer process exited,
// descriptors closed -- before any spec is able to `electron.launch` it. That
// ordering is the fix for the ETXTBSY/ENOENT race; the retry in `launch.ts` is
// the smaller safety net behind it.
//
// It is a plain function and not a step in `npm run test:e2e`, because the
// command is not the only way in: `npx playwright test test/e2e/live.spec.ts`
// and `npm run test:e2e -- security` are both documented commands, and a setup
// step on one of them would leave the other racing. Playwright runs this for
// every invocation of the config, test selection included.

import type { FullConfig } from '@playwright/test'

import { resolveElectronBinary } from './electronRuntime'

/**
 * globalSetup makes the Electron runtime present and complete, once, for the
 * whole run. It reports the path it resolved: a CI log that says where the
 * runtime came from is what makes a later launch failure readable.
 */
export default function globalSetup(_config: FullConfig): void {
  const binary = resolveElectronBinary()
  // The run's own log line, and the only place the resolution is visible before
  // the first spec prints anything: CI's e2e step is otherwise silent until a
  // worker starts, which is what made the ENOENT/ETXTBSY pair hard to read.
  process.stdout.write(`[electron] runtime ready before any worker: ${binary}\n`)
}
