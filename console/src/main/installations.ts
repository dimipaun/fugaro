// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
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
//   - **unique in both keys**: an id (which `active` names) and a project name
//     (which every mutation and every reader of the registry names). Two entries
//     for one project would make `replace` -- which writes a record over every
//     entry whose project matches -- put the same id in the file twice, and the
//     next load would refuse the store outright.

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
import { dirname, isAbsolute, join, posix, win32 } from 'node:path'

import { NO_ACTIVE_INSTALLATION } from './ipc'
import {
  MAX_DISTRO_NAME_LENGTH,
  isDistroName,
  isSafeInstallationValue,
  isWindowsPathSpelling,
  privateWorkingDirectory,
  resolveBinary,
  resolveInstalledBinary,
  wslUncPath,
} from './spawn'
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
 * silently drop) or something a person added by hand.
 *
 * **`distro` joined the set in one version step (F15, F46) and it did not move
 * `INSTALLATION_FILE_VERSION`.** The rule the version step exists for is the one
 * F46 names: a console that writes a key and then refuses to read its own file
 * on the next start loses every remembered installation. So a field is added in
 * the one direction that is safe -- *written* by this version and *read* by it,
 * with a file that does not carry it still accepted, because an installation
 * with no `distro` is not an incomplete record: it is a **host** installation,
 * which is what every record this console wrote before Windows is. The reader
 * refuses an entry carrying a key it does not know; it never refuses an entry
 * for the *absence* of one, and that asymmetry is what makes the addition
 * backward-compatible in the direction that matters. */
export const INSTALLATION_FILE_KEYS: readonly string[] = Object.freeze(['version', 'active', 'installations'])
export const INSTALLATION_KEYS: readonly string[] = Object.freeze([
  'id',
  'project',
  'binaryPath',
  'configPath',
  'checkout',
  'distro',
])

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

/**
 * StoredInstallation is one installation as it is kept: the project name, and
 * the console's own choices about it. Nothing else is persisted, and in
 * particular no version.
 *
 * **`binaryPath` is optional, and that is the owner's ruling of 2026-10-20 in
 * the file's own shape.** The install no longer asks the operator for a binary:
 * the main process resolves the installed `fugaro` (design section 3.4 rule 4),
 * and this field is what is left of the older form -- an *override* a store may
 * already carry, asked first and never asked for. An old entry keeps working
 * unchanged, no migration is needed, and an entry with no binary is the ordinary
 * case rather than an incomplete one.
 *
 * `configPath` is the same kind of legacy field for the same reason: the console
 * never asks for a config path either, and this is only ever read. `checkout` is
 * still written -- it is the one thing the picker may set, from the checkouts
 * the CLI itself listed for that installation.
 */
