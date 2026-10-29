// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
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

import type { SpawnLogFs, SpawnLogWriter } from '../../src/main/spawnlog'
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
    // No child existed, so no line was recorded and the lazily opened log was
    // never opened (review round 2 of PR 514: the line used to be written
    // before the spawn). Whatever was opened is closed.
    expect(counts.opened).toBe(0)
    expect(counts.closed).toBe(counts.opened)
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

describe('a log the caller injected', () => {
  it('is left open by a live feed that ends, so the next child still gets its line', async () => {
    // Review round 5 of PR 478: `startSidecar`'s `finish()` closed the writer it
    // was handed, so the first live feed that ended ended the log for every
    // child after it -- three spawns, two lines, against design section 5.1 rule
    // 10's "one line per child process it spawns". The sidecar never opens a
    // log, so it is never the closer: `CliSource.close()` is, and only when the
    // source opened it (`#ownsLog`), which this test holds from the other side.
    const stub = installStub(directory(), {
      commands: {
        version: { stdout: '1.4.2\n' },
        // A watch that refuses: exit 1 ends the feed without a restart, which is
        // the case that used to close the log.
        watch: { stdout: '{"day":"2026-10-15"}\n', stderr: 'no budget backend\n', code: 1 },
        'runs ls': { stdout: '{}\n' },
      },
    })
    let records = 0
    let closes = 0
    const injected: SpawnLogWriter = {
      enabled: true,
      path: '/nonexistent/spawn.jsonl',
      get records(): number {
        return records
      },
      error: null,
      record: () => {
        records += 1
      },
      close: () => {
        closes += 1
      },
    }
    const source = await CliSource.create({
      target: { project: PROJECT },
      configuredBinaryPath: stub.binary,
      appEnv: ENV,
      spawnLog: injected,
    })
    // The version probe at `create` is the first line.
    expect(records).toBe(1)

    const feed = source.subscribeLive({})
    await feed.stop()
    expect(closes).toBe(0)

    await source.listRuns({})
    expect(records).toBe(3)

    await source.close()
    // Still the caller's writer to close, after everything the source does.
    expect(closes).toBe(0)
    injected.close()
    expect(closes).toBe(1)
  })
})
