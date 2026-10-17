// The spawn log a `CliSource` opens is a descriptor the source owns, and every
// way out of the source closes it (review round 4 of PR 478).
//
// `CliSource.create` opens its own log when it is not handed one, so the test
// cannot inject the writer's `fs` through an option -- that would make the log
// the caller's, which the source must never close. Instead `createSpawnLog` is
// wrapped, once for this file, so the writer the source builds for itself runs
// over a counting `fs` and never touches the disk. Each test then holds one
// door:
//
//   - `create` failing because the binary's `version` is not a version (the
//     resolution is not ok) closes the log it opened;
//   - `create` failing because the spawn itself throws closes it too;
//   - `close()` on a live source closes it, exactly once.

import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { SpawnLogFs } from '../../src/main/spawnlog'
import { SPAWN_LOG_ENV } from '../../src/main/spawnlog'
import { CliSource } from '../../src/source/cli'
import type { ChildSpawner } from '../../src/main/spawn'
import { installStub } from './stub'

const counts = vi.hoisted(() => ({ opened: 0, closed: 0 }))

vi.mock('../../src/main/spawnlog', async (importOriginal) => {
  const real = await importOriginal<typeof import('../../src/main/spawnlog')>()
  const counting: SpawnLogFs = {
    open: () => {
      counts.opened += 1
      return 100 + counts.opened
    },
    write: () => undefined,
    close: () => {
      counts.closed += 1
    },
  }
  return {
    ...real,
    createSpawnLog: (appEnv: Readonly<Record<string, string | undefined>>) => real.createSpawnLog(appEnv, counting),
  }
})

const PROJECT = 'fugaro'
const ENV = { [SPAWN_LOG_ENV]: '/nonexistent/spawn.jsonl' }

function directory(): string {
  return mkdtempSync(join(tmpdir(), 'fugaro-cli-log-'))
}

describe('the spawn log a source opens for itself', () => {
  beforeEach(() => {
    counts.opened = 0
    counts.closed = 0
  })

  it('is closed when create refuses a binary whose version is not one', async () => {
    const stub = installStub(directory(), { commands: { version: { stdout: 'not a version\n', code: 1 } } })
    await expect(
      CliSource.create({ target: { project: PROJECT }, configuredBinaryPath: stub.binary, appEnv: ENV }),
    ).rejects.toThrow()
    expect(counts.opened).toBe(1)
    expect(counts.closed).toBe(1)
  })

  it('is closed when the spawn behind create throws', async () => {
    const stub = installStub(directory(), { commands: { version: { stdout: '1.4.2\n' } } })
    const spawner = ((): never => {
      throw new Error('spawn failed before a child existed')
    }) as unknown as ChildSpawner
    await expect(
      CliSource.create({ target: { project: PROJECT }, configuredBinaryPath: stub.binary, appEnv: ENV, spawner }),
    ).rejects.toThrow('spawn failed before a child existed')
    expect(counts.opened).toBe(1)
    expect(counts.closed).toBe(1)
  })

  it('is closed by close(), once', async () => {
    const stub = installStub(directory(), {
      commands: { version: { stdout: '1.4.2\n' }, 'runs ls': { stdout: '{}\n' } },
    })
    const source = await CliSource.create({ target: { project: PROJECT }, configuredBinaryPath: stub.binary, appEnv: ENV })
    await source.listRuns({})
    expect(counts.opened).toBe(1)
    expect(counts.closed).toBe(0)
    await source.close()
    expect(counts.closed).toBe(1)
  })
})
