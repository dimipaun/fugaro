// The screen the screenshot run has to pass before it is allowed to start the
// console (owner ruling 2026-10-20: the website's console pictures are taken
// from FIXTURE data, by a script the owner runs on their own Mac).
//
// Three things are checked, and each of them is a way a screenshot of "the
// console with fixture data" could quietly become a picture of the owner's real
// installation:
//
//   1. **the binary.** The console resolves its `fugaro` from the configured
//      path, then the well-known prefixes (`/opt/homebrew/bin`, `/usr/local/bin`,
//      `~/.local/bin`, `/usr/bin`), then the app's own `PATH`
//      (`src/main/spawn.ts`'s `binDirectories`). Two of those steps are absolute
//      paths this run cannot change, so a machine with a real CLI in
//      `/opt/homebrew/bin` would resolve *it* the moment the configured path is
//      not the fixture's -- and the pictures would be of real runs.
//      `assertFixtureBinary` refuses that, with the path it found.
//   2. **the environment.** `HOME`, `XDG_CONFIG_HOME` and the window's
//      `--user-data-dir` are the three places a launch can reach an
//      installation from: `~/.config/fugaro` is where the CLI keeps its config,
//      `~/.local/bin` is one of the resolver's prefixes, and the console's own
//      `installations.json` lives in the user-data directory. All three are
//      **fresh directories under one scratch tree** this run creates, and
//      `assertIsolated` refuses a launch whose environment names anything else.
//   3. **the rest of the environment.** The child's environment is built from an
//      allow-list on purpose (`launchEnvironment`): a denylist would have to
//      name every variable that can carry a credential or a path, and the ones
//      it misses are exactly the ones nobody thought of. What is carried is the
//      handful of keys a windowed Electron process needs to draw -- a display, a
//      locale, a temporary directory, a locale's time zone -- and nothing else,
//      so `FUGARO_CONFIG`, `GOOGLE_APPLICATION_CREDENTIALS`, `SSH_AUTH_SOCK` and
//      their relatives cannot reach the app or the CLI it spawns.
//
// Everything here is a pure function of its arguments (or of `process.env` and
// the filesystem, in `newIsolation`), so the rules are unit-tested without a
// window: `console/test/unit/screenshots.test.ts` drives each refusal with the
// planted value and asserts the sentence names it.

import { mkdirSync, mkdtempSync } from 'node:fs'
import { homedir, tmpdir } from 'node:os'
import { join, resolve, sep } from 'node:path'

/** SCRATCH_PREFIX names the one temporary tree a run owns. It is long enough to
 * be recognisable in a `ps` listing or a leftover directory. */
export const SCRATCH_PREFIX = 'fugaro-console-screenshots-'

/**
 * ENV_ALLOW_LIST is every key of the ambient environment the launched console
 * is given, in the words of the comment at the top of this file: a display (so
 * Electron has a window at all on Linux), a locale and its time zone, and a
 * temporary directory. `PATH`, `HOME` and `XDG_CONFIG_HOME` are not here
 * because this run *writes* them (`launchEnvironment`).
 *
 * `TMPDIR` is carried and *not* redirected: Chromium wants a temporary
 * directory it can write, and the scratch tree is a fine one, but a `TMPDIR`
 * that names a path this run does not own is not a way to reach an
 * installation -- it is where a crashed renderer's sockets live -- and
 * redirecting it would be a second temporary tree to clean up.
 */
export const ENV_ALLOW_LIST: readonly string[] = Object.freeze([
  'DISPLAY',
  'WAYLAND_DISPLAY',
  'XAUTHORITY',
  'LANG',
  'LC_ALL',
  'LC_CTYPE',
  'TZ',
  'TERM',
  'TMPDIR',
])

/** Isolation is the one scratch tree a run owns and the three directories under
 * it that a launch is pointed at. */
export interface Isolation {
  /** scratch is the tree: `mkdtemp`'s own name, mode 0700, removed by a run
   * that finished and left as evidence by a run that did not. */
  readonly scratch: string
  /** home is the child's `HOME`: `~/.local/bin` and `~/.config/fugaro` are both
   * under it, and both are empty when the run starts. */
  readonly home: string
  /** xdgConfigHome is the child's `XDG_CONFIG_HOME`. It is under `home` as well
   * as named, because the CLI reads the variable when it is set and the
   * directory when it is not. */
  readonly xdgConfigHome: string
  /** userData is the seed for every launch's `--user-data-dir` (the console's
   * own `installations.json`) and, when a launch points at it directly, the
   * profile itself. */
  readonly userData: string
  /** bin is the app's whole `PATH`: the one directory a fake `fugaro` is
   * installed in, so the resolver's last step can only find the fixture. */
  readonly bin: string
}

/**
 * newIsolation creates the scratch tree. The three directories are created
 * rather than left to the app: Chromium refuses a `--user-data-dir` it cannot
 * write, and an empty `HOME` is what makes `~/.local/bin` a directory the
 * resolver looks in and finds nothing in.
 */
export function newIsolation(): Isolation {
  const scratch = mkdtempSync(join(tmpdir(), SCRATCH_PREFIX))
  const home = join(scratch, 'home')
  const xdgConfigHome = join(home, '.config')
  const userData = join(scratch, 'user-data')
  const bin = join(scratch, 'bin')
  for (const directory of [home, xdgConfigHome, userData, bin]) {
    mkdirSync(directory, { recursive: true, mode: 0o700 })
  }
  return Object.freeze({ scratch, home, xdgConfigHome, userData, bin })
}

