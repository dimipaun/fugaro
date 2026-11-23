// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The lapse's second ask, where a write really starts (console-improvements plan
// I-OPS; the design is `docs/lapse-read-only/spec.md`, D436 and T12).
//
// **What this file holds, and why it is a file of its own.** `handlers.ts` builds
// the thirteen rows and hands the flows that spawn a child **after a window's own
// yes** -- the models write, the image write and the setup wizard -- a `lapsed`
// reader. The gate on the row is not what stops those children:
// `lapseHandlers`' `refusedAfterLapse` column takes the operation away from the
// renderer, while the yes arrives from the window's main frame with no IPC
// handler on the path at all (`modelsadd.ts` says so at the field). This file
// drives that path end to end -- a plan, a window, a yes -- and holds `deps.lapsed`
// to being asked there, with the installation's own project.
//
// **Why the floor is supplied here.** All three flows are behind a release floor
// no CLI release carries yet (`MODELS_ADD_CLI_FLOOR` and `IMAGE_ADD_CLI_FLOOR` are
// `null`), and a plan below its floor refuses before it spawns the dry run -- so
// the window, and the yes that reaches `runApply`, are unreachable from outside
// today. The mock below supplies only an **unset** floor, with a release this
// case's fake CLI is above: a real constant is returned as it is, so the case can
// never weaken a floor a release carries, and `ops-contract.test.ts` still holds
// the real constants as they are.

import { describe, expect, it, vi } from 'vitest'

