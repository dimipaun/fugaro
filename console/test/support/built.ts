// The built tree, for the unit tests that are about what `npm run build`
// produces (desktop-console plan Task 3.4a).
//
// Two unit tests read the build's output rather than the sources --
// `preload-surface.test.ts` holds the *compiled* `dist/preload/index.js` to its
// one `require`, and `bundle.test.ts` holds `dist/bundle/` to `SERVED_FILES` and
// the bundle to the modules it was made of -- and neither can assume a `dist/`
// that a previous command happened to leave behind. The plan's own command
// builds first (`npm run build && npm run test:unit`), but the workflow does
// **not**: `console-tests.yml` runs `npm run test:unit` on a fresh checkout,
// straight after `npm ci`. A test that read `dist/` unconditionally would fail
// on the first job and pass locally, which is the worst of both.
//
// So this module builds, once, when what is on disk is older than what it was
// made from:
//
//   - the two steps are `npm run build`'s own (`tsc -p tsconfig.build.json`,
//     then the bundler), so nothing here is a second build;
//   - `tsc` runs as a child process, because it is a compiler and this is a
//     test; the bundler is *imported*, because it is a function of two paths;
//   - the work is guarded by a lock file in `dist/`, because vitest runs test
//     files in parallel workers and two of them compiling into one `dist/` at
//     once would be two compilers writing the same files.
//
// Nothing here builds into a temporary directory: `dist/bundle` is the tree the
// console actually serves (`rendererRoot` in `src/main/index.ts`), and a test
// that checked a private copy of it would be checking the pipeline rather than
// what `npm run build` leaves in the checkout.

import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { bundleRenderer, bundleStylesheet, consoleBundles, consoleStylesheet } from '../../tools/bundle-renderer/index'

/** CONSOLE_ROOT is the console package, from this file's location. */
export const CONSOLE_ROOT = fileURLToPath(new URL('../../', import.meta.url))

/** DIST is the compiled tree package.json's `main` points into. */
export const DIST = join(CONSOLE_ROOT, 'dist')

/** BUNDLE_DIR is the served root: the one directory the protocol handler reads
 * a document or a script from. */
export const BUNDLE_DIR = join(DIST, 'bundle')

/** LOCK is the file two workers agree on while one of them is building. */
const LOCK = join(DIST, '.unit-build.lock')

/** STALE_LOCK_MS is when a lock is old enough to be somebody's crash rather
 * than somebody's build. A build here is a second or two; a minute is far past
 * it and still short enough that a wedged worker does not hang the suite. */
const STALE_LOCK_MS = 60_000

/** WAIT_LIMIT_MS is how long a worker waits for the other one's build before
 * giving up loudly rather than building a second time. */
const WAIT_LIMIT_MS = 120_000

/** OUTPUTS are the files a build produces, as this suite reads them: the entry
 * points Electron loads (with the picker's preload and entry, Task 3.4b) and
 * the six served files -- the stylesheet among them since Task 6.2, because a
 * `console.css` that is missing or stale is exactly what the stylesheet cases
 * in `bundle.test.ts` read. */
const OUTPUTS: readonly string[] = Object.freeze([
  join(DIST, 'bundle', 'console.css'),
  join(DIST, 'bundle', 'console.js'),
  join(DIST, 'bundle', 'index.html'),
  join(DIST, 'bundle', 'installations.html'),
  join(DIST, 'bundle', 'installations.js'),
  join(DIST, 'bundle', 'refusal.html'),
  join(DIST, 'main', 'index.js'),
  join(DIST, 'preload', 'index.js'),
  join(DIST, 'preload', 'installations.js'),
  join(DIST, 'renderer', 'entry.js'),
  join(DIST, 'renderer', 'installations.js'),
])

/** sourced is every `.ts`, `.html` and `.css` file under a directory,
 * recursively. The `.css` half is the theme's: `dist/bundle/console.css` is
 * concatenated from `src/renderer/theme/*.css` (Task 6.2), so an edited
 * stylesheet is a stale build exactly as an edited module is -- and a staleness
 * check that missed it would let the concatenation cases pass against the
 * previous build's bytes. */
