// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The models write's main-process half (the console-improvements plan, task MW1:
// D2903; `console/src/main/modelsadd.ts`, `console/src/main/modelsview.ts`).
//
// This is the only test that runs the write under plain Node: the two children a
// plan and its write spawn are a recording stub, the window is a fake
// `LaunchConfirmer`, and both command lines are built by `planFor` from **the
// real allow list** (`spawn.ts`'s `SPAWN_COMMANDS`), so nothing here reaches a
// network, a home directory or a real cloud -- and the argv this file pins is
// the argv the console would really spawn.
//
// Each case names the mutation it catches:
//
//   - a write built from **the renderer's values again** rather than from the
//     held plan fails the headline case, which compares the two children's argv
//     word for word (`plan.filter((word) => word !== '--dry-run' && word !==
//     '--json')` is the write's);
//   - a console that spawns the write **without the window's yes** fails "an
//     apply with no yes spawns nothing", which goes straight at the `models.add`
//     handler;
//   - a console that **believes the apply's payload** fails "an apply whose
//     values are not the plan's own", which sends another model ID over the
//     channel and asserts no second child;
//   - a **second plan replacing a pending one** fails "one write at a time";
//   - a console that reads a child's **stdout without its exit status** fails
//     the two cases whose child printed bytes and exited non-zero -- which is
//     what `models add` does when it refuses *after* printing the path and the
//     diff, so the screen would say the model was added when nothing was;
//   - a console that spawns **below the floor**, or with the floor still unset,
//     fails the floor cases, which assert the sentence names the command to run
//     by hand and that no child was started;
//   - a value **outside its grammar** fails the validation case, which sends it
//     over the real table (`validateArguments`) and asserts no child exists --
//     and the case that follows it pins the other half: a value spelled like a
//     flag is a value, because the model ID is the positional behind `--`;
//   - a console that reaches `models rm` or `models use` fails the never-table
//     case, which asks `isNeverReachable` about both and about this write's own
//     two operations by name.
//
// What is *not* here is the renderer's half: the Manage kinds' Add action, the
// window's own drawing of this view and the §5.1 rows are MWR's task, and the
// view this file pins is main's own (`modelsview.ts`).

import { createHash } from 'node:crypto'

import { describe, expect, it } from 'vitest'

import type {
  ChildSpawner,
  SpawnOptionsLike,
  SpawnPlan,
  SpawnedChildLike,
} from '../../src/main/spawn'
import { MODELS_ADD_OPERATION, MODELS_ADD_PLAN_OPERATION, planFor } from '../../src/main/spawn'
import type { ConfirmSubject, LaunchConfirmer } from '../../src/main/launch'
import { validateArguments } from '../../src/main/ipc'
import { NEVER_REACHABLE, isNeverReachable } from '../../src/main/ipc'
import {
  MODELS_ADD_COMMAND,
  MODELS_ADD_DISAGREES,
  MODELS_ADD_DOCUMENT_CHANGED_REFUSAL,
  MODELS_ADD_FLOOR_UNSET,
  MODELS_ADD_NO_PLAN_REFUSAL,
  MODELS_ADD_NO_YES_REFUSAL,
  MODELS_ADD_PLAN_PENDING_REFUSAL,
  MODELS_ADD_WORD,
  createModelsAdd,
  modelsAddDigest,
  modelsAddFloorSentence,
} from '../../src/main/modelsadd'
import type { ModelsAddHandlers, ModelsAddPlanner, ModelsAddValues } from '../../src/main/modelsadd'
import { LAPSE_REFUSAL } from '../../src/main/licence-state'
import {
  MODELS_ADD_DOCUMENT_SHAPE_REFUSAL,
  MODELS_ADD_DOCUMENT_VERSION_REFUSAL,
  MODELS_ADD_TICK_LABEL,
  modelsAddConfirmID,
} from '../../src/main/modelsview'
import type { ModelsAddView } from '../../src/main/modelsview'
import { SourceRefusal } from '../../src/source/source'

// ---------------------------------------------------------------------------
// The values, and the two rows
// ---------------------------------------------------------------------------

/** BINARY is the resolved CLI the plan's first word is, and WORKDIR is the
 * directory a plan's `cwd` is (neither is read by the flow: they are what a real
 * plan carries). */
const BINARY = '/usr/local/bin/fugaro'
const WORKDIR = '/tmp'

/** PROJECT is the active installation, ID, ROUTE, PRICE_IN and PRICE_OUT are one
 * well-formed set of the four values -- the model ID inside `MODEL_ID_RE`, the
 * route inside `MODEL_ROUTE_RE` and the two rates inside `MODEL_PRICE_RE`. */
