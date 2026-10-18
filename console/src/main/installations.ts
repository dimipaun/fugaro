// The operator's installations: where they are kept, what is checked before
// they are kept, and the closed surface the picker window is allowed to use
// (desktop-console plan Task 3.4b; design sections 3.4 rules 5 and 7, 5.1 rule
// 10, 7.4, D13, D14, D20; owner ruling C8, 2026-10-18).
//
// This module is the whole of the store, and it is deliberately **free of
// Electron**: the paths it touches arrive as arguments, the store's filesystem
// arrives as `InstallationFs` (`nodeInstallationFs` is the only place it meets
// `node:fs`), and the one check that needs the CLI
// arrives as `deps.verify`. That is what lets `test/unit/installations-store.test.ts`
// hold the 0600 mode, the atomic rename and every refusal against a recording
// filesystem under plain Node, and it is also what keeps the module inside
// design section 3.4 rule 7: there is no `homedir()` here, no environment read
// that is not handed in, and no read of a config-shaped path (see
// `test/unit/configread.test.ts`, whose scan covers this file).
//
// The shape of the file, and why each bound exists:
//
//   - **one file, `installations.json`, in `userData`** (C8). It is the second
//     thing the app writes, and the design's rule 10 and section 7.4 name it as
//     such; the spawn log is the other, and it is off unless the operator names
//     a path.
//   - **tuples only**: a project name, a binary path, an optional config path
//     and an optional checkout directory. There is no credential, no
//     environment, no CLI output and no version in it -- the version is
//     re-read by the handshake on every launch and every choose, so a stored
//     one could only ever go stale.
//   - **the config path is a string and never opened here.** It is passed to
//     the child as `--config`; the CLI reads it. `validateInstallation` `stat`s
//     it (to refuse a path that is not a file) and does nothing else with it.
//   - **bounded**: at most `MAX_INSTALLATIONS` entries, and a file past
//     `MAX_INSTALLATION_FILE_BYTES` is refused unread. A refusal refuses the
//     *whole* file and never rewrites or deletes it: an operator who edited it
//     by hand gets a console that says so, not one that silently discards their
//     work.

import { randomBytes } from 'node:crypto'
import {
  closeSync,
  constants as fsConstants,
  fstatSync,
  lstatSync,
  mkdirSync,
  openSync,
  readSync,
  renameSync,
  statSync,
  unlinkSync,
  writeFileSync,
} from 'node:fs'
import { dirname, isAbsolute, join, resolve } from 'node:path'

import { NO_ACTIVE_INSTALLATION } from './ipc'
import { isSafeInstallationValue, isExecutableFile, planFor, privateWorkingDirectory, probeVersion } from './spawn'
import type { ChildSpawner, SpawnTarget } from './spawn'
import { createSpawnLog } from './spawnlog'
import type { SpawnLogWriter } from './spawnlog'
import { classifyCliVersion } from '../shared/version'
import type { CliCompatibility } from '../shared/version'
import type { InstallationCandidate } from '../source/cli'

/**
 * CONSOLE_APP_NAME is the app's own name, and it is the reason the store is
 * outside the CLI's config directory rather than beside it: `~/.config/fugaro`
 * belongs to the CLI, and an app named `fugaro` on Linux would put its
 * `userData` exactly there. `test/unit/installations-store.test.ts` pins that this
 * name lands outside all three of the CLI's config directories on every
 * platform the console ships on.
 */
export const CONSOLE_APP_NAME = 'fugaro-console'

/** INSTALLATIONS_FILE_NAME is the one file this module writes. */
export const INSTALLATIONS_FILE_NAME = 'installations.json'

/** CLI_CONFIG_DIR_NAME is the directory the CLI keeps its own config in, under
 * `$XDG_CONFIG_HOME` (or `~/.config`). The console must never write inside it:
 * that is the operator's CLI configuration, and rule 7 makes the CLI its only
 * reader. */
export const CLI_CONFIG_DIR_NAME = 'fugaro'

/** INSTALLATION_FILE_VERSION is the schema's version, and the only value the
 * reader accepts. */
export const INSTALLATION_FILE_VERSION = 1

/** MAX_INSTALLATIONS bounds the list. Sixteen is far past any operator's real
 * set, and it is what makes the file's size bound arithmetic rather than hope. */
export const MAX_INSTALLATIONS = 16

/** MAX_INSTALLATION_FILE_BYTES is the read bound: a larger file is refused
 * without being parsed, so a path that has been replaced by something enormous
 * costs a `stat` and not a parse. */
export const MAX_INSTALLATION_FILE_BYTES = 64 * 1024

/** INSTALLATION_FILE_MODE is the mode the file is created with, and the mode
 * the store re-asserts when it is written again. The directory is 0700. */
export const INSTALLATION_FILE_MODE = 0o600
export const INSTALLATION_DIR_MODE = 0o700

/** MAX_INSTALLATION_PROJECT_LENGTH and MAX_INSTALLATION_PATH_LENGTH are the
 * two field bounds. They are *stricter* than `isSafeInstallationValue`'s 4096,
 * which is about what may reach an argv and not about what is worth keeping. */
export const MAX_INSTALLATION_PROJECT_LENGTH = 200
export const MAX_INSTALLATION_PATH_LENGTH = 1024

/** INSTALLATION_ID_LENGTH is 16 hex characters: 64 bits, which is not a
 * security boundary but is far past collision for a list of at most 16. The id
 * is the app's own; the CLI never sees it and no screen may use it as a path. */
export const INSTALLATION_ID_LENGTH = 16
export const INSTALLATION_ID_PATTERN = /^[0-9a-f]{16}$/

/** INSTALLATION_FILE_KEYS is the whole of what the file may hold, and
 * INSTALLATION_KEYS the whole of what one entry may hold. Both are checked as
 * *exact* sets: an unknown key refuses the file, because a key this version
 * does not know is either a newer version's field (which this version must not
 * silently drop) or something a person added by hand. */
export const INSTALLATION_FILE_KEYS: readonly string[] = Object.freeze(['version', 'active', 'installations'])
export const INSTALLATION_KEYS: readonly string[] = Object.freeze(['id', 'project', 'binaryPath', 'configPath', 'checkout'])

