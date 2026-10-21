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
//
// The double is also the seam Parts C to G drive their own subjects through, so
// the four shapes they need are cases here rather than surprises there: the two
// queries, a `wslpath` translation (T-D2), the `sh -c` wrapper the CLI runs
// inside (T-F4), and a `wsl.exe` that fails outright (T-C4).

import { existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync } from 'node:fs'
import { spawn, spawnSync } from 'node:child_process'
import { once } from 'node:events'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { CHILD_ENV_KEYS, FUGARO_BINARY_NAME } from '../../src/main/spawn'
import { DISTRO_DIRECTORY, DISTRO_PATH, installWslStub } from './wslstub'
import type { WslFixture, WslPathPair, WslStub, WslStubOptions } from './wslstub'

/** WINDOWS_SRC is a path spelled the way Windows spells one, and it is the
 * `wslpath` table's key: the console asks `wslpath` for the Linux spelling
 * rather than computing it, and it is a Windows spelling in `--cd` that the
 * double refuses (T-C5, T-D3, T-D4). */
const WINDOWS_SRC = 'C:\\Users\\dimi\\src'

/** LINUX_SRC is the same path as the distribution spells it. */
const LINUX_SRC = '/mnt/c/Users/dimi/src'

/** UNC_SRC is the other direction's vector: the spelling Windows opens a path
 * inside a distribution by (T-D2, T-D3). */
const UNC_SRC = '\\\\wsl.localhost\\Ubuntu\\home\\dimi\\src'

/** PATHS is the translation table, in both directions, and it is the test's own
 * record: the double answers it and never derives one spelling from the other
 * (D4(b)). */
const PATHS: readonly WslPathPair[] = [
  { windows: WINDOWS_SRC, linux: LINUX_SRC },
  { windows: UNC_SRC, linux: '/home/dimi/src' },
]

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
 * starts the stub `fugaro` *in* the `--cd` directory, so the directory has to
 * exist -- resolved, because the child's own report is `process.cwd()` and on
 * macOS the temporary directory has a second spelling. */
function linuxDirectory(root: string, ...segments: readonly string[]): string {
  const path = join(root, ...segments)
  mkdirSync(path, { recursive: true })
  return realpathSync(path)
}

/**
 * install is the arrangement every case below starts from: a fake `wsl.exe`, the
 * stub `fugaro` inside its distribution, a directory a `--cd` can name, and the
 * translation table.
 */
