// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The crash-reporting switch: the one boolean `crash.json` holds, what the
// application menu does with it, and the gate it feeds (the descriptor's run 2;
// the owner's ruling of 2026-10-30 that the switch is per-user, local to the
// console, opt-in and applied at the next launch).
//
// Every test here names the mutation that breaks it:
//
//   - a store that answers **on** when it has nothing to read fails "is off when
//     there is no file": off is the whole of opt-in, and a console that sent a
//     report because nobody had answered yet would be reporting without consent;
//   - a switch held in memory rather than in the file fails "keeps an on switch
//     across a restart": the value has to be read out of `crash.json` again, with
//     nothing carried between the two reads;
//   - a reader that reads a **truthy** value fails "reads every value that is not
//     the boolean as off": `"true"`, `1` and an object all arrive from a file this
//     console did not write, and consent is the one answer it may not guess at;
//   - a writer that writes in place, or on every call, fails "holds one JSON
//     boolean and nothing else, 0600, by rename";
//   - a reader that follows the name fails "reads a symlink, a directory, a FIFO
//     and an oversized file as off, and leaves each where it is": a switch read
//     out of a name somebody else chose is not this operator's consent;
//   - a reader that says its line on every read fails "says its one line once,
//     however many times the gate reads the file";
//   - a toggle that answers the state the click moved the menu to rather than the
//     file's fails "answers the value in force when the write behind a click
//     fails", which is what the menu redraws from.