const PROJECT = 'sandbox'
const ID = 'deepseek/deepseek-v4.1-flash'
const ROUTE = 'openrouter'
const PRICE_IN = '0.14'
const PRICE_OUT = '0.28'

/** aValues is the payload a renderer sends: the four values and nothing else. */
function aValues(overrides: Partial<Record<string, unknown>> = {}): Record<string, unknown> {
  return { id: ID, route: ROUTE, priceIn: PRICE_IN, priceOut: PRICE_OUT, ...overrides }
}

/** PLAN_ARGV is what the allow list's `models.add.plan` row builds for these
 * values: the command's own words, the three `=`-form values, the installation's
 * flag and the model ID behind the terminator. It is spelled out here rather
 * than computed from the plan, because the whole point of the headline case is
 * to compare a spawned argv with a **written-out** expectation.
 *
 * `spawn.test.ts` is where the row itself is pinned; what this file pins is that
 * the flow adds no word of its own to it. */
const PLAN_ARGV: readonly string[] = Object.freeze([
  BINARY,
  'models',
  'add',
  '--no-apply',
  '--dry-run',
  '--json',
  `--route=${ROUTE}`,
  `--price-in=${PRICE_IN}`,
  `--price-out=${PRICE_OUT}`,
  '--project',
  PROJECT,
  '--',
  ID,
])

/** WRITE_ARGV is the plan's own argv without `--dry-run --json`, which is what
 * `models add --no-apply` is: the console's write. */
const WRITE_ARGV: readonly string[] = Object.freeze(
  PLAN_ARGV.filter((word) => word !== '--dry-run' && word !== '--json'),
)

// ---------------------------------------------------------------------------
// The document MD1 prints, and the child that prints it
// ---------------------------------------------------------------------------

/**
 * aDocument is the dry run's document as MD1 writes it
 * (`schemas/models-add-plan.schema.json`): the version, the layer and the file
 * the change lands in, the model and its route, the three nested records and the
 * steps. The `steps` carry `saved` because main sets `--no-apply`: that is what
 * the window shows and what the write does.
 */
function aDocument(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    version: 1,
    layer: 'local',
    path: '/home/ana/.config/fugaro/sandbox.yaml',
    model: ID,
    route: ROUTE,
    changed: true,
    claim: { key: `providers.${ROUTE}.models`, entry: ID, claimed_by: '' },
    allowed_models: { key: 'budget.allowed_models', entry: ID, present: true },
    prices: {
      key: `model_prices.${ID}`,
      input_per_m: 0.14,
      output_per_m: 0.28,
      source: 'typed',
    },
    steps: [
      { name: 'publish', state: 'saved' },
      { name: 'redeploy', state: 'saved' },
    ],
    refusal: '',
    ...overrides,
  }
}

/** DOCUMENT is one document as the child's stdout: one JSON object on a line
 * (`json.NewEncoder(...).Encode`). */
function aDocumentText(overrides: Record<string, unknown> = {}): string {
  return `${JSON.stringify(aDocument(overrides))}\n`
}

// ---------------------------------------------------------------------------
// A child, and a spawner that feeds it
// ---------------------------------------------------------------------------

interface StubChild extends SpawnedChildLike {
  emitStdout(text: string): void
  emitStderr(text: string): void
  exit(code: number | null, signal: string | null): void
}

/** stubChild is one child whose stdout and exit this test drives, and whose
 * `spawn` event fires at registration -- the ordering `runToCompletion` needs. */
function stubChild(): StubChild {
  const stdout: ((chunk: Buffer | string) => void)[] = []
  const stderr: ((chunk: Buffer | string) => void)[] = []
  const exits: ((code: number | null, signal: string | null) => void)[] = []
  const closes: ((code: number | null, signal: string | null) => void)[] = []
  const child = {
    stdout: {
      on: (_event: string, listener: (chunk: Buffer | string) => void) => stdout.push(listener),
    },
    stderr: {
      on: (_event: string, listener: (chunk: Buffer | string) => void) => stderr.push(listener),
    },
    stdin: {
      write: () => undefined,
      end: () => undefined,
      on: (_event: 'error', listener: (error: Error) => void) => listener,
    },
    on(
      event: 'spawn' | 'error' | 'exit' | 'close',
      listener: (() => void) | ((...args: unknown[]) => void),
    ): undefined {
      if (event === 'spawn') (listener as () => void)()
      else if (event === 'exit') {
        exits.push(listener as (code: number | null, signal: string | null) => void)
      } else if (event === 'close') {
        closes.push(listener as (code: number | null, signal: string | null) => void)
      }
      return undefined
    },
    kill: () => true,
    emitStdout: (text: string) => {
      for (const listener of stdout) listener(text)
    },
    emitStderr: (text: string) => {
      for (const listener of stderr) listener(text)
    },
    exit: (code: number | null, signal: string | null) => {
      for (const listener of exits) listener(code, signal)
      for (const listener of closes) listener(code, signal)
    },
  }
  return child as unknown as StubChild
}

