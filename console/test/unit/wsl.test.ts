// The WSL seam's own module (docs/console-windows/implementation-plan.md Part C,
// T-C4; SECURITY-SENSITIVE, the credential boundary's near side).
//
// Two subjects, and they are the two halves of "what does this machine have?":
//
//   - **where `wsl.exe` is** (`wslExecutable`), which is a path this console
//     *executes*, so it is computed from a checked constant and never from
//     whatever the app's environment happens to say;
//   - **what it answers** (`wslDistributions`), which is the one source of truth
//     about the distributions this machine has -- the picker (Part G) and the
//     resolver (T-C5) both read it, so they cannot disagree.
//
// **Every case here is driven through the fake `wsl.exe`** (T-A1,
// `test/unit/wslstub.ts`), spawned as a real process and read back out of the
// report file. The claim the report carries is the one that matters and the one
// the console cannot make about itself: what the *fake* received -- its argv, its
// cwd, its environment -- rather than what the console believes it passed.
//
// The four answers the module has to tell apart, and the case for each:
//
//   | `wsl.exe` does | `wslDistributions` answers | case below |
//   |---|---|---|
//   | lists two, names one default | `listed`, both, one marked | "returns both, in the order ..." |
//   | lists two, names no default | `listed`, both, **none** marked | "marks nothing when --status names no default" |
//   | answers nothing (non-zero) | `absent`, with its sentence | "answers absent, not an empty list ..." |
//   | lists none | `listed`, empty | "answers listed and empty when WSL is there and has none" |
//
// The third and the fourth are the pair this module exists for: a console that
// read "WSL is not installed" as "no distributions" would send the operator
// looking for a distribution to install when the missing thing is WSL itself.

