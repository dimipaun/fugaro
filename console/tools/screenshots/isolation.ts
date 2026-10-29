// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// The screen the screenshot run has to pass before it is allowed to start the
// console (owner ruling 2026-10-20: the website's console pictures are taken
// from FIXTURE data, by a script the owner runs on their own Mac).
//
// Three things are checked, and each of them is a way a screenshot of "the
// console with fixture data" could quietly become a picture of the owner's real
// installation:
//
//   1. **the binary.** The console's startup handshake resolves its `fugaro`
//      from the *store*: with a chosen installation it asks that
//      installation's one path first (rule 4's configured candidate; this
//      module checks it with `src/main/spawn.ts`'s one-path
//      `resolveInstalledBinary`); with nothing chosen it walks rule 4's list --
//      the configured path, then this platform's well-known directories
//      (`spawn.ts`'s table: on macOS `/opt/homebrew/bin`, `/usr/local/bin`,
//      `~/.local/bin` and `/usr/bin`, and Linux has its own list since the
//      owner's ruling of 2026-10-20); the app's own `PATH` is never a step. That second
//      walk reaches directories this run cannot change, and on the owner's Mac
//      the first of them holds their own CLI, so a launch that walked would
//      photograph real runs. `assertLaunchIsFixture` is the rule that makes
//      that impossible rather than unlikely: it reads the store this run just
//      seeded, refuses a store that chooses no installation (the handshake
//      would walk), and drives the console's own single-path resolution for the
//      chosen row -- so a launch happens only when the path the console will
//      read *is* the fake, with the launch's own environment.
//
//      A note on what this deliberately does **not** do, because the first
//      version of it did and it was wrong: it does not ask the machine's
//      resolver what it would find. `resolveBinary` with no configured path
//      walks the absolute prefixes above and probes the winner by *executing*
//      it, so asking that question on the owner's Mac would run the operator's
//      real `fugaro` and then refuse the run -- a check that both touched the
//      thing it exists to avoid and made the run impossible on the one machine
//      it is for. The question a launch asks is about one path, so that is the
//      question this module asks.
//   2. **the environment.** `HOME`, `XDG_CONFIG_HOME`, `TMPDIR` and the window's
//      `--user-data-dir` are the four places a launch can reach an installation
//      from: `~/.config/fugaro` is where the CLI keeps its config,
//      `~/.local/bin` is one of the resolver's prefixes, the console's own
//      `installations.json` lives in the user-data directory, and `TMPDIR` is
//      where the console puts the private working directory its children run in
//      (`spawn.ts`'s `privateWorkingDirectory`, `$TMPDIR/fugaro-console-<uid>`).
//      All four are **fresh directories under one scratch tree** this run
//      creates, and `assertIsolated` refuses a launch whose environment names
//      anything else.
//   3. **the rest of the environment.** The child's environment is built from an
//      allow-list on purpose (`launchEnvironment`): a denylist would have to
//      name every variable that can carry a credential or a path, and the ones
//      it misses are exactly the ones nobody thought of. What is carried is the
//      handful of keys a windowed Electron process needs to draw -- a display, a
//      locale, a locale's time zone -- and nothing else, so `FUGARO_CONFIG`,
//      `GOOGLE_APPLICATION_CREDENTIALS`, `SSH_AUTH_SOCK` and their relatives
//      cannot reach the app or the CLI it spawns.
//
// Everything here is a pure function of its arguments (or of `process.env` and
// the filesystem, in `newIsolation`), so the rules are unit-tested without a
// window: `console/test/unit/screenshots.test.ts` drives each refusal with the
// planted value and asserts the sentence names it.

import { mkdirSync, mkdtempSync, readFileSync } from 'node:fs'
import { homedir, tmpdir } from 'node:os'
import { join, resolve, sep } from 'node:path'

import { INSTALLATIONS_FILE_NAME, readInstallationFile } from '../../src/main/installations'
import { locationsFor, resolveInstalledBinary } from '../../src/main/spawn'

/** walk is rule 4's list of well-known directories on **this** platform, as the
 * refusal sentences below spell it.
 *
 * It is read from `spawn.ts`'s platform table (`locationsFor`) rather than
 * written out here, because that table is the one place the console decides
 * which directories to walk -- since the owner's Linux ruling of 2026-10-20 it
 * is per platform (`DARWIN_LOCATIONS`, `LINUX_LOCATIONS`), and a sentence that
 * named macOS's directories on a Linux machine would name a walk the handshake
 * would not make. The unit suite holds the sentence to the platform's literals
 * (`test/unit/screenshots.test.ts`, §3b) rather than to this expression. */