/** SpawnCall is what the console handed the spawner. */
interface SpawnCall {
  readonly command: string
  readonly args: readonly string[]
  readonly options: SpawnOptionsLike
  readonly child: StubChild
}

/** commandLine is one call as the whole command line it is: the program the OS
 * starts and every word after it. */
function commandLine(call: SpawnCall): readonly string[] {
  return [call.command, ...call.args]
}

/** AFeed is one child's stdout, or `null` for a child that never answers (which
 * is how a test holds a run in flight). */
interface AFeed {
  readonly stdout: string
  readonly stderr?: string
  readonly code?: number
}

/** aSpawner answers each child with the next feed, one turn later, so
 * `runToCompletion` sees the bytes and then the close. */
function aSpawner(feeds: readonly (AFeed | null)[]): {
  readonly spawner: ChildSpawner
  readonly calls: readonly SpawnCall[]
} {
  const calls: SpawnCall[] = []
  const spawner = ((command: string, args: readonly string[], options: SpawnOptionsLike) => {
    const child = stubChild()
    calls.push({ command, args, options, child })
    const feed = feeds[calls.length - 1]
    if (feed !== null && feed !== undefined) {
      queueMicrotask(() => {
        if (feed.stderr !== undefined) child.emitStderr(feed.stderr)
        child.emitStdout(feed.stdout)
        child.exit(feed.code ?? 0, null)
      })
    }
    return child
  }) as unknown as ChildSpawner
  return { spawner, calls }
}

// ---------------------------------------------------------------------------
// The window, and the planner
// ---------------------------------------------------------------------------

/** aConfirmer is the seam SU1's window fills, recording what it was shown. It
 * never decides by itself: the test calls `decide`, which is what only the
 * window's own main frame may do (`confirm.ts`'s sender and frame gate). */
function aConfirmer(): {
  readonly confirmer: LaunchConfirmer
  readonly opened: {
    readonly subject: ConfirmSubject
    readonly decide: (yes: boolean, reason?: string) => void
  }[]
  readonly closed: readonly string[]
} {
  const opened: {
    subject: ConfirmSubject
    decide: (yes: boolean, reason?: string) => void
  }[] = []
  const closed: string[] = []
  return {
    confirmer: {
      open: (subject: ConfirmSubject, decide: (yes: boolean, reason?: string) => void) => {
        opened.push({ subject, decide })
      },
      close: (id: string) => {
        closed.push(id)
      },
    },
    opened,
    closed,
  }
}

/** argsFor is the four values as the arguments `argvFor` reads: an absent rate
 * is an absent key, because the CLI's own flag is optional. */
function argsFor(values: ModelsAddValues): Record<string, string> {
  const args: Record<string, string> = { id: values.id, route: values.route }
  if (values.priceIn !== '') args['priceIn'] = values.priceIn
  if (values.priceOut !== '') args['priceOut'] = values.priceOut
  return args
}

/**
 * plannerFor is the real allow list behind the flow's seam: `planFor` builds
 * each phase's argv from `SPAWN_COMMANDS`' own row for it, so the argv a case
 * compares with `PLAN_ARGV` is the argv the console would really spawn -- and a
 * row that grew a word, or a value that reached an argv it should not, fails
 * here rather than in production.
 */
function plannerFor(): { readonly planner: ModelsAddPlanner; readonly built: string[] } {
  const built: string[] = []
  const planner: ModelsAddPlanner = (name, values, project) => {
    built.push(name)
    return planFor(name, argsFor(values), {
      target: { project },
      binaryPath: BINARY,
      directory: WORKDIR,
      appEnv: {},
    }) as SpawnPlan
  }
  return { planner, built }
}

/** aWriter is one write built over everything a test drives. */
function aWriter(
  options: {
    readonly feeds?: readonly (AFeed | null)[]
    readonly floor?: string | null
    readonly cliVersion?: string
    readonly project?: string
    readonly digest?: (document: string) => string
    readonly lapsed?: () => boolean
  } = {},
) {
  const confirmer = aConfirmer()
  const rows = plannerFor()
  const recorder = aSpawner(options.feeds ?? [{ stdout: aDocumentText() }, { stdout: 'wrote the claim\n' }])
  const writer = createModelsAdd({
    confirmer: confirmer.confirmer,
    plan: rows.planner,
    project: options.project ?? PROJECT,
    cliVersion: options.cliVersion ?? '0.120.0',
    // The licence is valid unless a case says otherwise: the lapse's gate is at
    // the door (`lapseHandlers`) *and* here, where the write really starts.
    lapsed: options.lapsed ?? (() => false),
    // The default floor is a release this CLI is above, so the ordinary cases
    // spawn: the real constant is `null` today (`MODELS_ADD_CLI_FLOOR`), which is
    // the state the floor case drives.
    floor: options.floor === undefined ? '0.26.0' : options.floor,
    spawner: recorder.spawner,
    ...(options.digest === undefined ? {} : { digest: options.digest }),
  })
  return { writer, confirmer, calls: recorder.calls, built: rows.built }
}