/**
 * launchEnvironment is the whole environment the console is launched with: the
 * allow-listed ambient keys, and the three paths this run owns.
 *
 * It answers a fresh object with **no `undefined` values**, which is both what
 * a child process receives and what Playwright's `env` option takes -- a key
 * that is absent is absent, and a key present with `undefined` is not the same
 * thing.
 */
export function launchEnvironment(
  ambient: Readonly<Record<string, string | undefined>>,
  isolation: Isolation,
): Record<string, string> {
  const env: Record<string, string> = {}
  for (const key of ENV_ALLOW_LIST) {
    const value = ambient[key]
    if (typeof value === 'string' && value !== '') env[key] = value
  }
  env['PATH'] = isolation.bin
  env['HOME'] = isolation.home
  env['XDG_CONFIG_HOME'] = isolation.xdgConfigHome
  return env
}

/**
 * assertIsolated refuses a launch whose environment or profile can reach
 * anything outside the scratch tree this run created.
 *
 * The three checks are the three doors: `HOME` (the resolver's `~/.local/bin`
 * and the CLI's `~/.config/fugaro`), `XDG_CONFIG_HOME` (the same config
 * directory, named), and the `--user-data-dir` the window is given (the
 * console's own `installations.json`). A value that is not `inside` the scratch
 * tree -- or that is, precisely, the operator's real `HOME` or the real
 * `XDG_CONFIG_HOME` -- is a refusal naming the key and the path.
 *
 * `real` is passed in rather than read here, so the same rule can be driven
 * from a test with a synthetic "operator" whose paths are the ones it planted.
 */
export function assertIsolated(options: {
  readonly env: Readonly<Record<string, string>>
  readonly isolation: Isolation
  readonly userDataDir: string | null
  readonly real: { readonly home: string; readonly xdgConfigHome: string | null }
}): void {
  const { env, isolation, userDataDir, real } = options
  const roots = [resolve(isolation.scratch)]
  const forbidden = new Map<string, string>()
  if (real.home !== '') forbidden.set(resolve(real.home), "the operator's home directory")
  if (real.xdgConfigHome !== null && real.xdgConfigHome !== '') {
    forbidden.set(resolve(real.xdgConfigHome), "the operator's XDG_CONFIG_HOME")
  }

  const check = (key: string, value: string, inside: readonly string[]): void => {
    const absolute = resolve(value)
    const named = forbidden.get(absolute)
    if (named !== undefined) {
      throw new Error(
        `the screenshot run refuses to start: ${key} is ${named} (${absolute}), and a launch that can read it is a launch that can read a real installation`,
      )
    }
    if (!inside.some((root) => absolute === root || absolute.startsWith(root + sep))) {
      throw new Error(
        `the screenshot run refuses to start: ${key} is ${absolute}, which is outside this run's scratch tree (${roots.join(', ')})`,
      )
    }
  }

  const home = env['HOME']
  if (home === undefined) {
    throw new Error('the screenshot run refuses to start: the launch environment sets no HOME')
  }
  check('HOME', home, [isolation.home, isolation.scratch])
  const xdg = env['XDG_CONFIG_HOME']
  if (xdg === undefined) {
    throw new Error('the screenshot run refuses to start: the launch environment sets no XDG_CONFIG_HOME')
  }
  check('XDG_CONFIG_HOME', xdg, [isolation.xdgConfigHome, isolation.scratch])
  if (userDataDir !== null) check('--user-data-dir', userDataDir, [isolation.userData, isolation.scratch])
}

/**
 * assertFixtureBinary refuses a resolution that is not the fixture CLI.
 *
 * `found` is the console's own answer to "which `fugaro` will this launch run"
 * (`test/support/cliStub.ts`'s `resolveConsoleBinary`, which drives the
 * console's real resolver), and `expected` is the fake CLI this run installed.
 * They have to be the same path: anything else means the resolver found a
 * binary earlier in its order than the one this run put there -- a machine's own
 * `/opt/homebrew/bin/fugaro` is the case that happens by itself -- and the run
 * would be taking pictures of whatever that binary answers.
 */
export function assertFixtureBinary(found: string | null, expected: string, step: string): void {
  if (found === expected) return
  throw new Error(
    `the screenshot run refuses to start: ${step} resolves ${found === null ? 'no fugaro at all' : found}, not the fixture CLI at ${expected}`,
  )
}

/**
 * realPaths is what `assertIsolated` is given as "the operator's own": the
 * home directory of the process running this tool, and its `XDG_CONFIG_HOME`
 * when it has one. **They are only ever compared against and never read** -- no
 * file under either is opened by this tool, by the console it launches, or by
 * the fixture CLI -- which is the property the whole module exists to make
 * checkable.
 */
export function realPaths(ambient: Readonly<Record<string, string | undefined>> = process.env): {
  readonly home: string
  readonly xdgConfigHome: string | null
} {
  const xdg = ambient['XDG_CONFIG_HOME']
  return { home: homedir(), xdgConfigHome: typeof xdg === 'string' && xdg !== '' ? xdg : null }
}