function walk(): string {
  return locationsFor().binaryPrefixes.join(', ')
}

/** SCRATCH_PREFIX names the one temporary tree a run owns. It is long enough to
 * be recognisable in a `ps` listing or a leftover directory. */
export const SCRATCH_PREFIX = 'fugaro-console-screenshots-'

/**
 * ENV_ALLOW_LIST is every key of the ambient environment the launched console
 * is given, in the words of the comment at the top of this file: a display (so
 * Electron has a window at all on Linux) and a locale with its time zone.
 * `PATH`, `HOME`, `XDG_CONFIG_HOME` and `TMPDIR` are not here because this run
 * *writes* them (`launchEnvironment`).
 *
 * `TMPDIR` is redirected into the scratch tree rather than carried: the
 * console's own `privateWorkingDirectory()` builds `$TMPDIR/fugaro-console-<uid>
 * /cwd` and runs every child there, so an ambient `TMPDIR` is a directory the
 * console writes to outside this run's tree -- and a run whose whole claim is
 * "everything it touched, it made and it removed" cannot leave that out. The
 * redirected one is removed with the rest of the tree, and it is also where
 * Chromium's own temporary files land.
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
   * profile itself. It is also the working directory this run's own resolution
   * check probes from, so that check runs a child nowhere but here. */
  readonly userData: string
  /** bin is the app's whole `PATH`: the one directory a fake `fugaro` is
   * installed in, so the resolver's last step can only find the fixture. */
  readonly bin: string
  /** tmp is the child's `TMPDIR`, and therefore the parent of the private
   * working directory the console runs its children in: inside the scratch
   * tree, so the run removes it with everything else it made. */
  readonly tmp: string
}

/**
 * newIsolation creates the scratch tree. The four directories are created
 * rather than left to the app: Chromium refuses a `--user-data-dir` it cannot
 * write, an empty `HOME` is what makes `~/.local/bin` a directory the resolver
 * looks in and finds nothing in, and a `TMPDIR` that does not exist yet is a
 * temporary directory a child cannot use.
 */
export function newIsolation(): Isolation {
  const scratch = mkdtempSync(join(tmpdir(), SCRATCH_PREFIX))
  const home = join(scratch, 'home')
  const xdgConfigHome = join(home, '.config')
  const userData = join(scratch, 'user-data')
  const bin = join(scratch, 'bin')
  const tmp = join(scratch, 'tmp')
  for (const directory of [home, xdgConfigHome, userData, bin, tmp]) {
    mkdirSync(directory, { recursive: true, mode: 0o700 })
  }
  return Object.freeze({ scratch, home, xdgConfigHome, userData, bin, tmp })
}

/**
 * launchEnvironment is the whole environment the console is launched with: the
 * allow-listed ambient keys, and the four paths this run owns.
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
  env['TMPDIR'] = isolation.tmp
  return env
}

/**
 * assertIsolated refuses a launch whose environment or profile can reach
 * anything outside the scratch tree this run created.
 *
 * The four checks are the four doors: `HOME` (the resolver's `~/.local/bin`
 * and the CLI's `~/.config/fugaro`), `XDG_CONFIG_HOME` (the same config
 * directory, named), `TMPDIR` (the private working directory the console runs
 * its children from), and the `--user-data-dir` the window is given (the
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
  const tmp = env['TMPDIR']
  if (tmp === undefined) {
    throw new Error('the screenshot run refuses to start: the launch environment sets no TMPDIR')
  }
  check('TMPDIR', tmp, [isolation.tmp, isolation.scratch])
  if (userDataDir !== null) check('--user-data-dir', userDataDir, [isolation.userData, isolation.scratch])
}

/**
 * assertFixtureBinary refuses a resolution that is not the fixture CLI.
 *
 * `found` is the console's own answer to "which `fugaro` will this launch run"
 * for one installation (`resolveInstalledBinary`, which reads exactly the path
 * the installation names), and `expected` is the fake CLI this run installed.
 * They have to be the same path: anything else means the store this run seeded
 * does not point at the fake -- the one way a real binary can still be reached
 * from a launch this run makes -- and the run would be taking pictures of
 * whatever that binary answers.
 */
export function assertFixtureBinary(found: string | null, expected: string, step: string): void {
  if (found === expected) return
  throw new Error(
    `the screenshot run refuses to start: ${step} resolves ${found === null ? 'no fugaro at all' : found}, not the fixture CLI at ${expected}`,
  )
}

