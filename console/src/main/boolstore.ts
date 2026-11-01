// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The one-boolean store both of this console's own settings files are: the
// update switch (`./updatestore`, `updates.json`) and the crash-reporting switch
// (`./crashstore`, `crash.json`). It was extracted from `./updatestore` when the
// second switch arrived, because the two files differ in exactly two things --
// their name and the answer a file that cannot be read gets -- and everything
// else about them is a rule rather than a preference:
//
//   - **exactly one boolean, and not an object.** Each file holds one setting, so
//     its whole content is `true` or `false`: a shape with room for a second
//     field would be a shape somebody has to version later.
//   - **opened with `O_NOFOLLOW` and `O_NONBLOCK`, and the descriptor `fstat`ed
//     before a byte is read.** The kernel refuses a symlink at the name (ELOOP)
//     rather than resolving it, a FIFO cannot make the open wait for a writer
//     that is never coming, and the regular-file and length rules are asked of
//     the very object the read returns -- one `open` rather than a name looked at
//     and then opened, which is two questions a name can answer differently. A
//     symlink, a directory or a FIFO is the same refusal as a malformed file, so
//     no name in this directory can be made to point a read elsewhere or to block
//     it. The line is one per file per process, because a switch is read as often
//     as the thing it gates runs.
//   - **atomic, and only on a change.** The document goes to a fresh temporary
//     file and a rename publishes it, exactly as `installations.json` does, and a
//     write whose value is already the file's is no write at all.
//   - **free of Electron.** The directory arrives as an argument and the
//     filesystem as a `BoolStoreFs` (`nodeBoolStoreFs` is the only place this
//     module meets `node:fs`), which is what lets the unit tests hold the mode,
//     the rename and the "only on a change" rule against a filesystem that
//     records what it was asked to do.
//
// What the two files deliberately do **not** share is which way they fail: the
// update switch is on when its file is missing or unreadable, because a check
// that stops is a console that stops updating, and the crash switch is **off**,
// because a report is only ever sent on a yes the operator gave. That difference
// is the `BoolStoreSpec` a caller hands in, and it is the only thing about the
// two that a reader has to look up.