/** INSTALLATION_CHANNEL_PREFIX is the picker window's own channel namespace.
 * The dashboard's is `fugaro:console/`; the two are gated separately, so a
 * frame on one document cannot reach the other's operations. */
export const INSTALLATION_CHANNEL_PREFIX = 'fugaro:installations/'

/** NO_ACTIVE_INSTALLATION is the one sentence a CLI-backed operation answers
 * with when no installation has been chosen. It is a fixed string and it names
 * no path: an operator who has chosen nothing gets a state, not a guess. It is
 * `src/main/ipc.ts`'s constant, re-exported, so the store's no-installation
 * state and the dashboard's refusal of a read cannot drift apart. */
export { NO_ACTIVE_INSTALLATION }

/** MAX_OUTSTANDING_PICKS bounds the pick tokens at once. A token is single-use
 * and is dropped when the picker closes; eight is more than the four pickers a
 * screen can have open. */
export const MAX_OUTSTANDING_PICKS = 8

/** PICK_TOKEN_LENGTH is 32 hex characters: a token is a capability to name one
 * path the *main* process chose, and it must not be guessable by the renderer
 * that redeems it. */
export const PICK_TOKEN_LENGTH = 32
const PICK_TOKEN_PATTERN = /^[0-9a-f]{32}$/

/** StoredInstallation is one installation as it is kept. It is the D13 tuple:
 * nothing else is persisted, and in particular no version. */
export interface StoredInstallation {
  readonly id: string
  readonly project: string
  readonly binaryPath: string
  readonly configPath?: string
  readonly checkout?: string
}

/** InstallationFile is the whole of `installations.json`. `active` is an id in
 * `installations`, or null. */
export interface InstallationFile {
  readonly version: 1
  readonly active: string | null
  readonly installations: readonly StoredInstallation[]
}

/** EMPTY_INSTALLATION_FILE is the file a first run has: no installation and
 * none chosen. It is not written until an add, a choose or a remove happens. */
export const EMPTY_INSTALLATION_FILE: InstallationFile = Object.freeze({
  version: INSTALLATION_FILE_VERSION,
  active: null,
  installations: Object.freeze([]),
})

/** InstallationRead is what reading the file answers: the file, or the sentence
 * that says why it was refused. A refusal is a value rather than a throw
 * because the console starts anyway -- it starts with no installation, says so
 * naming the path, and refuses an add until the operator fixes the file. */
export type InstallationRead =
  | { readonly ok: true; readonly file: InstallationFile }
  | { readonly ok: false; readonly reason: string }

/**
 * InstallationFs is `node:fs` as this module uses it, and nothing more. Every
 * call is here so the tests can hold the mode, the atomicity and the refusals
 * against a filesystem that records what it was asked to do.
 */
export interface InstallationFs {
  mkdir(path: string, options: { readonly recursive: true; readonly mode: number }): void
  writeFile(path: string, data: string, options: { readonly flag: 'wx'; readonly mode: number }): void
  rename(from: string, to: string): void
  /** lstat does **not** follow a symbolic link: the store's own file is read
   * only when it is a regular file at that name (review S8). */
  lstat(path: string): { readonly isFile: () => boolean; readonly isSymbolicLink: () => boolean; readonly size: number }
  /** readRegularFile opens the file with `O_NOFOLLOW | O_NONBLOCK`, refuses
   * (throws) anything `fstat` says is not a regular file, and reads **at most
   * `limit` bytes** from that one descriptor -- so a file swapped for a link, a
   * FIFO or something larger between the `lstat` and the read is still refused,
   * and a FIFO never blocks the main thread. `bytes` is how many were read. */
  readRegularFile(path: string, limit: number): { readonly bytes: number; readonly text: string }
  stat(path: string): { readonly isFile: () => boolean; readonly isDirectory: () => boolean; readonly size: number }
  unlink(path: string): void
}

/** nodeInstallationFs is the real filesystem. The read bound is checked against
 * the bytes read from the descriptor, never against a `stat` taken earlier. */
export const nodeInstallationFs: InstallationFs = Object.freeze<InstallationFs>({
  mkdir: (path, options) => {
    mkdirSync(path, options)
  },
  writeFile: (path, data, options) => {
    writeFileSync(path, data, options)
  },
  rename: (from, to) => {
    renameSync(from, to)
  },
  lstat: (path) => {
    const info = lstatSync(path)
    return { isFile: () => info.isFile(), isSymbolicLink: () => info.isSymbolicLink(), size: info.size }
  },
  readRegularFile: (path, limit) => {
    const fd = openSync(path, fsConstants.O_RDONLY | fsConstants.O_NOFOLLOW | fsConstants.O_NONBLOCK)
    try {
      if (!fstatSync(fd).isFile()) throw new Error('it is not a regular file')
      const buffer = Buffer.alloc(limit)
      let total = 0
      while (total < limit) {
        const read = readSync(fd, buffer, total, limit - total, null)
        if (read === 0) break
        total += read
      }
      return { bytes: total, text: buffer.subarray(0, total).toString('utf8') }
    } finally {
      closeSync(fd)
    }
  },
  stat: (path) => {
    const info = statSync(path)
    return { isFile: () => info.isFile(), isDirectory: () => info.isDirectory(), size: info.size }
  },
  unlink: (path) => {
    unlinkSync(path)
  },
})

/** isInside reports whether `candidate` is `directory` or sits under it. The
 * comparison is on resolved paths with a separator appended, so `/a/bc` is not
 * inside `/a/b`. */
export function isInside(directory: string, candidate: string): boolean {
  const outer = resolve(directory)
  const inner = resolve(candidate)
  return inner === outer || inner.startsWith(outer.endsWith('/') ? outer : `${outer}/`)
}

/**
 * cliConfigDirectories is the three places the CLI's own configuration lives,
 * as far as this console is concerned: `$XDG_CONFIG_HOME/fugaro`,
 * `$FUGARO_CONFIG`'s directory, and `~/.config/fugaro`.
 *
 * The home directory arrives as an argument -- Electron's `app.getPath('home')`
 * -- rather than being read here, which is what keeps `homedir()` out of this
 * module (`test/unit/configread.test.ts` refuses it anywhere outside binary
 * resolution) and keeps the function testable for a home it did not have.
 *
 * This is a *path* computation and it touches no file: knowing where the CLI's
 * config would be is not reading it, and refusing to write there is the point.
 */
