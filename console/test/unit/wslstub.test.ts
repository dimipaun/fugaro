// The fake `wsl.exe`'s own contract (docs/console-windows/implementation-plan.md
// Part A, T-A1 to T-A3).
//
// `wslstub.ts` is the double every test of Parts C to G runs against, so a
// defect in it is a defect in all of them: a fake that quietly ran something
// else, or that handed the Linux child the host's environment, would make a
// green assertion about the WSL route mean nothing at all. Each case below names
// the mutation it catches, and every one of them is asserted through the double
// as a *process* -- spawned, and read back out of the report file -- because the
// claim that matters is what the Linux side received.

import { mkdirSync, mkdtempSync, readFileSync, realpathSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { CHILD_ENV_KEYS, FUGARO_BINARY_NAME } from '../../src/main/spawn'
import { DISTRO_PATH, installWslStub } from './wslstub'
import type { WslFixture, WslStub, WslStubOptions } from './wslstub'

/** WINDOWS_SRC is the path a test translates, spelled the way Windows spells
 * one: the table's key is the console's own `--cd` on the Windows side. */
const WINDOWS_SRC = 'C:\\Users\\dimi\\src'

/** WINDOWS_PATH is a Windows-shaped `PATH`, and it is the point of T-A2: the
 * host's separator and the distribution's are visible side by side in one spawn,
 * and the Linux child must never see this string. */
const WINDOWS_PATH = 'C:\\Windows\\System32;C:\\Windows'

/** HOST_ENV is the environment the console hands `wsl.exe`. `NOT_A_CHILD_KEY` is
 * a sentinel that is not one of `CHILD_ENV_KEYS`: it stands for everything else
 * the host's environment holds, and none of it may cross into the distribution. */
const HOST_ENV: Readonly<Record<string, string>> = {
  PATH: WINDOWS_PATH,
  HOME: '/home/dimi',
  FUGARO_PROJECT: 'fugaro',
  NOT_A_CHILD_KEY: 'planted-on-the-host',
}

/** FIXTURE is the two queries' answer, in the encoding most Windows builds
 * use. The UTF-16 vector is a case of its own (U9). */
const FIXTURE: WslFixture = {
  list: 'Ubuntu\nDebian\n',
  status: 'Default Distribution: Ubuntu\nDefault Version: 2\n',
}

/** freshDirectory is a temporary directory this test owns. */
function freshDirectory(): string {
  return mkdtempSync(join(tmpdir(), 'fugaro-console-wsl-'))
}

/** linuxDirectory is a directory the child can actually run in -- the fake
 * starts the stub `fugaro` *in* the translated path, so the path has to exist --
 * resolved, because the child's own report is `process.cwd()` and on macOS the
 * temporary directory has a second spelling. */
function linuxDirectory(root: string, ...segments: readonly string[]): string {
  const path = join(root, ...segments)
  mkdirSync(path, { recursive: true })
  return realpathSync(path)
}

/**
 * install is the arrangement every case below starts from: a fake `wsl.exe`, the
 * stub `fugaro` inside its distribution, and a translation table that carries
 * `C:\Users\dimi\src`.
 */
function install(root: string, options: Partial<WslStubOptions> = {}): { wsl: WslStub; linux: string } {
  const linux = linuxDirectory(root, 'windows', 'distro', 'home', 'dimi', 'src')
  const wsl = installWslStub(join(root, 'windows'), {
    scenario: { commands: { version: { stdout: '1.4.2\n' } } },
    distros: FIXTURE,
    paths: { [WINDOWS_SRC]: linux },
    ...options,
  })
  return { wsl, linux }
}

/** run spawns the fake the way the console would: as a process, with the argv it
 * was given and the environment it was given. */
function run(
  wsl: WslStub,
  argv: readonly string[],
  options: { readonly cwd?: string; readonly env?: Readonly<Record<string, string>> } = {},
): ReturnType<typeof spawnSync> {
  return spawnSync(wsl.binary, [...argv], {
    cwd: options.cwd ?? wsl.directory,
    env: options.env ?? HOST_ENV,
    encoding: 'utf8',
  })
}

/** only is the one entry a list is expected to hold. It asserts the count rather
 * than indexing, so "the double recorded nothing" fails here instead of making
 * every assertion about the missing line vacuous. */
function only<T>(entries: readonly T[]): T {
  expect(entries).toHaveLength(1)
  const [entry] = entries
  if (entry === undefined) throw new Error('the fake recorded nothing')
  return entry
}

describe('the fake wsl.exe', () => {
  it('records its own argv, its distro and the child it started', () => {
    // T-A1's first case. The two hops are read apart: the fake's own account of
    // what the console asked for, and the child's own account of what it got --
    // the second one written by the stub `fugaro` itself, so a test that reads
    // it is reading the Linux side rather than the console's plan object.
    const { wsl, linux } = install(freshDirectory())
    const argv = ['-d', 'Ubuntu', '--cd', WINDOWS_SRC, '--', FUGARO_BINARY_NAME, 'version']
    const result = run(wsl, argv, { cwd: linux })
    expect(result.status).toBe(0)

    const hop = only(wsl.reports())
    expect(hop.hop).toBe('wsl')
    expect(hop.kind).toBe('spawn')
    expect(hop.argv).toEqual(argv)
    expect(hop.distro).toBe('Ubuntu')
    expect(wsl.calls()).toBe(1)

    const child = only(wsl.children())
    expect(child.argv).toEqual(['version'])
    expect(wsl.childCalls()).toBe(1)
  })

  it('starts the child in the translated directory, and the Linux side reports it', () => {
    // The translation is a recorded table rather than a guess (T-A1 step 3), and
    // the child's own `cwd` is the proof that it was *used* (T-A1 step 4).
    // Mutations this catches: pass `--cd` through unchanged (the child's cwd and
    // `linuxCwd` both go back to the Windows spelling), and drop the `cwd` from
    // the spawn (the child reports the fake's own directory instead).
    const { wsl, linux } = install(freshDirectory())
    const result = run(wsl, ['-d', 'Ubuntu', '--cd', WINDOWS_SRC, '--', FUGARO_BINARY_NAME, 'version'])
    expect(result.status).toBe(0)

    const hop = only(wsl.reports())
    expect(hop.cd).toBe(WINDOWS_SRC)
    expect(hop.linuxCwd).toBe(linux)
    expect(hop.untranslated).toBeNull()
    expect(only(wsl.children()).cwd).toBe(linux)
    // And the fake's own directory is not where the child ran: the two are
    // different, so the assertion above cannot pass by accident.
    expect(hop.cwd).not.toBe(linux)
  })

  it('reports a --cd its table does not carry as untranslated rather than guessing', () => {
    // The other half of T-A1 step 3, and the reason the table exists at all: a
    // path the table does not carry comes back *stated* as untranslated. A
    // mapping computed by the double would be right for `C:\Users\X` and wrong
    // for every custom mount, so the double refuses to compute one (D4(b)).
    const root = freshDirectory()
    const elsewhere = linuxDirectory(root, 'windows', 'distro', 'home', 'dimi', 'other')
    const { wsl } = install(root)
    const result = run(wsl, ['-d', 'Ubuntu', '--cd', elsewhere, '--', FUGARO_BINARY_NAME, 'version'])
    expect(result.status).toBe(0)

    const hop = only(wsl.reports())
    expect(hop.linuxCwd).toBeNull()
    expect(hop.untranslated).toBe(elsewhere)
    // The child still ran, in the directory it was given: an untranslated path
    // is a fact in the report, not a silent substitution.
    expect(only(wsl.children()).cwd).toBe(elsewhere)
  })

  it('gives the child the distribution’s PATH, not the host’s', () => {
    // T-A2, the fake's half of F5. The fake's own `PATH` is the Windows-shaped
    // one -- the report says so -- and the child's is the distribution's, because
    // on the WSL leg the child is a Linux process. The mutation this catches is
    // the obvious one: forward our own `PATH`.
    const { wsl, linux } = install(freshDirectory())
    const result = run(wsl, ['-d', 'Ubuntu', '--cd', WINDOWS_SRC, '--', FUGARO_BINARY_NAME, 'version'], {
      cwd: linux,
    })
    expect(result.status).toBe(0)

    const hop = only(wsl.reports())
    expect(hop.path).toBe(WINDOWS_PATH)
    expect(hop.childEnv['PATH']).toBe(DISTRO_PATH)

    const child = only(wsl.children())
    expect(child.path).toBe(DISTRO_PATH)
    expect(child.path).toContain(':')
    expect(child.path).not.toContain('C:\\')
    expect(child.path).not.toContain(';')
  })

  it('passes the eleven CHILD_ENV_KEYS and nothing else across the boundary', () => {
    // The double's half of D5(a). A sentinel that is not one of the eleven keys
    // is planted in the environment the console hands `wsl.exe`, and it must not
    // reach the child: a fake that forwarded its whole environment would make
    // every later assertion about the credential boundary a claim about a fake
    // that leaks.
    const { wsl, linux } = install(freshDirectory())
    run(wsl, ['-d', 'Ubuntu', '--cd', WINDOWS_SRC, '--', FUGARO_BINARY_NAME, 'version'], { cwd: linux })

    const hop = only(wsl.reports())
    expect(hop.envKeys).toContain('NOT_A_CHILD_KEY')
    expect(Object.keys(hop.childEnv).sort()).toEqual(['FUGARO_PROJECT', 'HOME', 'PATH'])

    const child = only(wsl.children())
    expect(child.envKeys).toEqual(['FUGARO_PROJECT', 'HOME', 'PATH'])
    expect(child.envKeys).not.toContain('NOT_A_CHILD_KEY')
    for (const key of child.envKeys) expect([...CHILD_ENV_KEYS]).toContain(key)
  })

  it('refuses to be a general-purpose shell', () => {
    // T-A3. The child is the stub `fugaro` and nothing else, because a double
    // that runs anything makes every assertion about *what was run* meaningless.
    // The mutation this catches: drop the check and run whatever was asked for --
    // the command's own output appears and the status goes to zero.
    const { wsl } = install(freshDirectory())

    const other = run(wsl, ['-d', 'Ubuntu', '--', '/bin/echo', 'this-is-not-the-stub'])
    expect(other.status).not.toBe(0)
    expect(other.stdout).toBe('')
    expect(other.stderr).toContain('/bin/echo')
    expect(wsl.children()).toHaveLength(0)
    expect(only(wsl.reports()).kind).toBe('refused')

    // The flags it does not model, and the shapes it does not model, are refused
    // for the same reason: `--terminate` is refused outright by the design
    // (D6(b)), and a missing `--` or a missing `-d` is a console that did not say
    // what it wanted.
    expect(run(wsl, ['--terminate', 'Ubuntu']).status).not.toBe(0)
    expect(run(wsl, ['-d', 'Ubuntu', FUGARO_BINARY_NAME]).status).not.toBe(0)
    expect(run(wsl, ['--', FUGARO_BINARY_NAME]).status).not.toBe(0)
    expect(wsl.children()).toHaveLength(0)
    expect(wsl.reports()).toHaveLength(4)
  })

  it('answers --list --quiet and --status from the fixture, and starts no child', () => {
    // T-A1 step 5. The two queries are the resolver's and the picker's, and they
    // are answered rather than run: no child, and a report line that says what
    // the console asked for (T-C4 and T-F2 read that line back).
    const { wsl } = install(freshDirectory())
    expect(run(wsl, ['--list', '--quiet']).stdout).toBe(FIXTURE.list)
    expect(run(wsl, ['--status']).stdout).toBe(FIXTURE.status)
    expect(wsl.children()).toHaveLength(0)
    expect(wsl.reports().map((hop) => hop.kind)).toEqual(['query', 'query'])
    expect(wsl.reports().map((hop) => hop.argv)).toEqual([['--list', '--quiet'], ['--status']])
    // And the two shapes it does not model are refused rather than answered with
    // something close: a decoder test that read the wrong output would be a test
    // about the wrong bytes.
    expect(run(wsl, ['--list']).status).not.toBe(0)
    expect(run(wsl, ['--status', '--verbose']).status).not.toBe(0)
  })

  it('writes the fixture in the encoding the test chose', () => {
    // U9, and the vector T-F2's decoder needs: some `wsl.exe` builds answer in
    // UTF-16LE with a BOM and `\r\n`, and the test says so rather than hoping.
    const utf16: WslFixture = {
      list: '\uFEFFUbuntu\r\nDebian\r\n',
      status: '\uFEFFDefault Distribution: Ubuntu\r\n',
      encoding: 'utf16le',
    }
    const { wsl } = install(freshDirectory(), { distros: utf16 })
    const result = spawnSync(wsl.binary, ['--list', '--quiet'], { env: HOST_ENV, encoding: 'buffer' })
    expect(result.status).toBe(0)
    expect(Buffer.from(result.stdout).toString('utf16le')).toBe(utf16.list)
    expect(Buffer.from(result.stdout).toString('utf8')).not.toBe(utf16.list)
  })

  it('carries its parsing into the double rather than a second copy', () => {
    // The generated script is the double's whole program, and it runs the three
    // exported functions' own source (the arrangement `stub.ts` uses for
    // `commandKey`). A second, inlined copy of the rules is exactly the defect a
    // review found in the stub, so it is asserted against rather than trusted.
    const { wsl } = install(freshDirectory())
    const script = readFileSync(join(wsl.directory, 'wsl-stub.mjs'), 'utf8')
    expect(script).toContain('function parseWslArgv(argv)')
    expect(script).toContain('function translateCd(cd, table)')
    expect(script).toContain('function childEnvironmentFor(env, keys, distroPath)')
    expect(script).toContain('const parsed = parseWslArgv(argv)')
    expect(script.match(/function parseWslArgv/g) ?? []).toHaveLength(1)
    expect(script.match(/function childEnvironmentFor/g) ?? []).toHaveLength(1)
  })
})