import { execFileSync } from 'node:child_process'
import {
  constants,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  statSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs'
import { tmpdir } from 'node:os'
import { basename, dirname, join } from 'node:path'

import { afterEach, describe, expect, it, vi } from 'vitest'

import { crashReportingDecision } from '../../src/main/crash'
import {
  CRASH_DIR_MODE,
  CRASH_FILE_MAX_BYTES,
  CRASH_FILE_MODE,
  CRASH_FILE_NAME,
  CRASH_FILE_UNREADABLE,
  CRASH_SWITCH_UNWRITTEN,
  createCrashSettings,
  nodeCrashFs,
  readCrashSwitch,
  writeCrashSwitch,
} from '../../src/main/crashstore'
import type { CrashFs } from '../../src/main/crashstore'

/** made is every temporary directory this file has handed out, so one rule at the
 * end of each case can take them all away: these hold FIFOs, links and files that
 * nothing would want to find in the machine's temp directory once the run is
 * over. */
const made: string[] = []

function freshDirectory(): string {
  const directory = mkdtempSync(join(tmpdir(), 'fugaro-crash-'))
  made.push(directory)
  return directory
}

afterEach(() => {
  for (const directory of made.splice(0)) rmSync(directory, { recursive: true, force: true })
})

/** mkfifoAbsent is whether this machine can make the FIFO the cases below need:
 * the tool is POSIX's, and a host without it skips those cases with the reason
 * rather than failing on a name it cannot create. */
const mkfifoAbsent = ((): boolean => {
  const probe = mkdtempSync(join(tmpdir(), 'fugaro-crash-'))
  try {
    execFileSync('mkfifo', [join(probe, 'probe')])
    return false
  } catch {
    return true
  } finally {
    rmSync(probe, { recursive: true, force: true })
  }
})()

const MKFIFO_SKIP = 'mkfifo is not on this machine'

/** noLinkRule is whether this platform defines the two flags the read is opened
 * with, for the reason `updatestore.test.ts` states: without them there is no
 * rule that refuses a link, because the regular-file rule is asked about the
 * target a link resolved to. */
const noLinkRule = ([constants.O_NOFOLLOW, constants.O_NONBLOCK] as readonly (number | undefined)[]).some(
  (flag) => typeof flag !== 'number',
)

const LINK_RULE_SKIP = 'this platform does not define O_NOFOLLOW and O_NONBLOCK'

/** only is the one entry a list is expected to hold, so "nothing was recorded"
 * fails here rather than making a later assertion vacuous. */
function only<T>(entries: readonly T[]): T {
  expect(entries).toHaveLength(1)
  const [entry] = entries
  if (entry === undefined) throw new Error('nothing was recorded')
  return entry
}

/** recordingFs is `node:fs` as the store uses it, with every call kept -- the
 * atomic write is a sequence, and the "nothing is written for a value that has
 * not changed" rule is the absence of one. */
function recordingFs(): CrashFs & { readonly calls: string[] } {
  const calls: string[] = []
  return {
    calls,
    mkdir: (path, options) => {
      calls.push(`mkdir:${path}`)
      nodeCrashFs.mkdir(path, options)
    },
    writeFile: (path, data, options) => {
      calls.push(`writeFile:${path}:${options.mode.toString(8)}:${options.flag}:${data}`)
      nodeCrashFs.writeFile(path, data, options)
    },
    rename: (from, to) => {
      calls.push(`rename:${from}:${to}`)
      nodeCrashFs.rename(from, to)
    },
    open: (path, flags) => {
      calls.push(`open:${path}:${flags}`)
      return nodeCrashFs.open(path, flags)
    },
    fstat: (handle) => nodeCrashFs.fstat(handle),
    readFile: (handle, maxBytes) => {
      calls.push(`readFile:${maxBytes}`)
      return nodeCrashFs.readFile(handle, maxBytes)
    },
    close: (handle) => {
      nodeCrashFs.close(handle)
    },
    unlink: (path) => {
      calls.push(`unlink:${path}`)
      nodeCrashFs.unlink(path)
    },
  }
}

/** silent runs a read with `console.error` held, and answers the lines it was
 * given: every refusal in this store is one line and no more. */
function silent(read: () => void): string[] {
  const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
  try {
    read()
    return logged.mock.calls.map((call) => String(call[0]))
  } finally {
    logged.mockRestore()
  }
}

describe('the crash-reporting switch’s file', () => {
  it('is off when there is no file, and none is created', () => {
    // Opt-in, and the whole of it: a first run has no file, and a first run that
    // wrote one would be a console that answered a question nobody was asked.
    const directory = freshDirectory()
    expect(readCrashSwitch(directory)).toBe(false)
    expect(readdirSync(directory)).toEqual([])
  })

  it('keeps an on switch across a restart, as the one boolean the file holds', () => {
    // The restart is the second read with nothing carried over: the only thing
    // between them is `crash.json`.
    const directory = freshDirectory()
    const path = join(directory, CRASH_FILE_NAME)

    writeCrashSwitch(directory, true)
    expect(readFileSync(path, 'utf8')).toBe('true')
    expect(readCrashSwitch(directory)).toBe(true)

    writeCrashSwitch(directory, false)
    expect(readFileSync(path, 'utf8')).toBe('false')
    expect(readCrashSwitch(directory)).toBe(false)
  })

  it('writes nothing when the value has not changed, and the absent file is off', () => {
    // Off is the value an absent file already holds, so moving the switch to off
    // on a first run is no write at all -- the same rule U4 has, which is why a
    // checkbox clicked twice does not move a file's inode.
    const directory = freshDirectory()
    const fs = recordingFs()
    writeCrashSwitch(directory, false, fs)
    expect(fs.calls).toEqual([`open:${join(directory, CRASH_FILE_NAME)}:${readFlags()}`])
    expect(readdirSync(directory)).toEqual([])
  })

  it('holds one JSON boolean and nothing else, 0600, by rename', () => {
    const directory = freshDirectory()
    const path = join(directory, CRASH_FILE_NAME)
    const fs = recordingFs()

    writeCrashSwitch(directory, true, fs)

    const text = readFileSync(path, 'utf8')
    expect(text).toBe('true')
    expect(typeof (JSON.parse(text) as unknown)).toBe('boolean')
    expect(statSync(path).mode & 0o777).toBe(CRASH_FILE_MODE)

    expect(fs.calls.filter((call) => call.startsWith('mkdir:'))).toEqual([`mkdir:${directory}`])
    const write = only(fs.calls.filter((call) => call.startsWith('writeFile:')))
    const temporary = write.slice('writeFile:'.length, -`:${CRASH_FILE_MODE.toString(8)}:wx:true`.length)
    expect(write).toMatch(new RegExp(`:${CRASH_FILE_MODE.toString(8)}:wx:true$`))
    expect(dirname(temporary)).toBe(directory)
    expect(basename(temporary)).toMatch(/^crash\.json\.[0-9a-f]{12}\.tmp$/)
    expect(only(fs.calls.filter((call) => call.startsWith('rename:')))).toBe(`rename:${temporary}:${path}`)
    expect(readdirSync(directory)).toEqual([CRASH_FILE_NAME])
    expect(CRASH_DIR_MODE).toBe(0o700)
  })

  it('reads every value that is not the boolean as off, says one line, and leaves the file alone', () => {
    // The store is the gate's only input, and the gate is `=== true`, so a file
    // holding `1`, `"true"` or an object is refused here as well as there: this
    // console never sends a report over a value it guessed at. Nothing is
    // repaired and nothing is deleted -- an operator who hand-edited the file
    // keeps their edit and gets the sentence, which names no part of the file.
    for (const text of ['{"send":true}', 'null', '1', '"true"', 'true false', '']) {
      const directory = freshDirectory()
      const path = join(directory, CRASH_FILE_NAME)
      writeFileSync(path, text)
      let answer: boolean | undefined
      const lines = silent(() => {
        answer = readCrashSwitch(directory)
      })
      expect(answer, text).toBe(false)
      expect(readFileSync(path, 'utf8')).toBe(text)
      expect(lines).toEqual([CRASH_FILE_UNREADABLE])
      if (text !== '') expect(lines[0]).not.toContain(text)
    }
  })

  it('opens the gate for exactly one content: the boolean true', () => {
    // The two halves of the wiring seen together: what the file holds, and what
    // the gate the reporter asks makes of it. `"true"` is the case the whole
    // strictness is for -- it is a yes to a truthy check and a no to this one.
    const yes = freshDirectory()
    writeCrashSwitch(yes, true)
    expect(crashReportingDecision({ optedIn: readCrashSwitch(yes) }).enabled).toBe(true)
    expect(crashReportingDecision({ optedIn: readCrashSwitch(yes) }).reason).toBe('enabled')

    for (const text of ['false', '"true"', '1', '{"optedIn":true}']) {
      const directory = freshDirectory()
      writeFileSync(join(directory, CRASH_FILE_NAME), text)
      const decision = { ...crashReportingDecision({ optedIn: readCrashSwitch(directory) }) }
      expect(decision.enabled, text).toBe(false)
      expect(decision.reason, text).toBe('opt-in-off')
    }
  })

  it('reads a directory, an oversized file, a FIFO and a name the open refuses as off, and leaves each where it is', () => {
    // Every one of these is a name at which the switch cannot be read, and the
    // answer for all of them is the default: nothing is sent. Nothing removes or
    // replaces any of them either -- the read is a read.
    const directory = freshDirectory()
    const path = join(directory, CRASH_FILE_NAME)
    const oversized = freshDirectory()
    writeFileSync(join(oversized, CRASH_FILE_NAME), `true${' '.repeat(CRASH_FILE_MAX_BYTES)}`)

    mkdirSync(path)
    const asDirectory = silent(() => {
      expect(readCrashSwitch(directory)).toBe(false)
    })
    expect(asDirectory).toEqual([CRASH_FILE_UNREADABLE])
    expect(statSync(path).isDirectory()).toBe(true)

    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    try {
      expect(readCrashSwitch(oversized)).toBe(false)
      expect(logged.mock.calls.map((call) => String(call[0]))).toContain(CRASH_FILE_UNREADABLE)
    } finally {
      logged.mockRestore()
    }
    expect(readFileSync(join(oversized, CRASH_FILE_NAME), 'utf8')).toMatch(/^true\s+$/)

    const refused = freshDirectory()
    writeFileSync(join(refused, CRASH_FILE_NAME), 'true')
    const denied: CrashFs = {
      ...nodeCrashFs,
      open: () => {
        throw Object.assign(new Error('EACCES: permission denied'), { code: 'EACCES' })
      },
    }
    const asDenied = silent(() => {
      expect(readCrashSwitch(refused, denied)).toBe(false)
    })
    expect(asDenied).toEqual([CRASH_FILE_UNREADABLE])
    expect(readFileSync(join(refused, CRASH_FILE_NAME), 'utf8')).toBe('true')
    expect(readCrashSwitch(refused)).toBe(true)
  })

  it.skipIf(mkfifoAbsent)(`reads a FIFO at the name itself as off, and asks it for no byte${mkfifoAbsent ? ` (${MKFIFO_SKIP})` : ''}`, () => {
    // A read-only open of a FIFO blocks until a writer appears, and the writer
    // of a name like this one is never coming: `O_NONBLOCK` is what makes the
    // open answer, and the regular-file rule is what refuses it before a byte is
    // read. The read count is asserted as well, because "did not block" and "did
    // not read" are two claims.
    const directory = freshDirectory()
    const path = join(directory, CRASH_FILE_NAME)
    execFileSync('mkfifo', [path])
    const fs = recordingFs()
    const lines = silent(() => {
      expect(readCrashSwitch(directory, fs)).toBe(false)
    })
    expect(lines).toEqual([CRASH_FILE_UNREADABLE])
    expect(fs.calls.filter((call) => call.startsWith('readFile:'))).toEqual([])
    expect(lstatSync(path).isFIFO()).toBe(true)
  })

  it.skipIf(noLinkRule)(`refuses a symlink to a file that says true by the link rule alone, and reads a plain one${noLinkRule ? ` (${LINK_RULE_SKIP})` : ''}`, () => {
    // A link whose target is this console's own file holding `true`: the answer
    // is **off**, because the name is a link rather than the file, and consent
    // has to come from the file this console reads. The plain file beside it is
    // the same bytes at a name that is a file, and it is read as on -- so the
    // refusal is about the kind of name and not about the content.
    const directory = freshDirectory()
    const target = join(directory, 'elsewhere.json')
    writeFileSync(target, 'true')
    symlinkSync(target, join(directory, CRASH_FILE_NAME))

    const lines = silent(() => {
      expect(readCrashSwitch(directory)).toBe(false)
    })
    expect(lines).toEqual([CRASH_FILE_UNREADABLE])
    expect(lstatSync(join(directory, CRASH_FILE_NAME)).isSymbolicLink()).toBe(true)
    expect(readFileSync(target, 'utf8')).toBe('true')

    // ...and a plain file of the same bytes is the switch, on.
    rmSync(join(directory, CRASH_FILE_NAME))
    writeFileSync(join(directory, CRASH_FILE_NAME), 'true')
    expect(readCrashSwitch(directory)).toBe(true)
  })

  it('says its one line once, however many times the gate reads the file', () => {
    const directory = freshDirectory()
    writeFileSync(join(directory, CRASH_FILE_NAME), 'nonsense')
    const logged = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    try {
      expect(readCrashSwitch(directory)).toBe(false)
      expect(readCrashSwitch(directory)).toBe(false)
      expect(readCrashSwitch(directory)).toBe(false)
      expect(logged).toHaveBeenCalledTimes(1)
    } finally {
      logged.mockRestore()
    }
  })
})

describe('the switch the menu moves', () => {
  it('answers the value in force, and says one line when the write behind a click fails', () => {
    // The menu draws Electron's own checkbox, which has already moved itself by
    // the time the click's callback runs: a write that did not land has to come
    // back as the value the file holds, so the item is drawn again from what is
    // in force. A throw out of here would be an uncaught error over a running
    // app, so there is none.
    const directory = freshDirectory()
    writeCrashSwitch(directory, true)
    const settings = createCrashSettings({
      directory,
      fs: {
        ...nodeCrashFs,
        writeFile: () => {
          throw new Error('ENOSPC: no space left on device')
        },
      },
    })
    expect(settings.send).toBe(true)
    const lines = silent(() => {
      expect(settings.setSend(false)).toBe(true)
    })
    expect(lines).toEqual([CRASH_SWITCH_UNWRITTEN])
    expect(readCrashSwitch(directory)).toBe(true)

    // ...and a write that lands is answered with the value it wrote.
    const working = createCrashSettings({ directory })
    expect(working.send).toBe(true)
    expect(working.setSend(false)).toBe(false)
    expect(readCrashSwitch(directory)).toBe(false)
    expect(working.send).toBe(false)

    // A write that did **not** throw and did not land is answered the same way,
    // which is the case the answer cannot be the argument for: the rename here
    // returns without publishing anything -- a directory that went away between
    // the write and the rename is a failure the filesystem reports as success --
    // so a menu drawn from the argument would draw a tick over a file that still
    // says off, and the report would be refused at the next launch.
    const unpublishing = createCrashSettings({
      directory,
      fs: { ...nodeCrashFs, rename: () => undefined },
    })
    expect(unpublishing.send).toBe(false)
    expect(unpublishing.setSend(true)).toBe(false)
    expect(readCrashSwitch(directory)).toBe(false)
  })

  it('names its temporary file freshly on every write, so two writers cannot collide', () => {
    // The atomic write is a temporary file and a rename, and the name carries
    // fresh random bytes per call. That is what makes the `wx` on the temporary
    // file safe beside anything else writing in this directory (the installation
    // list's own store among them): `wx` refuses a name that already exists, and
    // the only name this call can be surprised by is its own, which it made a
    // moment ago. A fixed temporary name would turn a second writer into an
    // `EEXIST` thrown out of a menu click -- Electron's uncaught-error dialog
    // over a running app -- which is the failure this pins away.
    const directory = freshDirectory()
    const temporaries: string[] = []
    const fs = (): CrashFs => {
      const inner = recordingFs()
      return {
        ...inner,
        writeFile: (path, data, options) => {
          temporaries.push(basename(path))
          inner.writeFile(path, data, options)
        },
      }
    }
    writeCrashSwitch(directory, true, fs())
    writeCrashSwitch(directory, false, fs())
    expect(temporaries).toHaveLength(2)
    expect(new Set(temporaries).size).toBe(2)
    for (const name of temporaries) expect(name).toMatch(/^crash\.json\.[0-9a-f]{12}\.tmp$/)
    expect(readCrashSwitch(directory)).toBe(false)
  })
})

/** readFlags is the mask the read opens the switch with, spelled here rather
 * than imported so that a change to it fails a case instead of moving every case
 * with it. */
function readFlags(): number {
  return constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0) | (constants.O_NONBLOCK ?? 0)
}