export function cliConfigDirectories(
  env: Readonly<Record<string, string | undefined>>,
  home: string,
): readonly string[] {
  const out: string[] = []
  const xdg = env['XDG_CONFIG_HOME']
  if (xdg !== undefined && xdg !== '' && isAbsolute(xdg)) out.push(join(xdg, CLI_CONFIG_DIR_NAME))
  const explicit = env['FUGARO_CONFIG']
  if (explicit !== undefined && explicit !== '' && isAbsolute(explicit)) out.push(dirname(explicit))
  out.push(join(home, '.config', CLI_CONFIG_DIR_NAME))
  return Object.freeze(out)
}

/** installationStorePath is the one path this module writes, and it refuses to
 * name one inside the CLI's configuration. The refusal is a throw rather than a
 * value because there is no correct path to fall back to: a console whose
 * `userData` is the CLI's config directory is a packaging mistake, and writing
 * `installations.json` there would put the app's file where the operator's CLI
 * reads. */
export function installationStorePath(options: {
  readonly userData: string
  readonly env: Readonly<Record<string, string | undefined>>
  readonly home: string
}): string {
  const inside = cliConfigDirectories(options.env, options.home).find((directory) =>
    isInside(directory, options.userData),
  )
  if (inside !== undefined) {
    throw new Error(
      `the console refuses to keep its installations under the CLI’s configuration (${inside}); ` +
        `its own directory is ${CONSOLE_APP_NAME}`,
    )
  }
  return join(options.userData, INSTALLATIONS_FILE_NAME)
}

/** isPlainObject is the reader's first test: an object, and not an array, a
 * null or anything with a prototype of its own. */
function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

/** hasExactlyKeys reports whether an object's own keys are exactly `keys`. An
 * extra key and a missing one are the same refusal. */
function hasExactlyKeys(value: Record<string, unknown>, keys: readonly string[]): boolean {
  const own = Object.keys(value)
  return own.length === keys.length && keys.every((key) => Object.prototype.hasOwnProperty.call(value, key))
}

/**
 * isProjectName is the project-name rule, and it is *stricter* than the
 * argv-safety rule on purpose: a project name becomes `--project`'s value on
 * every spawn, and a name with whitespace in it is either a typo or a line
 * someone pasted -- the plan's red step names "a project with a space" as one of
 * the values the file must refuse. Everything else `isSafeInstallationValue`
 * admits is admitted here too: dots, dashes, underscores, slashes and case are
 * all ordinary in a project name.
 */
export function isProjectName(value: string): boolean {
  return value !== '' && value.length <= MAX_INSTALLATION_PROJECT_LENGTH && isSafeInstallationValue(value) && !/\s/.test(value)
}

/** isAbsolutePath is the path check every stored path passes: a string, within
 * the length bound, safe to reach an argv, and absolute. A relative path is
 * refused because it would mean whatever directory the child happened to start
 * in, which is a decision this store does not make. */
function isAbsolutePath(value: unknown, max: number): value is string {
  return typeof value === 'string' && value !== '' && value.length <= max && isSafeInstallationValue(value) && isAbsolute(value)
}

/** readInstallationEntry checks one entry, and names the field it refused. */
function readInstallationEntry(value: unknown, seenIds: Set<string>): StoredInstallation | string {
  if (!isPlainObject(value)) return 'an entry is not an object'
  const unknown = Object.keys(value).filter((key) => !INSTALLATION_KEYS.includes(key))
  if (unknown.length > 0) return `an entry carries a key this version does not know (${unknown.join(', ')})`
  const id = value['id']
  if (typeof id !== 'string' || !INSTALLATION_ID_PATTERN.test(id)) return 'an entry has an id that is not sixteen hex characters'
  if (seenIds.has(id)) return `two entries share the id ${id}`
  seenIds.add(id)
  const project = value['project']
  if (typeof project !== 'string' || !isProjectName(project)) {
    return 'an entry has a project name this console will not store'
  }
  if (!isAbsolutePath(value['binaryPath'], MAX_INSTALLATION_PATH_LENGTH)) {
    return 'an entry’s binary path is not an absolute path'
  }
  const configPath = value['configPath']
  if (configPath !== undefined && !isAbsolutePath(configPath, MAX_INSTALLATION_PATH_LENGTH)) {
    return 'an entry’s config path is not an absolute path'
  }
  const checkout = value['checkout']
  if (checkout !== undefined && !isAbsolutePath(checkout, MAX_INSTALLATION_PATH_LENGTH)) {
    return 'an entry’s checkout is not an absolute path'
  }
  return {
    id,
    project,
    binaryPath: value['binaryPath'],
    ...(configPath === undefined ? {} : { configPath }),
    ...(checkout === undefined ? {} : { checkout }),
  }
}

/**
 * readInstallationFile is the closed-schema check, and it is the *whole* of the
 * reader: a file it refuses is never partially accepted. It takes the file's
 * text, so the bound is checked by the caller that read it -- see
 * `createInstallationFileStore`, which `stat`s before it reads and refuses
 * anything past `MAX_INSTALLATION_FILE_BYTES` without decoding it.
 */
export function readInstallationFile(raw: string): InstallationRead {
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return { ok: false, reason: 'the file is not JSON' }
  }
  if (!isPlainObject(parsed)) return { ok: false, reason: 'the file is not a JSON object' }
  if (!hasExactlyKeys(parsed, INSTALLATION_FILE_KEYS)) {
    return { ok: false, reason: `the file must hold exactly ${INSTALLATION_FILE_KEYS.join(', ')}` }
  }
  if (parsed['version'] !== INSTALLATION_FILE_VERSION) {
    return { ok: false, reason: `the file’s version is not ${String(INSTALLATION_FILE_VERSION)}` }
  }
  const entries = parsed['installations']
  if (!Array.isArray(entries)) return { ok: false, reason: 'installations is not a list' }
  if (entries.length > MAX_INSTALLATIONS) {
    return { ok: false, reason: `the file holds more than ${String(MAX_INSTALLATIONS)} installations` }
  }
  const seenIds = new Set<string>()
  const installations: StoredInstallation[] = []
  for (const entry of entries) {
    const read = readInstallationEntry(entry, seenIds)
    if (typeof read === 'string') return { ok: false, reason: read }
    installations.push(read)
  }
  const active = parsed['active']
  if (active !== null && (typeof active !== 'string' || !seenIds.has(active))) {
    return { ok: false, reason: 'active does not name an installation in the file' }
  }
  return {
    ok: true,
    file: { version: INSTALLATION_FILE_VERSION, active, installations: Object.freeze(installations) },
  }
}

