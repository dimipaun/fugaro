// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The update off switch: the one boolean `<userData>/updates.json` keeps, and
// the two application-menu items that move it (auto-update U4; owner ruling
// D768 (a) of 2026-10-30; design section 5.1 rule 10 -- which U5 amends to name
// this file beside `installations.json`).
//
// The shape, and why each part of it is this way:
//
//   - **exactly one boolean, and not an object.** There is one setting here, so
//     the file's whole content is `true` or `false`: a shape with room for a
//     second field would be a shape somebody has to version later, and the
//     console has no other update setting to put in it (the CLI's own
//     `update_check: false` is rule 7's, not the console's -- D769).
//   - **on when there is no file, and on when the file cannot be read or is
//     not a boolean.** The default the console ships, and the safe direction
//     for a file nobody moved: an operator who hand-edited it keeps their edit
//     and gets one constant line, and the console never deletes or repairs it.
//     **Opened with `O_NOFOLLOW` and `O_NONBLOCK`, and the descriptor
//     `fstat`ed before a byte is read**: the kernel refuses a symlink at the
//     name (ELOOP) rather than resolving it, a FIFO cannot make the open wait
//     for a writer that is never coming, and the regular-file and length rules
//     are asked of the very object the read returns -- one `open` rather than a
//     name looked at and then opened, which is two questions a name can answer
//     differently. A symlink, a directory or a FIFO is the same refusal as a
//     malformed one, so no name in this directory can be made to point the read
//     elsewhere or to block it. The line is one per file per process, because
//     the gate reads this switch before every check, every prompt and every
//     install.
//   - **atomic, and only on a change.** The document goes to a fresh temporary
//     file and a rename publishes it, exactly as `installations.json` does, and
//     a write whose value is already the file's is no write at all.
//   - **free of Electron.** The directory arrives as an argument, the
//     filesystem as `UpdatesFs` (`nodeUpdatesFs` is the only place this module
//     meets `node:fs`), and the controller as a getter -- read at the moment an
//     item is used rather than held, because the entry point assigns it after
//     these settings are built. That is what lets the unit test hold the store
//     and the toggle under plain Node.

