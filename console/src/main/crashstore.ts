// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The crash-reporting switch: the one boolean `<userData>/crash.json` keeps, the
// application-menu checkbox that moves it, and the value the entry point hands
// the reporter's gate at startup (the descriptor's run 2; the owner's rulings
// D733, D734 and D768 of 2026-10-30, and the ruling of 2026-10-31 that the
// console's own settings are per-user and local to it; design section 5.1 rule
// 10 names this file beside `installations.json` and `updates.json`).
//
// The file's shape and every rule about reading and writing it are
// `./boolstore`, which this module and `./updatestore` share. What is this
// file's own is the direction it fails in, and it is the one thing here that a
// reader must not get wrong:
//
//   - **off when there is no file, and off when the file cannot be read or is
//     not a boolean.** Off is the whole of opt-in (D733): the console sends a
//     report only on a yes the operator gave, so a missing file, a file this
//     process may not read, and a file somebody left a string or a `1` in all
//     answer the same way, and none of them is consent. The two stores in this
//     console fail in opposite directions on purpose -- `updates.json` is on when
//     it is unreadable, because a check that stops is a console that stops
//     updating, and this file is off, because a report is not a check.
//   - **no repair, and one line.** A malformed file is not rewritten, not
//     deleted and not corrected: an operator who hand-edited it keeps their edit
//     and gets one constant sentence, once per process, which names no part of
//     the file.
//   - **the switch is the only gate.** There is no DSN to be absent and no build
//     to be without one -- since the owner's ruling of 2026-10-31 the DSN is one
//     embedded public constant (`./crash-dsn`), so this file is the whole of what
//     decides whether a report is sent. Nothing here reads the environment, and
//     nothing here reads the CLI's config.
//   - **it applies at the next launch.** The reporter is started once, in the
//     entry point's own startup, and the switch is read there: turning it off
//     does not interrupt a client that is already running, and turning it on
//     does not start one. That is what the menu item says out loud, and it is the
//     honest description of what this console does rather than a limitation
//     somebody has to discover.

import type { BoolStoreFs, BoolStoreSpec } from './boolstore'
import { BOOL_DIR_MODE, BOOL_FILE_MAX_BYTES, BOOL_FILE_MODE, nodeBoolStoreFs, readBoolSwitch, writeBoolSwitch } from './boolstore'

/** CRASH_FILE_NAME is the file in `userData`, and the only name this module
 * writes. */
export const CRASH_FILE_NAME = 'crash.json'

/** CRASH_FILE_MODE is the file's mode, and CRASH_DIR_MODE the directory's: the
 * setting is the operator's own, and nobody else's business. */
export const CRASH_FILE_MODE = BOOL_FILE_MODE
export const CRASH_DIR_MODE = BOOL_DIR_MODE

/** CRASH_FILE_MAX_BYTES is the longest file that can still be the setting: the
 * document is one boolean, and a longer file is not this console's own. */
export const CRASH_FILE_MAX_BYTES = BOOL_FILE_MAX_BYTES

/** CRASH_FILE_UNREADABLE is the one line a file that cannot be read or is not a
 * boolean is worth. It says what the operator can rely on -- nothing is sent --
 * and it carries no part of the file, which is untrusted text. */
export const CRASH_FILE_UNREADABLE = 'the console could not read its crash-reporting setting, so nothing is sent'

/** CRASH_SWITCH_UNWRITTEN is the one line a write that did not land is worth:
 * the failure is the filesystem's, this module names no path and repeats no
 * error text, and what it means -- the switch did not move -- is one sentence. */
export const CRASH_SWITCH_UNWRITTEN = 'the console could not write its crash-reporting setting, so the switch did not move'

/** CrashFs is `node:fs` as this module uses it, and nothing more. */
export type CrashFs = BoolStoreFs

/** nodeCrashFs is the real filesystem. */
export const nodeCrashFs: CrashFs = nodeBoolStoreFs

/** CRASH_STORE is the spec the shared store reads this file with. */
const CRASH_STORE: BoolStoreSpec = Object.freeze({
  fileName: CRASH_FILE_NAME,
  whenUnread: false,
  unreadable: CRASH_FILE_UNREADABLE,
  unwritten: CRASH_SWITCH_UNWRITTEN,
})

/** readCrashSwitch answers whether crash reporting is on, read from the file
 * **now**. Only the store's own `true` is a yes: `"true"`, `1` and an object are
 * things a file this console did not write can hold, and the gate
 * (`crashReportingDecision` in `./crash`) refuses them again anyway. */
export function readCrashSwitch(directory: string, fs: CrashFs = nodeCrashFs): boolean {
  return readBoolSwitch(directory, CRASH_STORE, fs)
}

/** writeCrashSwitch writes the switch, and writes nothing when the file already
 * says this. */
export function writeCrashSwitch(directory: string, on: boolean, fs: CrashFs = nodeCrashFs): void {
  writeBoolSwitch(directory, on, CRASH_STORE, fs)
}

/** CrashSettings is the switch as the menu sees it: the value a checkbox is drawn
 * with, and the one thing the item does with it. It is deliberately not the
 * store: the menu is handed plain values and callbacks, so that the template
 * stays a value a unit test can read and `./picker` keeps knowing nothing about
 * where the switch is kept. */
export interface CrashSettings {
  /** send is the store's value **now**, not the one the menu was built with. */
  readonly send: boolean
  /** setSend moves the switch and answers the value it holds **after** the call,
   * read back from the file: Electron has already drawn the state this click
   * moved its checkbox to, and a write that did not land is only visible as this
   * answer disagreeing with it. A write that did not land is one line and never a
   * throw -- an error out of a menu click is Electron's uncaught-error dialog over
   * a running app. */
  setSend(on: boolean): boolean
}

/** createCrashSettings is the store as the menu sets it. Nothing is started or
 * stopped here: the switch is read at the next launch (see the header), which is
 * what keeps this module free of the reporter and of Electron. */
export function createCrashSettings(options: {
  readonly directory: string
  readonly fs?: CrashFs
}): CrashSettings {
  const fs = options.fs ?? nodeCrashFs
  return {
    get send(): boolean {
      return readCrashSwitch(options.directory, fs)
    },
    setSend(on: boolean): boolean {
      try {
        writeCrashSwitch(options.directory, on, fs)
      } catch {
        console.error(CRASH_SWITCH_UNWRITTEN)
      }
      // **The answer is always the file's, on both paths.** A write that threw
      // is one line and the file is where it was; a write that returned is a
      // rename this process was told about and cannot confirm, since a directory
      // that went away between the write and the rename is a failure the
      // filesystem reports as success, and the value asked for is then not the
      // value in force. What the menu redraws from is this, so it is the file
      // that is asked and never the argument.
      return readCrashSwitch(options.directory, fs)
    },
  }
}