/** serializeInstallationFile writes the canonical form: the three keys in one
 * order, and one entry per line so a person who opens the file can read it. */
export function serializeInstallationFile(file: InstallationFile): string {
  const entries = file.installations.map((installation) => `    ${JSON.stringify(installation)}`)
  const body = entries.length === 0 ? '[]' : `[\n${entries.join(',\n')}\n  ]`
  return `{\n  "version": ${String(INSTALLATION_FILE_VERSION)},\n  "active": ${JSON.stringify(file.active)},\n  "installations": ${body}\n}\n`
}

/** InstallationFileStore is the persistence seam: read once, written only when
 * the operator adds, chooses or removes. */
export interface InstallationFileStore {
  readonly path: string
  load(): InstallationRead
  save(file: InstallationFile): void
}

/** createInstallationFileStore returns the store for one path.
 *
 * The write is **atomic**: the document goes to a fresh temporary file in the
 * same directory (mode 0600, `wx` so it cannot follow an existing file), and
 * only then is it renamed over the target. A crash between the two leaves the
 * temporary file and the previous document intact -- never a half-written
 * `installations.json`, which is the failure that would lose an operator's list
 * at exactly the moment they were changing it.
 *
 * A load of a file that is not there is the empty file, not a refusal: a first
 * run has no file, and that is not an error. A load of a file that is there and
 * is refused is a refusal -- and this store does not unlink, truncate or
 * overwrite it. */
export function createInstallationFileStore(options: {
  readonly path: string
  readonly fs?: InstallationFs
}): InstallationFileStore {
  const fs = options.fs ?? nodeInstallationFs
  return {
    path: options.path,
    load(): InstallationRead {
      // `lstat`, not `stat`: a symbolic link at this name is refused rather than
      // followed to whatever file it names, and a FIFO or a device is refused
      // before anything opens it (review S8). Only a file that is not there at
      // all is the first run's empty file.
      let info: ReturnType<InstallationFs['lstat']>
      try {
        info = fs.lstat(options.path)
      } catch (error) {
        if ((error as NodeJS.ErrnoException).code === 'ENOENT') return { ok: true, file: EMPTY_INSTALLATION_FILE }
        return { ok: false, reason: `the file could not be examined (${error instanceof Error ? error.message : 'unknown'})` }
      }
      if (info.isSymbolicLink()) {
        return { ok: false, reason: 'the file is a symbolic link, which the console does not follow' }
      }
      if (!info.isFile()) return { ok: false, reason: 'the file is not a regular file' }
      if (info.size > MAX_INSTALLATION_FILE_BYTES) {
        return { ok: false, reason: `the file is larger than ${String(MAX_INSTALLATION_FILE_BYTES)} bytes` }
      }
      let read: { readonly bytes: number; readonly text: string }
      try {
        // One byte past the bound, from the descriptor: a file that grew after
        // the `lstat` is still caught here, and is never read in full.
        read = fs.readRegularFile(options.path, MAX_INSTALLATION_FILE_BYTES + 1)
      } catch (error) {
        return { ok: false, reason: `the file could not be read (${error instanceof Error ? error.message : 'unknown'})` }
      }
      if (read.bytes > MAX_INSTALLATION_FILE_BYTES) {
        return { ok: false, reason: `the file is larger than ${String(MAX_INSTALLATION_FILE_BYTES)} bytes` }
      }
      return readInstallationFile(read.text)
    },
    save(file: InstallationFile): void {
      const directory = dirname(options.path)
      fs.mkdir(directory, { recursive: true, mode: INSTALLATION_DIR_MODE })
      const temporary = `${options.path}.${randomBytes(6).toString('hex')}.tmp`
      try {
        fs.writeFile(temporary, serializeInstallationFile(file), { flag: 'wx', mode: INSTALLATION_FILE_MODE })
        fs.rename(temporary, options.path)
      } catch (error) {
        // The temporary file is this function's to clean up: a rename that
        // failed leaves nothing behind that a later run could mistake for the
        // document.
        try {
          fs.unlink(temporary)
        } catch {
          /* the write itself failed; there is nothing to remove */
        }
        throw error
      }
    },
  }
}

/**
 * refusedInstallationStore is the store a console gets when it has **no** place
 * to keep its installations: `installationStorePath` refused the directory
 * (review S7 -- a `$FUGARO_CONFIG` in the home directory makes every `userData`
 * "inside" the CLI's config). The console still starts: `load` answers the
 * refusal with the sentence, which the picker and the dashboard draw, and
 * `save` refuses, so nothing is ever written there.
 */
export function refusedInstallationStore(path: string, reason: string): InstallationFileStore {
  return {
    path,
    load: (): InstallationRead => ({ ok: false, reason }),
    save: (): void => {
      throw new Error(`the console keeps no installations file: ${reason}`)
    },
  }
}

/** ValidationDeps is everything `validateInstallation` needs, all of it handed
 * in. `verify` is the one check that runs the CLI, and it is
 * `verifyCandidate` from `src/source/cli.ts`: the verification logic stays in
 * exactly one place. */
export interface ValidationDeps {
  readonly appEnv?: Readonly<Record<string, string | undefined>>
  readonly spawner?: ChildSpawner
  readonly spawnLog?: SpawnLogWriter
  readonly fs?: InstallationFs
  readonly verify: (candidate: InstallationCandidate) => Promise<void>
  /** workingDirectory is where a probe without a checkout runs: the private
   * directory (design section 3.4 rule 5), never the binary's own directory.
   * It is a dep only so a test can see which one was used. */
  readonly workingDirectory?: () => string
}