/** flush lets every microtask the handlers queued run: `runToCompletion`'s
 * bytes-then-close, and the `void ensureApply` the window's yes starts. */
async function flush(times = 4): Promise<void> {
  for (let index = 0; index < times; index += 1) await Promise.resolve()
}

/** refusalOf runs one handler call and answers the sentence it refused with, or
 * `null` when it answered. */
async function refusalOf(run: () => Promise<unknown>): Promise<string | null> {
  try {
    await run()
    return null
  } catch (error) {
    if (error instanceof SourceRefusal) return error.sentence
    throw error
  }
}

/**
 * ask is the bridge's own path for one call, and it is the real one: the payload
 * goes through `validateArguments` against `CONSOLE_OPERATIONS`' row, and only a
 * payload that passed reaches the handler -- exactly what `ipc.ts`'s `dispatch`
 * does. It answers the reply shape, so a case reads a refusal the way a screen
 * does.
 */
async function ask(
  writer: ModelsAddHandlers,
  name: 'models.add.plan' | 'models.add',
  payload: unknown,
): Promise<{ readonly ok: true; readonly data: unknown } | { readonly ok: false; readonly reason: string }> {
  const decision = validateArguments(name, payload)
  if (!decision.ok) return { ok: false, reason: decision.reason }
  try {
    return { ok: true, data: await writer[name](decision.args) }
  } catch (error) {
    if (error instanceof SourceRefusal) return { ok: false, reason: error.sentence }
    throw error
  }
}

/** viewOf is the subject the window was shown, as a models view: a case that
 * reads it has already asserted the kind. */
function viewOf(subject: ConfirmSubject | undefined): ModelsAddView {
  if (subject === undefined || subject.kind !== 'models-add') {
    throw new Error('the window was not shown a models write')
  }
  return subject
}

// ---------------------------------------------------------------------------
// The flow
// ---------------------------------------------------------------------------

