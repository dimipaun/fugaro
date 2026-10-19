// The Electron runtime, resolved once per run -- before anything can launch it.
//
// The console installs with `npm ci --ignore-scripts` (design section 5.2, and
// console/README.md's "Commands"), so the electron package's `postinstall`
// never runs and `node_modules/electron/` holds no runtime after an install.
// The runtime arrives later, and the file that fetches it is the package's own
// `index.js`: `require('electron')` reads `path.txt`, and when the executable
// is missing it spawns `install.js`, which downloads the archive through
// `@electron/get`, unpacks it into `node_modules/electron/dist/` -- writing
// `dist/electron` as it goes -- and finally writes `path.txt`. Its first line
// of output is the `Downloading Electron binary...` a CI log shows.
//
// That is a *writer*, and Playwright's `electron.launch` resolves the very same
// module immediately before it spawns the executable. With more than one
// Playwright worker on a cold `node_modules`, two workers reach that writer at
// the same instant: both unpack into the same `dist/`, and whichever spawns
// first execs a binary the other still holds open for writing. Linux refuses
// that with ETXTBSY; a worker that spawns before the archive has produced the
// file at all gets ENOENT instead. Both were observed in the same failed CI
// job (run 39174336894, attempt 1, `console (ubuntu-latest)`): the first
// attempt of `test/e2e/degraded.spec.ts` failed with
// `electron.launch: Failed to launch: Error: spawn .../dist/electron ENOENT`
// and its retry, a second later, with `electron.launch: spawn ETXTBSY` -- while
// `test/e2e/entry.spec.ts`'s launch in the other worker printed
// `Downloading Electron binary...` at the same moment.
//
// The fix is an ordering, not a sleep: resolve the binary in Playwright's
// `globalSetup` (`test/support/globalSetup.ts`), which runs once in the main
// process before any worker is created, so the download completes -- the writer
// process exits, which closes every descriptor it held -- before the first
// launch exists to race it. `launchConsole` resolves it again on the way into
// every launch, which is free once the module is cached and is the state the
// launch helper wants to be true of its own accord: no worker execs a binary it
// has not just seen on disk. The bounded ETXTBSY retry in `launch.ts` is the
// belt beside that brace, for a machine whose `dist/` is being written by
// something this run does not control.

import { existsSync } from 'node:fs'

/** ELECTRON_MODULE is the file Playwright resolves too: the electron package's
 * entry, whose `require` is what unpacks the runtime when it is missing. It is
 * spelled `electron/index.js`, exactly as `playwright-core`'s Electron launcher
 * spells it, rather than the bare `electron` specifier. */
const ELECTRON_MODULE = 'electron/index.js'

/**
 * resolveElectronBinary asks the electron package for the executable it would
 * run, and returns its path.
 *
 * On a tree where `node_modules/electron/dist` is missing -- which is every
 * fresh `npm ci --ignore-scripts` -- this *is* the download: `index.js` spawns
 * `install.js` synchronously and only returns once the archive has been
 * unpacked and `path.txt` written, so when this function returns, the file it
 * names is complete and no writer holds it. On a tree where the runtime is
 * already there it is an existence check and a `readFileSync`, which is what
 * makes it cheap enough to call before every launch.
 */
export function resolveElectronBinary(): string {
  // A `require`, not an `import`: the electron package's entry *is* a CommonJS
  // module whose `module.exports` is a string path (its own `index.js` calls
  // `process.exit`-style `spawnSync` on the way to it), and these spec files are
  // loaded as CommonJS by Playwright -- the same reason `__dirname` is in use
  // beside it. A static `import` would also be hoisted and evaluated before
  // anything in this module could decide to defer it.
  const binary: unknown = require(ELECTRON_MODULE)
  if (typeof binary !== 'string' || binary === '') {
    throw new Error(`${ELECTRON_MODULE} did not resolve to a path: got ${String(binary)}`)
  }
  if (!existsSync(binary)) {
    throw new Error(
      `${ELECTRON_MODULE} resolved to ${binary}, which is not on disk: the Electron runtime is not installed`,
    )
  }
  return binary
}