/** ValidatedInstallation is an installation that passed every check, with the
 * compatibility the handshake read. `mode` is `ok` or `reduced`: a `refused`
 * CLI is never stored. */
export interface ValidatedInstallation {
  readonly installation: StoredInstallation
  readonly compatibility: CliCompatibility
}

/** InstallationValidation is the answer: the installation, or the sentence that
 * says why it was not kept. Nothing is stored on the refusing path. */
export type InstallationValidation =
  | { readonly ok: true; readonly value: ValidatedInstallation }
  | { readonly ok: false; readonly reason: string }

/**
 * withSpawnLog runs one probe with a spawn log: the one the caller handed in, or
 * one this function opens from the operator's `FUGARO_CONSOLE_SPAWN_LOG` and
 * closes when the probe is done -- the same rule `verifyCandidate` and the
 * startup handshake follow, so an add's or a choose's `version` spawn is in the
 * log with the candidate's project (plan 3.4b step 2; review S2).
 */
async function withSpawnLog<T>(
  env: Readonly<Record<string, string | undefined>>,
  given: SpawnLogWriter | undefined,
  body: (log: SpawnLogWriter) => Promise<T>,
): Promise<T> {
  const log = given ?? createSpawnLog(env)
  try {
    return await body(log)
  } finally {
    if (log !== given) log.close()
  }
}

/** newInstallationId is the app's own identifier for an installation. */
export function newInstallationId(): string {
  return randomBytes(INSTALLATION_ID_LENGTH / 2).toString('hex')
}

/**
 * validateInstallation runs the checks in the order the plan fixes, and stops
 * at the first refusal:
 *
 *   1. the schema: a project name that is safe and bounded, and an id;
 *   2. the binary: absolute, and an existing **executable regular file**;
 *   3. the config path, when given: absolute and an existing file, `stat`ed and
 *      **never opened**;
 *   4. the checkout, when given: absolute and an existing directory;
 *   5. the version handshake: `version` is run and classified, and a `refused`
 *      CLI is not stored (a `reduced` one is, and is marked);
 *   6. `deps.verify` -- one allow-listed `doctor --json` -- whose refusal is the
 *      CLI's own sentence, shown verbatim.
 *
 * Steps 2 and 4 are about *existence* and step 3 is about *shape*; none of them
 * is a read of the file's contents, and step 6 is the only place a config is
 * read at all -- by the CLI, which is the only thing that names it.
 */
export async function validateInstallation(
  candidate: InstallationCandidate,
  deps: ValidationDeps,
): Promise<InstallationValidation> {
  const fs = deps.fs ?? nodeInstallationFs
  const env = deps.appEnv ?? {}

  // 1. The schema.
  const project = candidate.project
  if (typeof project !== 'string' || !isProjectName(project)) {
    return {
      ok: false,
      reason: 'a project name is required: no whitespace, no leading dash, no control character, at most 200 characters',
    }
  }
  const binaryPath = candidate.binaryPath
  if (!isAbsolutePath(binaryPath, MAX_INSTALLATION_PATH_LENGTH)) {
    return { ok: false, reason: 'the binary path must be an absolute path' }
  }
  const configPath = candidate.configPath
  if (configPath !== undefined && !isAbsolutePath(configPath, MAX_INSTALLATION_PATH_LENGTH)) {
    return { ok: false, reason: 'the config path must be an absolute path' }
  }
  const checkout = candidate.checkout
  if (checkout !== undefined && !isAbsolutePath(checkout, MAX_INSTALLATION_PATH_LENGTH)) {
    return { ok: false, reason: 'the checkout must be an absolute path' }
  }

  // 2. The binary: exists, is a regular file, and is executable. The `stat` and
  // the executable bit are one check, so a directory that happens to be
  // searchable is not mistaken for a binary.
  if (!isExecutableFile(binaryPath)) {
    return { ok: false, reason: `${binaryPath} is not an executable file` }
  }
  if (!fs.stat(binaryPath).isFile()) {
    return { ok: false, reason: `${binaryPath} is not a regular file` }
  }

  // 3. The config path: a `stat` and nothing else. This is the check the design
  // says is allowed -- the console may know that a file exists without knowing
  // what is in it.
  if (configPath !== undefined) {
    let isFile = false
    try {
      isFile = fs.stat(configPath).isFile()
    } catch {
      isFile = false
    }
    if (!isFile) return { ok: false, reason: `${configPath} is not a file` }
  }

  // 4. The checkout: a directory, because it becomes the child's working
  // directory (design section 3.4 rule 5).
  if (checkout !== undefined) {
    let isDirectory = false
    try {
      isDirectory = fs.stat(checkout).isDirectory()
    } catch {
      isDirectory = false
    }
    if (!isDirectory) return { ok: false, reason: `${checkout} is not a directory` }
  }

  // 5. The handshake, against the binary that was just checked. The target
  // carries the project and the config, because `version` is the one command
  // that takes neither -- but `planFor` decides that, and this is the same
  // target every other spawn from this installation uses.
  const target: SpawnTarget = { project, ...(configPath === undefined ? {} : { configPath }) }
  // The checkout, or the private directory -- the same choice `verifyCandidate`
  // makes for step 6, so one add spawns from one directory (rule 5; review S2).
  const directory = checkout ?? (deps.workingDirectory ?? privateWorkingDirectory)()
  const plan = planFor('cli.version', {}, {
    target,
    binaryPath,
    directory,
    appEnv: env,
  })
  const probed = await withSpawnLog(env, deps.spawnLog, (log) =>
    probeVersion(plan, { ...(deps.spawner === undefined ? {} : { spawner: deps.spawner }), log }),
  )
  if (!probed.ok) return { ok: false, reason: probed.reason }
  const compatibility = classifyCliVersion(probed.version)
  if (compatibility.mode === 'refused') {
    return {
      ok: false,
      reason:
        `this console cannot drive the CLI at ${binaryPath}: ` +
        `it reports ${compatibility.cliVersion ?? 'no version'}, and this console needs ` +
        `${compatibility.supported.join(' or ')}.`,
    }
  }

  // 6. The one allow-listed read that proves the pairing. Its refusal is the
  // CLI's own sentence, passed through unchanged -- and a refusal is a **value**
  // here, not a throw: `deps.verify` is `verifyCandidate`, which signals a
  // refusal by throwing a `SourceRefusal` whose message *is* the CLI's line.
  // Letting it escape would give the picker an unhandled rejection instead of
  // the sentence the operator needs to read, and would make the "nothing is
  // stored" guarantee depend on a caller's `catch` rather than on this step.
  try {
    await deps.verify({ project, binaryPath, ...(configPath === undefined ? {} : { configPath }), ...(checkout === undefined ? {} : { checkout }) })
  } catch (error) {
    return { ok: false, reason: error instanceof Error ? error.message : 'the pairing check refused this installation' }
  }

  return {
    ok: true,
    value: {
      installation: {
        id: newInstallationId(),
        project,
        binaryPath,
        ...(configPath === undefined ? {} : { configPath }),
        ...(checkout === undefined ? {} : { checkout }),
      },
      compatibility,
    },
  }
}