describe('the models write behind the modal', () => {
  it('spawns the CLI’s own dry run, shows the window the document and the argv, and writes over the plan’s own argv', async () => {
    const { writer, confirmer, calls } = aWriter()
    expect(await ask(writer, 'models.add.plan', aValues())).toStrictEqual({
      ok: true,
      data: { ok: true, sentence: 'the plan is waiting for your answer in its window' },
    })
    expect(calls).toHaveLength(1)
    // **The row's own argv and no word of main's own.** The plan carries
    // `--no-apply` (main's word: the steps are saved, never run) and the two
    // read words; the four values are the renderer's, each one word, the ID
    // behind the terminator.
    expect(commandLine(calls[0] as SpawnCall)).toStrictEqual([...PLAN_ARGV])

    const shown = viewOf(confirmer.opened[0]?.subject)
    expect(confirmer.opened).toHaveLength(1)
    expect(shown.kind).toBe('models-add')
    // The view is the CLI's document as main parsed it, and the two facts main
    // holds: the digest of the document as it arrived, and the exact argv the
    // window's yes will spawn -- the write's own line, not the dry run's, so
    // the operator is not shown a command marked `--dry-run` and told the write
    // is what it does.
    expect(shown.model).toBe(ID)
    expect(shown.route).toBe(ROUTE)
    expect(shown.priceIn).toBe(0.14)
    expect(shown.priceOut).toBe(0.28)
    expect(shown.steps).toStrictEqual([
      { name: 'publish', state: 'saved' },
      { name: 'redeploy', state: 'saved' },
    ])
    expect(shown.project).toBe(PROJECT)
    expect(shown.digest).toBe(createHash('sha256').update(aDocumentText().trim(), 'utf8').digest('hex'))
    expect(shown.argv).toStrictEqual([...WRITE_ARGV])
    // A widening's window: cancel is the default button and the tick gates the
    // yes (`confirm.ts`).
    expect(shown.widening).toBe(true)
    expect(shown.defaultButton).toBe('cancel')
    expect(shown.tick).toBe(MODELS_ADD_TICK_LABEL)
    expect(shown.confirmNeedsTick).toBe(true)

    // The window's own top frame says yes, and the write runs the plan's argv
    // less the two read words -- the **same** argv, from main's own record.
    confirmer.opened[0]?.decide(true)
    await flush()
    expect(calls).toHaveLength(2)
    expect(commandLine(calls[1] as SpawnCall)).toStrictEqual([...WRITE_ARGV])
    // **The write is the plan less its two read words, word for word** -- not a
    // second argv built from the renderer's values again, which is what a
    // renderer that changed a value between the two calls would produce.
    expect(commandLine(calls[1] as SpawnCall)).toStrictEqual(
      (commandLine(calls[0] as SpawnCall) as readonly string[]).filter(
        (word) => word !== '--dry-run' && word !== '--json',
      ),
    )

    // ...and the renderer's own door answers what the child printed.
    expect(await ask(writer, 'models.add', aValues())).toStrictEqual({
      ok: true,
      data: { ok: true, sentence: 'wrote the claim' },
    })
  })

  it('names the active installation on the write’s argv, and never one a renderer chose', async () => {
    const { writer, confirmer, calls } = aWriter({ project: 'chosen' })
    await ask(writer, 'models.add.plan', aValues())
    confirmer.opened[0]?.decide(true)
    await flush()
    expect(commandLine(calls[0] as SpawnCall)).toContain('chosen')
    expect(commandLine(calls[1] as SpawnCall)).toContain('chosen')
    // The payload names no installation at all: the flag is main's, and a
    // renderer that sent one would fail `validateArguments` by name.
    expect(await ask(writer, 'models.add.plan', aValues({ project: 'other' }))).toStrictEqual({
      ok: false,
      reason: 'models.add.plan takes no project',
    })
  })

  it('is one write at a time: a second plan while one is pending is refused, and writes nothing', async () => {
    const { writer, confirmer, calls } = aWriter()
    await ask(writer, 'models.add.plan', aValues())
    // The second plan asks for **another** model, which is the mutation this
    // catches: a console that replaced the pending plan would write the second
    // one's argv over the first's, and the operator would be answering a window
    // whose argv had moved under it.
    expect(
      await ask(writer, 'models.add.plan', aValues({ id: 'deepseek/another-model' })),
    ).toStrictEqual({ ok: false, reason: MODELS_ADD_PLAN_PENDING_REFUSAL })
    expect(calls).toHaveLength(1)
    expect(confirmer.opened).toHaveLength(1)

    // ...and the yes that follows still writes the **first** plan, once: the
    // refused second plan left no record and no argv behind it.
    expect(viewOf(confirmer.opened[0]?.subject).model).toBe(ID)
    confirmer.opened[0]?.decide(true)
    await flush()
    expect(calls).toHaveLength(2)
    expect(commandLine(calls[1] as SpawnCall)).toStrictEqual([...WRITE_ARGV])
    // A second yes is not a second write.
    confirmer.opened[0]?.decide(true)
    await flush()
    expect(calls).toHaveLength(2)
  })

  it('spawns nothing when the apply arrives before the window’s own yes', async () => {
    const { writer, calls } = aWriter()
    await ask(writer, 'models.add.plan', aValues())
    expect(await ask(writer, 'models.add', aValues())).toStrictEqual({
      ok: false,
      reason: MODELS_ADD_NO_YES_REFUSAL,
    })
    await flush()
    expect(calls).toHaveLength(1)
  })

  it('refuses an apply with no plan behind it at all', async () => {
    const { writer, calls } = aWriter()
    expect(await ask(writer, 'models.add', aValues())).toStrictEqual({
      ok: false,
      reason: MODELS_ADD_NO_PLAN_REFUSAL,
    })
    expect(calls).toHaveLength(0)
  })

  it('refuses an apply whose values are not the plan’s own, and spawns nothing', async () => {
    const { writer, confirmer, calls } = aWriter()
    await ask(writer, 'models.add.plan', aValues())
    confirmer.opened[0]?.decide(true)
    await flush()
    expect(calls).toHaveLength(2)
    // A renderer that sends another model over the channel is refused rather
    // than believed: the argv main re-runs is the argv the window showed.
    expect(
      await ask(writer, 'models.add', aValues({ id: 'deepseek/another-model' })),
    ).toStrictEqual({ ok: false, reason: MODELS_ADD_DISAGREES })
    expect(await ask(writer, 'models.add', aValues({ priceIn: '9.99' }))).toStrictEqual({
      ok: false,
      reason: MODELS_ADD_DISAGREES,
    })
    expect(calls).toHaveLength(2)
  })

  it('joins the write the yes started rather than spawning a second child', async () => {
    const { writer, confirmer, calls } = aWriter()
    await ask(writer, 'models.add.plan', aValues())
    confirmer.opened[0]?.decide(true)
    const first = ask(writer, 'models.add', aValues())
    const second = ask(writer, 'models.add', aValues())
    expect(await first).toStrictEqual({ ok: true, data: { ok: true, sentence: 'wrote the claim' } })
    expect(await second).toStrictEqual({ ok: true, data: { ok: true, sentence: 'wrote the claim' } })
    expect(calls).toHaveLength(2)
  })

  it('stops the write the window’s own yes starts when the licence has lapsed', async () => {
    // **The lapse is asked where the child is spawned, and not only at the
    // door.** The window's yes reaches `runApply` from `decide`, and no IPC
    // handler is on that path at all -- so `lapseHandlers`' gate on `models.add`
    // (the row's own `refusedAfterLapse` column) is not what stops a plan the
    // operator made after the lapse, which is kept and opens its window. The
    // plan is a dry run and stays reachable; the write it stands for is the row
    // the lapse takes away, and this is that column on the window's path.
    const { writer, confirmer, calls } = aWriter({ lapsed: () => true })
    expect(await ask(writer, 'models.add.plan', aValues())).toStrictEqual({
      ok: true,
      data: { ok: true, sentence: 'the plan is waiting for your answer in its window' },
    })
    // The dry run is a child of its own and runs: the lapse takes the write, not
    // the plan the operator reads before deciding.
    expect(calls).toHaveLength(1)
    expect(confirmer.opened).toHaveLength(1)
    confirmer.opened[0]?.decide(true)
    await flush()
    // **Nothing was spawned**, and the renderer's own door answers the lapse's
    // sentence rather than the write's -- the one sentence a lapsed console
    // gives (`licence-state.ts`), so the two paths say the same thing.
    expect(calls).toHaveLength(1)
    expect(await ask(writer, 'models.add', aValues())).toStrictEqual({
      ok: true,
      data: { ok: false, sentence: LAPSE_REFUSAL },
    })
    expect(calls).toHaveLength(1)

    // **The answer is read per write and not captured once**, which is the
    // reason `lapseHandlers` reads its own per call: a renewal pasted during the
    // session gives the write back at the next plan, and the refused one left
    // nothing behind that would keep refusing.
    let lapsed = true
    const renewed = aWriter({
      lapsed: () => lapsed,
      feeds: [{ stdout: aDocumentText() }, { stdout: aDocumentText() }, { stdout: 'wrote the claim\n' }],
    })
    await ask(renewed.writer, 'models.add.plan', aValues())
    renewed.confirmer.opened[0]?.decide(true)
    await flush()
    expect(renewed.calls).toHaveLength(1)
    lapsed = false
    await ask(renewed.writer, 'models.add.plan', aValues())
    renewed.confirmer.opened[1]?.decide(true)
    await flush()
    expect(renewed.calls).toHaveLength(3)
    expect(await ask(renewed.writer, 'models.add', aValues())).toStrictEqual({
      ok: true,
      data: { ok: true, sentence: 'wrote the claim' },
    })
  })

  it('reads a write that exited non-zero as a refusal, whatever its stdout holds', async () => {
    // `models add` prints the path and the diff to stdout **before** it can
    // still refuse -- the config changed since it was read, a coding-agent
    // session that may not write, a backup that failed -- so every one of those
    // exits non-zero with stdout that is not empty. A console reading stdout
    // alone answers `ok: true` and the screen says the model was added when
    // nothing was written.
    const { writer, confirmer, calls } = aWriter({
      feeds: [
        { stdout: aDocumentText() },
        {
          stdout: `${WORKDIR}/fugaro.yaml\n+ deepseek/deepseek-v4.1-flash\n`,
          stderr: 'fugaro: the config changed since it was read: nothing was written\n',
          code: 1,
        },
      ],
    })
    await ask(writer, 'models.add.plan', aValues())
    confirmer.opened[0]?.decide(true)
    await flush()
    expect(calls).toHaveLength(2)
    expect(await ask(writer, 'models.add', aValues())).toStrictEqual({
      ok: true,
      data: { ok: false, sentence: 'the config changed since it was read: nothing was written' },
    })
  })

  it('reads a dry run that exited non-zero as a refusal, even with a document on stdout', async () => {
    // The plan child's status is the first fact as well: a document beside a
    // non-zero exit is not a plan to agree to, and the CLI's own sentence is
    // what a screen draws.
    const { writer, confirmer, calls } = aWriter({
      feeds: [
        { stdout: aDocumentText(), stderr: 'fugaro: nothing was written\n', code: 1 },
      ],
    })
    expect(await ask(writer, 'models.add.plan', aValues())).toStrictEqual({
      ok: false,
      reason: 'nothing was written',
    })
    expect(confirmer.opened).toHaveLength(0)
    expect(calls).toHaveLength(1)
  })

  it('refuses the write when the held document no longer hashes to what the window showed', async () => {
    // The digest is main's own record of what the window drew. Nothing in this
    // console can change the document; the seam here is how a test changes it,
    // and a document that changed is a write nobody agreed to.
    let calls = 0
    const { writer, confirmer, spawnCalls } = ((): {
      writer: ModelsAddHandlers
      confirmer: ReturnType<typeof aConfirmer>
      spawnCalls: readonly SpawnCall[]
    } => {
      const built = aWriter({
        digest: (document: string) => {
          calls += 1
          return calls === 1 ? modelsAddDigest(document) : 'a'.repeat(64)
        },
      })
      return { writer: built.writer, confirmer: built.confirmer, spawnCalls: built.calls }
    })()
    await ask(writer, 'models.add.plan', aValues())
    confirmer.opened[0]?.decide(true)
    await flush()
    expect(spawnCalls).toHaveLength(1)
    // The write's own refusal is an **outcome** and not a channel error (the
    // settings write's shape): the window's yes already started the run, so
    // nobody is awaiting it, and the renderer's own door answers the same
    // sentence the run would have.
    expect(await ask(writer, 'models.add', aValues())).toStrictEqual({
      ok: true,
      data: { ok: false, sentence: MODELS_ADD_DOCUMENT_CHANGED_REFUSAL },
    })
  })

  it('shows no window and answers the CLI’s own sentence when the dry run refused', async () => {
    const { writer, confirmer, calls } = aWriter({
      feeds: [{ stdout: '', stderr: 'fugaro: no route openrouter in this project\n', code: 1 }],
    })
    expect(await ask(writer, 'models.add.plan', aValues())).toStrictEqual({
      ok: false,
      reason: 'no route openrouter in this project',
    })
    expect(confirmer.opened).toHaveLength(0)
    // The write is not reachable afterwards: no plan is held.
    expect(await ask(writer, 'models.add', aValues())).toStrictEqual({
      ok: false,
      reason: MODELS_ADD_NO_PLAN_REFUSAL,
    })
    expect(calls).toHaveLength(1)
  })

  it('refuses a document it cannot read, with the console’s own sentence when the CLI said nothing', async () => {
    const { writer, confirmer } = aWriter({ feeds: [{ stdout: 'not json at all\n' }] })
    expect(await ask(writer, 'models.add.plan', aValues())).toStrictEqual({
      ok: false,
      reason: MODELS_ADD_DOCUMENT_SHAPE_REFUSAL,
    })
    expect(confirmer.opened).toHaveLength(0)
  })

  it('refuses a document of another version rather than reading it best-effort', async () => {
    const { writer, confirmer } = aWriter({ feeds: [{ stdout: aDocumentText({ version: 2 }) }] })
    expect(await ask(writer, 'models.add.plan', aValues())).toStrictEqual({
      ok: false,
      reason: MODELS_ADD_DOCUMENT_VERSION_REFUSAL,
    })
    expect(confirmer.opened).toHaveLength(0)
  })

  it('refuses a document that carries a refusal, which no CLI prints', async () => {
    const { writer, confirmer } = aWriter({
      feeds: [{ stdout: aDocumentText({ refusal: 'the price table refuses this model' }) }],
    })
    expect(await ask(writer, 'models.add.plan', aValues())).toStrictEqual({
      ok: false,
      reason: 'the price table refuses this model',
    })
    expect(confirmer.opened).toHaveLength(0)
  })

  it('spawns nothing at all below its floor, and says which command does the same thing', async () => {
    // The constant is `null` today: no release carries MD1's document, so every
    // version is below the floor and the sentence names the command to run by
    // hand.
    const unset = aWriter({ floor: null })
    expect(await ask(unset.writer, 'models.add.plan', aValues())).toStrictEqual({
      ok: false,
      reason: modelsAddFloorSentence(null, '0.120.0'),
    })
    expect(unset.calls).toHaveLength(0)
    expect(unset.confirmer.opened).toHaveLength(0)
    expect(modelsAddFloorSentence(null, '0.120.0')).toContain(MODELS_ADD_COMMAND)
    expect(modelsAddFloorSentence(null, '0.120.0')).toContain(MODELS_ADD_WORD)
    expect(modelsAddFloorSentence(null, '0.120.0')).toContain(MODELS_ADD_FLOOR_UNSET)

    const below = aWriter({ floor: '0.126.0', cliVersion: '0.120.0' })
    expect(await ask(below.writer, 'models.add.plan', aValues())).toStrictEqual({
      ok: false,
      reason: modelsAddFloorSentence('0.126.0', '0.120.0'),
    })
    expect(below.calls).toHaveLength(0)

    // ...and the apply is refused by the same check, so a renderer that skipped
    // the plan cannot reach the write either.
    expect(await ask(below.writer, 'models.add', aValues())).toStrictEqual({
      ok: false,
      reason: MODELS_ADD_NO_PLAN_REFUSAL,
    })
    expect(below.calls).toHaveLength(0)
  })
})