import { randomBytes } from 'node:crypto'
import { closeSync, constants, fstatSync, mkdirSync, openSync, readSync, renameSync, unlinkSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

import type { UpdateController } from './updates'

/** UPDATES_FILE_NAME is the file in `userData`, and the only name this module
 * writes. */
export const UPDATES_FILE_NAME = 'updates.json'

/** UPDATES_FILE_MODE is the file's mode: it is the app's own setting, and a
 * person opening it to read one boolean is the whole of what may see it. */
export const UPDATES_FILE_MODE = 0o600

/** UPDATES_DIR_MODE is the directory's, for the same reason
 * `INSTALLATION_DIR_MODE` is: the temporary file lands here before the rename,
 * so the directory is not more open than the document it holds. */
export const UPDATES_DIR_MODE = 0o700

/** UPDATES_FILE_UNREADABLE is the one line a file that cannot be read or is not
 * a boolean is worth. It carries no part of the file: that text is untrusted,
 * and the answer -- automatic checks stay on -- does not depend on it. */
export const UPDATES_FILE_UNREADABLE = 'the console could not read its update setting, so automatic checks stay on'

/** UPDATES_SWITCH_UNWRITTEN is the one line a write that did not land is worth,
 * for the reason the line above is one: the failure is the filesystem's, this
 * module names no path and repeats no error text, and what it means to the
 * operator -- the switch did not move -- is the same sentence either way. */
export const UPDATES_SWITCH_UNWRITTEN = 'the console could not write its update setting, so the switch did not move'

/** UPDATES_FILE_MAX_BYTES is the longest file that can still be the setting:
 * `false` plus the newline a hand editor leaves is six bytes, and the whole
 * document is one boolean, so anything longer is not this console's own file
 * and is refused by its length rather than parsed. */
export const UPDATES_FILE_MAX_BYTES = 8

/** UPDATES_READ_FLAGS is how the switch is opened: read-only, `O_NOFOLLOW`, so
 * the kernel answers ELOOP for a name that is a symlink instead of resolving it
 * to whatever it points at, and `O_NONBLOCK`, so a FIFO is opened rather than
 * waited on -- a read-only open of a FIFO blocks until a writer appears, and
 * the writer of a name like this one is never coming. A platform that does not
 * define one of the flags contributes nothing to the mask: `undefined` in a
 * bitwise or is zero, and there the regular-file rule below is the whole of the
 * refusal. */
export const UPDATES_READ_FLAGS = constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK

/** UpdatesFs is `node:fs` as this module uses it, and nothing more. Every call
 * is here so the tests can hold the mode, the atomic rename and the "only on a
 * change" rule against a filesystem that records what it was asked to do. */
export interface UpdatesFs {
  mkdir(path: string, options: { readonly recursive: true; readonly mode: number }): void
  writeFile(path: string, data: string, options: { readonly flag: 'wx'; readonly mode: number }): void
  rename(from: string, to: string): void
  /** open is `open`, and it is the only way this module reaches the switch for
   * reading: the flags decide what a name is allowed to be, and the descriptor
   * it answers is what everything below is asked about. */
  open(path: string, flags: number): number
  /** fstat answers about the descriptor the open returned, not about the name
   * again: there is one object behind both calls, so nothing can be put at that
   * name in between the rules and the read. `isFile` is the regular-file rule
   * and `size` is what bounds the read below. */
  fstat(handle: number): { readonly isFile: () => boolean; readonly size: number }
  readFile(handle: number, maxBytes: number): string
  close(handle: number): void
  unlink(path: string): void
}

/** nodeUpdatesFs is the real filesystem. A missing file is reported the way
 * `node:fs` reports it -- a throw with `ENOENT` -- rather than collapsed here,
 * so the one caller that has to tell "there is no file" from "the file is
 * there and unusable" can. */
export const nodeUpdatesFs: UpdatesFs = Object.freeze<UpdatesFs>({
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

/** reported is every path this process has already named its one line for: the
 * gate reads the switch before every check, every prompt and every install, and
 * a file nobody is going to repair would be one line per read without it. */
const reported = new Set<string>()

function reportUnreadable(path: string): void {
  if (reported.has(path)) return
  reported.add(path)
  console.error(UPDATES_FILE_UNREADABLE)
}

/** readUpdateSwitch answers whether automatic checks are on, read from the file
 * **now**: a hand-edited file, or one another run of the console wrote, is the
 * answer the next ask gets. */
export function readUpdateSwitch(directory: string, fs: UpdatesFs = nodeUpdatesFs): boolean {
  const path = join(directory, UPDATES_FILE_NAME)
  let handle: number
  try {
    handle = fs.open(path, UPDATES_READ_FLAGS)
  } catch (error) {
    // A first run has no file, and that is not worth a line: it is the default
    // the console ships rather than something to report. Everything else -- a
    // permission, a name this process may not look at, a symlink the kernel
    // refused with ELOOP -- is one sentence, and the answer is the same one.
    if ((error as NodeJS.ErrnoException).code !== 'ENOENT') reportUnreadable(path)
    return true
  }
  try {
    // The rules are asked of the descriptor rather than of the name: what is
    // read below is the object that was opened, so nothing can be put at that
    // name between the question and the read. The name has to be a regular file
    // of a boolean's length before anything is read out of it -- a directory or
    // a FIFO is refused here, and the length rule is the read's cap, because the
    // document is one boolean and a longer file is not this console's own.
    let info: { readonly isFile: () => boolean; readonly size: number }
    try {
      info = fs.fstat(handle)
    } catch {
      reportUnreadable(path)
      return true
    }
    if (!info.isFile() || info.size > UPDATES_FILE_MAX_BYTES) {
      reportUnreadable(path)
      return true
    }
    let text: string
    try {
      text = fs.readFile(handle, UPDATES_FILE_MAX_BYTES)
    } catch {
      reportUnreadable(path)
      return true
    }
    let parsed: unknown
    try {
      parsed = JSON.parse(text) as unknown
    } catch {
      reportUnreadable(path)
      return true
    }
    if (typeof parsed !== 'boolean') {
      reportUnreadable(path)
      return true
    }
    return parsed
  } finally {
    // Every path above leaves through here, so a refused file is a descriptor
    // the process does not keep: the gate reads this switch before every check,
    // every prompt and every install, and a read that leaked one would run the
    // process out of them.
    try {
      fs.close(handle)
    } catch {
      /* the answer is already decided; a descriptor that will not close is not
         a reason to change it */
    }
  }
}

/** writeUpdateSwitch writes the switch, and does nothing at all when the file
 * already says this: the checkbox can be clicked twice, and a file rewritten
 * for nothing is a file whose inode and timestamp move for nothing.
 *
 * The write itself is **atomic** -- a fresh temporary file in the same
 * directory (mode 0600, `wx` so it cannot follow an existing file), then a
 * rename over the target -- so a crash between the two leaves the previous
 * value intact rather than a half-written one. */
export function writeUpdateSwitch(directory: string, on: boolean, fs: UpdatesFs = nodeUpdatesFs): void {
  if (readUpdateSwitch(directory, fs) === on) return
  const path = join(directory, UPDATES_FILE_NAME)
  fs.mkdir(directory, { recursive: true, mode: UPDATES_DIR_MODE })
  const temporary = `${path}.${randomBytes(6).toString('hex')}.tmp`
  try {
    fs.writeFile(temporary, on ? 'true' : 'false', { flag: 'wx', mode: UPDATES_FILE_MODE })
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

/** UpdateSettings is the switch as the menu and the policy see it: the value,
 * and the two things an item does with it. */
export interface UpdateSettings {
  /** auto is the store's value **now**, not the one the menu was built with:
   * the policy's gate asks it before every check, every prompt and every
   * install, and the file is what it reads. */
  readonly auto: boolean
  /** setAuto moves the switch, and then the running controller: off stops the
   * timer at once (a download already in flight is refused by the controller's
   * own gate, which reads this same switch), and on starts it again.
   *
   * It answers the value the switch holds **after** the call, read back from
   * the file: a write that did not land leaves the switch where it was, and the
   * menu draws this answer rather than the state its checkbox was clicked to. */
  setAuto(on: boolean): boolean
  /** checkNow is `Check for Updates Now`. A check while the switch is off is
   * refused by that same gate in the controller, so nothing here has to decide
   * it twice. */
  checkNow(): void
}

/** createUpdateSettings is the whole of U4's wiring: the store, and what the
 * two menu items do to the controller. The controller arrives as a getter
 * because the entry point assigns the module-scope one after these settings are
 * built, and because on anything but a packaged macOS app it stays null. */
export function createUpdateSettings(options: {
  readonly directory: string
  readonly controller: () => UpdateController | null
  readonly fs?: UpdatesFs
}): UpdateSettings {
  const fs = options.fs ?? nodeUpdatesFs
  return {
    get auto(): boolean {
      return readUpdateSwitch(options.directory, fs)
    },
    setAuto(on: boolean): boolean {
      // The store first: `start()` reads the gate, and the gate is this file.
      try {
        writeUpdateSwitch(options.directory, on, fs)
      } catch {
        // A write that did not land is one line and nothing else: the switch is
        // where it was, the controller is left exactly as it is -- a switch that
        // did not move must not stop or start anything -- and the answer is what
        // the file says, so the menu is drawn from the real value.
        console.error(UPDATES_SWITCH_UNWRITTEN)
        return readUpdateSwitch(options.directory, fs)
      }
      const controller = options.controller()
      if (controller === null) return on
      if (on) void controller.start()
      else controller.stop()
      return on
    },
    checkNow(): void {
      void options.controller()?.checkNow()
    },
  }
}