/**
 * probeInstallation runs **only** the version handshake against a stored
 * installation: `version`, classified, and nothing else. It is what a *choose*
 * runs -- the plan's step 5, "a choose of an installation whose CLI is refused
 * is not a choose" -- and it is deliberately not `validateInstallation`, which
 * would run the pairing check again. Choosing is not adding: the operator has
 * already proved the pairing once, and re-running `doctor` on every choose would
 * be a second spawn for a fact that has not changed.
 *
 * The compatibility is returned so the caller can record it: the version is
 * never stored (a stored version can only go stale), and this is the read that
 * keeps it current for the run.
 */
export async function probeInstallation(
  installation: StoredInstallation,
  deps: {
    readonly appEnv?: Readonly<Record<string, string | undefined>>
    readonly spawner?: ChildSpawner
    readonly spawnLog?: SpawnLogWriter
    readonly workingDirectory?: () => string
  },
): Promise<{ readonly ok: true; readonly compatibility: CliCompatibility } | { readonly ok: false; readonly reason: string }> {
  const env = deps.appEnv ?? {}
  const target: SpawnTarget = {
    project: installation.project,
    ...(installation.configPath === undefined ? {} : { configPath: installation.configPath }),
  }
  const plan = planFor('cli.version', {}, {
    target,
    binaryPath: installation.binaryPath,
    directory: installation.checkout ?? (deps.workingDirectory ?? privateWorkingDirectory)(),
    appEnv: env,
  })
  const probed = await withSpawnLog(env, deps.spawnLog, (log) =>
    probeVersion(plan, { ...(deps.spawner === undefined ? {} : { spawner: deps.spawner }), log }),
  )
  if (!probed.ok) return { ok: false, reason: probed.reason }
  const compatibility = classifyCliVersion(probed.version)
  if (compatibility.mode === 'refused') {
    return {
      ok: false,
      reason:
        `this console cannot drive the CLI at ${installation.binaryPath}: ` +
        `it reports ${compatibility.cliVersion ?? 'no version'}, and this console needs ` +
        `${compatibility.supported.join(' or ')}.`,
    }
  }
  return { ok: true, compatibility }
}

// ---------------------------------------------------------------------------
// The picker's closed surface
// ---------------------------------------------------------------------------

/** InstallationOperationName is the eight channels the picker window may use.
 * They are the picker's whole surface: three dialogs that answer tokens, the
 * list, the current row, and the three mutations. */
export type InstallationOperationName =
  | 'list'
  | 'current'
  | 'pickBinary'
  | 'pickConfig'
  | 'pickCheckout'
  | 'add'
  | 'choose'
  | 'remove'

/** InstallationArgumentKind is the closed set of shapes one argument may take.
 * `installationToken` is the one that matters: it is a token this process
 * issued, and a literal path -- however well-formed -- is not one. */
export type InstallationArgumentKind = 'installationName' | 'installationId' | 'installationToken'

/** InstallationArgument is one argument of one operation. */
export interface InstallationArgument {
  readonly key: string
  readonly kind: InstallationArgumentKind
  readonly required: boolean
}

/** InstallationOperation is one row of the table. */
export interface InstallationOperation {
  readonly name: InstallationOperationName
  readonly method: string
  readonly args: readonly InstallationArgument[]
}

/**
 * INSTALLATION_OPERATIONS is the picker's allow-list, and it is the *whole* of
 * it. There is no row that takes a path, a command, a URL or a free-form
 * string: `add` names its three paths by **pick token**, which is a capability
 * the main process issued for a path the main process chose in a native dialog.
 * A renderer that sends a path -- even the path it was just shown -- has sent
 * something no row accepts, and it is refused by name.
 */
export const INSTALLATION_OPERATIONS: readonly InstallationOperation[] = Object.freeze(([
  { name: 'list', method: 'list', args: [] },
  { name: 'current', method: 'current', args: [] },
  { name: 'pickBinary', method: 'pickBinary', args: [] },
  { name: 'pickConfig', method: 'pickConfig', args: [] },
  { name: 'pickCheckout', method: 'pickCheckout', args: [] },
  {
    name: 'add',
    method: 'add',
    args: [
      { key: 'project', kind: 'installationName', required: true },
      { key: 'binary', kind: 'installationToken', required: true },
      { key: 'config', kind: 'installationToken', required: false },
      { key: 'checkout', kind: 'installationToken', required: false },
    ],
  },
  { name: 'choose', method: 'choose', args: [{ key: 'id', kind: 'installationId', required: true }] },
  { name: 'remove', method: 'remove', args: [{ key: 'id', kind: 'installationId', required: true }] },
] satisfies readonly InstallationOperation[]).map(
  (row): InstallationOperation => Object.freeze({ ...row, args: Object.freeze(row.args.map((arg) => Object.freeze(arg))) }),
))

/** INSTALLATION_OPERATION_NAMES is the picker's names, frozen. */
export const INSTALLATION_OPERATION_NAMES: readonly InstallationOperationName[] = Object.freeze(
  INSTALLATION_OPERATIONS.map((row) => row.name),
)

/** installationChannel is the only place a picker channel is built. */
export function installationChannel(name: InstallationOperationName): string {
  return INSTALLATION_CHANNEL_PREFIX + name
}