function sourced(directory: string): readonly string[] {
  const out: string[] = []
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) out.push(...sourced(path))
    else if (
      entry.name.endsWith('.ts') ||
      entry.name.endsWith('.html') ||
      entry.name.endsWith('.css')
    ) {
      out.push(path)
    }
  }
  return out
}

/** INPUTS are the files a build is made from: the sources and the documents the
 * bundler copies (an edited document is a stale build too), the bundler, and the
 * two configs that decide what the build does. */
function inputs(): readonly string[] {
  return [
    ...sourced(join(CONSOLE_ROOT, 'src')),
    join(CONSOLE_ROOT, 'tools', 'bundle-renderer', 'index.ts'),
    join(CONSOLE_ROOT, 'tsconfig.build.json'),
    join(CONSOLE_ROOT, 'package.json'),
  ]
}

/** mtime is a file's modification time, or `null` when it is not there. */
function mtime(file: string): number | null {
  try {
    return statSync(file).mtimeMs
  } catch {
    return null
  }
}

/** isStale reports whether some output is missing, or older than some input. */
function isStale(): boolean {
  const newestInput = Math.max(...inputs().map((file) => mtime(file) ?? 0))
  for (const output of OUTPUTS) {
    const built = mtime(output)
    if (built === null || built < newestInput) return true
  }
  return false
}

/** build runs the steps `npm run build` runs, in its order: the compiler, then
 * the bundler that writes the stylesheet and the two bundles. */
function build(): void {
  execFileSync(
    process.execPath,
    [join(CONSOLE_ROOT, 'node_modules', 'typescript', 'bin', 'tsc'), '-p', 'tsconfig.build.json'],
    { cwd: CONSOLE_ROOT, stdio: 'pipe' },
  )
  bundleStylesheet(consoleStylesheet(CONSOLE_ROOT))
  for (const bundle of consoleBundles(CONSOLE_ROOT)) bundleRenderer(bundle)
}

/** takeLock is the one-writer rule: `wx` either creates the file or says
 * somebody else has it. An old lock is removed as a crash's, not a build's. */
function takeLock(): boolean {
  try {
    writeFileSync(LOCK, `${String(process.pid)}\n`, { flag: 'wx' })
    return true
  } catch {
    const age = mtime(LOCK)
    if (age !== null && Date.now() - age > STALE_LOCK_MS) rmSync(LOCK, { force: true })
    return false
  }
}

let built = false

/**
 * ensureBuilt makes the compiled tree as new as its sources, once per worker.
 *
 * A worker that finds the lock taken waits for it rather than building beside
 * it, and re-checks afterwards: the build it waited for may be exactly the one
 * it wanted. Waiting past `WAIT_LIMIT_MS` is an error rather than a second
 * build, because two compilers in one `dist/` is the state this exists to
 * prevent.
 */
export function ensureBuilt(): void {
  if (built) return
  if (!isStale()) {
    built = true
    return
  }
  mkdirSync(DIST, { recursive: true })
  const deadline = Date.now() + WAIT_LIMIT_MS
  while (!takeLock()) {
    if (!isStale()) {
      built = true
      return
    }
    if (Date.now() > deadline) {
      throw new Error(`another worker has held ${LOCK} for over ${String(WAIT_LIMIT_MS)} ms`)
    }
    // A synchronous sleep: the alternative is an async suite runner, and the
    // wait is a second or two in the one case that reaches it.
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 100)
  }
  try {
    if (isStale()) build()
  } finally {
    rmSync(LOCK, { force: true })
  }
  built = true
}

/** builtFile is one file of the build, as text, with the build made first. */
export function builtFile(relative: string): string {
  ensureBuilt()
  return readFileSync(join(DIST, relative), 'utf8')
}

/** builtListing is one directory of the build, sorted, with the build made
 * first. */
export function builtListing(relative: string): readonly string[] {
  ensureBuilt()
  return readdirSync(join(DIST, relative)).sort()
}

/** builtPath is a path inside the build, for a test that wants to stat it. */
export function builtPath(relative: string): string {
  ensureBuilt()
  const path = join(DIST, relative)
  if (!existsSync(path)) throw new Error(`${relative} is not in the build`)
  return path
}
