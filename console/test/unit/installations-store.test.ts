// The installation store: what it writes, when, and what it refuses
// (desktop-console plan Task 3.4b; design sections 3.4 rules 5 and 7, 5.1 rule
// 10, 7.4, D13, D14; owner rulings C8 and C9, 2026-10-18).
//
// Every test here is written against the *contract* rather than against the
// implementation, and each names the mutation that breaks it:
//
//   - a store that writes on a load, a pick or a failed validation fails "writes
//     nothing until an add, a choose or a remove" -- asserted on a recording
//     filesystem as well as on disk;
//   - a store that writes the file in place fails "leaves no partial file behind
//     when the rename never happens", which makes `rename` throw and then reads
//     the directory;
//   - a reader that accepts an extra key fails "refuses the whole file", whose
//     checker is itself tested against a planted `token` field -- the plan's red
//     step -- so the check is not vacuous;
//   - a store that keeps a CLI below the floor fails "a refused CLI is not
//     stored";
//   - a store whose path is the CLI's own config directory fails the path
//     refusal, and the app-name test is what keeps `fugaro-console` outside all
//     three of them on every platform the console ships on.

import { execFileSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, readFileSync, readdirSync, realpathSync, statSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import {
  CONSOLE_APP_NAME,
  EMPTY_INSTALLATION_FILE,
  INSTALLATIONS_FILE_NAME,
  INSTALLATION_FILE_MODE,
  INSTALLATION_FILE_VERSION,
  INSTALLATION_KEYS,
  MAX_INSTALLATIONS,
  MAX_INSTALLATION_FILE_BYTES,
  cliConfigDirectories,
  createInstallationFileStore,
  createInstallationRegistry,
  installationStorePath,
  isInside,
  readInstallationFile,
  serializeInstallationFile,
  validateInstallation,
  validateInstallationArguments,
  installationOperationFor,
  nodeInstallationFs,
  probeInstallation,
  withResolvedBinary,
} from '../../src/main/installations'
import type { InstallationFs, StoredInstallation } from '../../src/main/installations'
// The pairing check the store runs is the **source layer's own**
// `verifyCandidate`, so the refusal test drives the real one rather than a stub
// that returns the sentence it is asserting.
import { verifyCandidate } from '../../src/source/cli'
import { installStub } from './stub'
import { installWslStub } from './wslstub'
import { classifyCliVersion } from '../../src/shared/version'
import { privateWorkingDirectory } from '../../src/main/spawn'
import { SPAWN_LOG_ENV } from '../../src/main/spawnlog'

const HOME = '/Users/owner'

/** only is the one entry a list is expected to hold, so "the fake recorded
 * nothing" fails here rather than making every later assertion vacuous. */
function only<T>(entries: readonly T[]): T {
  expect(entries).toHaveLength(1)
  const [entry] = entries
  if (entry === undefined) throw new Error('the fake recorded nothing')
  return entry
}

function freshDirectory(): string {
  return mkdtempSync(join(tmpdir(), 'fugaro-installations-'))
}

/** installation is one entry, with the id overridable so a test can plant a
 * duplicate or a dangling `active`. */
function installation(overrides: Partial<StoredInstallation> = {}): StoredInstallation {
  return { id: '0123456789abcdef', project: 'fugaro', binaryPath: '/usr/local/bin/fugaro', ...overrides }
}

/** fileText is a well-formed file with the entries given. */
function fileText(entries: readonly unknown[], active: string | null = null): string {
  return JSON.stringify({ version: INSTALLATION_FILE_VERSION, active, installations: entries })
}

// ---------------------------------------------------------------------------
// The file: where it is, and what it may not be
// ---------------------------------------------------------------------------

describe('the store’s path', () => {
  it('is installations.json in userData', () => {
    const path = installationStorePath({ userData: '/Users/owner/Library/Application Support/fugaro-console', env: {}, home: HOME })
    expect(path).toBe(join('/Users/owner/Library/Application Support/fugaro-console', INSTALLATIONS_FILE_NAME))
    expect(INSTALLATIONS_FILE_NAME).toBe('installations.json')
  })

  it('is refused inside the CLI’s own configuration, in all three places', () => {
    // The three the design names: ~/.config/fugaro, $XDG_CONFIG_HOME/fugaro and
    // $FUGARO_CONFIG's directory. A console whose userData is any of them would
    // be writing the app's file where the operator's CLI reads, which rule 7
    // makes the CLI's alone.
    const cases: readonly (readonly [string, Readonly<Record<string, string | undefined>>, string])[] = [
      ['the home directory’s config', {}, join(HOME, '.config', 'fugaro')],
      ['$XDG_CONFIG_HOME', { XDG_CONFIG_HOME: '/home/owner/.config' }, join('/home/owner/.config', 'fugaro')],
      ['$FUGARO_CONFIG’s directory', { FUGARO_CONFIG: '/etc/fugaro/config.toml' }, '/etc/fugaro'],
    ]
    for (const [why, env, userData] of cases) {
      expect(() => installationStorePath({ userData, env, home: HOME }), why).toThrow(/refuses to keep its installations/)
      // ...and a directory *under* one of them is refused too, which a
      // prefix-on-strings test would get wrong in both directions.
      expect(() => installationStorePath({ userData: join(userData, 'nested'), env, home: HOME }), why).toThrow()
    }
  })

  it('names the app’s own directory, which is outside all three on every platform', () => {
    // The plan's red step for this half: an app named `fugaro` on Linux would put
    // userData at ~/.config/fugaro, which is the CLI's. The name checked is the
    // one **Electron** derives `userData` from -- `package.json`'s
    // `productName`, else its `name` -- read from the file, not a constant
    // compared with its own literal (review S9). `build.productName` is
    // electron-builder's name for the packaged app, and is checked too, so a
    // rename of either to `fugaro` fails here.
    const manifest = JSON.parse(
      readFileSync(fileURLToPath(new URL('../../package.json', import.meta.url)), 'utf8'),
    ) as { readonly name: string; readonly productName?: string; readonly build?: { readonly productName?: string } }
    const electronName = manifest.productName ?? manifest.name
    const names = [electronName, ...(manifest.build?.productName === undefined ? [] : [manifest.build.productName])]
    expect(electronName).toBe(CONSOLE_APP_NAME)
    for (const name of names) {
      const userDataByPlatform: readonly (readonly [string, string])[] = [
        ['linux', join(HOME, '.config', name)],
        ['darwin', join(HOME, 'Library', 'Application Support', name)],
      ]
      for (const [platform, userData] of userDataByPlatform) {
        for (const configDirectory of cliConfigDirectories({ XDG_CONFIG_HOME: join(HOME, '.config') }, HOME)) {
          expect(isInside(configDirectory, userData), `${platform}: ${userData}`).toBe(false)
        }
        expect(() => installationStorePath({ userData, env: {}, home: HOME })).not.toThrow()
      }
    }
    // ...and `isInside` is not a prefix test: a sibling that merely starts with
    // the config directory's name is not inside it.
    expect(isInside('/a/fugaro', '/a/fugaro-console')).toBe(false)
    expect(isInside('/a/fugaro', '/a/fugaro')).toBe(true)
    expect(isInside('/a/fugaro', '/a/fugaro/x')).toBe(true)
  })
})

// ---------------------------------------------------------------------------
// The reader: the closed schema
// ---------------------------------------------------------------------------

describe('the file’s schema', () => {
  it('round-trips a well-formed file', () => {
    const one = installation()
    const read = readInstallationFile(fileText([one], one.id))
    expect(read.ok).toBe(true)
    if (!read.ok) return
    expect(read.file.version).toBe(1)
    expect(read.file.active).toBe(one.id)
    expect(read.file.installations).toEqual([one])
    expect(Object.keys(read.file)).toEqual(['version', 'active', 'installations'])
  })

  it('accepts exactly the five keys of an entry, and the plan’s tuple', () => {
    expect(INSTALLATION_KEYS).toEqual(['id', 'project', 'binaryPath', 'configPath', 'checkout'])
    const read = readInstallationFile(
      fileText([{ id: '0123456789abcdef', project: 'fugaro', binaryPath: '/b', configPath: '/c', checkout: '/d' }]),
    )
    expect(read.ok).toBe(true)
  })

  it('refuses a planted token field, and the checker is not vacuous', () => {
    // The plan's red step: an entry with a `token` in it. A credential in this
    // file is the one thing C8 forbids outright, and the refusal is of the whole
    // file -- a reader that dropped the key and kept the entry would be a reader
    // that silently rewrote the operator's file.
    const planted = fileText([{ ...installation(), token: ['gh', 'p_', 'a'.repeat(20)].join('') }])
    const read = readInstallationFile(planted)
    expect(read.ok).toBe(false)
    if (read.ok) return
    expect(read.reason).toContain('token')
    // The checker against the *same* entry without the extra key: without this
    // half, a checker that refused everything would pass the assertion above.
    expect(readInstallationFile(fileText([installation()])).ok).toBe(true)
  })

  it.each([
    ['an unknown top-level key', JSON.stringify({ version: 1, active: null, installations: [], cache: {} }), 'exactly'],
    ['a missing top-level key', JSON.stringify({ version: 1, installations: [] }), 'exactly'],
    ['a version that is not 1', JSON.stringify({ version: 2, active: null, installations: [] }), 'version'],
    ['a dangling active', fileText([installation()], 'ffffffffffffffff'), 'active'],
    ['an active that is not an id', fileText([installation()], 'fugaro'), 'active'],
    ['a duplicate id', fileText([installation(), installation()]), 'share the id'],
    [
      'two entries for one project',
      fileText([installation(), installation({ id: 'fedcba9876543210' })]),
      'share the project',
    ],
    ['an id that is not sixteen hex characters', fileText([installation({ id: 'ZZZ' })]), 'sixteen hex'],
    ['a relative binary path', fileText([installation({ binaryPath: 'bin/fugaro' })]), 'absolute'],
    ['a relative config path', fileText([{ ...installation(), configPath: 'config.toml' }]), 'absolute'],
    ['a project with a space', fileText([installation({ project: 'two words' })]), 'project name'],
    ['an empty project', fileText([installation({ project: '' })]), 'project name'],
    ['an entry that is not an object', fileText(['fugaro']), 'not an object'],
    ['installations that is not a list', JSON.stringify({ version: 1, active: null, installations: {} }), 'not a list'],
    ['a file that is not JSON', 'not json at all', 'not JSON'],
    ['a file that is not an object', '[]', 'not a JSON object'],
  ])('refuses %s', (_why, raw, fragment) => {
    const read = readInstallationFile(raw)
    expect(read.ok).toBe(false)
    if (read.ok) return
    expect(read.reason).toContain(fragment)
  })

  it('refuses the seventeenth entry, and accepts the sixteenth', () => {
    // Distinct projects as well as distinct ids: this file is addressed by both
    // (the schema refuses two entries for one project), and a fixture that varied
    // only the ids would be testing the count bound with a file the store itself
    // would never write.
    const sixteen = Array.from({ length: MAX_INSTALLATIONS }, (_unused, index) =>
      installation({ id: index.toString(16).padStart(16, '0'), project: `project-${String(index)}` }),
    )
    expect(MAX_INSTALLATIONS).toBe(16)
    expect(readInstallationFile(fileText(sixteen)).ok).toBe(true)
    const seventeen = [...sixteen, installation({ id: 'ffffffffffffffff', project: 'one-too-many' })]
    const read = readInstallationFile(fileText(seventeen))
    expect(read.ok).toBe(false)
    if (!read.ok) expect(read.reason).toContain('more than 16')
  })

  it('refuses a file past the read bound without parsing it, and leaves it alone', () => {
    const directory = freshDirectory()
    const path = join(directory, INSTALLATIONS_FILE_NAME)
    // A valid prefix and then filler: a reader that parsed first would accept
    // the prefix and ignore the rest.
    writeFileSync(path, `${fileText([installation()])}${' '.repeat(MAX_INSTALLATION_FILE_BYTES)}`)
    const before = readFileSync(path)
    const store = createInstallationFileStore({ path })
    const read = store.load()
    expect(read.ok).toBe(false)
    if (!read.ok) expect(read.reason).toContain('larger than')
    expect(readFileSync(path)).toEqual(before)
  })

  it('reads a file that is not there as the empty file, and never creates one', () => {
    const directory = freshDirectory()
    const store = createInstallationFileStore({ path: join(directory, INSTALLATIONS_FILE_NAME) })
    const read = store.load()
    expect(read.ok).toBe(true)
    if (read.ok) expect(read.file).toEqual(EMPTY_INSTALLATION_FILE)
    expect(readdirSync(directory)).toEqual([])
  })

  it('leaves a refused file’s bytes exactly as they were', () => {
    const directory = freshDirectory()
    const path = join(directory, INSTALLATIONS_FILE_NAME)
    const broken = fileText([{ ...installation(), token: 'secret-shaped' }])
    writeFileSync(path, broken)
    const before = readFileSync(path)
    expect(createInstallationFileStore({ path }).load().ok).toBe(false)
    expect(readFileSync(path)).toEqual(before)
  })

  it('refuses a symbolic link at its name, even to a well-formed file (review S8)', () => {
    const directory = freshDirectory()
    const target = join(directory, 'elsewhere.json')
    writeFileSync(target, fileText([installation()], installation().id))
    const path = join(directory, INSTALLATIONS_FILE_NAME)
    symlinkSync(target, path)
    const read = createInstallationFileStore({ path }).load()
    expect(read.ok).toBe(false)
    if (!read.ok) expect(read.reason).toContain('symbolic link')
    // The descriptor-level guard too: `O_NOFOLLOW` refuses the link even when a
    // caller skipped the `lstat`.
    expect(() => nodeInstallationFs.readRegularFile(path, MAX_INSTALLATION_FILE_BYTES + 1)).toThrow()
  })

  it('refuses a FIFO at its name without blocking on it (review S8)', () => {
    const directory = freshDirectory()
    const path = join(directory, INSTALLATIONS_FILE_NAME)
    execFileSync('mkfifo', [path])
    // A `readFileSync` here would block the test forever: there is no writer.
    // The vitest timeout is the hang detector.
    const read = createInstallationFileStore({ path }).load()
    expect(read.ok).toBe(false)
    // Refused by the `lstat`, before anything opens it.
    if (!read.ok) expect(read.reason).toBe('the file is not a regular file')
    // And the read itself, if it is ever reached: `O_NONBLOCK` opens the FIFO
    // without waiting for a writer, and `fstat` refuses it.
    expect(() => nodeInstallationFs.readRegularFile(path, MAX_INSTALLATION_FILE_BYTES + 1)).toThrow(/not a regular file/)
  }, 5000)

  it('reads at most one byte past the bound from the descriptor', () => {
    // The `lstat` size check is a fast path, not the bound: a file that grows
    // between the two calls is caught by the read. The fake answers a small
    // size and then a large read.
    const calls: number[] = []
    const fs: InstallationFs = {
      ...nodeInstallationFs,
      lstat: () => ({ isFile: () => true, isSymbolicLink: () => false, size: 10 }),
      readRegularFile: (_path, limit) => {
        calls.push(limit)
        return { bytes: limit, text: ' '.repeat(limit) }
      },
    }
    const read = createInstallationFileStore({ path: '/nowhere/installations.json', fs }).load()
    expect(calls).toEqual([MAX_INSTALLATION_FILE_BYTES + 1])
    expect(read.ok).toBe(false)
    if (!read.ok) expect(read.reason).toContain('larger than')
  })
})

// ---------------------------------------------------------------------------
// The writer: 0600, atomic, and only on a change
// ---------------------------------------------------------------------------

/** recordingFs is `node:fs` as the store uses it, with every call kept. */
function recordingFs(): InstallationFs & {
  readonly calls: string[]
  readonly written: string[]
} {
  const calls: string[] = []
  const written: string[] = []
  const real: InstallationFs = {
    mkdir: (path, options) => {
      calls.push(`mkdir:${path}:${options.mode.toString(8)}`)
      // Not performed: the tests that use this never want a directory made.
    },
    writeFile: (path, data, options) => {
      calls.push(`writeFile:${path}:${options.mode.toString(8)}:${options.flag}`)
      written.push(data)
    },
    rename: (from, to) => {
      calls.push(`rename:${from}:${to}`)
    },
    lstat: () => {
      calls.push('lstat')
      const error = new Error('ENOENT') as Error & { code: string }
      error.code = 'ENOENT'
      throw error
    },
    readRegularFile: () => {
      calls.push('readRegularFile')
      return { bytes: 0, text: '' }
    },
    stat: () => {
      calls.push('stat')
      const error = new Error('ENOENT') as Error & { code: string }
      error.code = 'ENOENT'
      throw error
    },
    unlink: (path) => {
      calls.push(`unlink:${path}`)
    },
  }
  return { calls, written, ...real }
}

describe('the writer', () => {
  it('writes nothing at all until a choose, a checkout or a remove', () => {
    // The C8 rule, asserted on the *writer* rather than on a file the test hoped
    // would not appear: a load, a `rows()` and a `current()` must not reach the
    // filesystem at all.
    const fs = recordingFs()
    const store = createInstallationFileStore({ path: '/tmp/userData/installations.json', fs })
    const registry = createInstallationRegistry({ store })
    registry.load()
    registry.rows()
    registry.current()
    expect(fs.written).toEqual([])
    expect(fs.calls.filter((call) => call.startsWith('rename:'))).toEqual([])
  })

  it('creates the file 0600 inside a 0700 directory, by rename', () => {
    const directory = freshDirectory()
    const path = join(directory, INSTALLATIONS_FILE_NAME)
    const store = createInstallationFileStore({ path })
    store.save({ version: INSTALLATION_FILE_VERSION, active: null, installations: [installation()] })
    expect(statSync(path).mode & 0o777).toBe(INSTALLATION_FILE_MODE)
    const written = readInstallationFile(readFileSync(path, 'utf8'))
    expect(written.ok).toBe(true)
    if (written.ok) expect(written.file.installations).toEqual([installation()])
    // The temporary file is gone: the only thing left is the document.
    expect(readdirSync(directory)).toEqual([INSTALLATIONS_FILE_NAME])
  })

  it('leaves no partial file behind when the rename never happens', () => {
    // The crash the atomic write exists for: the document is written to a
    // temporary file and the rename is what publishes it, so a failure between
    // the two leaves the previous document intact -- never a half-written one.
    const directory = freshDirectory()
    const path = join(directory, INSTALLATIONS_FILE_NAME)
    writeFileSync(path, fileText([installation({ project: 'previous' })], installation().id))
    const before = readFileSync(path)
    const fs: InstallationFs = {
      mkdir: () => undefined,
      writeFile: (target, data, options) => writeFileSync(target, data, { flag: options.flag, mode: options.mode }),
      rename: () => {
        throw new Error('ENOSPC: no space left on device')
      },
      lstat: (target) => nodeInstallationFs.lstat(target),
      readRegularFile: (target, limit) => nodeInstallationFs.readRegularFile(target, limit),
      stat: (target) => statSync(target),
      unlink: (target) => {
        // Deliberately not performed: this asserts the file the *caller* sees,
        // and a store that left its temporary file behind is a store that has
        // already failed the point.
      },
    }
    expect(() =>
      createInstallationFileStore({ path, fs }).save({
        version: INSTALLATION_FILE_VERSION,
        active: null,
        installations: [installation({ project: 'new' })],
      }),
    ).toThrow(/ENOSPC/)
    expect(readFileSync(path)).toEqual(before)
  })

  it('is serialized one entry per line, and re-reads as what it wrote', () => {
    const file = {
      version: INSTALLATION_FILE_VERSION as 1,
      active: installation().id,
      installations: [installation(), installation({ id: 'ffffffffffffffff', project: 'other' })],
    }
    const text = serializeInstallationFile(file)
    expect(text.split('\n').filter((line) => line.startsWith('    {'))).toHaveLength(2)
    const read = readInstallationFile(text)
    expect(read.ok).toBe(true)
    if (read.ok) expect(read.file).toEqual(file)
  })
})

// ---------------------------------------------------------------------------
// Validation: the checks, in order, against stub binaries
// ---------------------------------------------------------------------------

describe('validation', () => {
  const verify = async (): Promise<void> => undefined

  it('refuses a project name that is not one, before touching the binary', async () => {
    const result = await validateInstallation({ project: 'two words', binaryPath: '/nowhere' }, { verify })
    expect(result.ok).toBe(false)
    if (!result.ok) expect(result.reason).toContain('project name')
  })

  it('refuses a relative binary path', async () => {
    const result = await validateInstallation({ project: 'fugaro', binaryPath: 'bin/fugaro' }, { verify })
    expect(result.ok).toBe(false)
    if (!result.ok) expect(result.reason).toContain('absolute')
  })

  it('walks past a stored binary that is not there, and refuses with the whole list', async () => {
    // Rule 4's configured path is a **candidate**, not a decision (owner's
    // ruling of 2026-10-20): the store's field is an optional override, so an
    // entry whose binary has since been moved falls through to the well-known
    // locations rather than pinning the console to a file that is gone. With
    // rule 4's prefixes emptied -- which is what a machine with no `fugaro`
    // installed looks like -- nothing is left, and the refusal names every
    // candidate it tried.
    const directory = freshDirectory()
    const missing = await validateInstallation(
      { project: 'fugaro', binaryPath: join(directory, 'nope') },
      { verify, prefixes: [] },
    )
    expect(missing.ok).toBe(false)
    if (!missing.ok) {
      expect(missing.reason).toContain(join(directory, 'nope'))
      expect(missing.reason).toContain('no fugaro')
    }

    // A candidate that is a file but is not executable is skipped for the same
    // reason, and a *file that answered badly* is not -- that one is refused
    // rather than skipped (rule 3 in `spawn.ts`).
    const plain = join(directory, 'plain')
    writeFileSync(plain, '#!/bin/sh\n')
    const notExecutable = await validateInstallation(
      { project: 'fugaro', binaryPath: plain },
      { verify, prefixes: [] },
    )
    expect(notExecutable.ok).toBe(false)
    if (!notExecutable.ok) expect(notExecutable.reason).toContain('no fugaro')
  })

  it('carries a stored config path to the CLI without opening or stating it', async () => {
    // The store's `configPath` is a value a screen *passes on*: the CLI reads the
    // file, and this module checks only that the string is absolute (it becomes
    // an argv element). It is not stat'ed, not opened, and not read -- the
    // recording filesystem below would refuse a `stat` of a path that is not
    // there, and the check is that no `stat` is asked for at all. The green step
    // of `test/unit/configread.test.ts`'s scan, held as behaviour rather than as
    // a line scan.
    const directory = freshDirectory()
    const stub = installStub(directory, { commands: { version: { stdout: '1.4.2\n' }, doctor: { stdout: '{}\n' } } })
    const fs = recordingFs()
    const result = await validateInstallation(
      { project: 'fugaro', binaryPath: stub.binary, configPath: join(directory, 'no-such-config.toml') },
      { verify, appEnv: {}, fs },
    )
    expect(result.ok).toBe(true)
    expect(fs.calls.filter((call) => call.startsWith('stat') || call.startsWith('lstat') || call.startsWith('readRegularFile'))).toEqual([])
    // A config path that is *not* absolute is still refused: it is an argv
    // element, and a relative one would be read from whichever directory the
    // child happens to run in.
    const relative = await validateInstallation(
      { project: 'fugaro', binaryPath: stub.binary, configPath: 'fugaro.yaml' },
      { verify, appEnv: {}, fs },
    )
    expect(relative.ok).toBe(false)
    if (!relative.ok) expect(relative.reason).toContain('absolute')
  })

  it('refuses a checkout that is not a directory', async () => {
    const directory = freshDirectory()
    const stub = installStub(directory, { commands: { version: { stdout: '1.4.2\n' }, doctor: { stdout: '{}\n' } } })
    const result = await validateInstallation(
      { project: 'fugaro', binaryPath: stub.binary, checkout: stub.binary },
      { verify, appEnv: {} },
    )
    expect(result.ok).toBe(false)
    if (!result.ok) expect(result.reason).toContain('is not a directory')
  })

  it('does not store a CLI below the floor', async () => {
    // The plan's red step for this half: a store that kept a `0.5.0` stub. The
    // console's floor is 0.6.0, so this is a refusal and not a reduced mode.
    const directory = freshDirectory()
    const stub = installStub(directory, { commands: { version: { stdout: '0.5.0\n' }, doctor: { stdout: '{}\n' } } })
    let verified = 0
    const result = await validateInstallation(
      { project: 'fugaro', binaryPath: stub.binary },
      {
        appEnv: {},
        verify: async () => {
          verified += 1
        },
      },
    )
    expect(result.ok).toBe(false)
    if (!result.ok) expect(result.reason).toContain('0.5.0')
    // ...and the pairing check never ran: a refused CLI is not driven.
    expect(verified).toBe(0)
  })

  it('runs the add’s and the choose’s probes from the private directory, and logs them (review S2)', async () => {
    // Design section 3.4 rule 5: with no checkout a child runs from the private
    // directory the console owns -- never `dirname(binaryPath)`, which is
    // `/usr/local/bin` or a directory inside an app bundle. And plan 3.4b step 2:
    // every spawn is in the operator's spawn log with the candidate's project.
    const directory = freshDirectory()
    const stub = installStub(join(directory, 'bin'), {
      commands: { version: { stdout: '0.8.1\n' }, doctor: { stdout: '{}\n' } },
    })
    const logPath = join(directory, 'spawn.jsonl')
    const appEnv = { [SPAWN_LOG_ENV]: logPath }
    const added = await validateInstallation({ project: 'acme', binaryPath: stub.binary }, { appEnv, verify })
    expect(added.ok).toBe(true)
    const probed = await probeInstallation(installation({ project: 'acme', binaryPath: stub.binary }), { appEnv })
    expect(probed.ok).toBe(true)
    const privateDirectory = privateWorkingDirectory()
    const versionCalls = stub.reports().filter((report) => report.argv[0] === 'version')
    expect(versionCalls).toHaveLength(2)
    for (const report of versionCalls) {
      expect(realpathSync(report.cwd)).toBe(privateDirectory)
      expect(report.cwd).not.toBe(join(directory, 'bin'))
    }
    const logged = readFileSync(logPath, 'utf8')
      .split('\n')
      .filter((line) => line !== '')
      .map((line) => JSON.parse(line) as { argv: string[]; project: string; cwd: string })
    expect(logged.map((record) => [record.argv[1], record.project, record.cwd])).toEqual([
      ['version', 'acme', privateDirectory],
      ['version', 'acme', privateDirectory],
    ])
  })

  it('accepts a CLI that is inside a distribution, and never asks the host about it', async () => {
    // T-C6 of docs/console-windows/implementation-plan.md, and F4's other half.
    // Under a `distro` the installation's `binaryPath` and `checkout` are Linux
    // spellings **inside** the distribution: the host cannot `stat` either of
    // them, so asking would refuse an installation that is perfectly good. What
    // answers instead is the distribution, through the same `version` probe
    // (T-C5) -- and the recording filesystem below proves the host was not asked.
    const root = freshDirectory()
    const host = join(root, 'windows')
    const wsl = installWslStub(host, {
      distros: { list: 'Ubuntu\r\n', status: 'Default Distribution: Ubuntu\r\n' },
      scenario: { commands: { version: { stdout: '1.4.2\n' } } },
    })
    const linuxBinary = '/home/dimi/.local/bin/fugaro'
    // A directory that exists, because the *distribution* starts the CLI in it
    // (the fake models that with a real child). What matters for this case is
    // that the host never asks about it, not that it is unreachable from here.
    const linuxCheckout = join(freshDirectory(), 'src')
    mkdirSync(linuxCheckout, { recursive: true })
    const fs = recordingFs()
    const result = await validateInstallation(
      { project: 'fugaro', distro: 'Ubuntu', binaryPath: linuxBinary, checkout: linuxCheckout },
      { appEnv: {}, fs, wslPath: wsl.binary, verify },
    )
    expect(result.ok).toBe(true)
    if (!result.ok) return
    // The Linux spellings are carried through as they were given, and the
    // version came from inside the distribution.
    expect(result.value.installation.binaryPath).toBe(linuxBinary)
    expect(result.value.installation.checkout).toBe(linuxCheckout)
    expect(result.value.compatibility.cliVersion).toBe('1.4.2')
    // No `stat`, no `lstat`, no read: the host was never asked about a path it
    // cannot see. This is the assertion the mutation -- one `fs.stat(checkout)`
    // put back for every route -- fails.
    expect(fs.calls).toEqual([])
    // And the CLI really did run inside the distribution, in the directory the
    // checkout names: `--cd` is the Linux spelling, which the fake refuses to
    // see spelled the Windows way.
    const spawn = only(wsl.reports().filter((report) => report.kind === 'spawn'))
    expect(spawn.distro).toBe('Ubuntu')
    expect(spawn.cd).toBe(linuxCheckout)
    // `wsl.exe`'s own words, then the shell that carries the CLI (T-F4): the
    // CLI's argv is inside the script, because the shell is what records the pid
    // a cancellation has to name.
    expect(spawn.argv.slice(0, 5)).toEqual(['-d', 'Ubuntu', '--cd', linuxCheckout, '--'])
    expect(spawn.argv.slice(5, 7)).toEqual(['sh', '-c'])
    expect(spawn.wrapper).toContain(`exec 'fugaro' 'version'`)
    // `wsl.exe` itself was started in a **host** directory, which the checkout
    // (a Linux path) is not.
    expect(spawn.cwd).toBe(privateWorkingDirectory())

    // And a checkout that is *not* there is the distribution's refusal, in
    // `wsl.exe`'s own words -- never the host's "is not a directory", because
    // the host never looked.
    const gone = await validateInstallation(
      { project: 'fugaro', distro: 'Ubuntu', binaryPath: linuxBinary, checkout: '/home/dimi/src/gone' },
      { appEnv: {}, fs, wslPath: wsl.binary, verify },
    )
    expect(gone.ok).toBe(false)
    if (!gone.ok) {
      expect(gone.reason).not.toContain('is not a directory')
      expect(gone.reason).toContain('could not start the child')
    }
    expect(fs.calls).toEqual([])
  })

  it('still refuses that same path when the installation names no distribution', async () => {
    // The other half of T-C6's red step, and the half that must keep passing: on
    // the host route a path that cannot be `stat`ed is not a binary, and rule 4
    // carries on to the well-known locations. With those emptied -- what a
    // machine with no `fugaro` looks like -- the refusal names every candidate.
    const result = await validateInstallation(
      { project: 'fugaro', binaryPath: '/home/dimi/.local/bin/fugaro' },
      { verify, prefixes: [] },
    )
    expect(result.ok).toBe(false)
    if (!result.ok) {
      expect(result.reason).toContain('/home/dimi/.local/bin/fugaro')
      expect(result.reason).toContain('no fugaro')
    }
  })

  it('stores a reduced-band CLI and marks it reduced', async () => {
    // 1.4.2 is newer than the built-against 0.8.1, which is the reduced band:
    // stored, and marked, because the console may drive it but withholds views.
    const directory = freshDirectory()
    const stub = installStub(directory, {
      commands: { version: { stdout: '1.4.2\n' }, doctor: { stdout: '{}\n' } },
    })
    const result = await validateInstallation(
      { project: 'fugaro', binaryPath: stub.binary },
      { appEnv: {}, verify },
    )
    expect(result.ok).toBe(true)
    if (!result.ok) return
    expect(result.value.compatibility.mode).toBe('reduced')
    expect(result.value.installation.project).toBe('fugaro')
    expect(result.value.installation.binaryPath).toBe(stub.binary)
    expect(result.value.installation.id).toMatch(/^[0-9a-f]{16}$/)
    // The version is **not** stored: it is re-read by the handshake on every
    // launch and every choose, and a stored one could only go stale.
    expect(Object.keys(result.value.installation)).toEqual(['id', 'project', 'binaryPath'])
  })

  it('shows the CLI’s own refusal when the pairing check fails, and stores nothing', async () => {
    // The pairing check is **the real one**: `verifyCandidate` against the stub
    // binary, so the sentence asserted below is the one the CLI printed rather
    // than a string this test handed in. `verifyCandidate` signals a refusal by
    // *throwing*, so this case also pins the contract that `validateInstallation`
    // turns that throw into `{ ok: false, reason }` instead of letting it escape
    // -- a `validateInstallation` that propagated it would fail here as a
    // rejected promise, and one that swallowed it would fail on `result.ok`.
    const directory = freshDirectory()
    const stub = installStub(directory, {
      commands: { version: { stdout: '1.4.2\n' }, doctor: { code: 1, stderr: 'fugaro: this checkout belongs to project acme; --project says fugaro\n' } },
    })
    let stored = 0
    const result = await validateInstallation(
      { project: 'fugaro', binaryPath: stub.binary, checkout: directory },
      {
        appEnv: {},
        verify: async (candidate) => {
          stored += 1
          await verifyCandidate(candidate, { appEnv: {}, home: directory })
        },
      },
    )
    expect(result.ok).toBe(false)
    if (!result.ok) expect(result.reason).toContain('this checkout belongs to project acme')
    // The check ran, and nothing came of it: `validateInstallation` answers a
    // value, so no caller can be the one that decides to store a refused one.
    expect(stored).toBe(1)
  })
})

// ---------------------------------------------------------------------------
// The registry: what is written, and when
// ---------------------------------------------------------------------------

describe('the registry', () => {
  function registryOver(path: string) {
    const store = createInstallationFileStore({ path })
    return createInstallationRegistry({ store })
  }

  it('starts with no installation, and says so without naming a figure', () => {
    const path = join(freshDirectory(), INSTALLATIONS_FILE_NAME)
    const registry = registryOver(path)
    registry.load()
    const current = registry.current()
    expect(current.chosen).toBe(false)
    if (!current.chosen) expect(current.sentence).toBe('no installation has been chosen')
    expect(registry.active()).toBeNull()
    // Nothing is stored, so nothing is written: a read is a read.
    expect(() => readFileSync(path, 'utf8')).toThrow()
  })

  it('persists a choose and a remove -- by project name, and nothing else', () => {
    // The form's `add` is gone (owner's ruling of 2026-10-20): the store's one
    // write is the operator's *choice* of an installation the CLI listed, and
    // what it holds is that choice plus the tuples a screen has since set. A
    // choose is keyed by the CLI's own name for the installation, which is what
    // the row carries and what a renderer is allowed to send.
    const directory = freshDirectory()
    const path = join(directory, INSTALLATIONS_FILE_NAME)
    const registry = registryOver(path)
    registry.load()
    expect(readdirSync(directory)).toEqual([])

    registry.choose('fugaro')
    expect(readdirSync(directory)).toEqual([INSTALLATIONS_FILE_NAME])
    const rows = registry.rows()
    expect(rows).toHaveLength(1)
    expect(rows[0]).toMatchObject({ project: 'fugaro', active: true, mode: 'unknown' })
    // No binary path was stored: the console never asks for one, so a record it
    // created carries the project and nothing else.
    expect(Object.keys(rows[0] ?? {}).sort()).toEqual(['active', 'id', 'mode', 'project'])
    const chosen = readInstallationFile(readFileSync(path, 'utf8'))
    expect(chosen.ok).toBe(true)
    if (chosen.ok) expect(chosen.file.active).toBe(rows[0]?.id ?? '')
    expect(registry.current().chosen).toBe(true)

    registry.remove('fugaro')
    // Removing the active installation leaves **none** active: there is no
    // "next one" the console may pick for the operator.
    expect(registry.current().chosen).toBe(false)
    const removed = readInstallationFile(readFileSync(path, 'utf8'))
    expect(removed.ok).toBe(true)
    if (removed.ok) {
      expect(removed.file.active).toBeNull()
      expect(removed.file.installations).toEqual([])
    }
  })

  it('keeps a project’s place in the file, and re-choosing one only moves `active`', () => {
    // The order is the file's own: the screen draws the CLI's list, and what the
    // store holds is a set of records. A write never reshuffles them, so an
    // operator who reads `installations.json` does not find it rewritten under
    // them.
    const directory = freshDirectory()
    const path = join(directory, INSTALLATIONS_FILE_NAME)
    const registry = registryOver(path)
    registry.load()
    registry.choose('one')
    registry.choose('two')
    const ids = registry.rows().map((row) => [row.project, row.id])
    expect(ids.map(([project]) => project)).toEqual(['one', 'two'])
    registry.choose('one')
    expect(registry.rows().map((row) => [row.project, row.id])).toEqual(ids)
    expect(registry.rows().find((row) => row.project === 'one')?.active).toBe(true)
    expect(registry.rows().find((row) => row.project === 'two')?.active).toBe(false)
  })

  it('records and clears one project’s checkout, and leaves the other keys alone', () => {
    const directory = freshDirectory()
    const path = join(directory, INSTALLATIONS_FILE_NAME)
    const registry = registryOver(path)
    registry.load()
    registry.choose('fugaro')
    registry.setCheckout('fugaro', '/Users/owner/src/fugaro')
    expect(registry.entry('fugaro')).toMatchObject({ checkout: '/Users/owner/src/fugaro' })
    expect(registry.rows()[0]?.checkout).toBe('/Users/owner/src/fugaro')
    // The record's identity and its project survive the edit: `setCheckout`
    // rebuilds the record field by field, and a bug that dropped `id` would
    // orphan `active`.
    const current = registry.current()
    expect(current.chosen && current.id).toBe(registry.rows()[0]?.id ?? '')
    // Clearing **removes the key**, rather than writing an `undefined` that
    // JSON would drop and the in-memory record would keep: the two must not
    // disagree about whether this installation has a checkout.
    registry.setCheckout('fugaro', undefined)
    expect(Object.keys(registry.entry('fugaro') ?? {}).sort()).toEqual(['id', 'project'])
    expect(registry.rows()[0]?.checkout).toBeUndefined()
    // ...and the file on disk says the same thing.
    const read = readInstallationFile(readFileSync(path, 'utf8'))
    expect(read.ok).toBe(true)
    if (read.ok) expect(Object.keys(read.file.installations[0] ?? {}).sort()).toEqual(['id', 'project'])
  })

  it('sets a checkout for a project it holds no record for by creating one', () => {
    // The CLI listed it and the operator set its repository before choosing it:
    // the record is the console's own fact about a project, and creating one is
    // the same operation a choose performs.
    const directory = freshDirectory()
    const path = join(directory, INSTALLATIONS_FILE_NAME)
    const registry = registryOver(path)
    registry.load()
    registry.setCheckout('acme', '/Users/owner/src/acme')
    expect(registry.entry('acme')).toMatchObject({ project: 'acme', checkout: '/Users/owner/src/acme' })
    // Nothing is active: setting a checkout is not choosing.
    expect(registry.active()).toBeNull()
  })

  it('removes a project it does not hold loudly, rather than writing an empty file', () => {
    const path = join(freshDirectory(), INSTALLATIONS_FILE_NAME)
    const registry = registryOver(path)
    registry.load()
    expect(() => registry.remove('invented')).toThrow(/no installation named invented/)
  })

  it('refuses a choose while the file is unreadable, and never overwrites it', () => {
    const directory = freshDirectory()
    const path = join(directory, INSTALLATIONS_FILE_NAME)
    // A key this version does not know is a refusal: it is either a newer
    // version's field or something a person added by hand, and writing over it
    // would silently drop either.
    const broken = fileText([{ ...installation(), token: 'nope' }])
    writeFileSync(path, broken)
    const registry = registryOver(path)
    const read = registry.load()
    expect(read.ok).toBe(false)
    expect(registry.refusal).not.toBeNull()
    // The dashboard's read says so too, naming the path (plan 3.4b step 1).
    const current = registry.current()
    expect(current.chosen).toBe(false)
    if (!current.chosen) {
      expect(current.path).toBe(path)
      expect(current.refusal).toContain('does not know')
    }
    // A refused file refuses every write, and the bytes stay exactly as they
    // were: the operator fixes the file, not the console.
    expect(() => registry.choose('fugaro')).toThrow(/keeps no installations/)
    expect(() => registry.setCheckout('fugaro', undefined)).toThrow(/keeps no installations/)
    expect(readFileSync(path, 'utf8')).toBe(broken)
  })

  it('reads an installation nobody probed as unknown, never ok, and carries the handshake it remembers (review S1)', () => {
    const directory = freshDirectory()
    const path = join(directory, INSTALLATIONS_FILE_NAME)
    writeFileSync(path, fileText([installation()], installation().id))
    const registry = registryOver(path)
    registry.load()
    const unprobed = registry.current()
    expect(unprobed.chosen).toBe(true)
    if (unprobed.chosen) {
      expect(unprobed.mode).toBe('unknown')
      expect(unprobed.cliVersion).toBeNull()
    }
    expect(registry.rows()[0]?.mode).toBe('unknown')
    // The startup's handshake, remembered: reduced, with its version, in both
    // the dashboard's read and the picker's row. It is keyed by **project**,
    // which is what the CLI's own name is and what a row is opened by.
    registry.remember('fugaro', classifyCliVersion('0.9.0'))
    const reduced = registry.current()
    if (reduced.chosen) {
      expect(reduced.mode).toBe('reduced')
      expect(reduced.cliVersion).toBe('0.9.0')
    }
    expect(registry.rows()[0]?.mode).toBe('reduced')
    expect(registry.activeCompatibility()?.cliVersion).toBe('0.9.0')
  })

  it('keeps the legacy binary override when it has one, and never invents one', () => {
    // The no-migration rule: an old store entry with a binary keeps working, and
    // the field is an *optional override* -- so a record the console creates
    // today has none, and rule 4 resolves for it instead.
    const directory = freshDirectory()
    const path = join(directory, INSTALLATIONS_FILE_NAME)
    writeFileSync(path, fileText([installation({ project: 'legacy' })], installation().id))
    const registry = registryOver(path)
    registry.load()
    expect(registry.rows()[0]?.binary).toBe('/usr/local/bin/fugaro')
    registry.choose('fresh')
    expect(registry.entry('fresh')).toEqual({ id: expect.stringMatching(/^[0-9a-f]{16}$/), project: 'fresh' })
  })

  it('refuses the seventeenth project, and accepts the sixteenth', () => {
    const directory = freshDirectory()
    const path = join(directory, INSTALLATIONS_FILE_NAME)
    const registry = registryOver(path)
    registry.load()
    for (let index = 0; index < MAX_INSTALLATIONS; index += 1) registry.choose(`project-${String(index)}`)
    expect(registry.rows()).toHaveLength(MAX_INSTALLATIONS)
    expect(() => registry.choose('one-too-many')).toThrow(/at most 16/)
    // ...and a project it already holds is still choosable, which is what keeps
    // the bound from being a console that stops working on its sixteenth.
    expect(() => registry.choose('project-0')).not.toThrow()
  })
})

// ---------------------------------------------------------------------------
// The picker's argument schema: five rows, and what each one may carry
// ---------------------------------------------------------------------------

describe('the header’s binary', () => {
  it('names the binary this run resolved, and not the path the store remembers', () => {
    // The defect this case exists for: a record this console wrote carries an id
    // and a project and **no binary**, so a header that read the store alone had
    // nothing to draw while the console was driving `/usr/local/bin/fugaro` --
    // and a legacy record whose stored override has gone made the console name a
    // path it was not running, because rule 4's configured path is a *candidate*
    // and the walk carries on (owner's ruling of 2026-10-20).
    const record = {
      chosen: true as const,
      id: '0123456789abcdef',
      project: 'fugaro',
      cliVersion: null,
      mode: 'unknown' as const,
    }
    expect(withResolvedBinary(record, '/usr/local/bin/fugaro')).toEqual({
      ...record,
      binary: '/usr/local/bin/fugaro',
    })

    const legacy = { ...record, binary: '/nowhere/fugaro' }
    expect(withResolvedBinary(legacy, '/opt/homebrew/bin/fugaro')).toEqual({
      ...legacy,
      binary: '/opt/homebrew/bin/fugaro',
    })
  })

  it('falls back to the store only when nothing has resolved in this run', () => {
    // The two cases where the resolution is absent, and both of them are answered
    // by the record: nothing has read a binary for this installation yet in this
    // run, and an empty string is a resolution that named no file.
    const record = {
      chosen: true as const,
      id: '0123456789abcdef',
      project: 'fugaro',
      binary: '/nowhere/fugaro',
      cliVersion: null,
      mode: 'unknown' as const,
    }
    expect(withResolvedBinary(record, undefined)).toEqual(record)
    expect(withResolvedBinary(record, '')).toEqual(record)
    // And a record with no binary of its own gains no key: absent is the
    // ordinary case, and an invented one would be a path no file backs.
    const bare = {
      chosen: true as const,
      id: '0123456789abcdef',
      project: 'fugaro',
      cliVersion: null,
      mode: 'unknown' as const,
    }
    const joined = withResolvedBinary(bare, undefined)
    expect(Object.keys(joined).sort()).toEqual(['chosen', 'cliVersion', 'id', 'mode', 'project'])
    expect('binary' in joined).toBe(false)
  })

  it('leaves D13’s no-installation answer alone', () => {
    // `chosen: false` names no file at all: it is the store's sentence, and the
    // store's refusal when the file could not be read. A resolution for some
    // other project -- which is what a stale map entry would be -- must not put a
    // binary on an answer that says nothing is chosen.
    const none = { chosen: false as const, sentence: 'no installation has been chosen' }
    expect(withResolvedBinary(none, '/usr/local/bin/fugaro')).toBe(none)
    const refused = {
      chosen: false as const,
      sentence: 'no installation has been chosen',
      refusal: 'the file could not be read',
      path: '/tmp/installations.json',
    }
    expect(withResolvedBinary(refused, '/usr/local/bin/fugaro')).toBe(refused)
  })
})

describe('the picker’s argument schema', () => {
  it('has the five rows the picker has, and they are the whole surface', () => {
    // The form's rows -- `current`, `pickBinary`, `pickConfig`, `pickCheckout`,
    // `add` -- are gone, and a row that came back fails here by name.
    const names = ['list', 'choose', 'checkout', 'remove', 'openExternal']
    expect(names.map((name) => installationOperationFor(name).name)).toEqual(names)
    for (const gone of ['add', 'current', 'pickBinary', 'pickConfig', 'pickCheckout']) {
      expect(() => installationOperationFor(gone), gone).toThrow(/not an installation operation/)
    }
    expect(() => installationOperationFor('run')).toThrow(/not an installation operation/)
  })

  it('lets a row carry only a project name, a directory out of the CLI’s list, or nothing', () => {
    for (const row of ['list', 'choose', 'checkout', 'remove', 'openExternal'].map((name) => installationOperationFor(name))) {
      for (const arg of row.args) {
        expect(['installationName', 'installationCheckout'], `${row.name}.${arg.key}`).toContain(arg.kind)
      }
    }
    // A `checkout`, when a row has one, is optional: absent means "no checkout",
    // which is the state an installation without a repository is in.
    const checkout = installationOperationFor('checkout').args.find((arg) => arg.key === 'checkout')
    expect(checkout?.required).toBe(false)
    // ...and the one link row takes **no arguments at all**, so the URL is the
    // main process's own constant and never crosses the boundary.
    expect(installationOperationFor('openExternal').args).toEqual([])
    expect(installationOperationFor('list').args).toEqual([])
  })

  it('refuses an unknown key, a missing required one, and a value of the wrong shape', () => {
    const binary = validateInstallationArguments(installationOperationFor('choose'), {
      project: 'fugaro',
      binary: '/usr/local/bin/fugaro',
    })
    expect(binary.ok).toBe(false)
    if (!binary.ok) expect(binary.reason).toContain('unknown argument: binary')

    const missing = validateInstallationArguments(installationOperationFor('choose'), {})
    expect(missing.ok).toBe(false)
    if (!missing.ok) expect(missing.reason).toContain('project is required')

    // The plan's red step, at its narrowest: a string that is not a project name
    // is refused where a project name is what the row declares -- a value with a
    // space in it, a leading dash, or a newline could all become something else
    // once it is an argv element.
    for (const project of ['two words', '-rf', 'fugaro\nfugaro']) {
      const named = validateInstallationArguments(installationOperationFor('choose'), { project })
      expect(named.ok, project).toBe(false)
      if (!named.ok) expect(named.reason).toContain('not a project name')
    }

    // A checkout must be absolute -- it becomes a child's working directory --
    // and a relative one is refused before the picker ever looks at its list.
    const relative = validateInstallationArguments(installationOperationFor('checkout'), {
      project: 'fugaro',
      checkout: 'src/fugaro',
    })
    expect(relative.ok).toBe(false)
    if (!relative.ok) expect(relative.reason).toContain('absolute')
  })

  it('refuses a URL on the link row, and every other key with it', () => {
    // The row that opens the getting-started page takes nothing: a renderer that
    // sends an address has sent a key no row declares.
    for (const payload of [{ url: 'https://fugaro.dev/getting-started/' }, { url: 'https://evil.test/' }, { path: '/etc/passwd' }]) {
      const decision = validateInstallationArguments(installationOperationFor('openExternal'), payload)
      expect(decision.ok, JSON.stringify(payload)).toBe(false)
      if (!decision.ok) expect(decision.reason).toContain('unknown argument')
    }
  })

  it('returns the checked values rather than the payload, and freezes them', () => {
    const decision = validateInstallationArguments(installationOperationFor('checkout'), {
      project: 'fugaro',
      checkout: '/Users/owner/src/fugaro',
    })
    expect(decision.ok).toBe(true)
    if (!decision.ok) return
    expect(decision.args).toEqual({ project: 'fugaro', checkout: '/Users/owner/src/fugaro' })
    expect(Object.isFrozen(decision.args)).toBe(true)
    // An absent optional argument is simply not a key, which is what makes
    // "clears the checkout" expressible at all.
    const cleared = validateInstallationArguments(installationOperationFor('checkout'), { project: 'fugaro' })
    expect(cleared.ok && cleared.args).toEqual({ project: 'fugaro' })
  })
})