/** installationOperationFor throws on a name that is not in the table. */
export function installationOperationFor(name: string): InstallationOperation {
  const found = INSTALLATION_OPERATIONS.find((row) => row.name === name)
  if (found === undefined) throw new Error(`not an installation operation: ${name}`)
  return found
}

/** MAX_INSTALLATION_ARGUMENT_KEYS is the same width the dashboard's validator
 * uses: a payload wider than any operation's schema is refused before it is
 * looked at. */
export const MAX_INSTALLATION_ARGUMENT_KEYS = 4
const MAX_INSTALLATION_ARGUMENT_LENGTH = 4096

/** InstallationArguments is a validated payload: the keys the row declares,
 * with values that are the shapes the row declares. */
export type InstallationArguments = Readonly<Record<string, string>>

/** validateInstallationArguments is the closed-schema check, and it returns the
 * checked values rather than the payload: a key the row does not declare never
 * reaches a handler, and neither does a value of the wrong shape.
 *
 * `redeem` is how a token is checked. It is called here, at validation time,
 * and it consumes the token: a token that is redeemed twice is refused the
 * second time, because the first redemption removed it. */
export function validateInstallationArguments(
  operation: InstallationOperation,
  payload: unknown,
  redeem: (kind: 'binary' | 'config' | 'checkout', token: string) => string | null,
): { readonly ok: true; readonly args: InstallationArguments } | { readonly ok: false; readonly reason: string } {
  const given: Record<string, unknown> = payload === undefined || payload === null ? {} : (payload as Record<string, unknown>)
  if (typeof given !== 'object' || Array.isArray(given)) return { ok: false, reason: 'the arguments are not an object' }
  const keys = Object.keys(given)
  if (keys.length > MAX_INSTALLATION_ARGUMENT_KEYS) return { ok: false, reason: 'too many arguments' }
  const declared = operation.args.map((arg) => arg.key)
  const unknown = keys.filter((key) => !declared.includes(key))
  if (unknown.length > 0) return { ok: false, reason: `unknown argument: ${unknown.join(', ')}` }

  const args: Record<string, string> = {}
  for (const arg of operation.args) {
    const value = given[arg.key]
    if (value === undefined) {
      if (arg.required) return { ok: false, reason: `${arg.key} is required` }
      continue
    }
    if (typeof value !== 'string' || value === '' || value.length > MAX_INSTALLATION_ARGUMENT_LENGTH) {
      return { ok: false, reason: `${arg.key} is not a value this operation accepts` }
    }
    switch (arg.kind) {
      case 'installationName':
        if (!isProjectName(value)) return { ok: false, reason: `${arg.key} is not a project name` }
        break
      case 'installationId':
        if (!INSTALLATION_ID_PATTERN.test(value)) return { ok: false, reason: `${arg.key} is not an installation id` }
        break
      case 'installationToken': {
        // The one check the whole picker turns on: the value must be an
        // outstanding token this process issued for this kind of path. A
        // literal path is refused here, by shape, before any redemption.
        if (!PICK_TOKEN_PATTERN.test(value)) return { ok: false, reason: `${arg.key} is not a path this console offered` }
        const path = redeem(arg.key as 'binary' | 'config' | 'checkout', value)
        if (path === null) return { ok: false, reason: `${arg.key} is not an outstanding choice` }
        args[arg.key] = path
        continue
      }
    }
    args[arg.key] = value
  }
  return { ok: true, args: Object.freeze(args) }
}

/** PickKind is the three dialogs, and the argument each answers for. */
export type PickKind = 'binary' | 'config' | 'checkout'

/** PickTokens is the pick-token register. */
export interface PickTokens {
  issue(kind: PickKind, path: string): string
  redeem(kind: PickKind, token: string): string | null
  dropAll(): void
  readonly outstanding: number
}

/**
 * createPickTokens returns the register.
 *
 * A token is **single-use** (redeeming removes it, so a replay answers
 * `null`), it is bound to the kind of path it was issued for (a token issued
 * for a checkout cannot arrive as a binary), it is **dropped when the picker
 * closes** (`dropAll`), and at most `MAX_OUTSTANDING_PICKS` are outstanding --
 * issuing past the bound drops the oldest, so a renderer that opens dialogs in
 * a loop cannot grow this map without limit.
 *
 * Nothing here is a path the renderer can read back: the tokens are the only
 * thing the renderer ever sees, and they are what makes "the renderer never
 * names a path" a property of the surface rather than a rule the renderer is
 * asked to follow.
 */
export function createPickTokens(): PickTokens {
  const issued = new Map<string, { readonly kind: PickKind; readonly path: string }>()
  return {
    issue(kind: PickKind, path: string): string {
      if (issued.size >= MAX_OUTSTANDING_PICKS) {
        const oldest = issued.keys().next()
        if (!oldest.done) issued.delete(oldest.value)
      }
      const token = randomBytes(PICK_TOKEN_LENGTH / 2).toString('hex')
      issued.set(token, { kind, path })
      return token
    },
    redeem(kind: PickKind, token: string): string | null {
      const held = issued.get(token)
      if (held === undefined || held.kind !== kind) return null
      issued.delete(token)
      return held.path
    },
    dropAll(): void {
      issued.clear()
    },
    get outstanding(): number {
      return issued.size
    },
  }
}

// ---------------------------------------------------------------------------
// The list the app holds for one run
// ---------------------------------------------------------------------------

/** InstallationMode is a row's mode: the compatibility the handshake last read
 * for it, or `unknown` when nothing has read it in this run. An installation
 * nobody probed is never reported `ok` (review S1). */
export type InstallationMode = CliCompatibility['mode'] | 'unknown'

/** InstallationRow is one row as a screen sees it. `active` is a boolean and
 * `mode` is the compatibility the handshake last read for it: the picker draws
 * a reduced installation differently, and the *reason* is the console's, not
 * the row's. */
export interface InstallationRow {
  readonly id: string
  readonly project: string
  readonly binary: string
  readonly config?: string
  readonly checkout?: string
  readonly active: boolean
  readonly mode: InstallationMode
}