// ---------------------------------------------------------------------------
// The surface: what a renderer may send, and what it may not reach
// ---------------------------------------------------------------------------

describe('the models write’s surface', () => {
  it('refuses a value outside its grammar before any child exists', async () => {
    const { writer, calls } = aWriter()
    // The model ID: an upper-case vendor and a space are both outside
    // `MODEL_ID_RE`, and the refusal names the field rather than the value.
    expect(await ask(writer, 'models.add.plan', aValues({ id: 'DeepSeek/Flash 4.1' }))).toStrictEqual({
      ok: false,
      reason: 'id must be a model ID: lower-case vendor/model, at most 128 characters',
    })
    expect(await ask(writer, 'models.add.plan', aValues({ route: 'OpenRouter' }))).toStrictEqual({
      ok: false,
      reason: "route must be a route name (lower-case letters, digits and '-')",
    })
    expect(await ask(writer, 'models.add.plan', aValues({ priceIn: '1,4' }))).toStrictEqual({
      ok: false,
      reason: 'priceIn must be a price in US dollars per million tokens, as a plain decimal',
    })
    // A rate above the CLI's own ceiling is refused here rather than by a child
    // that would price a model at a figure no catalog holds.
    expect(await ask(writer, 'models.add.plan', aValues({ priceIn: '1001' }))).toStrictEqual({
      ok: false,
      reason: 'priceIn must be more than 0 and at most 1000 dollars per million tokens',
    })
    // The payload carries the four values and no others.
    expect(await ask(writer, 'models.add.plan', aValues({ apply: true }))).toStrictEqual({
      ok: false,
      reason: 'models.add.plan takes no apply',
    })
    expect(calls).toHaveLength(0)
  })

  it('puts the model ID behind the terminator, so a value spelled like a flag stays a value', async () => {
    // The four grammars all start with a letter or a digit, so no value this
    // surface accepts can begin with `-` -- and that is exactly why the ID is
    // the positional behind `--` rather than a bare word: the shape a future
    // grammar admits cannot turn into a flag. `spawn.test.ts` pins the argv's
    // own half (`--inline` behind the terminator is not refused); what is pinned
    // here is that the write's argv really has one.
    const { writer, calls } = aWriter()
    await ask(writer, 'models.add.plan', aValues())
    const argv = commandLine(calls[0] as SpawnCall) as readonly string[]
    const terminator = argv.indexOf('--')
    expect(terminator).toBeGreaterThan(0)
    expect(argv[terminator + 1]).toBe(ID)
    expect(argv.indexOf(ID)).toBe(argv.length - 1)
    expect(argv.slice(0, terminator)).toContain(`--route=${ROUTE}`)
  })

  it('refuses `models rm` and `models use`, and grants this write’s two operations by name', () => {
    // The never table is what keeps the group's writers off the surface, and the
    // write this task adds is the one named exception (AL extended `except` to
    // command rows; MW1 extends it to the `models`/`add` argv row the same way).
    const forbidden = (name: string, command: string): boolean =>
      isNeverReachable({ name, command, flags: [] }) !== null

    expect(forbidden('models.rm', 'models rm')).toBe(true)
    expect(forbidden('models.use', 'models use')).toBe(true)
    // The word alone is refused, and so is the command path -- which is what
    // catches a future operation whose command is `models add <id>`.
    expect(forbidden('something.else', 'models add')).toBe(true)
    expect(forbidden('something.else', 'models rm')).toBe(true)

    // ...and the two rows of this write are the exception, by name.
    expect(isNeverReachable({ name: MODELS_ADD_PLAN_OPERATION, command: 'models add --no-apply --dry-run', flags: [] })).toBeNull()
    expect(isNeverReachable({ name: MODELS_ADD_OPERATION, command: 'models add --no-apply', flags: [] })).toBeNull()

    // The rows themselves stay in the table: an exception is a grant to two
    // named operations, never a row taken away.
    const values = NEVER_REACHABLE.map((row) => row.value)
    expect(values).toContain('models add')
    expect(values).toContain('models rm')
    expect(values).toContain('models use')
  })

  it('files the window under the installation the write lands in', () => {
    // The identity `close` reaches the window by, and the one a re-chosen
    // installation closes: the project, not the model.
    expect(modelsAddConfirmID(PROJECT)).toBe(`models-add:${PROJECT}`)
  })
})
