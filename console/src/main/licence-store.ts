// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The console's own keeper of the licence key (subscriptions plan S2.7; the
// design is docs/licensing/licence-key.md, "Storage and hand-off (F41)").
//
// **The console's row of that table is "the console's own store on the machine,
// one per installation", and this module is it.** One key per installation, in
// one file beside the console's other records (`installations.json`, whose
// directory rule this file reuses rather than restating), kept at mode 0600 in
// a directory of the console's own at mode 0700 -- the same modes
// `installations.ts` keeps its file at, and for the same reason: the key is a
// credential and a file another local user can read is the file the design keeps
// out of argv.
//
// **A paste replaces the key the installation had.** The design's overlap -- two
// keys installed at once, the later `expires` applying (T26) -- is the *CLI's*
// file's shape (`ee/licence/store.go`, `maxInstalled`): that file is written by
// an operator by hand, so the CLI has to choose between two lines. The console's
// store is written by a person looking at the screen, pasting the key they were
// issued for *this* installation, and a renewal pasted 30 days early replaces
// the key it follows -- the later expiry is the one that applies either way, so
// nothing is lost that T26 exists to keep. What the design does keep, and what
// this file therefore does too: a key that does not verify is **refused and
// written nowhere**, so a mistyped paste never costs an installation the key it
// had.
//
// **The key text leaves this module by exactly one door.** `keyFor` is it, and
// the child environment is the only caller (`childEnvironment`'s `licenceKey`,
// `src/main/spawn.ts`): the renderer's read is `read`, which answers the
// verifier's own `LicenceRead` -- claims and state, or a refusal sentence -- and
// never the key. That is the half the design's "never written to a file, a log
// or an error message" is about in a console: a screen that held the key would
// draw it, and a reply carrying it would be a key in the renderer.
//
// **No key is ever read out of this process's environment.** A key arrives from
// a paste, through `save`; `test/unit/licence-store.test.ts` plants one in the
// app's environment and holds the hand-off to handing over nothing.

import { randomBytes } from 'node:crypto'
import { dirname } from 'node:path'

import {
  MAX_INSTALLATIONS,
  isProjectName,
  nodeInstallationFs,
  type InstallationFs,
} from './installations'
import {
  MAX_LICENCE_KEY_LENGTH,
  PRODUCTION_KEYRING,
  verifyLicenceKey,
  type LicenceClaims,
  type LicenceKeyring,
  type LicenceRead,
  type LicenceState,
} from './licence'

/** LICENCE_FILE_NAME is the console's own file, beside `installations.json` in
 * the console's user-data directory. It is **not** the CLI's `licence.key`: the
 * CLI keeps its own file in its own config directory (S2.3), and the console
 * hands the key over in the child's environment rather than by writing there. */
export const LICENCE_FILE_NAME = 'licence.json'

/** LICENCE_FILE_VERSION is the file's own version, read strictly: a file a
 * later console wrote is refused whole rather than half-read. */
export const LICENCE_FILE_VERSION = 1

/** MAX_LICENCE_FILE_BYTES bounds the file this store reads. A key is a few
 * hundred bytes and an installation may hold one, so the bound is generous by
 * two orders of magnitude and still refuses a file that is not this store's. */
export const MAX_LICENCE_FILE_BYTES = 16 * 1024

/** LICENCE_FILE_MODE and LICENCE_DIR_MODE are the modes the store keeps, and
 * they are `installations.ts`'s numbers: a credential file is 0600 in a 0700
 * directory, so the group's and the other users' bits are refused rather than
 * tolerated. */
export const LICENCE_FILE_MODE = 0o600
export const LICENCE_DIR_MODE = 0o700

/** LICENCE_FILE_KEYS and LICENCE_ENTRY_KEYS are the closed schemas: an extra
 * key and a missing one are the same refusal, so a field a later version adds
 * is a version bump rather than a silently ignored key. */