function install(root: string, options: Partial<WslStubOptions> = {}): { wsl: WslStub; linux: string } {
  const linux = linuxDirectory(root, 'windows', DISTRO_DIRECTORY, 'home', 'dimi', 'src')
  const wsl = installWslStub(join(root, 'windows'), {
    scenario: { commands: { version: { stdout: '1.4.2\n' } } },
    distros: FIXTURE,
    paths: PATHS,
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

/** waitFor polls until a condition holds, and throws with the sentence the case
 * needs when it never does. The wrapper case is the one asynchronous one: the
 * pid file is written *before* the CLI it `exec`s starts, so both have to be
 * waited for rather than assumed. */
async function waitFor(what: string, check: () => boolean): Promise<void> {
  for (let attempt = 0; attempt < 300; attempt += 1) {
    if (check()) return
    await new Promise((resolve) => setTimeout(resolve, 10))
  }
  throw new Error(`waited 3s and ${what} never happened`)
}

/** waitForNumber waits for a file to hold a number: the wrapper writes the pid
 * it captured as its first act, and the test reads it while the CLI it `exec`ed
 * is still alive. */
async function waitForNumber(path: string): Promise<number> {
  let found = 0
  await waitFor(`${path} to hold a pid`, () => {
    if (!existsSync(path)) return false
    const value = Number.parseInt(readFileSync(path, 'utf8').trim(), 10)
    if (!Number.isInteger(value) || value <= 0) return false
    found = value
    return true
  })
  return found
}

describe('the fake wsl.exe', () => {
  it('records its own argv, its distro and the child it started', () => {
    // T-A1's first case. The two hops are read apart: the fake's own account of
    // what the console asked for, and the child's own account of what it got --
    // the second one written by the stub `fugaro` itself, so a test that reads
    // it is reading the Linux side rather than the console's plan object.
    const { wsl, linux } = install(freshDirectory())
    const argv = ['-d', 'Ubuntu', '--cd', linux, '--', FUGARO_BINARY_NAME, 'version']
    const result = run(wsl, argv)
    expect(result.status).toBe(0)

    const hop = only(wsl.reports())
    expect(hop.hop).toBe('wsl')
    expect(hop.kind).toBe('spawn')
    expect(hop.argv).toEqual(argv)
    expect(hop.distro).toBe('Ubuntu')
    expect(hop.cd).toBe(linux)
    expect(hop.wrapper).toBeNull()
    expect(hop.wslpath).toBeNull()
    expect(wsl.calls()).toBe(1)

    const child = only(wsl.children())
    expect(child.argv).toEqual(['version'])
    expect(wsl.childCalls()).toBe(1)
  })

  it('starts the child in the directory the console named, and the Linux side reports it', () => {
    // `--cd` is the distribution's own spelling (T-C5, T-D4), so the fake uses it
    // as it stands -- and the child's own `cwd` is the proof that it was *used*
    // rather than merely recorded (T-A1 step 4). The mutation this catches: drop
    // the `cwd` from the spawn, and the child reports the fake's own directory.
    const { wsl, linux } = install(freshDirectory())
    const result = run(wsl, ['-d', 'Ubuntu', '--cd', linux, '--', FUGARO_BINARY_NAME, 'version'])
    expect(result.status).toBe(0)

    const hop = only(wsl.reports())
    expect(hop.cd).toBe(linux)
    expect(only(wsl.children()).cwd).toBe(linux)
    // And the fake's own directory is not where the child ran: the two are
    // different, so the assertion above cannot pass by accident.
    expect(hop.cwd).not.toBe(linux)
  })

  it('refuses a Windows spelling in --cd rather than starting a child in it', () => {
    // The console translates with `wslpath` and sends the distribution's own
    // spelling (T-C5, T-D3, T-D4), so a Windows path here is a Part C or D bug.
    // The mutation this catches: accept it, and the child is started in a
    // directory that does not exist -- an ENOENT that says nothing about which
    // side spelled the path wrong.
    const { wsl } = install(freshDirectory())
    const result = run(wsl, ['-d', 'Ubuntu', '--cd', WINDOWS_SRC, '--', FUGARO_BINARY_NAME, 'version'])
    expect(result.status).not.toBe(0)
    expect(result.stdout).toBe('')
    expect(result.stderr).toContain(WINDOWS_SRC)
    expect(wsl.children()).toHaveLength(0)
    expect(only(wsl.reports()).kind).toBe('refused')
  })

  it('gives the child the distribution’s PATH, not the host’s', () => {
    // T-A2, the fake's half of F5. The fake's own `PATH` is the Windows-shaped
    // one -- the report says so -- and the child's is the distribution's, because
    // on the WSL leg the child is a Linux process. The mutation this catches is
    // the obvious one: forward our own `PATH`.
    const { wsl, linux } = install(freshDirectory())
    const result = run(wsl, ['-d', 'Ubuntu', '--cd', linux, '--', FUGARO_BINARY_NAME, 'version'])
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
    run(wsl, ['-d', 'Ubuntu', '--cd', linux, '--', FUGARO_BINARY_NAME, 'version'])

    const hop = only(wsl.reports())
    expect(hop.envKeys).toContain('NOT_A_CHILD_KEY')
    expect(Object.keys(hop.childEnv).sort()).toEqual(['FUGARO_PROJECT', 'HOME', 'PATH'])

    const child = only(wsl.children())
    expect(child.envKeys).toEqual(['FUGARO_PROJECT', 'HOME', 'PATH'])
    expect(child.envKeys).not.toContain('NOT_A_CHILD_KEY')
    for (const key of child.envKeys) expect([...CHILD_ENV_KEYS]).toContain(key)
  })

  it('refuses to be a general-purpose shell', () => {
    // T-A3. The children are the stub `fugaro`, the `wslpath` query and the
    // `sh -c` wrapper the plan writes -- and nothing else, because a double that
    // runs anything makes every assertion about *what was run* meaningless. The
    // mutation this catches: drop the check and run whatever was asked for -- the
    // command's own output appears and the status goes to zero.
    const { wsl } = install(freshDirectory())

    const other = run(wsl, ['-d', 'Ubuntu', '--', '/bin/echo', 'this-is-not-the-stub'])
    expect(other.status).not.toBe(0)
    expect(other.stdout).toBe('')
    expect(other.stderr).toContain('/bin/echo')
    expect(wsl.children()).toHaveLength(0)
    expect(only(wsl.reports()).kind).toBe('refused')

    // A shell that is not the wrapper's own shape, and a wrapper with no script,
    // are refused for the same reason: `sh -c <script>` is the one shape the plan
    // writes (T-F4), and anything else is a console this double does not model.
    expect(run(wsl, ['-d', 'Ubuntu', '--', 'bash', '-c', 'echo hi']).status).not.toBe(0)
    expect(run(wsl, ['-d', 'Ubuntu', '--', 'sh', '-x', 'echo hi']).status).not.toBe(0)
    expect(run(wsl, ['-d', 'Ubuntu', '--', 'sh', '-c']).status).not.toBe(0)

    // The flags it does not model, and the shapes it does not model, are refused
    // for the same reason: `--terminate` is refused outright by the design
    // (D6(b)), and a missing `--` or a missing `-d` is a console that did not say
    // what it wanted.
    expect(run(wsl, ['--terminate', 'Ubuntu']).status).not.toBe(0)
    expect(run(wsl, ['-d', 'Ubuntu', FUGARO_BINARY_NAME]).status).not.toBe(0)
    expect(run(wsl, ['--', FUGARO_BINARY_NAME]).status).not.toBe(0)
    expect(wsl.children()).toHaveLength(0)
    expect(wsl.reports()).toHaveLength(7)
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

  it('models a wsl.exe that cannot answer at all', () => {
    // T-C4's `absent` outcome: WSL is not installed, or the feature is off, and
    // `wsl.exe` fails instead of listing nothing. A double that could only answer
    // would leave that classification untestable -- and "no distributions" and
    // "no WSL" are different states on the screen.
    const failure = { status: 1, stderr: 'Windows Subsystem for Linux has no installed distributions.\r\n' }
    const { wsl } = install(freshDirectory(), { distros: { ...FIXTURE, queryFailure: failure } })

    const list = run(wsl, ['--list', '--quiet'])
    expect(list.status).toBe(failure.status)
    expect(list.stdout).toBe('')
    expect(list.stderr).toBe(failure.stderr)
    expect(run(wsl, ['--status']).status).toBe(failure.status)

    // What the console asked for is a fact either way, so the argv is recorded
    // and the failure is not a silent one.
    expect(wsl.reports().map((hop) => hop.argv)).toEqual([['--list', '--quiet'], ['--status']])
    expect(wsl.children()).toHaveLength(0)
  })

  it('answers wslpath in both directions from the table, and refuses a path it does not carry', () => {
    // T-D2, and the reason the table is the test's own: the console asks
    // `wslpath` rather than computing a mapping, because a computed one is right
    // for a `C:` drive path and wrong for every custom mount (D4(b)). The
    // mutation this catches: answer the Windows spelling back, or guess a
    // `/mnt/c/...` prefix -- the round trip and the refusal both go wrong.
    const { wsl } = install(freshDirectory())

    const toLinux = run(wsl, ['-d', 'Ubuntu', '--', 'wslpath', '-a', '-u', WINDOWS_SRC])
    expect(toLinux.status).toBe(0)
    expect(toLinux.stdout).toBe(`${LINUX_SRC}\n`)

    const toWindows = run(wsl, ['-d', 'Ubuntu', '--', 'wslpath', '-a', '-w', '/home/dimi/src'])
    expect(toWindows.status).toBe(0)
    expect(toWindows.stdout).toBe(`${UNC_SRC}\n`)

    // A translation is a query inside the distribution: no CLI ran, and the
    // report carries the *arguments* the console sent, not only the answer.
    expect(wsl.children()).toHaveLength(0)
    expect(wsl.childCalls()).toBe(0)
    const [first, second] = wsl.reports()
    expect(first?.kind).toBe('translate')
    expect(first?.argv).toEqual(['-d', 'Ubuntu', '--', 'wslpath', '-a', '-u', WINDOWS_SRC])
    expect(first?.wslpath).toEqual({ direction: 'toLinux', path: WINDOWS_SRC, answer: LINUX_SRC })
    expect(second?.wslpath).toEqual({ direction: 'toWindows', path: '/home/dimi/src', answer: UNC_SRC })

    // A path the table does not carry is refused, with the path named: the
    // console has to state the miss rather than invent a mapping.
    const miss = run(wsl, ['-d', 'Ubuntu', '--', 'wslpath', '-a', '-u', 'D:\\elsewhere'])
    expect(miss.status).not.toBe(0)
    expect(miss.stdout).toBe('')
    expect(miss.stderr).toContain('D:\\elsewhere')
    expect(wsl.reports()[2]?.wslpath?.answer).toBeNull()

    // And the shapes it does not model are refused rather than guessed at: a
    // translation whose direction it cannot tell is one it must not answer.
    expect(run(wsl, ['-d', 'Ubuntu', '--', 'wslpath', '-a', '/home/dimi/src']).status).not.toBe(0)
    expect(run(wsl, ['-d', 'Ubuntu', '--', 'wslpath', '-a', '-u', '-w', '/home/dimi/src']).status).not.toBe(0)
    expect(run(wsl, ['-d', 'Ubuntu', '--', 'wslpath', '-a', '-u']).status).not.toBe(0)
    expect(run(wsl, ['-d', 'Ubuntu', '--', 'wslpath', '-z', '-u', '/home/dimi/src']).status).not.toBe(0)
  })

  it('runs the sh -c wrapper the way the distribution would, pid and all', async () => {
    // T-F4: the CLI runs inside `wsl.exe -d <d> -- sh -c 'echo $$ > <file>; exec
    // fugaro …'`, which is how the pid inside the distribution is captured at
    // all (D6(a)). The double's job is that the wrapper really runs and that the
    // CLI inside it is the stub: the pid's *identity* is `exec`'s property, and
    // whether a killed `wsl.exe` kills the Linux child is U5's, which no test on
    // this machine settles.
    const root = freshDirectory()
    const pidFile = join(root, 'wrapper.pid')
    const { wsl, linux } = install(root, { scenario: { commands: { version: { hold: true } } } })
    const script = `echo $$ > ${pidFile}; exec ${FUGARO_BINARY_NAME} version`
    const child = spawn(wsl.binary, ['-d', 'Ubuntu', '--cd', linux, '--', 'sh', '-c', script], {
      cwd: wsl.directory,
      env: HOST_ENV,
      stdio: 'ignore',
    })

    const pid = await waitForNumber(pidFile)
    await waitFor('the CLI inside the wrapper to start', () => wsl.children().length === 1)
    // The pid the wrapper wrote is a *live* process -- the one that `exec`ed the
    // CLI -- which is what makes it the pid the console has to kill.
    expect(() => process.kill(pid, 0)).not.toThrow()

    process.kill(pid, 'SIGKILL')
    const [code, signal] = (await once(child, 'exit')) as [number | null, NodeJS.Signals | null]
    // A killed child is a killed `wsl.exe` rather than a successful one (T-F1).
    expect(signal).toBe('SIGKILL')
    expect(code).toBeNull()

    const hop = only(wsl.reports())
    expect(hop.kind).toBe('spawn')
    expect(hop.wrapper).toBe(script)
    expect(hop.cd).toBe(linux)
    // The wrapper's `PATH` is the distribution's, with the directory the double
    // installed the stub in front of it -- a real distribution has the CLI on
    // its own `PATH`, and `exec fugaro` inside the wrapper is what needs it.
    expect(hop.childEnv['PATH']).toBe(`${join(wsl.directory, DISTRO_DIRECTORY)}:${DISTRO_PATH}`)

    const grandchild = only(wsl.children())
    expect(grandchild.argv).toEqual(['version'])
    expect(grandchild.cwd).toBe(linux)
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

  it('reads the stub fugaro as the child hop, not the shared report file', () => {
    // The two hops share one file, so the handle named after the child has to
    // answer with the child's lines: a `stub.reports()` that returned the
    // `wsl.exe` hop too would be the one place the separation this file is built
    // around does not hold. The mutation this catches: hand back the raw stub.
    const { wsl, linux } = install(freshDirectory())
    run(wsl, ['-d', 'Ubuntu', '--cd', linux, '--', FUGARO_BINARY_NAME, 'version'])
    run(wsl, ['--list', '--quiet'])

    expect(wsl.reports()).toHaveLength(2)
    expect(wsl.children()).toHaveLength(1)
    expect(wsl.stub.reports()).toEqual(wsl.children())
    expect(wsl.stub.calls()).toBe(1)
    expect(wsl.stub.calls()).toBe(wsl.childCalls())
    expect(wsl.stub.reports().every((line) => !('hop' in line))).toBe(true)
    // Its `reportPath` is still the shared file: that is where its child writes.
    expect(wsl.stub.reportPath).toBe(wsl.reportPath)
  })

  it('carries its parsing into the double rather than a second copy', () => {
    // The generated script is the double's whole program, and it runs the five
    // exported functions' own source (the arrangement `stub.ts` uses for
    // `commandKey`). A second, inlined copy of the rules is exactly the defect a
    // review found in the stub, so it is asserted against rather than trusted.
    const { wsl } = install(freshDirectory())
    const script = readFileSync(join(wsl.directory, 'wsl-stub.mjs'), 'utf8')
    for (const declaration of [
      'function parseWslArgv(argv)',
      'function isWindowsSpelling(path)',
      'function parseWslpathArgv(argv)',
      'function translatePath(direction, path, pairs)',
      'function childEnvironmentFor(env, keys, distroPath)',
    ]) {
      expect(script).toContain(declaration)
      const name = declaration.slice('function '.length, declaration.indexOf('('))
      expect(script.match(new RegExp(`function ${name}\\(`, 'g')) ?? []).toHaveLength(1)
    }
    expect(script).toContain('const parsed = parseWslArgv(argv)')
  })
})