/** ActiveInstallation is what the dashboard's `installation.current` answers.
 *
 * With none chosen it carries the store's refusal and path when the file was
 * refused, so the dashboard can say so in a sentence naming the path (plan 3.4b
 * step 1). With one, it carries the handshake's version and mode for it (plan
 * step 6), or `cliVersion: null` and `mode: 'unknown'` when nothing has read it
 * in this run. */
export type ActiveInstallation =
  | {
      readonly chosen: false
      readonly sentence: string
      readonly refusal?: string
      readonly path?: string
    }
  | {
      readonly chosen: true
      readonly id: string
      readonly project: string
      readonly binary: string
      readonly config?: string
      readonly checkout?: string
      readonly cliVersion: string | null
      readonly mode: InstallationMode
    }

/** InstallationRegistry is the store as the rest of the app uses it. */
export interface InstallationRegistry {
  /** load reads the file once. It is called at startup, before any window. */
  load(): InstallationRead
  rows(): readonly InstallationRow[]
  active(): StoredInstallation | null
  activeCompatibility(): CliCompatibility | null
  current(): ActiveInstallation
  /** add validates the candidate and persists it. Nothing is written on the
   * refusing path. */
  add(candidate: InstallationCandidate): Promise<InstallationValidation>
  /** choose binds the installation and persists `active`. */
  choose(id: string): InstallationFile
  /** remove drops the installation; removing the active one leaves none
   * active. */
  remove(id: string): InstallationFile
  /** remember records the compatibility the handshake read for one
   * installation, so a row can say `reduced` without re-running `version`. */
  remember(id: string, compatibility: CliCompatibility): void
  readonly path: string
}

/**
 * createInstallationRegistry returns the registry over one file store.
 *
 * It holds the list for the run and writes the file on exactly three
 * occasions: an add that validated, a choose, and a remove. A load that was
 * refused is remembered (`refusal`), the console starts with no installation,
 * and **an add is refused** until the file is fixed -- because the alternative
 * is writing a new file over one this version did not understand.
 */
export function createInstallationRegistry(options: {
  readonly store: InstallationFileStore
  readonly validate: (candidate: InstallationCandidate) => Promise<InstallationValidation>
}): InstallationRegistry & { readonly refusal: string | null } {
  let file: InstallationFile = EMPTY_INSTALLATION_FILE
  let refusal: string | null = null
  const compatibility = new Map<string, CliCompatibility>()

  const persist = (next: InstallationFile): InstallationFile => {
    options.store.save(next)
    file = next
    return next
  }

  /** activeOf is the active installation, or null. It is a closure rather than
   * a method so `current()` and `active()` cannot drift. */
  const activeOf = (): StoredInstallation | null => {
    if (file.active === null) return null
    return file.installations.find((installation) => installation.id === file.active) ?? null
  }

  return {
    get path(): string {
      return options.store.path
    },
    get refusal(): string | null {
      return refusal
    },
    load(): InstallationRead {
      const read = options.store.load()
      if (!read.ok) {
        refusal = read.reason
        file = EMPTY_INSTALLATION_FILE
        return read
      }
      refusal = null
      file = read.file
      return read
    },
    rows(): readonly InstallationRow[] {
      return file.installations.map((installation) => ({
        id: installation.id,
        project: installation.project,
        binary: installation.binaryPath,
        ...(installation.configPath === undefined ? {} : { config: installation.configPath }),
        ...(installation.checkout === undefined ? {} : { checkout: installation.checkout }),
        active: installation.id === file.active,
        mode: compatibility.get(installation.id)?.mode ?? 'unknown',
      }))
    },
    active: activeOf,
    activeCompatibility(): CliCompatibility | null {
      const active = file.active
      return active === null ? null : (compatibility.get(active) ?? null)
    },
    current(): ActiveInstallation {
      const active = activeOf()
      if (active === null) {
        return {
          chosen: false,
          sentence: NO_ACTIVE_INSTALLATION,
          ...(refusal === null ? {} : { refusal, path: options.store.path }),
        }
      }
      const read = compatibility.get(active.id)
      return {
        chosen: true,
        id: active.id,
        project: active.project,
        binary: active.binaryPath,
        ...(active.configPath === undefined ? {} : { config: active.configPath }),
        ...(active.checkout === undefined ? {} : { checkout: active.checkout }),
        cliVersion: read?.cliVersion ?? null,
        mode: read?.mode ?? 'unknown',
      }
    },
    async add(candidate: InstallationCandidate): Promise<InstallationValidation> {
      if (refusal !== null) {
        return { ok: false, reason: `the console will not add an installation while ${options.store.path} is unreadable (${refusal})` }
      }
      if (file.installations.length >= MAX_INSTALLATIONS) {
        return { ok: false, reason: `the console keeps at most ${String(MAX_INSTALLATIONS)} installations` }
      }
      const validated = await options.validate(candidate)
      if (!validated.ok) return validated
      // The bound is checked again *after* the await: validation runs a child
      // process, so two adds can be in flight at once, and a check made only
      // before it would let a seventeenth entry through. There is no `await`
      // between this check and the persist, which is what makes it hold.
      if (file.installations.length >= MAX_INSTALLATIONS) {
        return { ok: false, reason: `the console keeps at most ${String(MAX_INSTALLATIONS)} installations` }
      }
      compatibility.set(validated.value.installation.id, validated.value.compatibility)
      persist({
        version: INSTALLATION_FILE_VERSION,
        active: file.active,
        installations: [...file.installations, validated.value.installation],
      })
      return validated
    },
    choose(id: string): InstallationFile {
      if (!file.installations.some((installation) => installation.id === id)) {
        throw new Error(`no installation with id ${id}`)
      }
      return persist({ version: INSTALLATION_FILE_VERSION, active: id, installations: file.installations })
    },
    remember(id: string, value: CliCompatibility): void {
      compatibility.set(id, value)
    },
    remove(id: string): InstallationFile {
      const remaining = file.installations.filter((installation) => installation.id !== id)
      compatibility.delete(id)
      // Removing the active installation leaves **none** active: there is no
      // "next one" the console may pick for the operator, and D13's no-guess
      // rule is the same rule here.
      return persist({
        version: INSTALLATION_FILE_VERSION,
        active: file.active === id ? null : file.active,
        installations: remaining,
      })
    },
  }
}