export const LICENCE_FILE_KEYS: readonly string[] = Object.freeze(['version', 'installations'])
export const LICENCE_ENTRY_KEYS: readonly string[] = Object.freeze(['project', 'key'])

/** LicenceEntry is one installation's key. `project` is the installation's own
 * name -- the same name the records and `--project` use -- and not a path, an
 * id or a machine: the key binds to an organisation (the design's key-binding
 * ruling), so nothing about the machine belongs in this file. */
export interface LicenceEntry {
  readonly project: string
  readonly key: string
}

/** LicenceFile is the whole file: a version and one entry per installation that
 * has a key. An installation with no key is simply not in it -- F43's no-key
 * state is the store being empty rather than an entry saying so. */
export interface LicenceFile {
  readonly version: 1
  readonly installations: readonly LicenceEntry[]
}

/** EMPTY_LICENCE_FILE is the file a console that was never given a key has. It
 * is not written until a paste happens. */
export const EMPTY_LICENCE_FILE: LicenceFile = Object.freeze({
  version: LICENCE_FILE_VERSION,
  installations: Object.freeze([]),
})

/** LicenceFileRead is what reading the file answers: the file, or the sentence
 * that says why it was refused. A refusal is a value rather than a throw,
 * `installations.ts`'s rule: the console starts anyway, says what is wrong with
 * the file, and refuses a paste until the operator fixes it. */
export type LicenceFileRead =
  | { readonly ok: true; readonly file: LicenceFile }
  | { readonly ok: false; readonly reason: string }

/**
 * LicenceReply is what a **screen** is told, and it is the shape the surface
 * answers `licence.read` and `licence.save` with (S2.7): the verifier's own
 * facts, a refusal in the verifier's own sentence, or F43's no-key state. It is
 * a closed union rather than a nullable read because the three are three
 * different things a screen draws, and a screen that read `null`/`undefined` as
 * "no key" would say an installation has none the moment a handler answered
 * nothing at all -- the fail-open direction, on the one fact the paid side is
 * decided by.
 *
 * **The key text is not in it**, in any of the three. What a screen draws is the
 * claims (`tier`, `seats`, `expires`, `organisation`, `features`) and the state;
 * `keyFor` is the module's only door for the key itself and the child
 * environment is its only caller.
 */
export type LicenceReply =
  | { readonly state: 'none' }
  | { readonly state: LicenceState; readonly claims: LicenceClaims }
  | { readonly state: 'refused'; readonly reason: string }

/** LicenceSaveResult is what a paste answers: the reply for the key that was
 * kept, or the sentence that refused it -- the verifier's own sentence for a key
 * that does not verify, and this module's for a file it may not write. The
 * refused case is a **refusal and not a reply**, because nothing was kept: a
 * screen draws it beside the box rather than as the installation's state. */
export type LicenceSaveResult =
  | { readonly ok: true; readonly reply: LicenceReply }
  | { readonly ok: false; readonly reason: string }

/**
 * LicenceStore is the whole surface of this module: what a screen reads, what
 * the hand-off asks for, and what a paste writes. `now` is an argument on all
 * three because a key's state is a fact about the key **and a moment**, exactly
 * as it is in the verifier (`src/main/licence.ts`), and a store that read the
 * clock itself would be a store no test could hold a state against.
 */
export interface LicenceStore {
  /** read answers the installation's key as a screen may be told about it: the
   * claims and the state, `{state: 'none'}` when the installation has none, and
   * a refusal when the key or the store's own file does not hold up. */
  read(project: string, now: Date): LicenceReply
  /** keyFor is the hand-off: the raw text of the key that applies, or
   * `undefined` when the installation has none or holds one that does not
   * verify. It is the **only** way the key text leaves this module. */
  keyFor(project: string, now: Date): string | undefined
  /** save verifies a pasted key and keeps it, or refuses it and changes
   * nothing. */
  save(project: string, raw: string, now: Date): LicenceSaveResult
}

