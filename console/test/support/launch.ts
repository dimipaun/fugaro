// The one way the e2e specs start the console (`_electron.launch`), and the two
// things every launch has to do before it spawns anything.
//
// Every spec used to call Playwright's `electron.launch` itself. Two of those
// calls, in two Playwright workers, were what CI's intermittent
// `Error: electron.launch: spawn ETXTBSY` came from: the Electron runtime is
// fetched lazily by the first `require('electron')` in each worker, so on a
// cold `node_modules` both workers unpacked the same archive into the same
// `node_modules/electron/dist` at once and one of them exec'd a file the other
// still held open for writing (ETXTBSY), or exec'd it before it existed
// (ENOENT). `test/support/electronRuntime.ts` has the full account, and
// `test/support/globalSetup.ts` is the fix: the runtime is resolved once,
// before any worker is created.
//
// This module is the call site of that ordering, and it keeps the specs from
// having to know about it:
//
//   1. it resolves the Electron binary first, so the runtime is on disk and
//      complete in *this* process before the launch spawns it;
//   2. it retries ETXTBSY, and only ETXTBSY, three times, 250 ms apart
//      (`test/support/textFileBusy.ts`) -- the belt beside the global setup's
//      brace, for a `dist/` something outside this run is still writing.
//
// Nothing else is wrapped: a launch that fails for any other reason reaches the
// spec unchanged, with Playwright's own message and call log.

import { _electron as electron } from '@playwright/test'
import type { ElectronApplication } from '@playwright/test'

import { resolveElectronBinary } from './electronRuntime'
import { retryOnTextFileBusy } from './textFileBusy'

/** ConsoleLaunch is what a spec hands `launchConsole`: exactly the two options
 * every spec passes Playwright -- the app directory plus the profile's
 * `--user-data-dir` in `args`, and the environment that decides which `fugaro`
 * the console resolves (`CliStub.launchArgs`/`CliStub.env`). */
export interface ConsoleLaunch {
  /** Electron's argv: the app directory first, then the switches. */
  readonly args: string[]
  /** The child's whole environment. Every value is a string (no `undefined`),
   * so the child receives exactly the keys given. */
  readonly env: Record<string, string>
}

/**
 * launchConsole starts the console, with the Electron runtime resolved and the
 * launch retried on ETXTBSY.
 */
export async function launchConsole(options: ConsoleLaunch): Promise<ElectronApplication> {
  resolveElectronBinary()
  return retryOnTextFileBusy(() => electron.launch({ args: options.args, env: options.env }))
}