import { randomBytes } from 'node:crypto'
import { closeSync, constants, fstatSync, mkdirSync, openSync, readSync, renameSync, unlinkSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

/** BOOL_FILE_MODE is the file's mode: it is the app's own setting, and a person
 * opening it to read one boolean is the whole of what may see it. */
export const BOOL_FILE_MODE = 0o600

/** BOOL_DIR_MODE is the directory's: the temporary file lands here before the
 * rename, so the directory is not more open than the document it holds. */
export const BOOL_DIR_MODE = 0o700

/** BOOL_FILE_MAX_BYTES is the longest file that can still be the setting:
 * `false` plus the newline a hand editor leaves is six bytes, and the whole
 * document is one boolean, so anything longer is not this console's own file and
 * is refused by its length rather than parsed. */
export const BOOL_FILE_MAX_BYTES = 8

/** BOOL_READ_FLAGS is how a switch is opened. A platform that does not define one
 * of the flags contributes nothing to the mask: `undefined` in a bitwise or is
 * zero, and there the regular-file rule below is the whole of the refusal. */
export const BOOL_READ_FLAGS = constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK

/** BoolStoreFs is `node:fs` as this module uses it, and nothing more. Every call
 * is here so the tests can hold the mode, the atomic rename and the "only on a
 * change" rule against a filesystem that records what it was asked to do. */
export interface BoolStoreFs {
  mkdir(path: string, options: { readonly recursive: true; readonly mode: number }): void
  writeFile(path: string, data: string, options: { readonly flag: 'wx'; readonly mode: number }): void
  rename(from: string, to: string): void
  /** open is `open`, and it is the only way this module reaches a switch for
   * reading: the flags decide what a name is allowed to be, and the descriptor it
   * answers is what everything below is asked about. */
  open(path: string, flags: number): number
  /** fstat answers about the descriptor the open returned, not about the name
   * again: there is one object behind both calls, so nothing can be put at that
   * name in between the rules and the read. `isFile` is the regular-file rule and
   * `size` is what bounds the read below. */
  fstat(handle: number): { readonly isFile: () => boolean; readonly size: number }
  readFile(handle: number, maxBytes: number): string
  close(handle: number): void
  unlink(path: string): void
}

/** nodeBoolStoreFs is the real filesystem. A missing file is reported the way
 * `node:fs` reports it -- a throw with `ENOENT` -- rather than collapsed here, so
 * the one caller that has to tell "there is no file" from "the file is there and
 * unusable" can. */
export const nodeBoolStoreFs: BoolStoreFs = Object.freeze<BoolStoreFs>({
  mkdir: (path, options) => {
    mkdirSync(path, options)
  },
  writeFile: (path, data, options) => {
    writeFileSync(path, data, options)
  },
  rename: (from, to) => {
    renameSync(from, to)
  },
  open: (path, flags) => openSync(path, flags),
  fstat: (handle) => fstatSync(handle),
  readFile: (handle, maxBytes) => {
    const buffer = Buffer.alloc(maxBytes)
    const read = readSync(handle, buffer, 0, maxBytes, 0)
    return buffer.toString('utf8', 0, read)
  },
  close: (handle) => {
    closeSync(handle)
  },
  unlink: (path) => {
    unlinkSync(path)
  },
})

/** BoolStoreSpec is what makes one of these files itself: its name under
 * `userData`, and the two sentences the store says about it. */
export interface BoolStoreSpec {
  /** fileName is the file in `userData`, and the only name the store writes. */
  readonly fileName: string
  /** whenUnread is the answer for a file that is missing, that cannot be read, or
   * that holds something other than a boolean -- the direction the setting fails
   * in. It is stated rather than defaulted because the two stores disagree about
   * it on purpose (see the header), and a default here would be one of the two
   * silently getting the other's answer. */
  readonly whenUnread: boolean
  /** unreadable is the one line a file that cannot be read or is not a boolean is
   * worth. It carries no part of the file: that text is untrusted, and the answer
   * does not depend on it. */
  readonly unreadable: string
  /** unwritten is the one line a write that did not land is worth, for the reason
   * the line above is one: the failure is the filesystem's, the store names no
   * path and repeats no error text, and what it means to the operator -- the
   * switch did not move -- is the same sentence either way. */
  readonly unwritten: string
}

/** reported is every path this process has already named its one line for: a
 * switch is read before every check, every prompt and every install, and a file
 * nobody is going to repair would be one line per read without it. */
const reported = new Set<string>()

function reportUnreadable(path: string, line: string): void {
  if (reported.has(path)) return
  reported.add(path)
  console.error(line)
}

/** readBoolSwitch answers what the file says **now**: a hand-edited file, or one
 * another run of the console wrote, is the answer the next ask gets. */
export function readBoolSwitch(directory: string, spec: BoolStoreSpec, fs: BoolStoreFs = nodeBoolStoreFs): boolean {
  const path = join(directory, spec.fileName)
  let handle: number
  try {
    handle = fs.open(path, BOOL_READ_FLAGS)
  } catch (error) {
    // A first run has no file, and that is not worth a line: it is the default
    // the console ships rather than something to report. Everything else -- a
    // permission, a name this process may not look at, a symlink the kernel
    // refused with ELOOP -- is one sentence, and the answer is the same one.
    if ((error as NodeJS.ErrnoException).code !== 'ENOENT') reportUnreadable(path, spec.unreadable)
    return spec.whenUnread
  }
  try {
    // The rules are asked of the descriptor rather than of the name: what is read
    // below is the object that was opened, so nothing can be put at that name
    // between the question and the read.
    let info: { readonly isFile: () => boolean; readonly size: number }
    try {
      info = fs.fstat(handle)
    } catch {
      reportUnreadable(path, spec.unreadable)
      return spec.whenUnread
    }
    if (!info.isFile() || info.size > BOOL_FILE_MAX_BYTES) {
      reportUnreadable(path, spec.unreadable)
      return spec.whenUnread
    }
    let text: string
    try {
      text = fs.readFile(handle, BOOL_FILE_MAX_BYTES)
    } catch {
      reportUnreadable(path, spec.unreadable)
      return spec.whenUnread
    }
    let parsed: unknown
    try {
      parsed = JSON.parse(text) as unknown
    } catch {
      reportUnreadable(path, spec.unreadable)
      return spec.whenUnread
    }
    if (typeof parsed !== 'boolean') {
      reportUnreadable(path, spec.unreadable)
      return spec.whenUnread
    }
    return parsed
  } finally {
    // Every path above leaves through here, so a refused file is a descriptor the
    // process does not keep: a switch is read as often as the thing it gates
    // runs, and a read that leaked one would run the process out of them.
    try {
      fs.close(handle)
    } catch {
      /* the answer is already decided; a descriptor that will not close is not a
         reason to change it */
    }
  }
}

/** writeBoolSwitch writes the switch, and does nothing at all when the file
 * already says this: a checkbox can be clicked twice, and a file rewritten for
 * nothing is a file whose inode and timestamp move for nothing.
 *
 * The write itself is **atomic** -- a fresh temporary file in the same directory
 * (mode 0600, `wx` so it cannot follow an existing file), then a rename over the
 * target -- so a crash between the two leaves the previous value intact rather
 * than a half-written one. */
export function writeBoolSwitch(
  directory: string,
  on: boolean,
  spec: BoolStoreSpec,
  fs: BoolStoreFs = nodeBoolStoreFs,
): void {
  if (readBoolSwitch(directory, spec, fs) === on) return
  const path = join(directory, spec.fileName)
  fs.mkdir(directory, { recursive: true, mode: BOOL_DIR_MODE })
  const temporary = `${path}.${randomBytes(6).toString('hex')}.tmp`
  try {
    fs.writeFile(temporary, on ? 'true' : 'false', { flag: 'wx', mode: BOOL_FILE_MODE })
    fs.rename(temporary, path)
  } catch (error) {
    // The temporary file is this function's to clean up: a rename that failed
    // leaves nothing behind that a later run could mistake for the setting.
    try {
      fs.unlink(temporary)
    } catch {
      /* the write itself failed; there is nothing to remove */
    }
    throw error
  }
}
