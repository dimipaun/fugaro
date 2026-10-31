// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The update switch: the one boolean `updates.json` holds, and the two menu
// items that move it (auto-update U4; owner ruling D768 (a), 2026-10-30).
//
// Every test here names the mutation that breaks it:
//
//   - a switch held in memory rather than in the file fails "keeps an off switch
//     across a restart": the value has to be read out of `updates.json` again,
//     with nothing carried between the two reads;
//   - a reader that answers a malformed file with **off** fails "reads a
//     malformed file as on" -- off is the answer that would silently stop the
//     checks, and a file somebody edited by hand is not consent to stop;
//   - a writer that writes on every call fails "writes only when the value
//     changes", which counts the calls a recording filesystem was asked to make;
//   - a writer that writes in place fails "leaves the previous value intact when
//     the rename never happens";
//   - a toggle that starts the controller before it writes the store fails
//     "starts the timer again when the switch comes back on", because the gate
//     `start()` reads is the file.

import { mkdirSync, mkdtempSync, readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { basename, dirname, join } from 'node:path'

import { describe, expect, it, vi } from 'vitest'

import { RELEASES_URL, UPDATE_INTERVAL_MS, createUpdateController } from '../../src/main/updates'
import type { UpdaterPort } from '../../src/main/updates'
import {
  UPDATES_DIR_MODE,
  UPDATES_FILE_MODE,
  UPDATES_FILE_NAME,
  UPDATES_FILE_UNREADABLE,
  createUpdateSettings,
  nodeUpdatesFs,
  readUpdateSwitch,
  writeUpdateSwitch,
} from '../../src/main/updatestore'
import type { UpdatesFs } from '../../src/main/updatestore'

function freshDirectory(): string {
  return mkdtempSync(join(tmpdir(), 'fugaro-updates-'))
}

/** only is the one entry a list is expected to hold, so "nothing was recorded"
 * fails here rather than making a later assertion vacuous. */
function only<T>(entries: readonly T[]): T {
  expect(entries).toHaveLength(1)
  const [entry] = entries
  if (entry === undefined) throw new Error('nothing was recorded')
  return entry
}

/** recordingFs is `node:fs` as this module uses it, with every call kept: the
 * atomic write is a *sequence* -- a temporary file, then a rename over the
 * target -- and a sequence is what a recording filesystem can hold. It performs
 * each call too, so a test can read the file back. */
function recordingFs(): UpdatesFs & {
  readonly calls: string[]
  readonly written: string[]
} {
  const calls: string[] = []
  const written: string[] = []
  const fs: UpdatesFs = {
    mkdir: (path, options) => {
      calls.push(`mkdir:${path}:${options.mode.toString(8)}`)
      nodeUpdatesFs.mkdir(path, options)
    },
    writeFile: (path, data, options) => {
      calls.push(`writeFile:${path}:${options.mode.toString(8)}:${options.flag}`)
      written.push(data)
      nodeUpdatesFs.writeFile(path, data, options)
    },
    rename: (from, to) => {
      calls.push(`rename:${from}:${to}`)
      nodeUpdatesFs.rename(from, to)
    },
    readFile: (path) => {
      calls.push(`readFile:${path}`)
      return nodeUpdatesFs.readFile(path)
    },
    unlink: (path) => {
      calls.push(`unlink:${path}`)
      nodeUpdatesFs.unlink(path)
    },
  }
  return { calls, written, ...fs }
}

describe('the update switch’s file', () => {
  it('is on when there is no file, and none is created', () => {
    // The default D768 (a) ships: on. A first run has no file, and a first run
    // that wrote one would be a console that wrote a setting nobody moved.
    const directory = freshDirectory()
    expect(readUpdateSwitch(directory)).toBe(true)
    expect(readdirSync(directory)).toEqual([])
  })

  it('keeps an off switch across a restart, as the one boolean the file holds', () => {
    // The plan's red step: a switch held in memory passes everything above and
    // fails here, because the second read is a fresh one with nothing carried
    // over -- the only thing between them is `updates.json`.
    const directory = freshDirectory()
    const path = join(directory, UPDATES_FILE_NAME)

    writeUpdateSwitch(directory, false)
    expect(readFileSync(path, 'utf8')).toBe('false')
    expect(readUpdateSwitch(directory)).toBe(false)

    writeUpdateSwitch(directory, true)
    expect(readFileSync(path, 'utf8')).toBe('true')
    expect(readUpdateSwitch(directory)).toBe(true)
  })

  it('holds one JSON boolean and nothing else, 0600, by rename', () => {
    const directory = freshDirectory()
    const path = join(directory, UPDATES_FILE_NAME)
    const fs = recordingFs()

    writeUpdateSwitch(directory, false, fs)

    // **The whole content is one boolean**, not an object with a flag in it:
    // there is one setting here, and a shape with room for more is a shape whose
    // second field would be a schema to version.
    const text = readFileSync(path, 'utf8')
    expect(text).toBe('false')
    expect(typeof (JSON.parse(text) as unknown)).toBe('boolean')
    expect(statSync(path).mode & 0o777).toBe(UPDATES_FILE_MODE)

    // The write is the atomic one: a fresh temporary file, then a rename onto
    // the target, with nothing left behind.
    const directories = fs.calls.filter((call) => call.startsWith('mkdir:'))
    expect(directories).toEqual([`mkdir:${directory}:${UPDATES_DIR_MODE.toString(8)}`])
    const write = only(fs.calls.filter((call) => call.startsWith('writeFile:')))
    const temporary = write.slice('writeFile:'.length, -`:${UPDATES_FILE_MODE.toString(8)}:wx`.length)
    expect(write).toMatch(new RegExp(`:${UPDATES_FILE_MODE.toString(8)}:wx$`))
    expect(dirname(temporary)).toBe(directory)
    expect(basename(temporary)).toMatch(/^updates\.json\.[0-9a-f]{12}\.tmp$/)
    expect(only(fs.calls.filter((call) => call.startsWith('rename:')))).toBe(`rename:${temporary}:${path}`)
    expect(readdirSync(directory)).toEqual([UPDATES_FILE_NAME])
  })

  it('reads a malformed file as on, says one line, and leaves the file alone', () => {
    // On is the safe direction **and** the honest one: the file is the app's
    // own, so a malformed one was not written by a person moving this switch,
    // and the default is on. Nothing is repaired and nothing is deleted -- an
    // operator who hand-edited it keeps their edit and gets the sentence.
    for (const text of ['{"auto":false}', 'null', '0', 'true false', '']) {
      const directory = freshDirectory()
      const path = join(directory, UPDATES_FILE_NAME)
      writeFileSync(path, text)
      const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
      try {
        expect(readUpdateSwitch(directory), text).toBe(true)
        expect(readFileSync(path, 'utf8')).toBe(text)
        expect(logged).toHaveBeenCalledTimes(1)
        const line = String(logged.mock.calls[0]?.[0])
        expect(line).toBe(UPDATES_FILE_UNREADABLE)
        // The log names no part of the file: it is untrusted text, and it is not
        // this line's to repeat.
        if (text !== '') expect(line).not.toContain(text)
      } finally {
        logged.mockRestore()
      }
    }
  })

  it('reads a file it cannot open as on, and leaves it where it is', () => {
    // A directory where the file should be is the refusal a permission would
    // give without a mode this test cannot set as root: what matters is that an
    // unreadable file is the default rather than a throw, and that nothing
    // removes or replaces it.
    const directory = freshDirectory()
    const path = join(directory, UPDATES_FILE_NAME)
    mkdirSync(path)
    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    try {
      expect(readUpdateSwitch(directory)).toBe(true)
      expect(logged).toHaveBeenCalledTimes(1)
      expect(logged.mock.calls[0]?.[0]).toBe(UPDATES_FILE_UNREADABLE)
    } finally {
      logged.mockRestore()
    }
    expect(statSync(path).isDirectory()).toBe(true)
  })

  it('writes only when the value changes', () => {
    // Off twice is one write. The second call is the one a menu item that is
    // clicked twice makes, and a file rewritten then is a file whose mtime --
    // and whose inode, because the write is a rename -- moves for nothing.
    const directory = freshDirectory()
    const path = join(directory, UPDATES_FILE_NAME)
    const fs = recordingFs()

    writeUpdateSwitch(directory, false, fs)
    expect(fs.written).toEqual(['false'])
    const inode = statSync(path).ino

    writeUpdateSwitch(directory, false, fs)
    writeUpdateSwitch(directory, false, fs)
    expect(fs.written).toEqual(['false'])
    expect(fs.calls.filter((call) => call.startsWith('rename:'))).toHaveLength(1)
    expect(statSync(path).ino).toBe(inode)

    // ...and a change is written, so the count above is not "it never writes".
    writeUpdateSwitch(directory, true, fs)
    expect(fs.written).toEqual(['false', 'true'])
    expect(readUpdateSwitch(directory)).toBe(true)
  })

  it('leaves the previous value intact when the rename never happens', () => {
    const directory = freshDirectory()
    const path = join(directory, UPDATES_FILE_NAME)
    writeUpdateSwitch(directory, false)
    const fs: UpdatesFs = {
      mkdir: (target, options) => nodeUpdatesFs.mkdir(target, options),
      writeFile: (target, data, options) => nodeUpdatesFs.writeFile(target, data, options),
      rename: () => {
        throw new Error('ENOSPC: no space left on device')
      },
      readFile: (target) => nodeUpdatesFs.readFile(target),
      unlink: (target) => nodeUpdatesFs.unlink(target),
    }

    expect(() => writeUpdateSwitch(directory, true, fs)).toThrow(/ENOSPC/)

    expect(readFileSync(path, 'utf8')).toBe('false')
    // The temporary file is this function's to clean up: a failed rename leaves
    // nothing a later run could mistake for the setting.
    expect(readdirSync(directory)).toEqual([UPDATES_FILE_NAME])
  })
})

/** TimersFake is the controller's clock: nothing here reaches the wall clock,
 * and `armed` is how many handles are live, so an interval `stop()` forgot to
 * clear stays visible as a second one. */
interface TimersFake {
  readonly delays: readonly number[]
  readonly armed: number
  readonly setInterval: (handler: () => void, ms: number) => ReturnType<typeof setInterval>
  readonly clearInterval: (handle: ReturnType<typeof setInterval>) => void
}

function timersFake(): TimersFake {
  const delays: number[] = []
  const handlers = new Map<number, () => void>()
  let next = 0
  return {
    get delays() {
      return delays
    },
    get armed() {
      return handlers.size
    },
    setInterval: (handler, ms) => {
      delays.push(ms)
      next += 1
      handlers.set(next, handler)
      return next as unknown as ReturnType<typeof setInterval>
    },
    clearInterval: (handle) => {
      handlers.delete(handle as unknown as number)
    },
  }
}

/** updaterFake is electron-updater's client as the policy uses it. The listing
 * this harness answers is empty, so no feed is ever pointed and no check is ever
 * asked for: these cases are about the timer. */
function updaterFake(): UpdaterPort {
  return {
    setFeedURL: () => undefined,
    checkForUpdates: async () => undefined,
    quitAndInstall: () => undefined,
    autoDownload: false,
    autoInstallOnAppQuit: false,
    allowDowngrade: false,
    allowPrerelease: false,
    requestHeaders: {},
    on: () => undefined,
  }
}

/** controllerOver is a real controller whose switch is the real store: the
 * `switchOn` below is the dep the entry point wires, read fresh at every ask. */
function controllerOver(directory: string) {
  const timers = timersFake()
  const fetches: string[] = []
  const controller = createUpdateController({
    platform: 'darwin',
    isPackaged: true,
    env: {},
    currentVersion: '0.116.0',
    switchOn: () => readUpdateSwitch(directory),
    timer: { setInterval: timers.setInterval, clearInterval: timers.clearInterval },
    fetchReleases: async (url) => {
      fetches.push(url)
      return '[]'
    },
    isLaunchFlowIdle: () => true,
    onReadyToRestart: () => undefined,
    updater: updaterFake(),
  })
  return { controller, timers, fetches }
}

describe('the switch the menu drives', () => {
  it('stops the timer the moment the switch goes off, and arms it again when it comes back', async () => {
    const directory = freshDirectory()
    const { controller, timers } = controllerOver(directory)
    const settings = createUpdateSettings({ directory, controller: () => controller })

    await controller.start()
    expect(timers.delays).toEqual([UPDATE_INTERVAL_MS])
    expect(settings.auto).toBe(true)

    settings.setAuto(false)
    expect(settings.auto).toBe(false)
    expect(timers.armed).toBe(0)
    expect(readUpdateSwitch(directory)).toBe(false)

    // The store is written **before** the controller is started again: `start()`
    // reads the gate, and the gate is this file, so a toggle that started first
    // would leave the timer unarmed.
    settings.setAuto(true)
    expect(timers.armed).toBe(1)
    expect(timers.delays).toEqual([UPDATE_INTERVAL_MS, UPDATE_INTERVAL_MS])
    expect(readUpdateSwitch(directory)).toBe(true)
  })

  it('does nothing to a console that has no controller, and still keeps the switch', () => {
    // Everything that is not a packaged macOS app: the menu items are not
    // offered there, and a toggle that somehow arrived writes the file and
    // reaches nothing.
    const directory = freshDirectory()
    const settings = createUpdateSettings({ directory, controller: () => null })
    expect(() => {
      settings.setAuto(false)
      settings.checkNow()
    }).not.toThrow()
    expect(readUpdateSwitch(directory)).toBe(false)
  })

  it('Check for Updates Now reaches the controller while the switch is on, and nothing while it is off', async () => {
    const on = freshDirectory()
    const running = controllerOver(on)
    const settingsOn = createUpdateSettings({ directory: on, controller: () => running.controller })
    settingsOn.checkNow()
    expect(running.fetches).toEqual([RELEASES_URL])

    const off = freshDirectory()
    writeUpdateSwitch(off, false)
    const stopped = controllerOver(off)
    const settingsOff = createUpdateSettings({ directory: off, controller: () => stopped.controller })
    await stopped.controller.start()
    // The switch was off before the app started, so nothing was armed and
    // nothing was fetched.
    expect(stopped.timers.armed).toBe(0)
    settingsOff.checkNow()
    expect(stopped.fetches).toEqual([])
  })
})