export interface StoredInstallation {
  readonly id: string
  readonly project: string
  readonly binaryPath?: string
  readonly configPath?: string
  readonly checkout?: string
  /** `distro` is the WSL distribution this installation's CLI runs inside, when
   * it has one (T-D1, D2(a)). **Absent is the ordinary case** -- a host
   * installation -- and present means the other three paths are Linux spellings
   * *inside* that distribution, which the host cannot `stat` (F4, F15) and which
   * `wsl.exe` answers for. It is read as a **name**: the console runs a shell
   * in a distribution named here, so the alphabet is `DISTRO_NAME_PATTERN`'s. */
  readonly distro?: string
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
 * inside `/a/b` and `C:\a\bc` is not inside `C:\a\b` (F13, T-D5).
 *
 * **The separator is the paths' own, and that is the whole of the repair.** The
 * function used to append a literal `'/'`, which is right on POSIX and wrong on
 * Windows in the direction that matters: `C:\Users\dimi\AppData\Fugaro Console`
 * does not start with `C:\Users\dimi\AppData/`, so the test **failed to fire**
 * and a console whose store sat inside the CLI's configuration would have been
 * allowed to write there. On Windows the paths this function is given are
 * Windows paths (Electron's `userData` is `%APPDATA%\…`), so the comparison has
 * to be Windows' -- and on Linux it has to be POSIX's.
 *
 * The rule is stated as "the paths' own separator" rather than "this process's"
 * (`path.sep`) for two reasons: a console that compared a distribution's POSIX
 * home against a Windows `userData` would otherwise pick the wrong one for one
 * of the two, and this repository's tests run on one platform while the paths
 * they must reason about are the *other* one's -- the F13 case is unprovable on
 * Linux if the separator is `process.platform`'s. A path is Windows-shaped when
 * it carries a backslash or a drive letter (`isWindowsPathSpelling`, `spawn.ts`)
 * and POSIX-shaped otherwise, and the two paths are read with the semantics of
 * whichever of them is Windows-shaped. */
export function isInside(directory: string, candidate: string): boolean {
  const windows = isWindowsPathSpelling(directory) || isWindowsPathSpelling(candidate)
  const parts = windows ? win32 : posix
  const outer = parts.resolve(directory)
  const inner = parts.resolve(candidate)
  const separator = windows ? '\\' : '/'
  return inner === outer || inner.startsWith(outer.endsWith(separator) ? outer : `${outer}${separator}`)
}

/**
 * explorerPathFor is the spelling the **file manager** opens for one
 * installation's checkout, or `undefined` when there is nothing to open (D3(a),
 * T-D3).
 *
 * It is the second half of the rule "the Linux spelling everywhere the console
 * states or passes; the UNC spelling only where the platform itself opens the
 * path". A row's `checkout` is the Linux spelling -- what the operator can paste
 * into a terminal inside the distribution, and what every word the console
 * passes carries -- while a reveal affordance needs the spelling the *file
 * manager* understands, which for a path inside a distribution is
 * `\\wsl.localhost\<distro>\…` (`wslUncPath`), because a drive path does not
 * exist for `/home/dimi/src`.
 *
 * **The UNC form is composed here rather than asked of `wslpath`.** The two are
 * not the same question: `wslpath -w` answers how *this distribution* spells a
 * path Windows can open, and a custom mount or an `/etc/wsl.conf` can change it
 * (D4(b), which is why the translation the console *uses* is a process). The
 * affordance is a convenience on top of a path the console already holds --
 * `\\wsl.localhost\` is the documented form Windows accepts for any
 * distribution -- and it is never what the console passes to the CLI.
 *
 * A host row's checkout is **already** the spelling its own file manager opens
 * -- that is what the host route resolves, and what the operator's own terminal
 * uses -- so it is handed back unchanged, and this function needs no idea of
 * which platform it is running on.
 */
export function explorerPathFor(entry: {
  readonly distro?: string | undefined
  readonly checkout?: string | undefined
}): string | undefined {
  const checkout = entry.checkout
  if (checkout === undefined || checkout === '') return undefined
  const distro = entry.distro ?? ''
  if (distro !== '') return wslUncPath(distro, checkout)
  return checkout
}

/**
 * cliConfigDirectories is the three places the CLI's own configuration lives,
 * as far as this console is concerned: `$XDG_CONFIG_HOME/fugaro`,
 * `$FUGARO_CONFIG`'s directory, and `~/.config/fugaro`.
 *
 * The home directory arrives as an argument -- the app's own environment's
 * `HOME`, the home `binDirectories` expands `~` with, and not Electron's
 * `app.getPath('home')`, which on macOS is `NSHomeDirectory()` and ignores
 * `HOME` (`installInstallationPicker` says why) -- rather than being read here,
 * which is what keeps `homedir()` out of this module
 * (`test/unit/configread.test.ts` refuses it anywhere outside binary
 * resolution) and keeps the function testable for a home it did not have.
 *
 * This is a *path* computation and it touches no file: knowing where the CLI's
 * config would be is not reading it, and refusing to write there is the point.
 *
 * **The WSL half, and what the console can honestly say about it** (F17, T-D6).
 * On the WSL route the CLI's configuration is the **distribution's** own
 * `~/.config/fugaro`, which is not on this host at all: the host's `$HOME` is a
 * Windows home and `$XDG_CONFIG_HOME` is a Windows variable, so none of the
 * three below names the directory the operator's CLI actually reads. The
 * distribution's home is therefore an *input* -- the caller that knows which
 * distribution this is hands its home directory in the distribution's own
 * spelling -- and the answer gains that one directory as a fourth entry.
 *
 * **Nothing here asks the distribution.** The home is not computed and not
 * defaulted: a `~` the console guessed would be a path the CLI does not use,
 * which is the same failure in the other direction. It arrives from the caller
 * (Part G's picker, which has the distribution in hand), and the spelling is the
 * distribution's, so the entry is compared with POSIX semantics -- which is what
 * `isInside` does by looking at the paths rather than at this process. */
export function cliConfigDirectories(
  env: Readonly<Record<string, string | undefined>>,
  home: string,
  distroHome?: string | undefined,
): readonly string[] {
  const out: string[] = []
  const xdg = env['XDG_CONFIG_HOME']
  if (xdg !== undefined && xdg !== '' && isAbsolute(xdg)) out.push(join(xdg, CLI_CONFIG_DIR_NAME))
  const explicit = env['FUGARO_CONFIG']
  if (explicit !== undefined && explicit !== '' && isAbsolute(explicit)) out.push(dirname(explicit))
  out.push(join(home, '.config', CLI_CONFIG_DIR_NAME))
  // The distribution's own, when the caller knows it. `posix.join` rather than
  // `join`: this one is a Linux path inside a distribution whatever host the
  // console is running on, and on Windows `join` would spell it `\home\…`.
  if (distroHome !== undefined && distroHome !== '') {
    out.push(posix.join(distroHome, '.config', CLI_CONFIG_DIR_NAME))
  }
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
  // **The host's three, and not the distribution's fourth** (F17, T-D6). The
  // set this reads is `cliConfigDirectories`', which carries the distribution's
  // own directory when its caller knows it -- but `userData` here is a **host**
  // path, and a host path is never inside a Linux home (F13's vacuous case, and
  // it must stay vacuous rather than become wrong). So no `distroHome` is passed
  // and none is needed: the refusal is a host-side packaging check, and the
  // store stays on the host (F14) whatever distribution the installation names.
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

/** MAX_INSTALLATION_DISTRO_LENGTH bounds the distribution's name. It is the
 * project bound, because both are short single-token names, and a distribution
 * name longer than a project name is a name `wsl.exe` would not have listed.
 *
 * The alphabet itself is `spawn.ts`'s (`DISTRO_NAME_PATTERN`, re-exported here):
 * a distribution name becomes the value of `wsl.exe`'s `-d`, it comes from a
 * document (`wsl.exe --list --quiet`, whose lines are data), and the console
 * then runs a **shell** in whatever it names -- so the rule belongs with the
 * wrapper that spells it, and the store reads the same one the plan builder
 * asserts. Two copies of it would be two answers to one question. */
export const MAX_INSTALLATION_DISTRO_LENGTH = MAX_DISTRO_NAME_LENGTH
export { isDistroName } from './spawn'

/** isAbsolutePath is the path check every stored path passes: a string, within
 * the length bound, safe to reach an argv, and absolute. A relative path is
 * refused because it would mean whatever directory the child happened to start
 * in, which is a decision this store does not make. */
function isAbsolutePath(value: unknown, max: number): value is string {
  return typeof value === 'string' && value !== '' && value.length <= max && isSafeInstallationValue(value) && isAbsolute(value)
}

/** readInstallationEntry checks one entry, and names the field it refused.
 *
 * Two uniqueness checks, one per key this file is addressed by: an id (which
 * `active` names) and a project name (which **every** mutation and every reader
 * of the registry names -- `entryOf`, `choose`, `setCheckout`, `remove`,
 * `remember`). The project one is not decoration: `replace` writes a record over
 * every entry whose project matches, so two records for one project would make
 * one mutation write the same id twice and the *next* load refuse the file --
 * a store the operator cannot repair from any screen, for a state no screen
 * caused. */
function readInstallationEntry(
  value: unknown,
  seenIds: Set<string>,
  seenProjects: Set<string>,
): StoredInstallation | string {
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
  if (seenProjects.has(project)) return `two entries share the project ${project}`
  seenProjects.add(project)
  // Optional, and checked only when it is there: an entry written before this
  // console stopped asking for a binary carries one, and it is an absolute path
  // or the entry is refused -- but an entry with none at all is the ordinary
  // shape now.
  const binaryPath = value['binaryPath']
  if (binaryPath !== undefined && !isAbsolutePath(binaryPath, MAX_INSTALLATION_PATH_LENGTH)) {
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
  // The WSL field, and the whole of what makes an installation a WSL one (F15,
  // T-D1): when it is there, `binaryPath`, `configPath` and `checkout` are Linux
  // spellings **inside** that distribution and the console reaches the CLI
  // through `wsl.exe` (D2(a)); when it is absent the entry is a host
  // installation, which is what every record written before Windows is.
  //
  // It is checked by name and against its own narrow alphabet rather than with
  // the path rule: a distribution name becomes the value of `wsl.exe`'s `-d`,
  // and the console will then run a **shell** inside whatever it names (T-D3).
  const distro = value['distro']
  if (distro !== undefined && (typeof distro !== 'string' || !isDistroName(distro))) {
    return 'an entry’s distro is not a WSL distribution name'
  }
  return {
    id,
    project,
    ...(binaryPath === undefined ? {} : { binaryPath }),
    ...(configPath === undefined ? {} : { configPath }),
    ...(checkout === undefined ? {} : { checkout }),
    ...(distro === undefined ? {} : { distro }),
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
  const seenProjects = new Set<string>()
  const installations: StoredInstallation[] = []
  for (const entry of entries) {
    const read = readInstallationEntry(entry, seenIds, seenProjects)
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

/** newInstallationId is the app's own identifier for an installation. */
export function newInstallationId(): string {
  return randomBytes(INSTALLATION_ID_LENGTH / 2).toString('hex')
}

/**
 * ValidationDeps is everything the two checks below need, all of it handed in.
 * `verify` is the one check that runs a second read, and it is `verifyCandidate`
 * from `src/source/cli.ts`: the pairing logic stays in exactly one place.
 */
export interface ValidationDeps {
  readonly appEnv?: Readonly<Record<string, string | undefined>>
  readonly spawner?: ChildSpawner
  readonly spawnLog?: SpawnLogWriter
  readonly fs?: InstallationFs
  /** `home` is what rule 4's `~/.local/bin` is resolved against, and `prefixes`
   * replaces the well-known list for a test that cannot create
   * `/opt/homebrew/bin` -- exactly as `resolveBinary` takes them. */
  readonly home?: string
  readonly prefixes?: readonly string[] | undefined
  /** verify is the pairing check of rule 7: one allow-listed `doctor --json`
   * run from the candidate's checkout, with the candidate's project. */
  readonly verify: (candidate: InstallationCandidate) => Promise<void>
  /** workingDirectory is where a probe without a checkout runs: the private
   * directory (design section 3.4 rule 5), never the binary's own directory.
   * It is a dep only so a test can see which one was used. */
  readonly workingDirectory?: () => string
  /** `wslPath` replaces the `wsl.exe` a WSL candidate's probe runs, so a test
   * can point the route at the fake (T-A1, T-C6). */
  readonly wslPath?: string | undefined
}

/** ProbedInstallation is a candidate the handshake answered for: the binary rule
 * 4 resolved, the version it reported, where it came from, and the compatibility
 * classified from it. */
export interface ProbedInstallation {
  readonly compatibility: CliCompatibility
  readonly path: string
  readonly version: string
  readonly source: 'configured' | 'well-known'
}

/** ValidatedInstallation is a candidate that passed every check, with the
 * compatibility the handshake read. `mode` is `ok` or `reduced`: a `refused` CLI
 * is never stored. `resolved` is the binary rule 4 actually found -- a fact of
 * this run, which the caller hands to the window it opens and which is **never**
 * written to the store. */
export interface ValidatedInstallation {
  readonly installation: StoredInstallation
  readonly compatibility: CliCompatibility
  readonly resolved: string
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
 * startup handshake follow, so a choose's or a checkout's `version` spawn is in
 * the log with the candidate's project (plan 3.4b step 2; review S2).
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

/**
 * probeInstallation answers "can this console drive the `fugaro` for this
 * installation, and which one is it?", and it is the whole of what a **choose**
 * runs: the binary resolved by rule 4 -- the store's override first when there
 * is one, then the well-known install locations (`/opt/homebrew/bin`,
 * `/usr/local/bin`, `~/.local/bin`, `/usr/bin`), and never the app's inherited
 * `PATH` -- and its `version`, classified. A `refused` CLI is not chosen: the
 * console shows the sentence it reached rather than binding a source it cannot
 * drive.
 *
 * It does **not** run the pairing check. Choosing is not adding: the pairing was
 * proved when the installation was given a checkout, and re-running `doctor` on
 * every choose would be a spawn for a fact that has not changed.
 *
 * The version is returned and never stored: a stored version can only go stale,
 * and this is the read that keeps it current for the run.
 */
export async function probeInstallation(
  candidate: InstallationCandidate,
  deps: Omit<ValidationDeps, 'verify' | 'fs'>,
): Promise<{ readonly ok: true; readonly value: ProbedInstallation } | { readonly ok: false; readonly reason: string }> {
  const project = candidate.project
  if (typeof project !== 'string' || !isProjectName(project)) {
    return { ok: false, reason: 'a project name is required: no whitespace, no leading dash, no control character, at most 200 characters' }
  }
  const configPath = candidate.configPath
  const checkout = candidate.checkout
  const target: SpawnTarget = { project, ...(configPath === undefined ? {} : { configPath }) }
  const env = deps.appEnv ?? {}
  // **Which route, and it is the candidate's `distro` that decides.** An
  // installation with a distribution is a Windows installation whose CLI is a
  // Linux program inside it (D2(a)): the binary's path is a Linux spelling the
  // host cannot `stat`, and the question "is it there, and does it run?" is the
  // distribution's -- asked through the same `version` probe, by
  // `resolveInstalledBinary`'s WSL branch (T-C5). Without a distro the candidate
  // is an ordinary host installation and rule 4's walk is the answer (F4).
  const distro = candidate.distro ?? ''
  const resolution = await withSpawnLog(env, deps.spawnLog, (log) => {
    const shared = {
      appEnv: env,
      target,
      ...(deps.spawner === undefined ? {} : { spawner: deps.spawner }),
      log,
    }
    if (distro !== '') {
      return resolveInstalledBinary({
        ...shared,
        binaryPath: candidate.binaryPath ?? '',
        distro,
        // The checkout is the CLI's working directory **inside** the
        // distribution, so it is the wrapper's `--cd`; `wsl.exe` itself is
        // started in a host directory, which the checkout is not.
        ...(checkout === undefined ? {} : { wslDirectory: checkout }),
        ...(deps.wslPath === undefined ? {} : { wslPath: deps.wslPath }),
        directory: (deps.workingDirectory ?? privateWorkingDirectory)(),
      })
    }
    return resolveBinary({
      ...shared,
      // The store's override, when it has one. Absent is the ordinary case: rule
      // 4 walks the well-known locations, and the console never asks.
      ...(candidate.binaryPath === undefined || candidate.binaryPath === ''
        ? {}
        : { configuredPath: candidate.binaryPath }),
      ...(deps.home === undefined ? {} : { home: deps.home }),
      ...(deps.prefixes === undefined ? {} : { prefixes: deps.prefixes }),
      directory: checkout ?? (deps.workingDirectory ?? privateWorkingDirectory)(),
    })
  })
  if (!resolution.ok) return { ok: false, reason: resolution.reason }
  const compatibility = classifyCliVersion(resolution.version)
  if (compatibility.mode === 'refused') {
    return {
      ok: false,
      reason:
        `this console cannot drive the CLI at ${resolution.path}: ` +
        `it reports ${compatibility.cliVersion ?? 'no version'}, and this console needs ` +
        `${compatibility.supported.join(' or ')}.`,
    }
  }
  return {
    ok: true,
    value: { compatibility, path: resolution.path, version: resolution.version, source: resolution.source },
  }
}

/**
 * resolveLaunchBinary answers which `fugaro` a **launch** for one stored
 * installation would run, and it is `probeInstallation`'s resolution without
 * its classification: the launch refuses a CLI below its own floor with the
 * launcher's sentence (`launchrules.ts`'s `launchSupported`) rather than with
 * this module's "cannot drive" one, so what is left here is the path and the
 * version.
 *
 * **It exists because the record decides the route, and a launch must run the
 * CLI the record names.** A record's `binaryPath` is an override and its
 * `distro` is the WSL route (T-D1, D2(a)): under a distribution the recorded
 * paths are Linux spellings *inside* it, which the host cannot `stat` at all
 * (F4, F15), and the question "is it there, and does it run?" is the
 * distribution's -- asked through the same `version` probe, by
 * `resolveInstalledBinary`'s WSL branch. Reading only the record's project and
 * then walking the host's well-known locations would resolve some other
 * `fugaro` for an installation whose own record points elsewhere -- a different
 * CLI, a different config, a different credential -- and report it as this
 * installation's, which is the one thing a launch must never do.
 */
export async function resolveLaunchBinary(
  record: StoredInstallation,
  deps: Omit<ValidationDeps, 'verify' | 'fs'>,
): Promise<
  { readonly ok: true; readonly value: { readonly path: string; readonly version: string } } | { readonly ok: false; readonly reason: string }
> {
  const env = deps.appEnv ?? {}
  const target: SpawnTarget = { project: record.project }
  const distro = record.distro ?? ''
  const binaryPath = record.binaryPath ?? ''
  // The working directory is the private one and never the record's checkout:
  // the probe is a read of the installation, and rule 5 puts a child with no
  // checkout of its own in the directory the console owns (a launch itself
  // never runs in the checkout at all -- `launch.ts` says why).
  const resolution = await withSpawnLog(env, deps.spawnLog, (log) => {
    const shared = {
      appEnv: env,
      target,
      ...(deps.spawner === undefined ? {} : { spawner: deps.spawner }),
      log,
    }
    if (distro !== '') {
      return resolveInstalledBinary({
        ...shared,
        binaryPath,
        distro,
        ...(deps.wslPath === undefined ? {} : { wslPath: deps.wslPath }),
        directory: (deps.workingDirectory ?? privateWorkingDirectory)(),
      })
    }
    return resolveBinary({
      ...shared,
      ...(binaryPath === '' ? {} : { configuredPath: binaryPath }),
      ...(deps.home === undefined ? {} : { home: deps.home }),
      ...(deps.prefixes === undefined ? {} : { prefixes: deps.prefixes }),
      directory: (deps.workingDirectory ?? privateWorkingDirectory)(),
    })
  })
  if (!resolution.ok) return { ok: false, reason: resolution.reason }
  return { ok: true, value: { path: resolution.path, version: resolution.version } }
}

/**
 * validateInstallation is the check that runs when an installation is given a
 * **checkout** -- the one thing the picker may set, from the directories the CLI
 * itself listed for that installation -- and it stops at the first refusal:
 *
 *   1. the schema: a project name that is safe and bounded, a config path and a
 *      checkout that are absolute when they are given;
 *   2. the checkout, when given **on the host route**: an existing directory,
 *      because it becomes the child's working directory (rule 5). Under a
 *      `distro` this check is not run at all -- the checkout is then a Linux
 *      spelling the host cannot see (F4, T-C6), and the probe below answers for
 *      it;
 *   3. `probeInstallation`: rule 4's resolution and the version handshake --
 *      inside the distribution when the candidate names one (T-C5) -- which is
 *      where a missing `fugaro` and a CLI this console cannot drive are
 *      refused;
 *   4. `deps.verify` -- one allow-listed `doctor --json` from that checkout,
 *      with that project -- whose refusal is the CLI's own sentence, shown
 *      verbatim. **It runs only when a checkout is given**: it is rule 7's
 *      pairing check, and an installation with no checkout has no pairing to
 *      check.
 *
 * None of these is a read of a config file: step 4 is the only place a config is
 * read at all, by the CLI, which is the only thing that names it.
 */
export async function validateInstallation(
  candidate: InstallationCandidate,
  deps: ValidationDeps,
): Promise<InstallationValidation> {
  const fs = deps.fs ?? nodeInstallationFs
  const configPath = candidate.configPath
  if (configPath !== undefined && !isAbsolutePath(configPath, MAX_INSTALLATION_PATH_LENGTH)) {
    return { ok: false, reason: 'the config path must be an absolute path' }
  }
  const checkout = candidate.checkout
  if (checkout !== undefined && !isAbsolutePath(checkout, MAX_INSTALLATION_PATH_LENGTH)) {
    return { ok: false, reason: 'the checkout must be an absolute path' }
  }
  // **The checkout's mode question is asked only on the host route.** Under a
  // `distro` the checkout is a Linux spelling inside the distribution (F15), and
  // the host cannot `stat` it -- asking would refuse an installation whose
  // directory is perfectly good, which is F4's "a path the host cannot see is
  // not the host's to answer". What answers for it is the probe below, which
  // starts the CLI *in* that directory (`--cd`, T-C5): a directory that is not
  // there is then `wsl.exe`'s refusal, in its own words.
  if (checkout !== undefined && (candidate.distro ?? '') === '') {
    let isDirectory = false
    try {
      isDirectory = fs.stat(checkout).isDirectory()
    } catch {
      isDirectory = false
    }
    if (!isDirectory) return { ok: false, reason: `${checkout} is not a directory` }
  }

  const probed = await probeInstallation(candidate, deps)
  if (!probed.ok) return { ok: false, reason: probed.reason }

  if (checkout !== undefined) {
    // The pairing check. Its refusal is the CLI's own sentence, passed through
    // unchanged -- and a refusal is a **value** here, not a throw:
    // `deps.verify` is `verifyCandidate`, which signals a refusal by throwing a
    // `SourceRefusal` whose message *is* the CLI's line. Letting it escape would
    // give the picker an unhandled rejection instead of the sentence the
    // operator needs to read, and would make the "nothing is stored" guarantee
    // depend on a caller's `catch` rather than on this step.
    //
    // **`distro` goes with it** (T-D1). For a WSL installation this read is
    // `doctor --json` **inside the distribution** (T-C5's route, D2(a)): the
    // checkout is a Linux directory the host cannot see, so a `verify` that did
    // not know the distribution would start the CLI on the host, in a directory
    // that does not exist there. What the CLI proves -- that this checkout
    // belongs to this project -- is a fact about the machine the CLI runs on.
    const distro = candidate.distro
    try {
      await deps.verify({
        project: candidate.project,
        binaryPath: probed.value.path,
        ...(configPath === undefined ? {} : { configPath }),
        checkout,
        ...(distro === undefined || distro === '' ? {} : { distro }),
      })
    } catch (error) {
      return { ok: false, reason: error instanceof Error ? error.message : 'the pairing check refused this installation' }
    }
  }

  return {
    ok: true,
    value: {
      installation: {
        id: newInstallationId(),
        project: candidate.project,
        // The candidate's own override, when it had one -- never the resolved
        // path. What rule 4 found is a fact of this run, and storing it would
        // pin the console to a binary it did not choose.
        ...(candidate.binaryPath === undefined || candidate.binaryPath === '' ? {} : { binaryPath: candidate.binaryPath }),
        ...(configPath === undefined ? {} : { configPath }),
        ...(checkout === undefined ? {} : { checkout }),
        // The WSL field, carried through unchanged (T-D1): it is what makes the
        // stored record a WSL one on every later read, and dropping it here
        // would send the next launch to the host route with Linux paths.
        ...(candidate.distro === undefined || candidate.distro === '' ? {} : { distro: candidate.distro }),
      },
      compatibility: probed.value.compatibility,
      resolved: probed.value.path,
    },
  }
}

// ---------------------------------------------------------------------------
// The picker's closed surface
// ---------------------------------------------------------------------------

/**
 * InstallationOperationName is the picker window's whole surface, and it is five
 * channels: the list, one click that opens an installation, the checkout an
 * installation is used from, the removal of the console's own record, and the
 * website link the onboarding screens offer.
 *
 * The add-an-installation form is **gone** (owner's ruling of 2026-10-20): there
 * is no `add`, no `pickBinary`, no `pickConfig` and no `pickCheckout`, because
 * the console asks the operator for no binary and no config. What is left is a
 * list the CLI printed, one button per row, and the two onboarding screens --
 * so a renderer cannot name a path this process did not itself obtain.
 */
export type InstallationOperationName = 'list' | 'choose' | 'checkout' | 'remove' | 'reveal' | 'openExternal'

/**
 * InstallationArgumentKind is the closed set of shapes one argument may take.
 *
 * `installationName` is a project name -- the handle a discovered row is opened
 * by, which is the CLI's own name for the installation. `installationCheckout`
 * is a directory the CLI *itself* listed for that installation; the shape is
 * checked here and membership of that list is checked by the picker, so a
 * renderer cannot name a directory the CLI never offered.
 *
 * There is deliberately no `externalUrl` kind: the picker's one link row takes
 * **no arguments at all**, so the URL is the main process's own constant and
 * never crosses the boundary. The console's external-link rule still applies to
 * it -- it is applied where the URL is opened, `openExternalValidated` in
 * `src/main/window.ts`, which is the one place `shell.openExternal` is called.
 */
export type InstallationArgumentKind = 'installationName' | 'installationCheckout'

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
 * it. Every row names its subject by the things the CLI itself printed -- a
 * project name, or a directory out of that installation's own `checkouts` list
 * -- and the one URL row is the console's validated external-link path. There is
 * no row that takes a binary path, a config path, a command or a free-form
 * string: a renderer that sends one has sent something no row accepts, and it is
 * refused by name.
 */
export const INSTALLATION_OPERATIONS: readonly InstallationOperation[] = Object.freeze(([
  { name: 'list', method: 'list', args: [] },
  { name: 'choose', method: 'choose', args: [{ key: 'project', kind: 'installationName', required: true }] },
  {
    name: 'checkout',
    method: 'checkout',
    args: [
      { key: 'project', kind: 'installationName', required: true },
      // Optional, and absent means "no checkout": the installation works
      // without one (design section 3.4 rule 5), and clearing one an operator
      // set is how they get back to that state.
      { key: 'checkout', kind: 'installationCheckout', required: false },
    ],
  },
  { name: 'remove', method: 'remove', args: [{ key: 'project', kind: 'installationName', required: true }] },
  // `reveal` takes a **project name** and no path: the path the file manager is
  // handed is computed in this process from its own record (`explorerPathFor`,
  // T-D3), so a renderer cannot name a directory for Explorer to open -- the
  // same rule the checkout row keeps by checking membership of the CLI's own
  // list.
  { name: 'reveal', method: 'reveal', args: [{ key: 'project', kind: 'installationName', required: true }] },
  // No arguments at all: `GETTING_STARTED_URL` is this process's constant, and
  // the one path that opens it is `openExternalValidated`. A renderer that sends
  // a URL has sent a key no row declares, and it is refused by name.
  { name: 'openExternal', method: 'openExternal', args: [] },
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
 * There is no token to redeem (the add-an-installation form, and with it
 * `createPickTokens` and `MAX_OUTSTANDING_PICKS`, is gone): what a row's subject
 * is checked against is the table's own declaration, and a `checkout`'s
 * membership of the list the CLI printed is the picker's check, against the
 * document it last read. */
export function validateInstallationArguments(
  operation: InstallationOperation,
  payload: unknown,
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
      case 'installationCheckout':
        // A shape check and nothing more: it must be an absolute directory path
        // this console will hand to a child as its `cwd`. Whether the CLI
        // actually listed it is the picker's check, against the document it
        // last read -- this one cannot know it, and a validator that guessed
        // would be the place a made-up path got through.
        if (!isAbsolutePath(value, MAX_INSTALLATION_PATH_LENGTH)) {
          return { ok: false, reason: `${arg.key} is not an absolute directory path` }
        }
        break
    }
    args[arg.key] = value
  }
  return { ok: true, args: Object.freeze(args) }
}

// ---------------------------------------------------------------------------
// The list the app holds for one run
// ---------------------------------------------------------------------------

/** InstallationMode is a row's mode: the compatibility the handshake last read
 * for it, or `unknown` when nothing has read it in this run. An installation
 * nobody probed is never reported `ok` (review S1). */
export type InstallationMode = CliCompatibility['mode'] | 'unknown'

/** InstallationRow is one **stored** record as a screen sees it: a project the
 * console has chosen, or been told something about, and whether it is active.
 *
 * The picker's rows are not these -- they are the CLI's own list, drawn as it
 * printed it, with these records merged in -- and the difference matters: a
 * store row is something the console decided, a discovered row is a fact of the
 * machine. `binary` is the legacy override when the record still carries one,
 * and `mode` is the compatibility the handshake last read for it in this run
 * (`unknown` when nothing has read it, review S1).
 */
export interface InstallationRow {
  readonly id: string
  readonly project: string
  readonly binary?: string
  readonly config?: string
  readonly checkout?: string
  /** `distro` is the WSL distribution this row's CLI runs inside, when it has
   * one (T-D1). A screen that draws a path for this row states the **Linux**
   * spelling (`checkout`) and never the UNC one, which is Windows' and lives on
   * `explorerPath` below (D3(a)). */
  readonly distro?: string
  /** `explorerPath` is the spelling **Windows opens** for this row's checkout,
   * and it is the only field a reveal affordance reads (D3(a), T-D3). It is the
   * UNC form (`\\wsl.localhost\<distro>\…`) for a WSL row and the checkout
   * itself for a host row on Windows, and it is **absent** when there is
   * nothing for Windows to open -- no checkout, or a platform that has no
   * Explorer for this console's paths. It is never what a screen draws as "the
   * installation's path": that line is `checkout`, the Linux spelling, which is
   * the one the operator can paste into a terminal inside the distribution. */
  readonly explorerPath?: string
  readonly active: boolean
  readonly mode: InstallationMode
}

/** ActiveInstallation is what the dashboard's `installation.current` answers.
 *
 * With none chosen it carries the store's refusal and path when the file was
 * refused, so the dashboard can say so in a sentence naming the path (plan 3.4b
 * step 1). With one, it carries the handshake's version and mode for it (plan
 * step 6), or `cliVersion: null` and `mode: 'unknown'` when nothing has read it
 * in this run. `binary` is the store's optional override: **absent is the
 * ordinary case**, because the console resolves the installed `fugaro` itself
 * (owner's ruling of 2026-10-20) and never stores the path it found.
 */
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
      readonly binary?: string
      readonly config?: string
      readonly checkout?: string
      /** `distro` is present exactly when the active installation is a WSL one
       * (T-D1): the header and the environment view say which side of the
       * boundary the CLI and its credentials are on (F21). */
      readonly distro?: string
      readonly cliVersion: string | null
      readonly mode: InstallationMode
    }

/**
 * withResolvedBinary joins a stored record with the binary rule 4 resolved for
 * it **in this run** (`resolved`), and it is the header's answer: the file the
 * console is actually driving.
 *
 * The resolution wins over the store's field, and that direction is the whole
 * point. A record's `binaryPath` is an **optional override** that rule 4 merely
 * *asks first* (owner's ruling of 2026-10-20): a console that read the store
 * alone would name a path it is not running the moment that file goes away and
 * the walk carries on to `/opt/homebrew/bin`, `/usr/local/bin`, `~/.local/bin`
 * and `/usr/bin`. For a record this console wrote itself there is nothing to
 * read at all -- `choose` stores an id and a project and no path -- so the
 * header would have no binary to draw while the console is driving one.
 *
 * `resolved` is `undefined` in exactly two cases, and both are answered by the
 * store: nothing has resolved a binary for this installation in this run yet,
 * and the file's own field is then the best answer there is (a legacy record,
 * before any read).
 *
 * A `chosen: false` answer carries no binary at all and is returned unchanged:
 * it is D13's sentence and the store's refusal, and neither names a file.
 */
export function withResolvedBinary(active: ActiveInstallation, resolved: string | undefined): ActiveInstallation {
  if (!active.chosen) return active
  const binary = resolved === undefined || resolved === '' ? active.binary : resolved
  return binary === undefined || binary === '' ? active : { ...active, binary }
}

/** InstallationRegistry is the store as the rest of the app uses it.
 *
 * Every method but `load` names an installation by its **project name**, which
 * is the handle the CLI's own list gives: the console's rows come from that
 * document, so a name it printed is what a screen has to send back.
 */
export interface InstallationRegistry {
  /** load reads the file once. It is called at startup, before any window. */
  load(): InstallationRead
  rows(): readonly InstallationRow[]
  /** entry is the stored record for a project, or null. */
  entry(project: string): StoredInstallation | null
  active(): StoredInstallation | null
  activeCompatibility(): CliCompatibility | null
  current(): ActiveInstallation
  /** choose records the project as this console's installation and persists
   * `active`. A project with no record gets one: it is the operator's choice,
   * and that choice is the only thing the store holds. */
  choose(project: string): InstallationFile
  /** setCheckout records the directory the installation is used from, or
   * removes it when `checkout` is undefined. */
  setCheckout(project: string, checkout: string | undefined): InstallationFile
  /** remove drops the console's record for that project; removing the active
   * one leaves none active. */
  remove(project: string): InstallationFile
  /** remember records the compatibility the handshake read for one project, so
   * a row can say `reduced` without re-running `version`. */
  remember(project: string, compatibility: CliCompatibility): void
  /**
   * sessionRecordFor is the record of an installation this console is **open on
   * without having chosen**: the store's own record when it holds one, and
   * otherwise a record made here and kept in memory only.
   *
   * It exists for the owner's open-first-readable rule (ruling of 2026-10-25):
   * the console opens an installation for this run -- the remembered choice when
   * it can be read, the first readable one otherwise -- and it must not write
   * that decision down, because the store holds the operator's choice and a boot
   * that rewrote `active` would drop it. So the session's record is built the way
   * a choose's would be (`recordOf`) and never reaches the file: `rows()` does not
   * list it, `active()` is not it, and nothing on disk changes.
   *
   * The bound `MAX_INSTALLATIONS` does not apply, and that is the point of the
   * separate function: a machine whose list is longer than this console remembers
   * still opens one of them, and the store is not the thing being asked.
   *
   * The id is stable for the run -- the same project answers the same record --
   * because it is the key the source is cached by (`createBoundSource`).
   */
  sessionRecordFor(project: string): StoredInstallation
  /** currentFor is `current()` for a record that is not the file's active one:
   * the session installation's own answer, in the shape `installation.current`
   * carries. */
  currentFor(record: StoredInstallation): ActiveInstallation
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

  const entryOf = (project: string): StoredInstallation | null =>
    file.installations.find((installation) => installation.project === project) ?? null

  /**
   * recordOf is `entryOf`, or a fresh record for a project the CLI listed and
   * the console has nothing about. Creating one is how a **choose** of a
   * discovered installation is written down: the row existed before the console
   * touched it, and what the store holds is the operator's choice of it.
   */
  const recordOf = (project: string): StoredInstallation => {
    const held = entryOf(project)
    if (held !== null) return held
    // The bound is the store's, and it is checked before a record is created:
    // a machine with more installations than this console keeps is a machine
    // whose list the picker still draws in full, with the ones it cannot
    // remember marked rather than hidden.
    if (file.installations.length >= MAX_INSTALLATIONS) {
      throw new Error(`the console keeps at most ${String(MAX_INSTALLATIONS)} installations`)
    }
    return { id: newInstallationId(), project }
  }

  /** sessionRecords holds the records `sessionRecordFor` made, so one project
   * answers one record for the run. Nothing else reads it and nothing writes it
   * to the file. */
  const sessionRecords = new Map<string, StoredInstallation>()

  /** currentFor is the answer for one record, in the shape `current()` gives the
   * file's active one: the store's fields plus the compatibility this run read
   * for that project, or `cliVersion: null` and `unknown` when nothing has. */
  const currentFor = (record: StoredInstallation): ActiveInstallation => {
    const read = compatibility.get(record.project)
    return {
      chosen: true,
      id: record.id,
      project: record.project,
      ...(record.binaryPath === undefined ? {} : { binary: record.binaryPath }),
      ...(record.configPath === undefined ? {} : { config: record.configPath }),
      ...(record.checkout === undefined ? {} : { checkout: record.checkout }),
      ...(record.distro === undefined ? {} : { distro: record.distro }),
      cliVersion: read?.cliVersion ?? null,
      mode: read?.mode ?? 'unknown',
    }
  }

  /** replace writes one record into the file, in the place the record it
   * replaces held -- or at the end, when it is new. The order is the file's own
   * and a write never reshuffles it. */
  const replace = (record: StoredInstallation, active: string | null): InstallationFile => {
    const held = file.installations.some((installation) => installation.project === record.project)
    if (!held && file.installations.length >= MAX_INSTALLATIONS) {
      throw new Error(`the console keeps at most ${String(MAX_INSTALLATIONS)} installations`)
    }
    const installations = held
      ? file.installations.map((installation) => (installation.project === record.project ? record : installation))
      : [...file.installations, record]
    return persist({ version: INSTALLATION_FILE_VERSION, active, installations })
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
        ...(installation.binaryPath === undefined ? {} : { binary: installation.binaryPath }),
        ...(installation.configPath === undefined ? {} : { config: installation.configPath }),
        ...(installation.checkout === undefined ? {} : { checkout: installation.checkout }),
        ...(installation.distro === undefined ? {} : { distro: installation.distro }),
        ...(explorerPathFor(installation) === undefined ? {} : { explorerPath: explorerPathFor(installation) }),
        active: installation.id === file.active,
        mode: compatibility.get(installation.project)?.mode ?? 'unknown',
      }))
    },
    entry: entryOf,
    active: activeOf,
    activeCompatibility(): CliCompatibility | null {
      const active = activeOf()
      return active === null ? null : (compatibility.get(active.project) ?? null)
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
      return currentFor(active)
    },
    sessionRecordFor(project: string): StoredInstallation {
      const held = entryOf(project)
      if (held !== null) return held
      const made = sessionRecords.get(project) ?? { id: newInstallationId(), project }
      sessionRecords.set(project, made)
      return made
    },
    currentFor,
    choose(project: string): InstallationFile {
      if (refusal !== null) {
        throw new Error(`the console keeps no installations while ${options.store.path} is unreadable (${refusal})`)
      }
      const record = recordOf(project)
      return replace(record, record.id)
    },
    setCheckout(project: string, checkout: string | undefined): InstallationFile {
      if (refusal !== null) {
        throw new Error(`the console keeps no installations while ${options.store.path} is unreadable (${refusal})`)
      }
      const held = recordOf(project)
      // Rebuilt field by field rather than spread with an `undefined`: clearing
      // a checkout must remove the key, so the in-memory record and the file it
      // is written to cannot disagree about whether this installation has one.
      const record: StoredInstallation = {
        id: held.id,
        project: held.project,
        ...(held.binaryPath === undefined ? {} : { binaryPath: held.binaryPath }),
        ...(held.configPath === undefined ? {} : { configPath: held.configPath }),
        ...(checkout === undefined ? {} : { checkout }),
        // **The route survives a checkout change** (T-D1). This record is
        // rebuilt field by field rather than spread, so a field left out here is
        // a field *deleted* from the file -- and a `distro` dropped by a
        // checkout would turn a WSL installation into a host one with Linux
        // paths in it, silently, on the next launch.
        ...(held.distro === undefined ? {} : { distro: held.distro }),
      }
      return replace(record, file.active)
    },
    remember(project: string, value: CliCompatibility): void {
      compatibility.set(project, value)
    },
    remove(project: string): InstallationFile {
      const held = entryOf(project)
      if (held === null) throw new Error(`no installation named ${project}`)
      compatibility.delete(project)
      // Removing the active installation leaves **none** active: there is no
      // "next one" the console may pick for the operator, and D13's no-guess
      // rule is the same rule here.
      return persist({
        version: INSTALLATION_FILE_VERSION,
        active: file.active === held.id ? null : file.active,
        installations: file.installations.filter((installation) => installation.id !== held.id),
      })
    },
  }
}