/** replyFor is the verifier's read as a screen may be told it. A read the
 * verifier refused becomes the `refused` state rather than an absent one: a key
 * that is installed and does not hold up is the case an operator has to see and
 * act on, and drawing it as "no key" would send them looking for a paste they
 * already made. */
function replyFor(read: LicenceRead): LicenceReply {
  if (!read.ok) return { state: 'refused', reason: read.reason }
  return { state: read.state, claims: read.claims }
}

/** isPlainObject and hasExactlyKeys are the reader's own two tests, spelled
 * here rather than imported: they are three lines each and `installations.ts`'s
 * are private to that module's reader. */
function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function hasExactlyKeys(value: Record<string, unknown>, keys: readonly string[]): boolean {
  const own = Object.keys(value)
  return own.length === keys.length && keys.every((key) => Object.prototype.hasOwnProperty.call(value, key))
}

/**
 * readLicenceFile is the closed-schema check, and it is the whole of the
 * reader: a file it refuses is never partially accepted. It takes the file's
 * text, so the byte bound is the caller's -- the store `lstat`s before it reads
 * and refuses anything past `MAX_LICENCE_FILE_BYTES` without decoding it.
 *
 * The key's own shape is **not** checked here. A key that does not verify is a
 * store holding a key nothing verifies, which the design names as a real state
 * ("an id may go once the grace is over: the store then holds a key nothing
 * verifies"), and what such an installation shows is S6.1's design. What this
 * reader refuses is a file that is not this file: another version, a key this
 * console did not write, a project name that is not one.
 */
export function readLicenceFile(raw: string): LicenceFileRead {
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return { ok: false, reason: 'the file is not JSON' }
  }
  if (!isPlainObject(parsed)) return { ok: false, reason: 'the file is not a JSON object' }
  if (!hasExactlyKeys(parsed, LICENCE_FILE_KEYS)) {
    return { ok: false, reason: `the file must hold exactly ${LICENCE_FILE_KEYS.join(', ')}` }
  }
  if (parsed['version'] !== LICENCE_FILE_VERSION) {
    return { ok: false, reason: `the file’s version is not ${String(LICENCE_FILE_VERSION)}` }
  }
  const entries = parsed['installations']
  if (!Array.isArray(entries)) return { ok: false, reason: 'installations is not a list' }
  if (entries.length > MAX_INSTALLATIONS) {
    return { ok: false, reason: `the file holds more than ${String(MAX_INSTALLATIONS)} installations` }
  }
  const seen = new Set<string>()
  const installations: LicenceEntry[] = []
  for (const entry of entries) {
    if (!isPlainObject(entry)) return { ok: false, reason: 'an installation is not a JSON object' }
    if (!hasExactlyKeys(entry, LICENCE_ENTRY_KEYS)) {
      return { ok: false, reason: `an installation must hold exactly ${LICENCE_ENTRY_KEYS.join(', ')}` }
    }
    const project = entry['project']
    const key = entry['key']
    if (typeof project !== 'string' || !isProjectName(project)) {
      return { ok: false, reason: 'an installation’s project is not a project name' }
    }
    if (seen.has(project)) return { ok: false, reason: 'the file names one installation twice' }
    seen.add(project)
    // A key is text and nothing else. The bound is the one both ends of the
    // hand-off share (`MAX_LICENCE_KEY_LENGTH`), so a file that is not a
    // paste's size is refused here before a screen is asked to draw it.
    if (typeof key !== 'string' || key === '' || key.length > MAX_LICENCE_KEY_LENGTH) {
      return { ok: false, reason: `an installation’s key is not a key of at most ${String(MAX_LICENCE_KEY_LENGTH)} characters` }
    }
    installations.push({ project, key })
  }
  return { ok: true, file: { version: LICENCE_FILE_VERSION, installations: Object.freeze(installations) } }
}

