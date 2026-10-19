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
import { mkdtempSync, readFileSync, readdirSync, realpathSync, statSync, symlinkSync, writeFileSync } from 'node:fs'
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
  MAX_OUTSTANDING_PICKS,
  cliConfigDirectories,
  createInstallationFileStore,
  createInstallationRegistry,
  createPickTokens,
  installationStorePath,
  isInside,
  readInstallationFile,
  serializeInstallationFile,
  validateInstallation,
  validateInstallationArguments,
  installationOperationFor,
  nodeInstallationFs,
  probeInstallation,
} from '../../src/main/installations'
import type { InstallationFs, StoredInstallation } from '../../src/main/installations'
// The pairing check the store runs is the **source layer's own**
// `verifyCandidate`, so the refusal test drives the real one rather than a stub
// that returns the sentence it is asserting.
import { verifyCandidate } from '../../src/source/cli'
import { installStub } from './stub'
import { classifyCliVersion } from '../../src/shared/version'
import { privateWorkingDirectory } from '../../src/main/spawn'
import { SPAWN_LOG_ENV } from '../../src/main/spawnlog'

const HOME = '/Users/owner'

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
    const sixteen = Array.from({ length: MAX_INSTALLATIONS }, (_unused, index) =>
      installation({ id: index.toString(16).padStart(16, '0') }),
    )
    expect(MAX_INSTALLATIONS).toBe(16)
    expect(readInstallationFile(fileText(sixteen)).ok).toBe(true)
    const seventeen = [...sixteen, installation({ id: 'ffffffffffffffff' })]
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
  it('writes nothing at all until an add, a choose or a remove', () => {
    // The C8 rule, asserted on the *writer* rather than on a file the test hoped
    // would not appear: a load, a `rows()` and a `current()` must not reach the
    // filesystem at all.
    const fs = recordingFs()
    const store = createInstallationFileStore({ path: '/tmp/userData/installations.json', fs })
    const registry = createInstallationRegistry({
      store,
      validate: async () => ({ ok: false, reason: 'not used' }),
    })
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

  it('refuses a binary that is not there, and one that is not executable', async () => {
    const directory = freshDirectory()
    const missing = await validateInstallation({ project: 'fugaro', binaryPath: join(directory, 'nope') }, { verify })
    expect(missing.ok).toBe(false)
    if (!missing.ok) expect(missing.reason).toContain('not an executable file')

    const plain = join(directory, 'plain')
    writeFileSync(plain, '#!/bin/sh\n')
    const notExecutable = await validateInstallation({ project: 'fugaro', binaryPath: plain }, { verify })
    expect(notExecutable.ok).toBe(false)
    if (!notExecutable.ok) expect(notExecutable.reason).toContain('not an executable file')
  })

  it('refuses a config path that is not a file, without opening it', async () => {
    const directory = freshDirectory()
    const stub = installStub(directory, { commands: { version: { stdout: '1.4.2\n' }, doctor: { stdout: '{}\n' } } })
    const result = await validateInstallation(
      { project: 'fugaro', binaryPath: stub.binary, configPath: join(directory, 'no-such-config.toml') },
      { verify, appEnv: {} },
    )
    expect(result.ok).toBe(false)
    if (!result.ok) expect(result.reason).toContain('is not a file')
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
  function registryOver(path: string, validate?: Parameters<typeof createInstallationRegistry>[0]['validate']) {
    const store = createInstallationFileStore({ path })
    return createInstallationRegistry({
      store,
      validate:
        validate ??
        (async () => ({
          ok: true,
          value: { installation: installation(), compatibility: { mode: 'ok' } as never },
        })),
    })
  }

  it('starts with no installation, and says so without naming a figure', () => {
    const path = join(freshDirectory(), INSTALLATIONS_FILE_NAME)
    const registry = registryOver(path)
    registry.load()
    const current = registry.current()
    expect(current.chosen).toBe(false)
    if (!current.chosen) expect(current.sentence).toBe('no installation has been chosen')
    expect(registry.active()).toBeNull()
  })

  it('persists an add, and a choose, and a remove -- and nothing else', () => {
    const directory = freshDirectory()
    const path = join(directory, INSTALLATIONS_FILE_NAME)
    const registry = registryOver(path)
    registry.load()
    expect(readdirSync(directory)).toEqual([])

    return registry.add({ project: 'fugaro', binaryPath: '/usr/local/bin/fugaro' }).then(() => {
      expect(readdirSync(directory)).toEqual([INSTALLATIONS_FILE_NAME])
      const added = registry.rows()
      expect(added).toHaveLength(1)
      const id = added[0]?.id ?? ''
      expect(added[0]?.active).toBe(false)

      registry.choose(id)
      const chosen = readInstallationFile(readFileSync(path, 'utf8'))
      expect(chosen.ok).toBe(true)
      if (chosen.ok) expect(chosen.file.active).toBe(id)
      expect(registry.current().chosen).toBe(true)

      registry.remove(id)
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
  })

  it('refuses an add while the file is unreadable, and never overwrites it', () => {
    const directory = freshDirectory()
    const path = join(directory, INSTALLATIONS_FILE_NAME)
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
    return registry.add({ project: 'fugaro', binaryPath: '/usr/local/bin/fugaro' }).then((result) => {
      expect(result.ok).toBe(false)
      expect(readFileSync(path, 'utf8')).toBe(broken)
    })
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
    // the dashboard's read and the picker's row.
    registry.remember(installation().id, classifyCliVersion('0.9.0'))
    const reduced = registry.current()
    if (reduced.chosen) {
      expect(reduced.mode).toBe('reduced')
      expect(reduced.cliVersion).toBe('0.9.0')
    }
    expect(registry.rows()[0]?.mode).toBe('reduced')
  })

  it('refuses the seventeenth installation', () => {
    const directory = freshDirectory()
    const path = join(directory, INSTALLATIONS_FILE_NAME)
    let next = 0
    const registry = registryOver(path, async () => ({
      ok: true,
      value: {
        installation: installation({ id: next.toString(16).padStart(16, '0') }),
        compatibility: { mode: 'ok' } as never,
      },
    }))
    registry.load()
    const adds = Array.from({ length: MAX_INSTALLATIONS + 1 }, async () => {
      next += 1
      return registry.add({ project: 'fugaro', binaryPath: '/usr/local/bin/fugaro' })
    })
    return Promise.all(adds).then((results) => {
      expect(results.filter((result) => result.ok)).toHaveLength(MAX_INSTALLATIONS)
      expect(results.at(-1)?.ok).toBe(false)
    })
  })
})

// ---------------------------------------------------------------------------
// Pick tokens and the picker's argument schema
// ---------------------------------------------------------------------------

describe('pick tokens', () => {
  it('are single-use', () => {
    const tokens = createPickTokens()
    const token = tokens.issue('binary', '/usr/local/bin/fugaro')
    expect(token).toMatch(/^[0-9a-f]{32}$/)
    expect(tokens.redeem('binary', token)).toBe('/usr/local/bin/fugaro')
    expect(tokens.redeem('binary', token)).toBeNull()
  })

  it('are bound to the kind of path they were issued for', () => {
    const tokens = createPickTokens()
    const token = tokens.issue('checkout', '/Users/owner/src/fugaro')
    expect(tokens.redeem('binary', token)).toBeNull()
    // ...and the failed redemption did not consume it: a kind mismatch is a
    // refusal, not a way for one screen to burn another's token.
    expect(tokens.redeem('checkout', token)).toBe('/Users/owner/src/fugaro')
  })

  it('are bounded at eight outstanding, oldest first', () => {
    const tokens = createPickTokens()
    const issued = Array.from({ length: MAX_OUTSTANDING_PICKS }, (_unused, index) => tokens.issue('binary', `/bin/${String(index)}`))
    expect(tokens.outstanding).toBe(MAX_OUTSTANDING_PICKS)
    const ninth = tokens.issue('binary', '/bin/ninth')
    expect(tokens.outstanding).toBe(MAX_OUTSTANDING_PICKS)
    // The oldest is the one that went.
    expect(tokens.redeem('binary', issued[0] ?? '')).toBeNull()
    expect(tokens.redeem('binary', ninth)).toBe('/bin/ninth')
  })

  it('are all dropped when the picker closes', () => {
    const tokens = createPickTokens()
    const token = tokens.issue('config', '/Users/owner/.config/fugaro/config.toml')
    tokens.dropAll()
    expect(tokens.outstanding).toBe(0)
    expect(tokens.redeem('config', token)).toBeNull()
  })
})

describe('the picker’s argument schema', () => {
  const tokens = createPickTokens()

  it('refuses an add that names a path instead of a token', () => {
    // The plan's red step for the IPC half, at its narrowest: the value is a
    // perfectly good absolute path, and it is still refused -- the renderer
    // never names a path, and a renderer that tries has nothing that validates.
    const decision = validateInstallationArguments(installationOperationFor('add'), {
      project: 'fugaro',
      binary: '/usr/local/bin/fugaro',
    }, (kind, token) => tokens.redeem(kind, token))
    expect(decision.ok).toBe(false)
    if (!decision.ok) expect(decision.reason).toContain('not a path this console offered')
  })

  it('refuses an unknown key and a missing required one', () => {
    const unknown = validateInstallationArguments(installationOperationFor('list'), { path: '/etc/passwd' }, () => null)
    expect(unknown.ok).toBe(false)
    const missing = validateInstallationArguments(installationOperationFor('add'), { project: 'fugaro' }, () => null)
    expect(missing.ok).toBe(false)
    if (!missing.ok) expect(missing.reason).toContain('binary is required')
  })

  it('resolves an outstanding token to the path the main process chose', () => {
    const fresh = createPickTokens()
    const token = fresh.issue('binary', '/usr/local/bin/fugaro')
    const decision = validateInstallationArguments(installationOperationFor('add'), { project: 'fugaro', binary: token }, (kind, value) =>
      fresh.redeem(kind, value),
    )
    expect(decision.ok).toBe(true)
    if (decision.ok) expect(decision.args['binary']).toBe('/usr/local/bin/fugaro')
  })

  it('refuses a choose whose id is not an installation id', () => {
    for (const id of ['/usr/local/bin/fugaro', '../../etc/passwd', 'ZZZ', '']) {
      const decision = validateInstallationArguments(installationOperationFor('choose'), { id }, () => null)
      expect(decision.ok, id).toBe(false)
    }
  })

  it('has eight rows, and every row takes no path, command or URL', () => {
    const names = ['list', 'current', 'pickBinary', 'pickConfig', 'pickCheckout', 'add', 'choose', 'remove']
    expect(names.map((name) => installationOperationFor(name).name)).toEqual(names)
    for (const name of names) {
      const row = installationOperationFor(name as (typeof names)[number])
      for (const arg of row.args) {
        expect(['installationName', 'installationId', 'installationToken'], `${name}.${arg.key}`).toContain(arg.kind)
      }
    }
    expect(() => installationOperationFor('run')).toThrow(/not an installation operation/)
  })
})