vi.mock('../../src/shared/version', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../src/shared/version')>()
  return {
    ...actual,
    MODELS_ADD_CLI_FLOOR: actual.MODELS_ADD_CLI_FLOOR ?? '0.1.0',
    IMAGE_ADD_CLI_FLOOR: actual.IMAGE_ADD_CLI_FLOOR ?? '0.1.0',
  }
})
import { chmodSync, mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { newOperationHandlers } from '../../src/main/handlers'
import type { NewOperationDeps, NewOperationHandlerName } from '../../src/main/handlers'
import { LAPSE_REFUSAL } from '../../src/main/licence-state'
import type { ChildSpawner, SpawnedChildLike } from '../../src/main/spawn'
import { IMAGE_ADD_COMMAND } from '../../src/main/imagesview'
import type { ConfirmSubject, LaunchConfirmer } from '../../src/main/launch'
import type { BoundInstallation } from '../../src/source/cli'
import { itHostRoute } from '../support/hostRoute'

/** VERSION is the version line the resolved binary's probe answers, above the
 * two floors this file supplies and so above every floor in the tree. */
const VERSION = '0.30.0'

/** PROJECT is the bound installation's own project -- the one every `lapsed`
 * reader below must be handed, and the one a reader built with `() => false`
 * would never name. */
const PROJECT = 'fugaro-dev'
const INSTALLATION: BoundInstallation = { id: 'inst-1', project: PROJECT }

const ID = 'deepseek/deepseek-v4.1-flash'
const ROUTE = 'openrouter'
const NAME = 'app'
const DOCKERFILE = 'acme/app:.fugaro/app.Dockerfile'

/** aModelsDocument is the dry run's document as MD1 writes it
 * (`schemas/models-add-plan.schema.json`), copied from the flow's own test: the
 * version, the layer and the file the change lands in, the model and its route,
 * the three nested records and the steps. */
function aModelsDocument(): string {
  return `${JSON.stringify({
    version: 1,
    layer: 'local',
    path: `/home/ana/.config/fugaro/${PROJECT}.yaml`,
    model: ID,
    route: ROUTE,
    changed: true,
    claim: { key: `providers.${ROUTE}.models`, entry: ID, claimed_by: '' },
    allowed_models: { key: 'budget.allowed_models', entry: ID, present: true },
    prices: { key: `model_prices.${ID}`, input_per_m: 0.14, output_per_m: 0.28, source: 'typed' },
    steps: [
      { name: 'publish', state: 'saved' },
      { name: 'redeploy', state: 'saved' },
    ],
    refusal: '',
  })}\n`
}

/** anImageDocument is MD2's document, the entry the add would append to the
 * installation's own project. */
function anImageDocument(): string {
  return `${JSON.stringify({
    version: 1,
    command: IMAGE_ADD_COMMAND,
    project: PROJECT,
    entry: {
      name: NAME,
      tags: ['node', 'redis'],
      description: 'the app image',
      default: false,
      source: `dockerfile ${DOCKERFILE}`,
    },
    steps: [
      { name: 'catalog-build', saved: true },
      { name: 'repository', saved: true },
      { name: 'policy', saved: true },
    ],
  })}\n`
}

/** aChild is one child whose stdout and close this file drives on the next
 * tick, once the flow has subscribed -- the ordering `runToCompletion` needs,
 * and the same shape the two flows' own tests use. */
function aChild(stdout: string): SpawnedChildLike {
  const readers: ((chunk: Buffer | string) => void)[] = []
  const closes: ((code: number | null, signal: string | null) => void)[] = []
  const child: SpawnedChildLike = {
    stdout: { on: (_event, listener) => readers.push(listener) },
    stderr: { on: () => undefined },
    on: ((event: string, listener: (...args: never[]) => void) => {
      if (event === 'spawn') (listener as () => void)()
      else if (event === 'close') {
        closes.push(listener as (code: number | null, signal: string | null) => void)
      }
      return undefined
    }) as SpawnedChildLike['on'],
    kill: () => true,
  }
  setImmediate(() => {
    for (const listener of readers) listener(stdout)
    for (const listener of closes) listener(0, null)
  })
  return child
}

/**
 * aCli is the spawner the resolution and both flows share, and it is one queue:
 * the first call is the version probe, the next is the dry run, and any call
 * after that is a write that should not have happened -- which is why `calls`
 * is what the cases assert, rather than only what the child answered.
 */
function aCli(documents: readonly string[]): {
  readonly spawner: ChildSpawner
  readonly calls: () => number
} {
  let calls = 0
  const spawner = (() => {
    const answer = calls === 0 ? `${VERSION}\n` : (documents[calls - 1] ?? 'wrote it\n')
    calls += 1
    return aChild(answer)
  }) as ChildSpawner
  return { spawner, calls: () => calls }
}

/** flush lets the promises a decided window started settle: the apply's child,
 * its close and the record it leaves behind. */
async function flush(times = 6): Promise<void> {
  for (let round = 0; round < times; round += 1) await new Promise((resolve) => setImmediate(resolve))
}

/** Confirmed is one window that opened, and the yes its own frame can send. */
interface Confirmed {
  readonly subject: ConfirmSubject
  decide(yes: boolean): void
}

/** aConfirmer records the windows the flows open and hands the case their own
 * `decide`, which is the path no IPC handler is on. */
function aConfirmer(): { readonly confirmer: LaunchConfirmer; readonly opened: Confirmed[] } {
  const opened: Confirmed[] = []
  const confirmer: LaunchConfirmer = {
    open: (subject, decide) => {
      opened.push({ subject, decide })
    },
    close: () => undefined,
  }
  return { confirmer, opened }
}

/** Run is one bench: the deps, and the three things a case watches. */
interface Run {
  readonly rows: ReturnType<typeof newOperationHandlers>['rows']
  /** how many children were started, whichever flow started them. */
  calls(): number
  readonly opened: Confirmed[]
  /** the project each `lapsed` question was asked with, in order. */
  readonly seen: string[]
}

/**
 * aRun builds the thirteen rows over one bound installation whose binary is a
 * scratch file this file creates, so the resolution walks nothing on the
 * machine that runs it, and a spawner that answers the probe first and the
 * documents after.
 */
function aRun(options: { readonly lapsed: () => boolean; readonly documents: readonly string[] }): Run {
  const directory = mkdtempSync(join(tmpdir(), 'fugaro-lapse-wiring-'))
  const binary = join(directory, 'fugaro')
  writeFileSync(binary, '#!/bin/sh\n')
  chmodSync(binary, 0o755)
  const { spawner, calls } = aCli(options.documents)
  const { confirmer, opened } = aConfirmer()
  const seen: string[] = []
  const deps: NewOperationDeps = {
    binding: () => ({ ...INSTALLATION, binaryPath: binary }),
    confirmer: () => confirmer,
    settingsEditor: () => {
      throw new Error('settings.editor.open is not one of the flows under test')
    },
    lapsed: (project) => {
      seen.push(project)
      return options.lapsed()
    },
    appEnv: {},
    spawner,
  }
  return { rows: newOperationHandlers(deps).rows, calls, opened, seen }
}

const MODELS_PLAN = { id: ID, route: ROUTE }
const IMAGE_PLAN = { name: NAME, dockerfile: DOCKERFILE }

describe('the lapse is asked at the window’s own yes, with the installation’s project', () => {
  itHostRoute('stops the models write, and reads the licence per apply', async () => {
    let lapsed = true
    const run = aRun({ lapsed: () => lapsed, documents: [aModelsDocument(), aModelsDocument()] })

    // **The plan reaches its window with the licence unread.** The dry run is
    // not the write, and a console that asked the licence at plan time would
    // take away the screen the operator reads before deciding.
    expect(await run.rows['models.add.plan'](MODELS_PLAN)).toStrictEqual({
      ok: true,
      sentence: 'the plan is waiting for your answer in its window',
    })
    expect(run.calls(), 'the probe and the dry run').toBe(2)
    expect(run.seen, 'the plan asks no licence').toEqual([])
    expect(run.opened).toHaveLength(1)

    // **The yes is what asks it** -- no IPC handler is on that path -- and it is
    // handed the installation's own project, which is the project whose key is
    // judged.
    run.opened[0]?.decide(true)
    await flush()
    expect(run.seen, 'the installation’s own project, and not the empty no-installation one').toEqual([
      PROJECT,
    ])
    expect(run.calls(), 'a lapsed console spawns nothing for the write').toBe(2)
    expect(await run.rows['models.add'](MODELS_PLAN)).toStrictEqual({
      ok: false,
      sentence: LAPSE_REFUSAL,
    })

    // **The answer is read per apply and not captured once**, which is why the
    // reader is a reader: a renewal pasted during the session gives the write
    // back at the next window's yes.
    lapsed = false
    expect(await run.rows['models.add.plan'](MODELS_PLAN)).toStrictEqual({
      ok: true,
      sentence: 'the plan is waiting for your answer in its window',
    })
    expect(run.calls(), 'the second plan’s dry run').toBe(3)
    run.opened[1]?.decide(true)
    await flush()
    expect(run.seen).toEqual([PROJECT, PROJECT])
    expect(run.calls(), 'a renewed licence lets the write spawn').toBe(4)
    expect(await run.rows['models.add'](MODELS_PLAN)).toStrictEqual({ ok: true, sentence: 'wrote it' })
  })

  itHostRoute('stops the image write the same way', async () => {
    const run = aRun({ lapsed: () => true, documents: [anImageDocument()] })
    expect(await run.rows['image.add.plan'](IMAGE_PLAN)).toStrictEqual({
      ok: true,
      sentence: 'the plan is waiting for your answer in its window',
    })
    expect(run.calls()).toBe(2)
    expect(run.seen).toEqual([])
    run.opened[0]?.decide(true)
    await flush()
    expect(run.seen, 'the image flow asks the same reader with the same project').toEqual([PROJECT])
    expect(run.calls()).toBe(2)
    expect(await run.rows['image.add'](IMAGE_PLAN)).toStrictEqual({
      ok: false,
      sentence: LAPSE_REFUSAL,
    })
  })
})