/** serializeLicenceFile writes the canonical form: the two keys in one order,
 * and one entry per line so a person who opens the file can read it -- the
 * shape `serializeInstallationFile` writes, and for the same reason. */
export function serializeLicenceFile(file: LicenceFile): string {
  const entries = file.installations.map((entry) => `    ${JSON.stringify(entry)}`)
  const body = entries.length === 0 ? '[]' : `[\n${entries.join(',\n')}\n  ]`
  return `{\n  "version": ${String(LICENCE_FILE_VERSION)},\n  "installations": ${body}\n}\n`
}

/** refusedLicenceStore is the store with no file to read: the console has no place
 * to keep its own records (`installationStorePath` refused the directory, review
 * S7), so it has none to keep a key in either. It answers the refusal to every
 * read and refuses every paste, and it writes nothing -- `refusedInstallationStore`
 * one file over. */
export function refusedLicenceStore(reason: string): LicenceStore {
  return {
    read: (): LicenceReply => ({ state: 'refused', reason }),
    keyFor: (): undefined => undefined,
    save: (): LicenceSaveResult => ({ ok: false, reason }),
  }
}

/**
 * createLicenceStore opens the file at `path` and answers the store over it.
 *
 * **The file is read once, here.** The hand-off asks for a key on the path of
 * every spawn, and a filesystem read in front of every child would put this
 * store's disk in the console's critical path for a value the console itself is
 * the only writer of: `save` updates what is in memory and writes the file in
 * the same call, so the two cannot disagree. A file edited by hand under a
 * running console is read by the next start, which is what the picker's own
 * store does with its file as well.
 *
 * A file that is not there is the empty store -- a fresh install, an upgrade
 * from a console that had no key, an evaluation that never asked for one (F43) --
 * and a file that is there and is refused is a refusal the screens draw. Neither
 * is a throw: this is called while the console is starting, and a console that
 * refuses to start because of a licence file would be a console that cannot be
 * told what is wrong with it.
 */
export function createLicenceStore(options: {
  readonly path: string
  readonly fs?: InstallationFs
  readonly keyring?: LicenceKeyring
}): LicenceStore {
  const fs = options.fs ?? nodeInstallationFs
  const keyring = options.keyring ?? PRODUCTION_KEYRING
  const loaded = loadLicenceFile({ path: options.path, fs })
  // `null` is the file's own refusal and not an absent file: an absent file is
  // the empty store, and a refusal is a sentence every read carries.
  const refusal = loaded.ok ? null : loaded.reason
  const keys = new Map<string, string>()
  if (loaded.ok) for (const entry of loaded.file.installations) keys.set(entry.project, entry.key)

  return {
    read(project: string, now: Date): LicenceReply {
      if (refusal !== null) return { state: 'refused', reason: refusal }
      const key = keys.get(project)
      if (key === undefined) return { state: 'none' }
      return replyFor(verifyLicenceKey(key, now, keyring))
    },
    keyFor(project: string, now: Date): string | undefined {
      if (refusal !== null) return undefined
      const key = keys.get(project)
      if (key === undefined) return undefined
      // A child is handed a key or nothing, and a key nothing verifies is
      // nothing: the CLI would refuse it in the same words this store already
      // has, and handing it over would put a refused string into a process's
      // environment for no reader.
      return verifyLicenceKey(key, now, keyring).ok ? key : undefined
    },
    save(project: string, raw: string, now: Date): LicenceSaveResult {
      if (refusal !== null) return { ok: false, reason: refusal }
      if (!isProjectName(project)) return { ok: false, reason: 'that is not a project name' }
      // Whitespace around a paste adds no meaning to a key (the verifier trims
      // for the same reason), so the store keeps the trimmed text: what is
      // written is what a child is handed, byte for byte.
      const key = raw.trim()
      const read = verifyLicenceKey(key, now, keyring)
      // **The verifier is what refuses a key, and its sentence is the one the
      // screen draws.** A bad signature, an id this build does not hold, a term
      // longer than the tier allows: all of them are `verifyLicenceKey`'s, and a
      // key it refuses is not written -- so the installation keeps the key it
      // had.
      if (!read.ok) return { ok: false, reason: read.reason }
      const next = new Map(keys)
      next.set(project, key)
      // The file is written first, and the reply is the read of what was
      // written: a save that could not be kept is a refusal rather than a key
      // this process remembers and the next start has forgotten.
      const written: LicenceFile = {
        version: LICENCE_FILE_VERSION,
        installations: Object.freeze(
          [...next].map(([name, text]) => Object.freeze({ project: name, key: text })),
        ),
      }
      try {
        writeLicenceFile({ path: options.path, fs }, written)
      } catch (error) {
        return {
          ok: false,
          reason: `the key could not be kept (${error instanceof Error ? error.message : 'unknown'})`,
        }
      }
      keys.set(project, key)
      return { ok: true, reply: replyFor(read) }
    },
  }
}

