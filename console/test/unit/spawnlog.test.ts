// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The spawn log, held to the schema Task 4.3's Check 59 reads (desktop-console
// plan Task 1.3, design section 5.1 rule 10, D20).
//
// The file is written against the *schema* rather than against the
// implementation: the four keys, the mode, the bound, and the two prohibitions
// (never read back, never transmitted) are the contract, and each is a test that
// a mutation can break:
//
//   - a writer that logs unconditionally fails "unset means nothing on disk",
//     which asserts on the injected `fs` as well as on the filesystem -- the
//     mutation the plan names is a writer that opens the path whatever the
//     environment says, and it must fail *here*;
//   - a line carrying an extra `env` (or `output`, or `pid`) field fails "writes
//     exactly the four keys", whose checker is itself tested against a planted
//     line that has five, so the check is not vacuous;
//   - a writer that appends past 1000 fails "stops at the bound";
//   - a `ts` with milliseconds fails the schema test, because Check 59's FACT
//     lines compare second-precision strings;
//   - a reader of the log anywhere in `src/`, or the log's path in a URL, a
//     fetch or an IPC payload, fails the two scans at the bottom.

import { mkdtempSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { readdirSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import { SPAWN_LOG_ENV, SPAWN_LOG_LIMIT, SPAWN_LOG_MODE, TRUNCATION_LINE, createSpawnLog, rfc3339Seconds } from '../../src/main/spawnlog'
import type { SpawnLogFs } from '../../src/main/spawnlog'
import { planFor, spawnedArgv } from '../../src/main/spawn'
import { CliSource, createInstallationStore } from '../../src/source/cli'
import { installStub } from './stub'

const SOURCE_ROOT = fileURLToPath(new URL('../../src', import.meta.url))
const PROJECT = 'fugaro'

function freshDirectory(): string {
  return mkdtempSync(join(tmpdir(), 'fugaro-spawnlog-'))
}

/** recordingFs is `node:fs` as this module uses it, with every call kept. It is
 * how "unset means nothing on disk" is asserted about the *writer* rather than
 * about a file the test hoped would not appear. */
function recordingFs(): SpawnLogFs & {
  readonly opened: string[]
  readonly written: string[]
  readonly closed: number[]
} {
  const opened: string[] = []
  const written: string[] = []
  const closed: number[] = []
  let next = 100
  const fdPaths = new Map<number, string>()
  return {
    opened,
    written,
    closed,
    open(path: string, flags: string, mode: number): number {
      opened.push(`${path}:${flags}:${mode.toString(8)}`)
      next += 1
      fdPaths.set(next, path)
      return next
    },
    write(fd: number, data: string): void {
      written.push(data)
    },
    close(fd: number): void {
      closed.push(fd)
    },
  }
}

/** lineKeys is the key set of one line, sorted -- the shape Check 59's `jq`
 * reads. It is a function so the checker can be tested against a planted line
 * before it is trusted with the real ones. */
function lineKeys(line: string): readonly string[] {
  const parsed = JSON.parse(line) as Record<string, unknown>
  return Object.keys(parsed).sort()
}

/** spawnRecord is what a caller records: the three values the writer does not
 * stamp itself. */
function spawnRecord(project = PROJECT): {
  readonly argv: readonly string[]
  readonly cwd: string
  readonly project: string
} {
  return { argv: ['/opt/homebrew/bin/fugaro', 'watch', '--json', '--project', project], cwd: '/Users/owner/src/fugaro', project }
}

describe('the spawn log', () => {
  it('unset means nothing on disk: no path, no descriptor, no file', () => {
    const fs = recordingFs()
    const directory = freshDirectory()
    for (const env of [{}, { [SPAWN_LOG_ENV]: '' }]) {
      const writer = createSpawnLog(env, fs)
      expect(writer.enabled).toBe(false)
      expect(writer.path).toBeNull()
      writer.record(spawnRecord())
      writer.record(spawnRecord('other'))
      expect(writer.records).toBe(0)
      writer.close()
    }
    expect(fs.opened).toEqual([])
    expect(fs.written).toEqual([])
    expect(readdirSync(directory)).toEqual([])
  })

  it('a full session of spawns writes no file anywhere when it is unset', async () => {
    const directory = freshDirectory()
    const stub = installStub(directory, {
      commands: { version: { stdout: '1.4.2\n' }, doctor: { stdout: '{}\n' }, 'budget show': { stdout: '{}\n' } },
    })
    const before = readdirSync(directory).sort()
    const source = await CliSource.create({ target: { project: PROJECT }, configuredBinaryPath: stub.binary, appEnv: {} })
    await source.environmentReport({})
    await source.showBudget({})
    await source.close()
    // The only thing that appeared is the stub's own report file -- the channel
    // these tests read, written by the fake CLI and not by the console. The
    // version handshake, doctor and budget show left nothing else behind, and in
    // particular no file the console chose a path for.
    const after = readdirSync(directory).sort()
    expect(after.filter((entry) => !before.includes(entry))).toEqual(['report.jsonl'])
    expect(before).not.toContain('report.jsonl')
  })

  it('when it is set, it opens the operator’s path once, append-only, mode 0600', () => {
    const fs = recordingFs()
    const path = '/tmp/operator-chose-this.jsonl'
    const writer = createSpawnLog({ [SPAWN_LOG_ENV]: path }, fs)
    expect(writer.enabled).toBe(true)
    expect(writer.path).toBe(path)
    writer.record(spawnRecord())
    writer.record(spawnRecord())
    // Once: a descriptor per record would be a leak per spawn.
    expect(fs.opened).toEqual([`${path}:a:600`])
    expect(SPAWN_LOG_MODE).toBe(0o600)
    writer.close()
    expect(fs.closed).toHaveLength(1)
  })

  it('stays closed once it is closed: a later record neither reopens nor appends', () => {
    // Review round 2 of PR 478: close() released the descriptor but left the
    // writer live, so the next record() reopened the path the caller had just
    // closed and appended to it.
    const fs = recordingFs()
    const writer = createSpawnLog({ [SPAWN_LOG_ENV]: '/tmp/operator-chose-this.jsonl' }, fs)
    writer.record(spawnRecord())
    writer.close()
    writer.record(spawnRecord())
    expect(fs.opened).toHaveLength(1)
    expect(fs.written).toHaveLength(1)
    expect(writer.records).toBe(1)
    expect(fs.closed).toHaveLength(1)
    // ...and a writer closed before it ever opened never opens either.
    const unused = recordingFs()
    const early = createSpawnLog({ [SPAWN_LOG_ENV]: '/tmp/operator-chose-this.jsonl' }, unused)
    early.close()
    early.record(spawnRecord())
    expect(unused.opened).toEqual([])
  })

  it('an installation check closes the log it opened, so each add leaks no descriptor', async () => {
    // `verifyCandidate` opens its own log when it is not handed one, and at
    // da86190 never closed it: one descriptor per installation added, held
    // until the process exits. Counted on /dev/fd, which macOS and Linux both
    // have; a leak is one descriptor per add, so ten adds are well clear of
    // whatever the test runner opens meanwhile.
    const directory = freshDirectory()
    const logPath = join(directory, 'spawn.jsonl')
    const stub = installStub(directory, { commands: { version: { stdout: '1.4.2\n' }, doctor: { stdout: '{}\n' } } })
    const store = createInstallationStore({ appEnv: { [SPAWN_LOG_ENV]: logPath } })
    await store.add({ project: PROJECT, binaryPath: stub.binary })
    const before = readdirSync('/dev/fd').length
    for (let i = 0; i < 10; i += 1) await store.add({ project: PROJECT, binaryPath: stub.binary })
    const after = readdirSync('/dev/fd').length
    expect(after - before).toBeLessThan(5)
    // The log was written all the same: the handshake and doctor, per add.
    expect(readFileSync(logPath, 'utf8').split('\n').filter((line) => line !== '')).toHaveLength(22)
  })

  it('creates the file with mode 0600 and one line per spawn', async () => {
    const directory = freshDirectory()
    const logPath = join(directory, 'spawn.jsonl')
    const stub = installStub(directory, {
      commands: { version: { stdout: '1.4.2\n' }, doctor: { stdout: '{}\n' }, 'runs ls': { stdout: '{}\n' } },
    })
    const source = await CliSource.create({
      target: { project: PROJECT },
      configuredBinaryPath: stub.binary,
      appEnv: { [SPAWN_LOG_ENV]: logPath },
    })
    await source.environmentReport({})
    await source.listRuns({})
    await source.close()

    const lines = readFileSync(logPath, 'utf8').split('\n').filter((line) => line !== '')
    // The handshake, doctor and runs ls.
    expect(lines).toHaveLength(3)
    expect(statSync(logPath).mode & 0o777).toBe(SPAWN_LOG_MODE)
  })

  it('writes exactly the four keys, checked against a planted fifth', () => {
    const seen: string[] = []
    const capture = createSpawnLog({ [SPAWN_LOG_ENV]: '/tmp/unused.jsonl' }, {
      open: () => 1,
      write: (_fd, data) => seen.push(data.trim()),
      close: () => undefined,
    })
    capture.record(spawnRecord())
    expect(seen).toHaveLength(1)
    expect(lineKeys(seen[0] ?? '')).toEqual(['argv', 'cwd', 'project', 'ts'])

    // The checker, against a line that carries one field too many: an `env` is
    // exactly the mutation the plan names, and it must be caught by *this*
    // assertion rather than by a reviewer noticing.
    const planted = JSON.stringify({
      ts: '2026-10-15T14:22:31Z',
      argv: ['/opt/homebrew/bin/fugaro'],
      cwd: '/tmp',
      project: PROJECT,
      env: { GOOGLE_APPLICATION_CREDENTIALS: '/tmp/creds.json' },
    })
    expect(lineKeys(planted)).not.toEqual(['argv', 'cwd', 'project', 'ts'])
  })

  it('stamps ts as RFC 3339 UTC at second precision', () => {
    const seen: string[] = []
    const writer = createSpawnLog({ [SPAWN_LOG_ENV]: '/tmp/unused.jsonl' }, {
      open: () => 1,
      write: (_fd, data) => seen.push(data.trim()),
      close: () => undefined,
    })
    writer.record(spawnRecord())
    const entry = JSON.parse(seen[0] ?? '{}') as { ts: string }
    expect(entry.ts).toBe(rfc3339Seconds(new Date()))
    expect(entry.ts).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/)
    expect(entry.ts).not.toContain('.')
  })

  it('records the argv, the cwd and the project the spawn actually used', async () => {
    const directory = freshDirectory()
    const logPath = join(directory, 'spawn.jsonl')
    const stub = installStub(directory, {
      commands: { version: { stdout: '1.4.2\n' }, 'runs ls': { stdout: '{}\n' } },
    })
    const source = await CliSource.create({
      target: { project: 'chosen' },
      configuredBinaryPath: stub.binary,
      appEnv: { [SPAWN_LOG_ENV]: logPath },
    })
    await source.listRuns({})
    await source.close()

    const entries = readFileSync(logPath, 'utf8')
      .split('\n')
      .filter((line) => line !== '')
      .map((line) => JSON.parse(line) as { argv: string[]; cwd: string; project: string })
    const handshake = entries[0]
    const runs = entries[1]
    expect(handshake?.argv).toEqual([stub.binary, 'version'])
    expect(runs?.argv).toEqual([stub.binary, 'runs', 'ls', '--json', '--project', 'chosen'])
    // ...and the project is recorded for the bare handshake too, which is what
    // the check's `sort -u` over `.project` depends on.
    expect(handshake?.project).toBe('chosen')
    expect(handshake?.cwd).toBe(stub.reports()[0]?.cwd)
    expect(new Set(entries.map((entry) => entry.project))).toEqual(new Set(['chosen']))
  })

  it('is closed by the source that opened it, and only by that one', async () => {
    // Review round 3 of PR 478: `verifyCandidate` closed only the log it opened,
    // but `CliSource.close()` closed an injected one too -- a caller's writer
    // ended under it. The ownership is now the same in both.
    const directory = freshDirectory()
    const stub = installStub(directory, {
      commands: { version: { stdout: '1.4.2\n' }, 'runs ls': { stdout: '{}\n' } },
    })
    const lines: string[] = []
    let closes = 0
    const injected = createSpawnLog({ [SPAWN_LOG_ENV]: '/tmp/unused.jsonl' }, {
      open: () => 1,
      write: (_fd, data) => lines.push(data),
      close: () => {
        closes += 1
      },
    })
    const source = await CliSource.create({
      target: { project: PROJECT },
      configuredBinaryPath: stub.binary,
      appEnv: {},
      spawnLog: injected,
    })
    await source.listRuns({})
    await source.close()
    expect(closes).toBe(0)
    expect(lines).toHaveLength(2)
    // The caller's writer is still live after the source is gone.
    await (await CliSource.create({
      target: { project: PROJECT },
      configuredBinaryPath: stub.binary,
      appEnv: {},
      spawnLog: injected,
    })).close()
    expect(lines).toHaveLength(3)
    injected.close()
    expect(closes).toBe(1)
  })

  it('stops at the bound: one truncation line at record 1001, then nothing', () => {
    const seen: string[] = []
    const writer = createSpawnLog({ [SPAWN_LOG_ENV]: '/tmp/unused.jsonl' }, {
      open: () => 1,
      write: (_fd, data) => seen.push(data.trim()),
      close: () => undefined,
    })
    for (let index = 0; index < SPAWN_LOG_LIMIT + 5; index += 1) writer.record(spawnRecord())
    expect(writer.records).toBe(SPAWN_LOG_LIMIT)
    expect(seen).toHaveLength(SPAWN_LOG_LIMIT + 1)
    expect(seen[SPAWN_LOG_LIMIT]).toBe(TRUNCATION_LINE)
    expect(TRUNCATION_LINE).toBe('{"truncated":true,"limit":1000}')
    // The truncation line is a statement about the file, not a record, and it is
    // written once: a second one would make the file's last line ambiguous.
    expect(seen.filter((line) => line.includes('truncated'))).toHaveLength(1)
    // The operator's earlier lines are never touched: nothing here rewrites the
    // file, so the only writing this writer can do is appending.
    expect(seen.slice(0, SPAWN_LOG_LIMIT).every((line) => lineKeys(line).join(',') === 'argv,cwd,project,ts')).toBe(true)
  })

  it('holds the bound per process, so a second writer starts its own count', () => {
    const seen: string[] = []
    const make = (): ReturnType<typeof createSpawnLog> =>
      createSpawnLog({ [SPAWN_LOG_ENV]: '/tmp/unused.jsonl' }, {
        open: () => 1,
        write: (_fd, data) => seen.push(data.trim()),
        close: () => undefined,
      })
    const first = make()
    for (let index = 0; index < SPAWN_LOG_LIMIT + 1; index += 1) first.record(spawnRecord())
    const second = make()
    second.record(spawnRecord())
    expect(seen.filter((line) => line.includes('truncated'))).toHaveLength(1)
    expect(lineKeys(seen.at(-1) ?? '')).toEqual(['argv', 'cwd', 'project', 'ts'])
  })

  it('never throws when the log cannot be written, and remembers why', () => {
    const writer = createSpawnLog({ [SPAWN_LOG_ENV]: '/nowhere/at/all.jsonl' }, {
      open: () => {
        throw new Error('EACCES: permission denied')
      },
      write: () => undefined,
      close: () => undefined,
    })
    expect(() => {
      writer.record(spawnRecord())
    }).not.toThrow()
    expect(writer.records).toBe(0)
    expect(writer.error).toContain('EACCES')
    expect(() => {
      writer.close()
    }).not.toThrow()
  })

  it('appends to a file that already has the operator’s earlier lines', () => {
    const directory = freshDirectory()
    const logPath = join(directory, 'spawn.jsonl')
    writeFileSync(logPath, '{"ts":"2020-01-01T00:00:00Z","argv":["old"],"cwd":"/tmp","project":"old"}\n')
    const writer = createSpawnLog({ [SPAWN_LOG_ENV]: logPath })
    writer.record(spawnRecord())
    writer.close()
    const lines = readFileSync(logPath, 'utf8').split('\n').filter((line) => line !== '')
    expect(lines).toHaveLength(2)
    expect(lines[0]).toContain('"old"')
  })
})

describe('what the log must never become', () => {
  const codeOnly = (relative: string): string =>
    readFileSync(join(SOURCE_ROOT, relative), 'utf8')
      .replace(/\/\*[\s\S]*?\*\//g, '')
      .replace(/^\s*\/\/.*$/gm, '')

  it('is never read back: the writer is the only file that names its variable', () => {
    // The scan is over every file under src/, so a screen, a source or an IPC
    // handler that started reading the log would fail here by name rather than
    // by a reviewer noticing that a file grew a `readFileSync`.
    const files = sourceFiles()
    const namers = files.filter((file) => codeOnly(file).includes(SPAWN_LOG_ENV))
    expect(namers).toEqual(['main/spawnlog.ts'])
    const writer = codeOnly('main/spawnlog.ts')
    expect(writer).not.toMatch(/readFileSync|readFile\(|createReadStream|readdirSync/)
    expect(writer).not.toMatch(/writeFileSync|unlinkSync|rmSync|renameSync|truncateSync/)
    expect(writer).toContain("open(path, 'a'")
    // There is no reader, no rotator and no deleter: `open` with 'a' is the only
    // way this module touches a path.
    expect(writer.match(/openSync\(/g) ?? []).toHaveLength(1)
  })

  it('is never transmitted, and the log path reaches no request', () => {
    const writer = codeOnly('main/spawnlog.ts')
    expect(writer).not.toMatch(/fetch\(|https?:\/\/|new URL\(|WebSocket|net\.|request\(/)
    // ...and nothing else in src/ knows the variable or the path: the log cannot
    // be put on the wire by a file that never learns where it is.
    for (const file of sourceFiles().filter((name) => name !== 'main/spawnlog.ts')) {
      expect(codeOnly(file), file).not.toContain('SPAWN_LOG_ENV')
      expect(codeOnly(file), file).not.toContain('FUGARO_CONSOLE_SPAWN_LOG')
    }
  })

  it('holds no path attribute or argv value from the app’s environment', () => {
    const writer = codeOnly('main/spawnlog.ts')
    expect(writer).not.toMatch(/process\.env/)
    // The environment arrives as an argument (`createSpawnLog(appEnv, ...)`),
    // so the module cannot read a variable the caller did not hand it.
    expect(writer).toContain('createSpawnLog(')
  })
})

/** sourceFiles is every `.ts` file under `console/src/`, relative to it. */
function sourceFiles(directory = SOURCE_ROOT, prefix = ''): string[] {
  const out: string[] = []
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const relative = prefix === '' ? entry.name : `${prefix}/${entry.name}`
    if (entry.isDirectory()) out.push(...sourceFiles(join(directory, entry.name), relative))
    else if (entry.name.endsWith('.ts')) out.push(relative)
  }
  return out.sort()
}

describe('the spawn plan the log records', () => {
  it('is the same argv that was handed to the spawner', async () => {
    const directory = freshDirectory()
    const logPath = join(directory, 'spawn.jsonl')
    const stub = installStub(directory, {
      commands: { version: { stdout: '1.4.2\n' }, 'budget show': { stdout: '{}\n' } },
    })
    const plan = planFor('budget.show', { repo: 'acme/widgets' }, {
      target: { project: PROJECT },
      binaryPath: stub.binary,
      directory,
      appEnv: {},
    })
    const writer = createSpawnLog({ [SPAWN_LOG_ENV]: logPath })
    writer.record({ argv: plan.argv, cwd: plan.cwd, project: plan.project })
    writer.close()
    const line = JSON.parse(readFileSync(logPath, 'utf8').trim()) as { argv: string[]; cwd: string }
    expect(line.argv).toEqual([...plan.argv])
    expect(line.cwd).toBe(plan.cwd)
  })

  it('is the whole command line on the WSL route, and unchanged on the host one', () => {
    // T-C1 of docs/console-windows/implementation-plan.md, decision D1(a): the
    // plan carries the program and the wrapper's words apart from the CLI's
    // argv, and the log records `spawnedArgv(plan)` -- the command, the
    // wrapper's words, then the CLI's argv. On the host route that is exactly
    // `plan.argv`, so every line Check 59a already reads is byte-identical; on
    // the WSL route it is the command line Task Manager shows, which is the
    // point of recording it at all.
    const host = planFor('budget.show', {}, {
      target: { project: PROJECT },
      binaryPath: '/opt/homebrew/bin/fugaro',
      directory: '/Users/owner/src/fugaro',
      appEnv: {},
    })
    expect([...spawnedArgv(host)]).toEqual([...host.argv])
    expect([...spawnedArgv(host)][0]).toBe('/opt/homebrew/bin/fugaro')

    const wsl = {
      ...host,
      command: 'C:\\Windows\\System32\\wsl.exe',
      prefix: ['-d', 'Ubuntu', '--cd', '/home/dimi', '-e'],
      argv: ['fugaro', 'budget', 'show', '--json', '--project', PROJECT],
    }
    expect([...spawnedArgv(wsl)]).toEqual([
      'C:\\Windows\\System32\\wsl.exe',
      '-d',
      'Ubuntu',
      '--cd',
      '/home/dimi',
      '-e',
      'fugaro',
      'budget',
      'show',
      '--json',
      '--project',
      PROJECT,
    ])
  })

  it('records the shell that carried the CLI, because that is what ran', () => {
    // T-F3/T-F4 of docs/console-windows/implementation-plan.md. On the WSL route
    // the program the OS starts is `wsl.exe` and the program *inside* the
    // distribution is a `sh -c` script (D6(a)): the log is the operator's record
    // of what ran (D1), so it says the shell, and the CLI's own words are inside
    // the script's `exec` line -- quoted, which is how they reach the
    // distribution. A log that printed the CLI's argv beside the script would
    // describe a command line that never existed.
    const wsl = planFor('budget.show', {}, {
      target: { project: PROJECT },
      binaryPath: '',
      directory: '/Users/owner/AppData/Local/Temp/fugaro-console/cwd',
      appEnv: {},
      wsl: {
        distro: 'Ubuntu',
        directory: '/home/dimi/src/fugaro',
        executable: 'C:\\Windows\\System32\\wsl.exe',
      },
    })
    const line = [...spawnedArgv(wsl)]
    expect(line.slice(0, 6)).toEqual([
      'C:\\Windows\\System32\\wsl.exe',
      '-d',
      'Ubuntu',
      '--cd',
      '/home/dimi/src/fugaro',
      '-e',
    ])
    expect(line.slice(6, 8)).toEqual(['sh', '-c'])
    expect(line).toHaveLength(9)
    expect(line[8]).toContain(`exec 'fugaro' 'budget' 'show' '--json' '--project' '${PROJECT}'`)
    // The CLI's words are not logged beside the script as well: they are in it.
    expect(line.filter((word) => word === 'budget')).toHaveLength(0)
  })
})