/**
 * assertLaunchIsFixture refuses a launch whose **own store** would send the
 * console's startup handshake anywhere but the fake CLI. It is the run's first
 * refusal, and it is made before the window exists.
 *
 * It asks the question the launch actually asks, in the order the launch asks
 * it. `profile` is the `--user-data-dir` this run just seeded, and the store in
 * it is read with the console's own reader (`readInstallationFile`) -- so a
 * store the console would refuse fails here, with the reader's own reason,
 * rather than opening a window on a refusal. Then:
 *
 *   - **a store that chooses no installation is refused**, because the
 *     handshake with `installation === null` is `resolveBinary` over rule 4's
 *     list: it walks `/opt/homebrew/bin`, `/usr/local/bin`, `~/.local/bin` and
 *     `/usr/bin`, and on the owner's Mac one
 *     of those holds their own `fugaro`. Every launch this run makes seeds a
 *     chosen installation, so this is a check on the run's own seeding rather
 *     than a machine's luck -- and it is why the picker capture opens the
 *     picker from the console's menu instead of launching a console with
 *     nothing chosen (`capture.ts`).
 *   - **the chosen row is resolved with `resolveInstalledBinary`**, the
 *     console's one-path read, with the *launch's* environment and a directory
 *     inside the scratch tree. That path is compared to the fake's
 *     (`assertFixtureBinary`), so the run stops with a sentence naming the path
 *     rather than photographing another binary's answers. And because the read
 *     is `resolveInstalledBinary` and not `resolveBinary`, the check never asks
 *     the machine what it *would* find: a walking resolution would execute the
 *     operator's own CLI to answer, which is the one thing this module exists
 *     to keep from happening.
 *
 * The probe child runs in `isolation.userData`, taken from the `Isolation`
 * itself rather than passed in, so that no caller can point this check at a
 * directory this run does not own: the one child it spawns runs inside the
 * scratch tree, which is the property `test/unit/screenshots.test.ts` reads off
 * the fake's own report (`cwd`).
 */
export async function assertLaunchIsFixture(options: {
  /** profile is the `--user-data-dir` that was just seeded. */
  readonly profile: string
  /** expected is the fake CLI's absolute path. */
  readonly expected: string
  /** env is the environment the launch will be given, verbatim. */
  readonly env: Readonly<Record<string, string>>
  /** isolation is the scratch tree this run owns; the probe runs inside it. */
  readonly isolation: Isolation
}): Promise<void> {
  const { profile, expected, env, isolation } = options
  const directory = isolation.userData
  const file = join(profile, INSTALLATIONS_FILE_NAME)
  let raw: string
  try {
    raw = readFileSync(file, 'utf8')
  } catch {
    throw new Error(
      `the screenshot run refuses to start: the profile it seeded (${profile}) holds no ${INSTALLATIONS_FILE_NAME}, so the console's startup handshake would resolve a fugaro by walking ${walk()}`,
    )
  }
  const read = readInstallationFile(raw)
  if (!read.ok) {
    throw new Error(`the screenshot run refuses to start: the store it seeded (${file}) is not one the console will read: ${read.reason}`)
  }
  const active = read.file.active
  if (active === null) {
    throw new Error(
      `the screenshot run refuses to start: the store it seeded (${file}) chooses no installation, and the startup handshake of a console with nothing chosen resolves a fugaro by walking ${walk()} -- the directories where the operator's own fugaro lives. Every launch this run makes names the fixture as its chosen installation.`,
    )
  }
  const row = read.file.installations.find((installation) => installation.id === active)
  if (row === undefined) {
    throw new Error(`the screenshot run refuses to start: the store it seeded chooses ${active}, which it does not hold`)
  }
  // The store's `binaryPath` is an optional override: a chosen row without one
  // is a handshake that walks rule 4's list, which is the walk this check
  // exists to refuse.
  if (row.binaryPath === undefined) {
    throw new Error(
      `the screenshot run refuses to start: the store it seeded chooses the ${row.project} installation but names no binaryPath for it, so the console's startup handshake would resolve a fugaro by walking ${walk()}`,
    )
  }
  const resolved = await resolveInstalledBinary({
    binaryPath: row.binaryPath,
    appEnv: env,
    directory,
    target: { project: row.project },
  })
  if (!resolved.ok) {
    throw new Error(
      `the screenshot run refuses to start: the startup handshake for the ${row.project} installation does not reach the fixture CLI at ${expected}: ${resolved.reason}`,
    )
  }
  assertFixtureBinary(resolved.path, expected, `the startup handshake for the ${row.project} installation`)
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
