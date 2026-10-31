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
//     `start()` reads is the file;
//   - a reader that follows the name instead of `lstat`ing it fails "reads a
//     symlink, a directory and an oversized file as on, and leaves each where it
//     is" (U4b);
//   - a reader that opens the name without `O_NOFOLLOW` fails "refuses a symlink
//     to a regular file by the link rule alone" (U4c): the two hold the same
//     bytes, and only the kind of name they sit at tells them apart;
//   - a reader that reads a name it has not `fstat`ed fails "reads a short
//     symlink to a FIFO as on, and does not block on it" (U4c): the FIFO is
//     shorter than the cap and is not a file, and a read of it never returns;
//   - the same reader at a name that is a FIFO itself rather than a link to one
//     fails "reads a FIFO at the name itself as on, and asks it for no byte"
//     (U4d): the link rule is not there to stop it first, so the regular-file
//     rule is the whole of the refusal and has to be asked before a byte is;
//   - a reader that lets an error out of the open instead of answering with the
//     default fails "reads a name the open itself is refused as on" (U4c) --
//     one line and the file untouched, whatever the open raised;
//   - a reader that says its line on every read fails "says its one line once,
//     however many times the gate reads the file" (U4b).

import { execFileSync } from 'node:child_process'
import {
  constants,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readlinkSync,
  readdirSync,
  rmSync,
  statSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs'
import { tmpdir } from 'node:os'
import { basename, dirname, join } from 'node:path'

import { afterEach, describe, expect, it, vi } from 'vitest'

import { RELEASES_URL, UPDATE_INTERVAL_MS, createUpdateController } from '../../src/main/updates'
import type { UpdaterPort } from '../../src/main/updates'
import {
  UPDATES_DIR_MODE,
  UPDATES_FILE_MAX_BYTES,
  UPDATES_FILE_MODE,
  UPDATES_FILE_NAME,
  UPDATES_FILE_UNREADABLE,
  createUpdateSettings,
  nodeUpdatesFs,
  readUpdateSwitch,
  writeUpdateSwitch,
} from '../../src/main/updatestore'
import type { UpdatesFs } from '../../src/main/updatestore'

/** made is every temporary directory this file has handed out, so one rule at
 * the end of each case can take them all away: these hold FIFOs, links and
 * files that nothing would want to find in the machine's temp directory once
 * the run is over. */
const made: string[] = []

function freshDirectory(): string {
  const directory = mkdtempSync(join(tmpdir(), 'fugaro-updates-'))
  made.push(directory)
  return directory
}

afterEach(() => {
  for (const directory of made.splice(0)) rmSync(directory, { recursive: true, force: true })
})

/** mkfifoAbsent is whether this machine can make the FIFO the cases below need:
 * the tool is POSIX's, and a host without it skips those cases with the reason
 * rather than failing on a name it cannot create. The probe's own directory,
 * and the FIFO inside it when there is one, go away whatever it answered. */
const mkfifoAbsent = ((): boolean => {
  const probe = mkdtempSync(join(tmpdir(), 'fugaro-updates-'))
  try {
    execFileSync('mkfifo', [join(probe, 'probe')])
    return false
  } catch {
    return true
  } finally {
    rmSync(probe, { recursive: true, force: true })
  }
})()

/** MKFIFO_SKIP is the reason those cases state when they are skipped. */
const MKFIFO_SKIP = 'mkfifo is not on this machine'

/** noLinkRule is whether this platform defines the two flags the read is opened
 * with: `O_NOFOLLOW` and `O_NONBLOCK` are POSIX's, and a `node:fs` that has
 * neither leaves the module opening the name the plain way -- a link is resolved
 * rather than refused, and there is no other rule that refuses one, because the
 * descriptor's regular-file rule is asked about the target a link resolved to.
 * The case that turns on those flags is skipped there with the reason rather
 * than asserting a refusal this platform cannot make. */
const noLinkRule = ([constants.O_NOFOLLOW, constants.O_NONBLOCK] as readonly (number | undefined)[]).some(
  (flag) => typeof flag !== 'number',
)

/** LINK_RULE_SKIP is the reason that case states when it is skipped. */
const LINK_RULE_SKIP = 'this platform does not define O_NOFOLLOW and O_NONBLOCK'

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
    open: (path, flags) => {
      calls.push(`open:${path}:${flags}`)
      return nodeUpdatesFs.open(path, flags)
    },
    fstat: (handle) => {
      calls.push(`fstat:${handle}`)
      return nodeUpdatesFs.fstat(handle)
    },
    readFile: (handle, maxBytes) => {
      calls.push(`readFile:${handle}:${maxBytes}`)
      return nodeUpdatesFs.readFile(handle, maxBytes)
    },
    close: (handle) => {
      calls.push(`close:${handle}`)
      nodeUpdatesFs.close(handle)
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

  it('reads a directory where the file should be as on, and leaves it where it is', () => {
    // The name opens -- a directory opens read-only -- and the descriptor's
    // regular-file rule is what refuses it, exactly as it refuses a FIFO. What
    // matters is that a name that is not this console's file is the default
    // rather than a throw, and that nothing removes or replaces it. (A
    // permission, which the OS refuses at the open instead, is the case below
    // this one.)
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

  it('reads a name the open itself is refused as on, and writes nothing (U4c)', () => {
    // A permission is refused by the OS at the open, which is now the first and
    // only call this module makes for the switch -- and the descriptor it never
    // got is the whole of what is missing: nothing is read, nothing is closed
    // and nothing is repaired. The refusal is a fake's rather than a mode bit,
    // because a suite that runs as root reads a file the file's own mode denies
    // everybody else.
    const directory = freshDirectory()
    const path = join(directory, UPDATES_FILE_NAME)
    writeFileSync(path, 'false')
    const fs: UpdatesFs = {
      ...nodeUpdatesFs,
      open: () => {
        throw Object.assign(new Error('EACCES: permission denied'), { code: 'EACCES' })
      },
    }

    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    try {
      expect(readUpdateSwitch(directory, fs)).toBe(true)
      expect(logged).toHaveBeenCalledTimes(1)
      expect(logged.mock.calls[0]?.[0]).toBe(UPDATES_FILE_UNREADABLE)
    } finally {
      logged.mockRestore()
    }

    // ...and the file the real filesystem holds is exactly where it was, still
    // saying `false`: the refusal above is the fake's, not the file's.
    expect(readFileSync(path, 'utf8')).toBe('false')
    expect(readUpdateSwitch(directory)).toBe(false)
  })

  it('reads a symlink as on, and follows it nowhere (U4b, U4c)', () => {
    // The gate is read on a path, and the one answer it must never give is one
    // read out of a name somebody else chose: the open does not follow the link
    // (`O_NOFOLLOW`), so a symlink is refused rather than parsed -- the target
    // is never read, and neither the link nor the file behind it is removed or
    // replaced. This target's path is longer than the cap below, so it is the
    // *length* that would stop a reader that never asked what the name is; the
    // case right after this one is the short target that only that question
    // stops.
    const directory = freshDirectory()
    const path = join(directory, UPDATES_FILE_NAME)
    const target = join(freshDirectory(), 'elsewhere.json')
    writeFileSync(target, 'false')
    symlinkSync(target, path)

    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    try {
      expect(readUpdateSwitch(directory)).toBe(true)
      expect(logged).toHaveBeenCalledTimes(1)
      expect(logged.mock.calls[0]?.[0]).toBe(UPDATES_FILE_UNREADABLE)
    } finally {
      logged.mockRestore()
    }
    expect(lstatSync(path).isSymbolicLink()).toBe(true)
    expect(readFileSync(target, 'utf8')).toBe('false')
  })

  it.skipIf(noLinkRule)(`refuses a symlink to a regular file by the link rule alone, and reads a plain one (U4c)${noLinkRule ? ` (${LINK_RULE_SKIP})` : ''}`, () => {
    // A link whose target is a regular file holding `false`: the answer is **on**
    // -- a name that is a link is not this switch whatever it points at, and
    // `false` is not a value this console may be made to take from somewhere
    // else. The plain file beside it is the same bytes at a name that is a file,
    // and it is read as off, so the refusal above is about the link rather than
    // about the content. The flags the open is asked for are recorded here too:
    // no-follow is what refuses the link, and non-blocking is what an open of a
    // FIFO needs to not wait for a writer (the case below).
    const directory = freshDirectory()
    const path = join(directory, UPDATES_FILE_NAME)
    const target = join(directory, 'elsewhere.json')
    writeFileSync(target, 'false')
    symlinkSync(target, path)

    const fs = recordingFs()
    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    try {
      expect(readUpdateSwitch(directory, fs)).toBe(true)
      expect(logged).toHaveBeenCalledTimes(1)
      expect(logged.mock.calls[0]?.[0]).toBe(UPDATES_FILE_UNREADABLE)
    } finally {
      logged.mockRestore()
    }
    const opened = only(fs.calls.filter((call) => call.startsWith('open:')))
    const flags = Number(opened.slice(opened.lastIndexOf(':') + 1))
    expect(flags & constants.O_NOFOLLOW).toBe(constants.O_NOFOLLOW)
    expect(flags & constants.O_NONBLOCK).toBe(constants.O_NONBLOCK)
    expect(lstatSync(path).isSymbolicLink()).toBe(true)
    expect(readFileSync(target, 'utf8')).toBe('false')

    const plain = freshDirectory()
    writeFileSync(join(plain, UPDATES_FILE_NAME), 'false')
    expect(readUpdateSwitch(plain)).toBe(false)
  })

  it.skipIf(mkfifoAbsent)(`reads a short symlink to a FIFO as on, and does not block on it (U4c)${mkfifoAbsent ? ` (${MKFIFO_SKIP})` : ''}`, () => {
    // The link's own target is three characters, well inside the cap, so a
    // reader that only measured the name would read on -- and the target is a
    // FIFO, which a read-only `open` waits on until a writer appears. This is
    // the case the target length cannot stand in for: the regular-file rule is
    // the whole of the refusal here, and a read that did not know about FIFOs
    // would hang the console's own menu thread instead of answering on.
    const directory = freshDirectory()
    const path = join(directory, UPDATES_FILE_NAME)
    const fifo = join(directory, 'f')
    execFileSync('mkfifo', [fifo])
    symlinkSync('f', path)

    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    try {
      expect(readUpdateSwitch(directory)).toBe(true)
      expect(logged).toHaveBeenCalledTimes(1)
      expect(logged.mock.calls[0]?.[0]).toBe(UPDATES_FILE_UNREADABLE)
    } finally {
      logged.mockRestore()
    }
    // The name and the FIFO are both left exactly where they were: this module
    // reads a setting, and a file it cannot use is not its to repair or delete.
    expect(lstatSync(path).isSymbolicLink()).toBe(true)
    expect(readlinkSync(path)).toBe('f')
    expect(statSync(fifo).isFIFO()).toBe(true)
    expect(readdirSync(directory).toSorted()).toEqual(['f', UPDATES_FILE_NAME])
  }, 10_000)

  it.skipIf(mkfifoAbsent)(`reads a FIFO at the name itself as on, and asks it for no byte (U4d)${mkfifoAbsent ? ` (${MKFIFO_SKIP})` : ''}`, () => {
    // The case above never reaches the regular-file rule: its name is a link, and
    // the kernel refuses the open before anything is asked about what the name
    // is. Here the FIFO **is** the name, so the open succeeds -- `O_NONBLOCK` is
    // what makes a read-only open of a FIFO return instead of waiting for a
    // writer -- and the descriptor's rule is the whole of the refusal. It has to
    // be asked *before* a byte is, because a read of this name is not the
    // setting however it answers: this case is what catches a reader that kept
    // the length rule and dropped the kind one.
    const directory = freshDirectory()
    const path = join(directory, UPDATES_FILE_NAME)
    execFileSync('mkfifo', [path])

    const fs = recordingFs()
    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    try {
      expect(readUpdateSwitch(directory, fs)).toBe(true)
      expect(logged).toHaveBeenCalledTimes(1)
      expect(logged.mock.calls[0]?.[0]).toBe(UPDATES_FILE_UNREADABLE)
    } finally {
      logged.mockRestore()
    }
    // What the calls say: the name was opened and the descriptor asked what it
    // is, and nothing was ever read out of it. A reader without the regular-file
    // rule records a `readFile` here.
    expect(fs.calls.filter((call) => call.startsWith('open:'))).toHaveLength(1)
    expect(fs.calls.filter((call) => call.startsWith('fstat:'))).toHaveLength(1)
    expect(fs.calls.filter((call) => call.startsWith('readFile:'))).toEqual([])
    // The FIFO is left exactly where it was: this module reads a setting, and a
    // name it cannot use is not its to repair or delete.
    expect(statSync(path).isFIFO()).toBe(true)
    expect(readdirSync(directory)).toEqual([UPDATES_FILE_NAME])
  }, 10_000)

  it('reads a file longer than one boolean as on, and leaves it untouched (U4b)', () => {
    // The refusal has to come from the length rather than from a truncated
    // parse: this file is a valid `false` with padding after it, so a reader
    // that took the first few bytes and parsed those would answer **off** --
    // and stop the checks on a file nobody moved.
    const directory = freshDirectory()
    const path = join(directory, UPDATES_FILE_NAME)
    const oversized = `false${' '.repeat(UPDATES_FILE_MAX_BYTES)}`
    writeFileSync(path, oversized)

    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    try {
      expect(readUpdateSwitch(directory)).toBe(true)
      expect(logged).toHaveBeenCalledTimes(1)
      expect(logged.mock.calls[0]?.[0]).toBe(UPDATES_FILE_UNREADABLE)
    } finally {
      logged.mockRestore()
    }
    expect(readFileSync(path, 'utf8')).toBe(oversized)

    // ...and the cap still admits the file a hand editor leaves: `false` and its
    // newline is inside it, and is read as off.
    const newline = freshDirectory()
    writeFileSync(join(newline, UPDATES_FILE_NAME), 'false\n')
    expect(readUpdateSwitch(newline)).toBe(false)
  })

  it('says its one line once, however many times the gate reads the file (U4b)', () => {
    // The gate reads this switch before every check, every prompt and every
    // install, so a bad file nobody is going to repair would otherwise be one
    // line per read. The header comment promises one, and one is what an
    // operator watching the log gets.
    const directory = freshDirectory()
    writeFileSync(join(directory, UPDATES_FILE_NAME), '{"auto":false}')
    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    try {
      for (let read = 0; read < 5; read += 1) expect(readUpdateSwitch(directory)).toBe(true)
      expect(logged).toHaveBeenCalledTimes(1)
      expect(logged.mock.calls[0]?.[0]).toBe(UPDATES_FILE_UNREADABLE)
    } finally {
      logged.mockRestore()
    }
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
      open: (target, flags) => nodeUpdatesFs.open(target, flags),
      fstat: (handle) => nodeUpdatesFs.fstat(handle),
      readFile: (handle, maxBytes) => nodeUpdatesFs.readFile(handle, maxBytes),
      close: (handle) => nodeUpdatesFs.close(handle),
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