/** loadLicenceFile is the store's reader: the same checks
 * `createInstallationFileStore`'s `load` makes, in the same order -- `lstat`
 * before anything opens the file (a symbolic link at this name is refused rather
 * than followed), a byte bound from the descriptor rather than from the `stat`,
 * and the closed schema on what was read. */
function loadLicenceFile(options: { readonly path: string; readonly fs: InstallationFs }): LicenceFileRead {
  const fs = options.fs
  let info: ReturnType<InstallationFs['lstat']>
  try {
    info = fs.lstat(options.path)
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') return { ok: true, file: EMPTY_LICENCE_FILE }
    return {
      ok: false,
      reason: `the file could not be examined (${error instanceof Error ? error.message : 'unknown'})`,
    }
  }
  if (info.isSymbolicLink()) {
    return { ok: false, reason: 'the file is a symbolic link, which the console does not follow' }
  }
  if (!info.isFile()) return { ok: false, reason: 'the file is not a regular file' }
  if (info.size > MAX_LICENCE_FILE_BYTES) {
    return { ok: false, reason: `the file is larger than ${String(MAX_LICENCE_FILE_BYTES)} bytes` }
  }
  let read: { readonly bytes: number; readonly text: string }
  try {
    // One byte past the bound, from the descriptor: a file that grew after the
    // `lstat` is still caught here, and is never read in full.
    read = fs.readRegularFile(options.path, MAX_LICENCE_FILE_BYTES + 1)
  } catch (error) {
    return {
      ok: false,
      reason: `the file could not be read (${error instanceof Error ? error.message : 'unknown'})`,
    }
  }
  if (read.bytes > MAX_LICENCE_FILE_BYTES) {
    return { ok: false, reason: `the file is larger than ${String(MAX_LICENCE_FILE_BYTES)} bytes` }
  }
  return readLicenceFile(read.text)
}

/** writeLicenceFile writes the file atomically: a temporary beside it, then a
 * rename, so a console that dies mid-write leaves the key it had rather than
 * half a document. The temporary is this function's to clean up, and the modes
 * are the store's own (`LICENCE_FILE_MODE`, `LICENCE_DIR_MODE`). */
function writeLicenceFile(
  options: { readonly path: string; readonly fs: InstallationFs },
  file: LicenceFile,
): void {
  const fs = options.fs
  fs.mkdir(dirname(options.path), { recursive: true, mode: LICENCE_DIR_MODE })
  const temporary = `${options.path}.${randomBytes(6).toString('hex')}.tmp`
  try {
    fs.writeFile(temporary, serializeLicenceFile(file), { flag: 'wx', mode: LICENCE_FILE_MODE })
    fs.rename(temporary, options.path)
  } catch (error) {
    try {
      fs.unlink(temporary)
    } catch {
      /* the write itself failed; there is nothing to remove */
    }
    throw error
  }
}