import { mkdirSync, mkdtempSync, realpathSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { CHILD_ENV_KEYS, FUGARO_BINARY_NAME, childPath, nodeSpawner } from '../../src/main/spawn'
import type { RecordedSpawn, SpawnLogWriter } from '../../src/main/spawnlog'
import {
  WSL_EXE_NAME,
  WSL_QUERY_TIMEOUT_MS,
  WSL_SYSTEM_ROOT,
  parseDefaultDistribution,
  parseDistributionList,
  createWslPathCache,
  wslDistributions,
  wslExecutable,
  wslpath,
} from '../../src/main/wsl'
import { installWslStub } from './wslstub'
import type { WslFixture, WslPathPair, WslStub, WslStubOptions } from './wslstub'

/** SENTINELS are the variables a hostile environment would carry into the
 * distribution if the boundary were one line thinner. Each is a name the cloud
 * SDKs read, plus one that is not a child key at all: the fake's report carries
 * the *keys* the console passed, so a leak is visible without anything having to
 * be printed. */
const SENTINELS: Readonly<Record<string, string>> = Object.freeze({
  GOOGLE_APPLICATION_CREDENTIALS: '/home/dimi/.config/gcloud/adc.json',
  AWS_SECRET_ACCESS_KEY: 'not-a-real-secret',
  ANTHROPIC_API_KEY: 'not-a-real-secret',
  NOT_A_CHILD_KEY: 'planted-on-the-host',
})

/** APP_ENV is the environment the console is launched with: the sentinels, and
 * the four variables `childEnvironment` is allowed to pass through. */
const APP_ENV: Readonly<Record<string, string | undefined>> = Object.freeze({
  ...SENTINELS,
  HOME: '/home/dimi',
  LANG: 'en_US.UTF-8',
  PATH: 'C:\\Windows\\System32;C:\\Windows',
})

/** TWO_DISTROS is a `wsl.exe` that has two distributions and names one of them
 * the default, with the `\r\n` endings a Windows build writes. */
const TWO_DISTROS: WslFixture = {
  list: 'Ubuntu\r\nDebian\r\n',
  status: 'Default Distribution: Debian\r\nDefault Version: 2\r\n\r\nWSL version: 2.6.1.0\r\n',
}

/** NO_WSL is a `wsl.exe` that cannot answer: the feature is off, or no
 * distribution is registered. Both queries exit non-zero with a sentence. */
const NO_WSL: WslFixture = {
  list: '',
  status: '',
  queryFailure: {
    status: 1,
    stderr: 'Windows Subsystem for Linux has no installed distributions.\r\n',
  },
}

/** freshDirectory is a temporary directory this test owns, resolved because the
 * child reports `process.cwd()` and macOS reaches `/tmp` through a link. */
function freshDirectory(): string {
  return realpathSync(mkdtempSync(join(tmpdir(), 'fugaro-console-wsl-')))
}

/** install is the arrangement the cases start from: a fake `wsl.exe` in a
 * directory of this test's own, and a directory a query can run in. */
function install(fixture: WslFixture, options: Partial<WslStubOptions> = {}): {
  wsl: WslStub
  cwd: string
} {
  const root = freshDirectory()
  const cwd = join(root, 'cwd')
  mkdirSync(cwd, { recursive: true })
  return { wsl: installWslStub(join(root, 'windows'), { distros: fixture, ...options }), cwd }
}

/** recordingLog is a `SpawnLogWriter` that keeps its lines, so a test can assert
 * what the console would have written without a file. */
function recordingLog(): { readonly writer: SpawnLogWriter; readonly lines: readonly RecordedSpawn[] } {
  const lines: RecordedSpawn[] = []
  return {
    lines,
    writer: {
      enabled: true,
      path: '/dev/null',
      get records(): number {
        return lines.length
      },
      error: null,
      record(spawn: RecordedSpawn): void {
        lines.push(spawn)
      },
      close: () => undefined,
    },
  }
}

/** only is the one entry a list is expected to hold, so "the fake recorded
 * nothing" fails here rather than making every later assertion vacuous. */
function only<T>(entries: readonly T[]): T {
  expect(entries).toHaveLength(1)
  const [entry] = entries
  if (entry === undefined) throw new Error('the fake recorded nothing')
  return entry
}

describe('where wsl.exe is', () => {
  it('is the Windows component directory, absolute, and not the app’s PATH', () => {
    // The whole reason the console names it absolutely: design section 3.4 rule
    // 4 -- the app's inherited `PATH` is not a place anything is installed. A
    // console that spawned the bare name `wsl.exe` would run whatever the
    // operator's environment put first, and a hostile `PATH` would decide which
    // program this console starts for every read on the machine.
    expect(wslExecutable({})).toBe('C:\\Windows\\System32\\wsl.exe')
    expect(wslExecutable({ PATH: 'C:\\evil' })).toBe('C:\\Windows\\System32\\wsl.exe')
    expect(wslExecutable({})).toBe(`${WSL_SYSTEM_ROOT}\\System32\\${WSL_EXE_NAME}`)
    // `%SystemRoot%` is honoured, because a Windows installation is allowed to
    // live elsewhere, and it is the only variable consulted.
    expect(wslExecutable({ SystemRoot: 'D:\\Windows' })).toBe('D:\\Windows\\System32\\wsl.exe')
    expect(wslExecutable({ windir: 'D:\\Win' })).toBe('D:\\Win\\System32\\wsl.exe')
  })

  it('uses the default rather than a value that is not a Windows directory', () => {
    // It is a path this console *executes*, so the environment's answer is
    // checked rather than trusted: anything that is not one plain drive-rooted
    // directory name is not the Windows directory, and the constant is used
    // instead. A relative path, a UNC path, a `..`, a forward slash, a NUL and a
    // second level are each refused -- "the app runs what the environment names"
    // is the property the rest of this module's callers exist to avoid.
    for (const hostile of [
      '',
      'Windows',
      '\\\\host\\share',
      'C:/Windows',
      'C:\\Windows\\System32',
      'C:\\Windows\\..\\..\\evil',
      '..\\Windows',
      'C:\\Wind ows\\evil',
      'C:\\Windows\u0000\\evil',
      'C:\\Windows\\evil*',
    ]) {
      expect(wslExecutable({ SystemRoot: hostile }), hostile).toBe('C:\\Windows\\System32\\wsl.exe')
    }
  })
})

describe('the distribution list', () => {
  it('returns both, in the order wsl.exe gave them, with the default marked', async () => {
    const { wsl, cwd } = install(TWO_DISTROS)
    const outcome = await wslDistributions({ appEnv: APP_ENV, cwd, wslPath: wsl.binary })
    expect(outcome.kind).toBe('listed')
    if (outcome.kind !== 'listed') return
    // The order is `wsl.exe`'s and not a sort: it is the order the operator's own
    // `wsl -l` shows, and re-ordering it would be the console presenting a
    // machine state nobody has.
    expect(outcome.distributions.map((row) => row.name)).toEqual(['Ubuntu', 'Debian'])
    expect(outcome.distributions.map((row) => row.isDefault)).toEqual([false, true])
    // Both queries were asked, and neither started a CLI: a query is a question
    // to `wsl.exe`, not a read of the distribution.
    expect(wsl.reports().map((report) => report.kind)).toEqual(['query', 'query'])
    expect(wsl.calls()).toBe(2)
    expect(wsl.childCalls()).toBe(0)
  })

  it('marks nothing when --status names no default, so the caller must ask', async () => {
    // The state the picker draws as a question rather than as a choice. A
    // console that marked the first row would be choosing a distribution the
    // operator never chose, and the choice is the one thing Part G asks for.
    for (const status of ['Default Distribution: (none)\r\n', 'Default Version: 2\r\n', '']) {
      const { wsl, cwd } = install({ ...TWO_DISTROS, status })
      const outcome = await wslDistributions({ appEnv: APP_ENV, cwd, wslPath: wsl.binary })
      expect(outcome.kind, status).toBe('listed')
      if (outcome.kind !== 'listed') continue
      expect(outcome.distributions.map((row) => row.name), status).toEqual(['Ubuntu', 'Debian'])
      expect(outcome.distributions.some((row) => row.isDefault), status).toBe(false)
    }
  })

  it('marks nothing when --status names a distribution --list did not report', async () => {
    // The two answers disagreeing is a real machine state -- a distribution
    // removed, or a stale `--status` -- and neither answer is the console's to
    // prefer: marking a name it cannot resolve would offer the operator a
    // default it cannot run.
    const { wsl, cwd } = install({ ...TWO_DISTROS, status: 'Default Distribution: Fedora\r\n' })
    const outcome = await wslDistributions({ appEnv: APP_ENV, cwd, wslPath: wsl.binary })
    expect(outcome.kind).toBe('listed')
    if (outcome.kind !== 'listed') return
    expect(outcome.distributions.some((row) => row.isDefault)).toBe(false)
  })

  it('answers absent, not an empty list, when wsl.exe cannot answer at all', async () => {
    // The failure this module exists to prevent: "Windows Subsystem for Linux
    // has no installed distributions" read as "the list is empty" sends the
    // operator looking for a distribution to install, when the thing that is
    // missing is WSL itself. `absent` is the absence of an answer, and the
    // sentence is `wsl.exe`'s own so the screen can show it.
    const { wsl, cwd } = install(NO_WSL)
    const outcome = await wslDistributions({ appEnv: APP_ENV, cwd, wslPath: wsl.binary })
    expect(outcome.kind).toBe('absent')
    if (outcome.kind !== 'absent') return
    expect(outcome.reason).toContain('no installed distributions')
    expect(outcome.reason).toContain('--list --quiet')
    // `--status` was never asked: there was nothing to mark, and a second
    // invocation that could only fail is a second process started for nothing.
    expect(wsl.calls()).toBe(1)
    // the fake's report carries its own argv, which is the words after the
    // program: `--list --quiet` and nothing else
    expect(only(wsl.reports()).argv).toEqual(['--list', '--quiet'])
  })

  it('answers absent when wsl.exe is not there to run at all', async () => {
    // The other half of "cannot answer": the program itself is missing. It is
    // the same outcome and a different sentence, because the operator's next
    // step is the same one -- install WSL -- and the console says what it tried.
    const { cwd } = install(TWO_DISTROS)
    const outcome = await wslDistributions({
      appEnv: APP_ENV,
      cwd,
      wslPath: join(cwd, 'there-is-no-wsl-here.exe'),
    })
    expect(outcome.kind).toBe('absent')
    if (outcome.kind !== 'absent') return
    expect(outcome.reason).toContain('did not answer')
  })

  it('answers listed and empty when WSL is there and has none', async () => {
    // The third state, and the one that must not be confused with `absent`: WSL
    // answered, and the answer is "none". Part G draws it as its own page --
    // install a distribution -- which is a different instruction from install
    // WSL.
    const { wsl, cwd } = install({ list: '', status: 'Default Distribution: (none)\r\n' })
    const outcome = await wslDistributions({ appEnv: APP_ENV, cwd, wslPath: wsl.binary })
    expect(outcome.kind).toBe('listed')
    if (outcome.kind !== 'listed') return
    expect(outcome.distributions).toEqual([])
  })

  it('answers listed when --list answered and --status did not', async () => {
    // The default is a detail of the list, not the answer to the question that
    // was asked: an operator whose `--status` is broken still has distributions,
    // and hiding them behind an `absent` would be a console refusing to show a
    // machine state it had in hand.
    const { wsl, cwd } = install(TWO_DISTROS)
    const outcome = await wslDistributions({
      appEnv: APP_ENV,
      cwd,
      wslPath: wsl.binary,
      spawner: (command, args, options) => {
        // The second invocation is the one that fails: `--list` is answered by
        // the fake, `--status` is not answered at all.
        if (args[0] === '--status') throw new Error('this wsl.exe cannot answer --status')
        return nodeSpawner(command, args, options)
      },
    })
    expect(outcome.kind).toBe('listed')
    if (outcome.kind !== 'listed') return
    expect(outcome.distributions.map((row) => row.name)).toEqual(['Ubuntu', 'Debian'])
    expect(outcome.distributions.some((row) => row.isDefault)).toBe(false)
  })
})

describe('the query is a spawn like any other', () => {
  it('hands wsl.exe the child’s allow-list and nothing else', async () => {
    // SECURITY-SENSITIVE: the credential boundary's near side. The environment
    // built for a query is `childEnvironment`'s -- the same eleven keys the CLI
    // gets -- so a query is not a hole in the allow-list. The fake's report
    // carries the keys the console passed it, which is the only account of this
    // that is not the console's own word.
    const { wsl, cwd } = install(TWO_DISTROS)
    await wslDistributions({ appEnv: APP_ENV, cwd, wslPath: wsl.binary })
    // macOS adds __CF_USER_TEXT_ENCODING to every process it starts (as in
    // spawn.test.ts and wslstub.test.ts): the platform's own key, not a leak.
    const platformKeys = process.platform === 'darwin' ? ['__CF_USER_TEXT_ENCODING'] : []
    for (const report of wsl.reports()) {
      for (const key of report.envKeys.filter((k) => !platformKeys.includes(k))) {
        expect(CHILD_ENV_KEYS, `${key} is not a key the child may receive`).toContain(key)
      }
      for (const planted of Object.keys(SENTINELS)) {
        expect(report.envKeys, `${planted} crossed into wsl.exe`).not.toContain(planted)
        expect(report.env[planted], `${planted} crossed into wsl.exe`).toBeUndefined()
      }
      // and the four that are allowed through are, so the filter is a filter and
      // not an empty environment
      expect(report.env['HOME']).toBe('/home/dimi')
      expect(report.env['LANG']).toBe('en_US.UTF-8')
    }
  })

  it('builds the query’s PATH from the win32 row, which is Linux’s, and names the CLI bare', async () => {
    // F5 on the near side: the process on the far side of `wsl.exe` is a Linux
    // process, so the tool directories are Linux's (`WIN32_LOCATIONS`, T-C2) --
    // and the binary the `PATH` is built for is the CLI's **bare name**, because
    // the console does not know where inside the distribution the CLI is. A
    // `.`-prefixed `PATH` is what `dirname` of a bare name would produce, and a
    // `.` entry means the child's own working directory.
    const { wsl, cwd } = install(TWO_DISTROS)
    await wslDistributions({ appEnv: APP_ENV, cwd, wslPath: wsl.binary })
    for (const report of wsl.reports()) {
      expect(report.path).toBe(childPath(FUGARO_BINARY_NAME, 'win32'))
      expect(report.path.split(':')).not.toContain('.')
      expect(report.path).toContain('/usr/bin')
      expect(report.path).not.toContain('\\')
      expect(report.path).not.toContain('C:')
    }
  })

  it('runs the query in the directory it was given, never the app’s own', async () => {
    // Rule 5's "never the app's own working directory", applied to the child
    // that is not the CLI: the fake reports its own `process.cwd()`, which is
    // the console's choice arriving on the other side.
    const { wsl, cwd } = install(TWO_DISTROS)
    await wslDistributions({ appEnv: APP_ENV, cwd, wslPath: wsl.binary })
    for (const report of wsl.reports()) expect(report.cwd).toBe(cwd)
  })

  it('records the query in the spawn log, wrapper and all', async () => {
    // The spawn log's rule is one line per child process the app starts
    // (`spawnlog.ts`), and a query is a child process. A query that bypassed the
    // log would be the one child the app ran that its own audit does not have.
    const { wsl, cwd } = install(TWO_DISTROS)
    const log = recordingLog()
    await wslDistributions({ appEnv: APP_ENV, cwd, wslPath: wsl.binary, log: log.writer })
    expect(log.lines.map((line) => line.argv)).toEqual([
      [wsl.binary, '--list', '--quiet'],
      [wsl.binary, '--status'],
    ])
    for (const line of log.lines) {
      expect(line.cwd).toBe(cwd)
      // The picker's query has no installation, and the log says so with an
      // empty project rather than with a name from somewhere else.
      expect(line.project).toBe('')
    }
  })

  it('stops a query that does not answer within its deadline', async () => {
    // A `wsl.exe` that never answers is a state (U15: a cold-starting
    // distribution is slow), and the deadline is what keeps it from being a
    // screen that waits forever. The budget is the module's own and it is longer
    // than the version handshake's, because a cold start is seconds.
    expect(WSL_QUERY_TIMEOUT_MS).toBeGreaterThan(5000)
    const { wsl, cwd } = install(TWO_DISTROS)
    const outcome = await wslDistributions({
      appEnv: APP_ENV,
      cwd,
      wslPath: wsl.binary,
      timeoutMs: 1,
      spawner: (command, args, options) =>
        // A child that exists and never speaks: the deadline is what settles it,
        // not the child.
        nodeSpawner(command === wsl.binary ? '/bin/sh' : command, ['-c', 'sleep 30'], options),
    })
    expect(outcome.kind).toBe('absent')
    if (outcome.kind !== 'absent') return
    expect(outcome.reason).toContain('did not answer')
  })
})

describe('reading the two answers', () => {
  it('reads the list from either line ending, with or without a BOM', () => {
    // The bytes are the test's vector, and both endings are real: a Windows
    // build writes `\r\n`, a WSL_UTF8 answer writes `\n`, and a UTF-8 BOM is
    // what some builds put in front of either. Blank lines are not names.
    expect(parseDistributionList('Ubuntu\r\nDebian\r\n')).toEqual(['Ubuntu', 'Debian'])
    expect(parseDistributionList('Ubuntu\nDebian\n')).toEqual(['Ubuntu', 'Debian'])
    expect(parseDistributionList('\uFEFFUbuntu\r\nDebian')).toEqual(['Ubuntu', 'Debian'])
    expect(parseDistributionList('Ubuntu\r\n\r\nDebian\r\n\r\n')).toEqual(['Ubuntu', 'Debian'])
    expect(parseDistributionList('  Ubuntu  \r\n')).toEqual(['Ubuntu'])
    expect(parseDistributionList('')).toEqual([])
  })

  it('refuses an answer that is not UTF-8 text rather than reading half of it', () => {
    // A UTF-16LE answer -- the encoding U9 leaves open, and the one T-F2 adds
    // the decoder for -- arrives as NUL-interleaved bytes. Half a name is worse
    // than no name: the console would run a CLI in a distribution whose name it
    // invented, and the distribution it meant is not the one it would name.
    expect(parseDistributionList('U\u0000b\u0000u\u0000n\u0000t\u0000u\u0000')).toBeNull()
    expect(parseDistributionList('\uFFFEU\u0000b\u0000')).toBeNull()
    // and the refusal is the outcome's, not a name's
    expect(parseDistributionList('Ubuntu\u0000\r\nDebian\r\n')).toBeNull()
  })

  it('reads the default from --status, and answers null when it names none', () => {
    expect(parseDefaultDistribution('Default Distribution: Ubuntu\r\nDefault Version: 2\r\n')).toBe('Ubuntu')
    expect(parseDefaultDistribution('Default Distribution:Ubuntu\n')).toBe('Ubuntu')
    expect(parseDefaultDistribution('  Default Distribution: Ubuntu\r\n')).toBeNull()
    expect(parseDefaultDistribution('Default Distribution: (none)\r\n')).toBeNull()
    expect(parseDefaultDistribution('Default Distribution:\r\n')).toBeNull()
    expect(parseDefaultDistribution('Default Version: 2\r\n')).toBeNull()
    expect(parseDefaultDistribution('')).toBeNull()
    // The name is data and is taken verbatim: a distribution may be called
    // anything, and trimming is the only thing done to it.
    expect(parseDefaultDistribution('Default Distribution: Ubuntu-22.04 LTS\r\n')).toBe('Ubuntu-22.04 LTS')
  })
})

// ---------------------------------------------------------------------------
// wslpath: the translation, both directions (Part D, T-D2, decision D4(a))
// ---------------------------------------------------------------------------

describe('translating a path with wslpath', () => {
  /** PATHS is the test's own table, and the double derives nothing: a mapping
   * computed in the console is right for `C:\Users\X` and wrong for every custom
   * mount (D4(b)), so the console asks and this is the only source of answers. */
  const PATHS: readonly WslPathPair[] = Object.freeze([
    { windows: 'C:\\Users\\dimi\\src', linux: '/mnt/c/Users/dimi/src' },
    { windows: '\\\\wsl.localhost\\Ubuntu\\home\\dimi\\src', linux: '/home/dimi/src' },
  ])
  const WINDOWS_PATH = 'C:\\Users\\dimi\\src'
  const LINUX_PATH = '/home/dimi/src'

  function arrangement(paths: readonly WslPathPair[] = PATHS): { wsl: WslStub; cwd: string } {
    return install(TWO_DISTROS, { paths })
  }

  it('reads a Windows spelling and answers the distribution’s, in one call inside it', async () => {
    // D4(a): one `wsl.exe -d <distro> -- wslpath -a -u <path>` per distinct
    // path, through the ordinary spawn path. The fake's report is the account of
    // what the distribution was asked -- the console's own plan object would
    // only be its self-consistency.
    const { wsl, cwd } = arrangement()
    const answer = await wslpath({
      direction: 'toLinux',
      path: WINDOWS_PATH,
      distro: 'Ubuntu',
      appEnv: APP_ENV,
      cwd,
      wslPath: wsl.binary,
    })
    expect(answer).toEqual({ ok: true, path: '/mnt/c/Users/dimi/src' })
    const report = only(wsl.reports())
    expect(report.kind).toBe('translate')
    expect(report.distro).toBe('Ubuntu')
    // the distribution is named, `--` ends `wsl.exe`'s own words, and the far
    // side's argv is `wslpath` and nothing else.
    expect([...report.argv.slice(0, 3)]).toEqual(['-d', 'Ubuntu', '--'])
    expect([...report.argv.slice(3)]).toEqual(['wslpath', '-a', '-u', WINDOWS_PATH])
    expect(report.cwd).toBe(cwd)
    // ...and the translation is recorded as a translation, with the answer the
    // table gave, so a test can read both what was asked and what came back.
    expect(report.wslpath).toEqual({ direction: 'toLinux', path: WINDOWS_PATH, answer: '/mnt/c/Users/dimi/src' })
    // No `--cd`: a translation of a path cannot be run *in* a path the console
    // has not translated yet.
    expect(report.cd).toBeNull()
    expect(wsl.childCalls()).toBe(0)
  })

  it('carries the same allow-list as any other child, on the translation’s own invocation', async () => {
    // SECURITY-SENSITIVE: the credential boundary's near side, on the third
    // shape of query this module makes. A translation is a new invocation of
    // `wsl.exe`, and a new invocation is a new place for the boundary to be one
    // line thinner: the fake's report carries the keys the console passed, so a
    // leak is visible without anything having to be printed.
    const { wsl, cwd } = arrangement()
    await wslpath({
      direction: 'toLinux',
      path: WINDOWS_PATH,
      distro: 'Ubuntu',
      appEnv: APP_ENV,
      cwd,
      wslPath: wsl.binary,
    })
    const report = only(wsl.reports())
    const platformKeys = process.platform === 'darwin' ? ['__CF_USER_TEXT_ENCODING'] : []
    for (const key of report.envKeys.filter((k) => !platformKeys.includes(k))) {
      expect(CHILD_ENV_KEYS, `${key} is not a key the child may receive`).toContain(key)
    }
    for (const planted of Object.keys(SENTINELS)) {
      expect(report.envKeys, `${planted} crossed into wsl.exe`).not.toContain(planted)
      expect(report.env[planted], `${planted} crossed into wsl.exe`).toBeUndefined()
    }
    expect(report.env['HOME']).toBe('/home/dimi')
  })

  it('reads the distribution’s spelling and answers Windows’ own, in the other direction', async () => {
    const { wsl, cwd } = arrangement()
    const answer = await wslpath({
      direction: 'toWindows',
      path: LINUX_PATH,
      distro: 'Ubuntu',
      appEnv: APP_ENV,
      cwd,
      wslPath: wsl.binary,
    })
    // The UNC spelling, because that is the one Windows opens for a path inside
    // a distribution -- and it is the *distribution's* answer rather than one
    // this console composed (`wslUncPath` exists for the plan, not for this).
    expect(answer).toEqual({ ok: true, path: '\\\\wsl.localhost\\Ubuntu\\home\\dimi\\src' })
    const report = only(wsl.reports())
    expect([...report.argv.slice(3)]).toEqual(['wslpath', '-a', '-w', LINUX_PATH])
    expect(report.wslpath?.direction).toBe('toWindows')
  })

  it('refuses a path the distribution cannot translate, and says which path', async () => {
    // The half that makes the rest honest: a mapping computed from `C:` would
    // answer here, and answer wrongly. The distribution's own refusal is the
    // answer, and it names the path so the operator can see which one failed.
    const { wsl, cwd } = arrangement()
    const answer = await wslpath({
      direction: 'toLinux',
      path: 'C:\\Users\\dimi\\elsewhere',
      distro: 'Ubuntu',
      appEnv: APP_ENV,
      cwd,
      wslPath: wsl.binary,
    })
    expect(answer.ok).toBe(false)
    if (answer.ok) return
    expect(answer.reason).toContain('C:\\Users\\dimi\\elsewhere')
    expect(answer.reason).toContain('wslpath')
    // The refusal is recorded with no answer: what was asked is a fact, what
    // came back is nothing.
    const report = only(wsl.reports())
    expect(report.kind).toBe('translate')
    expect(report.wslpath).toEqual({ direction: 'toLinux', path: 'C:\\Users\\dimi\\elsewhere', answer: null })
    expect(wsl.childCalls()).toBe(0)
  })

  it('refuses an answer that is not an absolute path rather than passing it on', async () => {
    // The fake's table is the test's, so it can carry a nonsense answer -- and
    // a value that is not a path must not reach a child as a working directory.
    const { wsl, cwd } = arrangement([{ windows: 'C:\\Users\\dimi\\odd', linux: 'nonsense' }])
    const answer = await wslpath({
      direction: 'toLinux',
      path: 'C:\\Users\\dimi\\odd',
      distro: 'Ubuntu',
      appEnv: APP_ENV,
      cwd,
      wslPath: wsl.binary,
    })
    expect(answer.ok).toBe(false)
    if (!answer.ok) expect(answer.reason).toContain('not an absolute path')
  })

  it('refuses an answer Windows would not open, in the direction where it matters most', async () => {
    // The `-w` direction's whole purpose is the spelling **Windows opens**
    // (D3(a)): it is what a reveal-in-Explorer or a file dialog is handed. An
    // answer that is not an absolute Windows path -- a relative one, a bare
    // name, a distribution's own spelling coming back -- must be refused rather
    // than passed on, exactly as the `-u` direction refuses a non-POSIX answer.
    const { wsl, cwd } = arrangement([{ windows: 'odd', linux: 'odd' }])
    const answer = await wslpath({
      direction: 'toWindows',
      path: 'odd',
      distro: 'Ubuntu',
      appEnv: APP_ENV,
      cwd,
      wslPath: wsl.binary,
    })
    expect(answer.ok).toBe(false)
    if (!answer.ok) {
      expect(answer.reason).toContain('not a path Windows would open')
      expect(answer.reason).toContain('odd')
    }
    // ...and the two forms Windows accepts are accepted, so the check is a check
    // and not a wall: a drive path and a UNC path.
    const drive = arrangement([{ windows: 'C:\\Users\\dimi\\src', linux: 'src' }])
    const unc = arrangement([{ windows: '\\\\wsl.localhost\\Ubuntu\\home\\dimi\\src', linux: 'src' }])
    for (const one of [drive, unc]) {
      const taken = await wslpath({
        direction: 'toWindows',
        path: 'src',
        distro: 'Ubuntu',
        appEnv: APP_ENV,
        cwd: one.cwd,
        wslPath: one.wsl.binary,
      })
      expect(taken.ok).toBe(true)
    }
  })

  it('asks no one when no distribution was named', async () => {
    // A translation is always *into* a distribution (F7: the installation's own,
    // never a default), so a call with none starts nothing at all.
    const { wsl, cwd } = arrangement()
    const answer = await wslpath({
      direction: 'toLinux',
      path: WINDOWS_PATH,
      distro: '',
      appEnv: APP_ENV,
      cwd,
      wslPath: wsl.binary,
    })
    expect(answer.ok).toBe(false)
    expect(wsl.calls()).toBe(0)
  })

  it('asks once per path for the life of the window, and asks again after a refusal', async () => {
    // D4(a)'s cache: the same path in the same direction is one process, not one
    // process per call -- and a refusal is not kept, because the answer depends
    // on a running distribution and a mount that may have arrived.
    const { wsl, cwd } = arrangement()
    const cache = createWslPathCache({
      distro: 'Ubuntu',
      appEnv: APP_ENV,
      cwd,
      wslPath: wsl.binary,
    })
    const [first, second] = await Promise.all([cache.toLinux(WINDOWS_PATH), cache.toLinux(WINDOWS_PATH)])
    expect(first.ok && first.path).toBe('/mnt/c/Users/dimi/src')
    expect(second).toEqual(first)
    expect(wsl.calls()).toBe(1)
    expect(cache.size()).toBe(1)
    // The other direction is a different question about the same path, and it is
    // asked: the key is the pair, not the path.
    await cache.toWindows(LINUX_PATH)
    expect(wsl.calls()).toBe(2)
    expect(cache.size()).toBe(2)
    // A refusal is forgotten when it is handed out...
    const refused = await cache.toLinux('C:\\Users\\dimi\\elsewhere')
    expect(refused.ok).toBe(false)
    expect(cache.size()).toBe(2)
    // ...so the next ask is a new process rather than the sentence again.
    await cache.toLinux('C:\\Users\\dimi\\elsewhere')
    expect(wsl.calls()).toBe(4)
  })
})
