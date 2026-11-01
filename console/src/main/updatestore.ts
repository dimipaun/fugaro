// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The update off switch: the one boolean `<userData>/updates.json` keeps, and
// the two application-menu items that move it (auto-update U4; owner ruling
// D768 (a) of 2026-10-30; design section 5.1 rule 10 -- which U5 amends to name
// this file beside `installations.json` and the crash-reporting switch).
//
// The file's shape and every rule about reading and writing it are
// `./boolstore`, which this module and `./crashstore` share: one boolean and not
// an object, `O_NOFOLLOW` and `O_NONBLOCK` with the descriptor `fstat`ed before a
// byte is read, an atomic rename, and a write only when the value changes. What
// is this file's own is the direction it fails in, stated below, and the two
// sentences it says.

import type { BoolStoreFs, BoolStoreSpec } from './boolstore'
import { BOOL_DIR_MODE, BOOL_FILE_MAX_BYTES, BOOL_FILE_MODE, BOOL_READ_FLAGS, nodeBoolStoreFs, readBoolSwitch, writeBoolSwitch } from './boolstore'

import type { UpdateController } from './updates'

/** UPDATES_FILE_NAME is the file in `userData`, and the only name this module
 * writes. */
export const UPDATES_FILE_NAME = 'updates.json'

/** UPDATES_FILE_MODE is the file's mode. */
export const UPDATES_FILE_MODE = BOOL_FILE_MODE

/** UPDATES_DIR_MODE is the directory's, for the reason `INSTALLATION_DIR_MODE`
 * is. */
export const UPDATES_DIR_MODE = BOOL_DIR_MODE

/** UPDATES_FILE_UNREADABLE is the one line a file that cannot be read or is not
 * a boolean is worth. It carries no part of the file: that text is untrusted, and
 * the answer -- automatic checks stay on -- does not depend on it. */
export const UPDATES_FILE_UNREADABLE = 'the console could not read its update setting, so automatic checks stay on'

/** UPDATES_SWITCH_UNWRITTEN is the one line a write that did not land is worth,
 * for the reason the line above is one: the failure is the filesystem's, this
 * module names no path and repeats no error text, and what it means to the
 * operator -- the switch did not move -- is the same sentence either way. */
export const UPDATES_SWITCH_UNWRITTEN = 'the console could not write its update setting, so the switch did not move'

/** UPDATES_FILE_MAX_BYTES is the longest file that can still be the setting. */
export const UPDATES_FILE_MAX_BYTES = BOOL_FILE_MAX_BYTES

/** UPDATES_READ_FLAGS is how the switch is opened. */
export const UPDATES_READ_FLAGS = BOOL_READ_FLAGS

/** UpdatesFs is `node:fs` as this module uses it, and nothing more. */
export type UpdatesFs = BoolStoreFs

/** nodeUpdatesFs is the real filesystem. */
export const nodeUpdatesFs: UpdatesFs = nodeBoolStoreFs

/** UPDATES_STORE is the spec the shared store reads this file with. **On when
 * there is no file, and on when the file cannot be read or is not a boolean**:
 * the default the console ships, and the safe direction for a file nobody moved:
 * an operator who hand-edited it keeps their edit and gets one constant line, and
 * the console never deletes or repairs it. */
const UPDATES_STORE: BoolStoreSpec = Object.freeze({
  fileName: UPDATES_FILE_NAME,
  whenUnread: true,
  unreadable: UPDATES_FILE_UNREADABLE,
  unwritten: UPDATES_SWITCH_UNWRITTEN,
})

/** readUpdateSwitch answers whether automatic checks are on, read from the file
 * **now**: a hand-edited file, or one another run of the console wrote, is the
 * answer the next ask gets. */
export function readUpdateSwitch(directory: string, fs: UpdatesFs = nodeUpdatesFs): boolean {
  return readBoolSwitch(directory, UPDATES_STORE, fs)
}

/** writeUpdateSwitch writes the switch, and does nothing at all when the file
 * already says this: the checkbox can be clicked twice, and a file rewritten for
 * nothing is a file whose inode and timestamp move for nothing. */
export function writeUpdateSwitch(directory: string, on: boolean, fs: UpdatesFs = nodeUpdatesFs): void {
  writeBoolSwitch(directory, on, UPDATES_STORE, fs)
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
