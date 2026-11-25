// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The image write's main-process half (the console-improvements plan, task MW2:
// D2903, O4; `console/src/main/imageadd.ts`, `console/src/main/imagesview.ts`).
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
//     apply with no yes spawns nothing", which goes straight at the `image.add`
//     handler;
//   - a console that **believes the apply's payload** fails "an apply whose
//     values are not the plan's own", which sends another entry name over the
//     channel and asserts no second child;
//   - a **second plan replacing a pending one** fails "one write at a time";
//   - a console that reads a child's **stdout without its exit status** fails
//     the two cases whose child printed bytes and exited non-zero -- which is
//     what `image add` does when it refuses *after* printing the entry it
//     appended, so the screen would say the entry was added when nothing was;
//   - a console that spawns **below the floor**, or with the floor still unset,
//     fails the floor cases, which assert the sentence names the command to run
//     by hand and that no child was started;
//   - a value **outside its grammar** fails the validation case, which sends it
//     over the real table (`validateArguments`) and asserts no child exists --
//     and the case that follows it pins the other half: a value spelled like a
//     flag is a value, because the entry's name is the positional behind `--`;
//   - a console that reads another command's document, or one for another
//     installation, fails the two document cases -- `command` and `project` are
//     the members the schema states for exactly that;
//   - a console that reaches `image rm` or `image promote` fails the
//     never-table case, which asks `isNeverReachable` about both and about this
//     write's own two operations by name.
//
// What is *not* here is the renderer's half: the Manage kinds' Add action, the
// window's own drawing of this view and the §5.1 rows are MWR's task, and the
// view this file pins is main's own (`imagesview.ts`).

import { createHash } from 'node:crypto'

import { describe, expect, it } from 'vitest'

import type {
  ChildSpawner,
  SpawnOptionsLike,
  SpawnPlan,
  SpawnedChildLike,
} from '../../src/main/spawn'
import { IMAGE_ADD_OPERATION, IMAGE_ADD_PLAN_OPERATION, planFor } from '../../src/main/spawn'
import type { ConfirmSubject, LaunchConfirmer } from '../../src/main/launch'
import { CONSOLE_OPERATIONS, NEVER_REACHABLE, isNeverReachable, validateArguments } from '../../src/main/ipc'
import {
  IMAGE_ADD_COMMAND,
  IMAGE_ADD_DISAGREES,
  IMAGE_ADD_DOCUMENT_CHANGED_REFUSAL,
  IMAGE_ADD_FLOOR_UNSET,
  IMAGE_ADD_NO_PLAN_REFUSAL,
  IMAGE_ADD_NO_YES_REFUSAL,
  IMAGE_ADD_PLAN_PENDING_REFUSAL,
  IMAGE_ADD_WORD,
  createImageAdd,
  imageAddDigest,
  imageAddFloorSentence,
} from '../../src/main/imageadd'
import type { ImageAddHandlers, ImageAddPlanner, ImageAddValues } from '../../src/main/imageadd'
import {
  IMAGE_ADD_COMMAND as IMAGE_ADD_DOOR,
  IMAGE_ADD_DOCUMENT_COMMAND_REFUSAL,
  IMAGE_ADD_DOCUMENT_PROJECT_REFUSAL,
  IMAGE_ADD_DOCUMENT_SHAPE_REFUSAL,
  IMAGE_ADD_DOCUMENT_VERSION_REFUSAL,
  imageAddConfirmID,
} from '../../src/main/imagesview'
import type { ImageAddView } from '../../src/main/imagesview'
import { LAPSE_REFUSAL } from '../../src/main/licence-state'
import { SourceRefusal } from '../../src/source/source'

// ---------------------------------------------------------------------------
// The values, and the two rows
// ---------------------------------------------------------------------------

/** BINARY is the resolved CLI the plan's first word is, and WORKDIR is the
 * directory a plan's `cwd` is (neither is read by the flow: they are what a real
 * plan carries). */
const BINARY = '/usr/local/bin/fugaro'
const WORKDIR = '/tmp'

/** PROJECT is the active installation; NAME, DOCKERFILE, TAGS and DESCRIPTION
 * are one well-formed set of the four values -- the name inside `IMAGE_NAME_RE`,
 * the definition a `owner/name:path` the repository rule takes, and two tags and
 * a description inside their own rules. */
const PROJECT = 'sandbox'
const NAME = 'app'
const DOCKERFILE = 'acme/app:.fugaro/app.Dockerfile'
const TAGS = 'node,redis'
const DESCRIPTION = 'the app image'

/** aValues is the payload a renderer sends: the four values and nothing else. */
function aValues(overrides: Partial<Record<string, unknown>> = {}): Record<string, unknown> {
  return { name: NAME, dockerfile: DOCKERFILE, tags: TAGS, description: DESCRIPTION, ...overrides }
}

/** PLAN_ARGV is what the allow list's `image.add.plan` row builds for these
 * values: the command's own words, the three `=`-form values, the installation's
 * flag and the entry name behind the terminator. It is spelled out here rather
 * than computed from the plan, because the whole point of the headline case is
 * to compare a spawned argv with a **written-out** expectation.
 *
 * `spawn.test.ts` is where the row itself is pinned; what this file pins is that
 * the flow adds no word of its own to it. */
const PLAN_ARGV: readonly string[] = Object.freeze([
  BINARY,
  'image',
  'add',
  '--no-apply',
  '--dry-run',
  '--json',
  `--dockerfile=${DOCKERFILE}`,
  `--tags=${TAGS}`,
  `--description=${DESCRIPTION}`,
  '--project',
  PROJECT,
  '--',
  NAME,
])

/** WRITE_ARGV is the plan's own argv without `--dry-run --json`, which is what
 * `image add --no-apply` is: the console's write. */
const WRITE_ARGV: readonly string[] = Object.freeze(
  PLAN_ARGV.filter((word) => word !== '--dry-run' && word !== '--json'),
)

// ---------------------------------------------------------------------------
// The document MD2 prints, and the child that prints it
// ---------------------------------------------------------------------------

/** SOURCE is the entry's definition in the one-line form `image ls` prints it,
 * and the member MD2's document carries for the source. */
const SOURCE = `dockerfile ${DOCKERFILE}`

/**
 * aDocument is the dry run's document as MD2 writes it
 * (`schemas/image-add-plan.schema.json`, and the golden file
 * `internal/cli/testdata/image_add_plan.golden.json`): the version, the command
 * the plan is for, the installation it lands in, the entry the add would append
 * and the steps it owes. The steps carry `saved` because main sets `--no-apply`:
 * that is what the window shows and what the write does.
 */
function aDocument(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    version: 1,
    command: IMAGE_ADD_DOOR,
    project: PROJECT,
    entry: {
      name: NAME,
      tags: TAGS.split(','),
      description: DESCRIPTION,
      default: false,
      source: SOURCE,
    },
    steps: [
      { name: 'catalog-build', saved: true },
      { name: 'repository', saved: true },
      { name: 'policy', saved: true },
    ],
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

/** argsFor is the four values as the arguments `argvFor` reads: an absent tag
 * list or description is an absent key, because the CLI's own flags are
 * optional and an absent flag is how this write says *none*. */
function argsFor(values: ImageAddValues): Record<string, string> {
  const args: Record<string, string> = { name: values.name, dockerfile: values.dockerfile }
  if (values.tags !== '') args['tags'] = values.tags
  if (values.description !== '') args['description'] = values.description
  return args
}

/**
 * plannerFor is the real allow list behind the flow's seam: `planFor` builds
 * each phase's argv from `SPAWN_COMMANDS`' own row for it, so the argv a case
 * compares with `PLAN_ARGV` is the argv the console would really spawn -- and a
 * row that grew a word, or a value that reached an argv it should not, fails
 * here rather than in production.
 */
function plannerFor(): { readonly planner: ImageAddPlanner; readonly built: string[] } {
  const built: string[] = []
  const planner: ImageAddPlanner = (name, values, project) => {
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
  const recorder = aSpawner(
    options.feeds ?? [{ stdout: aDocumentText() }, { stdout: 'added the entry\n' }],
  )
  const writer = createImageAdd({
    confirmer: confirmer.confirmer,
    plan: rows.planner,
    project: options.project ?? PROJECT,
    cliVersion: options.cliVersion ?? '0.120.0',
    // The licence is valid unless a case says otherwise: the lapse's gate is at
    // the door (`lapseHandlers`) *and* here, where the write really starts.
    lapsed: options.lapsed ?? (() => false),
    // The default floor is a release this CLI is above, so the ordinary cases
    // spawn: the real constant is `null` today (`IMAGE_ADD_CLI_FLOOR`), which is
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

/**
 * ask is the bridge's own path for one call, and it is the real one: the payload
 * goes through `validateArguments` against `CONSOLE_OPERATIONS`' row, and only a
 * payload that passed reaches the handler -- exactly what `ipc.ts`'s `dispatch`
 * does. It answers the reply shape, so a case reads a refusal the way a screen
 * does.
 */
async function ask(
  writer: ImageAddHandlers,
  name: 'image.add.plan' | 'image.add',
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

/** viewOf is the subject the window was shown, as an image view: a case that
 * reads it has already asserted the kind. */
function viewOf(subject: ConfirmSubject | undefined): ImageAddView {
  if (subject === undefined || subject.kind !== 'image-add') {
    throw new Error('the window was not shown an image write')
  }
  return subject
}

// ---------------------------------------------------------------------------
// The flow
// ---------------------------------------------------------------------------

describe('the image write behind the modal', () => {
  it('spawns the CLI’s own dry run, shows the window the document and the argv, and writes over the plan’s own argv', async () => {
    const { writer, confirmer, calls } = aWriter()
    expect(await ask(writer, 'image.add.plan', aValues())).toStrictEqual({
      ok: true,
      data: { ok: true, sentence: 'the plan is waiting for your answer in its window' },
    })
    expect(calls).toHaveLength(1)
    // **The row's own argv and no word of main's own.** The plan carries
    // `--no-apply` (main's word: the steps are saved, never run) and the two
    // read words; the four values are the renderer's, each one word, the name
    // behind the terminator.
    expect(commandLine(calls[0] as SpawnCall)).toStrictEqual([...PLAN_ARGV])

    const shown = viewOf(confirmer.opened[0]?.subject)
    expect(confirmer.opened).toHaveLength(1)
    expect(shown.kind).toBe('image-add')
    // The view is the CLI's document as main parsed it, and the two facts main
    // holds: the digest of the document as it arrived, and the exact argv the
    // window's yes will spawn -- the write's own line, not the dry run's, so
    // the operator is not shown a command marked `--dry-run` and told the write
    // is what it does.
    expect(shown.command).toBe(IMAGE_ADD_DOOR)
    expect(shown.project).toBe(PROJECT)
    expect(shown.name).toBe(NAME)
    expect(shown.tags).toStrictEqual(['node', 'redis'])
    expect(shown.description).toBe(DESCRIPTION)
    expect(shown.entryDefault).toBe(false)
    expect(shown.source).toBe(SOURCE)
    expect(shown.steps).toStrictEqual([
      { name: 'catalog-build', saved: true },
      { name: 'repository', saved: true },
      { name: 'policy', saved: true },
    ])
    expect(shown.digest).toBe(createHash('sha256').update(aDocumentText().trim(), 'utf8').digest('hex'))
    expect(shown.argv).toStrictEqual([...WRITE_ARGV])
    // **A narrowing’s window, and not a widening’s**: this write raises no
    // limit, so its yes is one deliberate click -- Confirm is the default
    // button and no tick gates it (`confirm.ts` holds the tick to the subjects
    // that really do raise one).
    expect(shown.defaultButton).toBe('confirm')
    expect(shown.confirmNeedsTick).toBe(false)

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
    expect(await ask(writer, 'image.add', aValues())).toStrictEqual({
      ok: true,
      data: { ok: true, sentence: 'added the entry' },
    })
  })

  it('leaves the two optional values off the argv when the plan set none, and refuses an empty one', async () => {
    // The way this write says *no tags* and *no description* is an absent flag,
    // and the way a renderer says it is an absent key: the CLI takes an entry
    // with neither, and an empty `--tags=` or `--description=` would be a
    // different argv for the same entry.
    const bare = aWriter({
      feeds: [
        {
          stdout: aDocumentText({
            entry: { name: NAME, tags: [], description: '', default: false, source: SOURCE },
          }),
        },
      ],
    })
    expect(await ask(bare.writer, 'image.add.plan', { name: NAME, dockerfile: DOCKERFILE })).toStrictEqual({
      ok: true,
      data: { ok: true, sentence: 'the plan is waiting for your answer in its window' },
    })
    const argv = commandLine(bare.calls[0] as SpawnCall)
    expect(argv.some((word) => word.startsWith('--tags='))).toBe(false)
    expect(argv.some((word) => word.startsWith('--description='))).toBe(false)
    // The document's own empty members are values rather than missing ones: a
    // window drawn on this plan shows an entry with no tags and no description,
    // which is what `image ls` prints for one.
    const bareView = viewOf(bare.confirmer.opened[0]?.subject)
    expect(bareView.tags).toStrictEqual([])
    expect(bareView.description).toBe('')

    // An **empty** value is refused rather than sent: `validateArguments` is
    // where the renderer is told to leave the key out instead.
    const clean = aWriter()
    expect(await ask(clean.writer, 'image.add.plan', aValues({ description: '' }))).toStrictEqual({
      ok: false,
      reason: 'description must be a description: leave it out for an entry with none',
    })
    expect(await ask(clean.writer, 'image.add.plan', aValues({ tags: '' }))).toStrictEqual({
      ok: false,
      reason:
        'tags must be a comma-separated list of tags: at most 32 of a-z, 0-9, \'.\', \'_\' and \'-\', each starting with a letter or a digit',
    })
    expect(clean.calls).toHaveLength(0)
  })

  it('names the active installation on the write’s argv, and never one a renderer chose', async () => {
    // The document carries the installation's own name, which is the member the
    // window's reader compares (the case below drives the mismatch): main names
    // the project, the CLI prints it back, and the two agreeing is what makes the
    // plan this installation's.
    const { writer, confirmer, calls } = aWriter({
      project: 'chosen',
      feeds: [{ stdout: aDocumentText({ project: 'chosen' }) }, { stdout: 'added the entry\n' }],
    })
    expect(await ask(writer, 'image.add.plan', aValues())).toStrictEqual({
      ok: true,
      data: { ok: true, sentence: 'the plan is waiting for your answer in its window' },
    })
    confirmer.opened[0]?.decide(true)
    await flush()
    expect(commandLine(calls[0] as SpawnCall)).toContain('chosen')
    expect(commandLine(calls[1] as SpawnCall)).toContain('chosen')
    // The payload names no installation at all: the flag is main's, and a
    // renderer that sent one would fail `validateArguments` by name.
    expect(await ask(writer, 'image.add.plan', aValues({ project: 'other' }))).toStrictEqual({
      ok: false,
      reason: 'image.add.plan takes no project',
    })
  })

  it('is one write at a time: a second plan while one is pending is refused, and writes nothing', async () => {
    const { writer, confirmer, calls } = aWriter()
    await ask(writer, 'image.add.plan', aValues())
    // The second plan asks for **another** entry, which is the mutation this
    // catches: a console that replaced the pending plan would write the second
    // one's argv over the first's, and the operator would be answering a window
    // whose argv had moved under it.
    expect(await ask(writer, 'image.add.plan', aValues({ name: 'another' }))).toStrictEqual({
      ok: false,
      reason: IMAGE_ADD_PLAN_PENDING_REFUSAL,
    })
    expect(calls).toHaveLength(1)
    expect(confirmer.opened).toHaveLength(1)

    // ...and the yes that follows still writes the **first** plan, once: the
    // refused second plan left no record and no argv behind it.
    expect(viewOf(confirmer.opened[0]?.subject).name).toBe(NAME)
    confirmer.opened[0]?.decide(true)
    await flush()
    expect(calls).toHaveLength(2)
    expect(commandLine(calls[1] as SpawnCall)).toStrictEqual([...WRITE_ARGV])
    // A second yes is not a second write.
    confirmer.opened[0]?.decide(true)
    await flush()
    expect(calls).toHaveLength(2)
  })

  it('is one write at a time across the dry run too: a plan still spawning refuses a second', async () => {
    // The two calls overlap **inside** the first plan's dry run: `ask` starts the
    // handler, which suspends at `await runChild`, and the second call arrives
    // before the child answers. A guard that read only `held` -- written after
    // that await -- would let both spawn and the second's window replace the
    // first's argv, which is the mutation the sequential case above catches one
    // step later.
    const { writer, confirmer, calls } = aWriter()
    const first = ask(writer, 'image.add.plan', aValues())
    const second = ask(writer, 'image.add.plan', aValues({ name: 'another' }))
    expect(await second).toStrictEqual({
      ok: false,
      reason: IMAGE_ADD_PLAN_PENDING_REFUSAL,
    })
    expect(await first).toStrictEqual({
      ok: true,
      data: { ok: true, sentence: 'the plan is waiting for your answer in its window' },
    })
    expect(calls).toHaveLength(1)
    expect(confirmer.opened).toHaveLength(1)
    expect(viewOf(confirmer.opened[0]?.subject).name).toBe(NAME)
  })

  it('spawns nothing when the apply arrives before the window’s own yes', async () => {
    const { writer, calls } = aWriter()
    await ask(writer, 'image.add.plan', aValues())
    expect(await ask(writer, 'image.add', aValues())).toStrictEqual({
      ok: false,
      reason: IMAGE_ADD_NO_YES_REFUSAL,
    })
    await flush()
    expect(calls).toHaveLength(1)
  })

  it('refuses an apply with no plan behind it at all', async () => {
    const { writer, calls } = aWriter()
    expect(await ask(writer, 'image.add', aValues())).toStrictEqual({
      ok: false,
      reason: IMAGE_ADD_NO_PLAN_REFUSAL,
    })
    expect(calls).toHaveLength(0)
  })

  it('refuses an apply whose values are not the plan’s own, and spawns nothing', async () => {
    const { writer, confirmer, calls } = aWriter()
    await ask(writer, 'image.add.plan', aValues())
    confirmer.opened[0]?.decide(true)
    await flush()
    expect(calls).toHaveLength(2)
    // A renderer that sends another entry over the channel is refused rather
    // than believed: the argv main re-runs is the argv the window showed.
    expect(await ask(writer, 'image.add', aValues({ name: 'another' }))).toStrictEqual({
      ok: false,
      reason: IMAGE_ADD_DISAGREES,
    })
    expect(
      await ask(writer, 'image.add', aValues({ dockerfile: 'acme/app:other.Dockerfile' })),
    ).toStrictEqual({ ok: false, reason: IMAGE_ADD_DISAGREES })
    expect(await ask(writer, 'image.add', aValues({ description: 'another' }))).toStrictEqual({
      ok: false,
      reason: IMAGE_ADD_DISAGREES,
    })
    expect(calls).toHaveLength(2)
  })

  it('joins the write the yes started rather than spawning a second child', async () => {
    const { writer, confirmer, calls } = aWriter()
    await ask(writer, 'image.add.plan', aValues())
    confirmer.opened[0]?.decide(true)
    const first = ask(writer, 'image.add', aValues())
    const second = ask(writer, 'image.add', aValues())
    expect(await first).toStrictEqual({ ok: true, data: { ok: true, sentence: 'added the entry' } })
    expect(await second).toStrictEqual({ ok: true, data: { ok: true, sentence: 'added the entry' } })
    expect(calls).toHaveLength(2)
  })

  it('stops the write the window’s own yes starts when the licence has lapsed', async () => {
    // **The lapse is asked where the child is spawned, and not only at the
    // door.** The window's yes reaches `runApply` from `decide`, and no IPC
    // handler is on that path at all -- so `lapseHandlers`' gate on `image.add`
    // (the row's own `refusedAfterLapse` column) is not what stops a plan the
    // operator made after the lapse, which is kept and opens its window. The
    // plan is a dry run and stays reachable; the write it stands for is the row
    // the lapse takes away, and this is that column on the window's path.
    const { writer, confirmer, calls } = aWriter({ lapsed: () => true })
    expect(await ask(writer, 'image.add.plan', aValues())).toStrictEqual({
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
    expect(await ask(writer, 'image.add', aValues())).toStrictEqual({
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
      feeds: [{ stdout: aDocumentText() }, { stdout: aDocumentText() }, { stdout: 'added the entry\n' }],
    })
    await ask(renewed.writer, 'image.add.plan', aValues())
    renewed.confirmer.opened[0]?.decide(true)
    await flush()
    expect(renewed.calls).toHaveLength(1)
    lapsed = false
    await ask(renewed.writer, 'image.add.plan', aValues())
    renewed.confirmer.opened[1]?.decide(true)
    await flush()
    expect(renewed.calls).toHaveLength(3)
    expect(await ask(renewed.writer, 'image.add', aValues())).toStrictEqual({
      ok: true,
      data: { ok: true, sentence: 'added the entry' },
    })
  })

  it('reads a write that exited non-zero as a refusal, whatever its stdout holds', async () => {
    // `image add` prints the entry it appended and the steps it saved to stdout
    // **before** it can still refuse -- the config changed since it was read, a
    // coding-agent session that may not write, a build it could not queue -- so
    // every one of those exits non-zero with stdout that is not empty. A console
    // reading stdout alone answers `ok: true` and the screen says the entry was
    // added when nothing was written.
    const { writer, confirmer, calls } = aWriter({
      feeds: [
        { stdout: aDocumentText() },
        {
          stdout: `${WORKDIR}/fugaro.yaml\n+ app\n`,
          stderr: 'fugaro: the config changed since it was read: nothing was written\n',
          code: 1,
        },
      ],
    })
    await ask(writer, 'image.add.plan', aValues())
    confirmer.opened[0]?.decide(true)
    await flush()
    expect(calls).toHaveLength(2)
    expect(await ask(writer, 'image.add', aValues())).toStrictEqual({
      ok: true,
      data: { ok: false, sentence: 'the config changed since it was read: nothing was written' },
    })
  })

  it('reads a dry run that exited non-zero as a refusal, even with a document on stdout', async () => {
    // The plan child's status is the first fact as well: a document beside a
    // non-zero exit is not a plan to agree to, and the CLI's own sentence is
    // what a screen draws.
    const { writer, confirmer, calls } = aWriter({
      feeds: [{ stdout: aDocumentText(), stderr: 'fugaro: nothing was written\n', code: 1 }],
    })
    expect(await ask(writer, 'image.add.plan', aValues())).toStrictEqual({
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
    const built = aWriter({
      digest: (document: string) => {
        calls += 1
        return calls === 1 ? imageAddDigest(document) : 'a'.repeat(64)
      },
    })
    await ask(built.writer, 'image.add.plan', aValues())
    built.confirmer.opened[0]?.decide(true)
    await flush()
    expect(built.calls).toHaveLength(1)
    // The write's own refusal is an **outcome** and not a channel error (the
    // settings write's shape): the window's yes already started the run, so
    // nobody is awaiting it, and the renderer's own door answers the same
    // sentence the run would have.
    expect(await ask(built.writer, 'image.add', aValues())).toStrictEqual({
      ok: true,
      data: { ok: false, sentence: IMAGE_ADD_DOCUMENT_CHANGED_REFUSAL },
    })
  })

  it('shows no window and answers the CLI’s own sentence when the dry run refused', async () => {
    const { writer, confirmer, calls } = aWriter({
      feeds: [{ stdout: '', stderr: 'fugaro: the catalog already holds app\n', code: 1 }],
    })
    expect(await ask(writer, 'image.add.plan', aValues())).toStrictEqual({
      ok: false,
      reason: 'the catalog already holds app',
    })
    expect(confirmer.opened).toHaveLength(0)
    // The write is not reachable afterwards: no plan is held.
    expect(await ask(writer, 'image.add', aValues())).toStrictEqual({
      ok: false,
      reason: IMAGE_ADD_NO_PLAN_REFUSAL,
    })
    expect(calls).toHaveLength(1)
  })

  it('spawns nothing at all below its floor, and says which command does the same thing', async () => {
    // The constant is `null` today: no release carries MD2's document, so every
    // version is below the floor and the sentence names the command to run by
    // hand.
    const unset = aWriter({ floor: null })
    expect(await ask(unset.writer, 'image.add.plan', aValues())).toStrictEqual({
      ok: false,
      reason: imageAddFloorSentence(null, '0.120.0'),
    })
    expect(unset.calls).toHaveLength(0)
    expect(unset.confirmer.opened).toHaveLength(0)
    expect(imageAddFloorSentence(null, '0.120.0')).toContain(IMAGE_ADD_COMMAND)
    expect(imageAddFloorSentence(null, '0.120.0')).toContain(IMAGE_ADD_WORD)
    expect(imageAddFloorSentence(null, '0.120.0')).toContain(IMAGE_ADD_FLOOR_UNSET)

    const below = aWriter({ floor: '0.126.0', cliVersion: '0.120.0' })
    expect(await ask(below.writer, 'image.add.plan', aValues())).toStrictEqual({
      ok: false,
      reason: imageAddFloorSentence('0.126.0', '0.120.0'),
    })
    expect(below.calls).toHaveLength(0)

    // ...and the apply is refused by the same check, so a renderer that skipped
    // the plan cannot reach the write either.
    expect(await ask(below.writer, 'image.add', aValues())).toStrictEqual({
      ok: false,
      reason: IMAGE_ADD_NO_PLAN_REFUSAL,
    })
    expect(below.calls).toHaveLength(0)
  })
})

// ---------------------------------------------------------------------------
// The document, as the window's own reader judges it
// ---------------------------------------------------------------------------

describe('the plan document the window is drawn from', () => {
  it('refuses a document it cannot read, with the console’s own sentence when the CLI said nothing', async () => {
    const { writer, confirmer } = aWriter({ feeds: [{ stdout: 'not json at all\n' }] })
    expect(await ask(writer, 'image.add.plan', aValues())).toStrictEqual({
      ok: false,
      reason: IMAGE_ADD_DOCUMENT_SHAPE_REFUSAL,
    })
    expect(confirmer.opened).toHaveLength(0)
  })

  it('refuses a document of another version rather than reading it best-effort', async () => {
    const { writer, confirmer } = aWriter({ feeds: [{ stdout: aDocumentText({ version: 2 }) }] })
    expect(await ask(writer, 'image.add.plan', aValues())).toStrictEqual({
      ok: false,
      reason: IMAGE_ADD_DOCUMENT_VERSION_REFUSAL,
    })
    expect(confirmer.opened).toHaveLength(0)
  })

  it('refuses the other add’s document, which is one version and another member away', async () => {
    // The two plan documents are the same version, and `command` is the member
    // the CLI writes for exactly this: a window that drew the models add's
    // document as an image's would show an entry nobody asked for.
    const { writer, confirmer } = aWriter({
      feeds: [{ stdout: aDocumentText({ command: 'models add' }) }],
    })
    expect(await ask(writer, 'image.add.plan', aValues())).toStrictEqual({
      ok: false,
      reason: IMAGE_ADD_DOCUMENT_COMMAND_REFUSAL,
    })
    expect(confirmer.opened).toHaveLength(0)
  })

  it('refuses a plan written for another installation than the one the console is acting on', async () => {
    // The member the schema states for this: a consumer compares `project` with
    // the installation it believes it is acting on, and a plan for another one
    // is a write that would land where the screen never named.
    const { writer, confirmer } = aWriter({
      feeds: [{ stdout: aDocumentText({ project: 'aurora' }) }],
    })
    expect(await ask(writer, 'image.add.plan', aValues())).toStrictEqual({
      ok: false,
      reason: IMAGE_ADD_DOCUMENT_PROJECT_REFUSAL,
    })
    expect(confirmer.opened).toHaveLength(0)
  })

  it('refuses a document with no step at all, which no add can produce', async () => {
    // The step list is never empty: the catalog's change is the resource type
    // the CLI's step table declares with three rows, so an add that owed nothing
    // would be a table gap rather than an add. A reader that accepted the empty
    // list would draw a write that owes nothing.
    const { writer, confirmer } = aWriter({ feeds: [{ stdout: aDocumentText({ steps: [] }) }] })
    expect(await ask(writer, 'image.add.plan', aValues())).toStrictEqual({
      ok: false,
      reason: IMAGE_ADD_DOCUMENT_SHAPE_REFUSAL,
    })
    expect(confirmer.opened).toHaveLength(0)
  })
})

// ---------------------------------------------------------------------------
// The surface: what a renderer may send, and what it may not reach
// ---------------------------------------------------------------------------

/** Row is the three members the never table judges an operation by. */
interface Row {
  readonly name: string
  readonly command: string | null
  readonly flags: readonly string[]
}

/** rowFor is the surface's own row for an operation, so a case asks the never
 * table about what the console would really spawn. */
function rowFor(name: string): Row {
  const row = CONSOLE_OPERATIONS.find((entry) => entry.name === name)
  if (row === undefined) throw new Error(`no operation ${name}`)
  return { name: row.name, command: row.command, flags: row.flags }
}

describe('the image write’s surface', () => {
  it('refuses a value outside its grammar before any child exists', async () => {
    const { writer, calls } = aWriter()
    // The entry name: the CLI's own catalog-name rule -- 1 to 40, lower-case
    // letters, digits and `-`, a letter or a digit at each end -- which is
    // **narrower than the model ID's shape**, so `App` is outside it.
    expect(await ask(writer, 'image.add.plan', aValues({ name: 'App' }))).toStrictEqual({
      ok: false,
      reason:
        "name must be a catalog entry name: 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or a digit",
    })
    // ...and the 40 the name's own rule allows is also the width this surface
    // accepts, so a 41-character name is refused by the length rather than by
    // the shape -- the two are separate refusals and a renderer is told which.
    expect(await ask(writer, 'image.add.plan', aValues({ name: `a${'b'.repeat(40)}` }))).toStrictEqual({
      ok: false,
      reason: 'name is longer than 40 characters',
    })
    // The definition: `owner/name:path`, cut at the first colon, with a
    // repository the console's own rule takes and a path that is one clean
    // relative path inside it.
    expect(await ask(writer, 'image.add.plan', aValues({ dockerfile: 'acme/app' }))).toStrictEqual({
      ok: false,
      reason: "dockerfile must be a Dockerfile in one of the project's repositories, as owner/name:path",
    })
    expect(
      await ask(writer, 'image.add.plan', aValues({ dockerfile: 'acme/app:/etc/passwd' })),
    ).toStrictEqual({
      ok: false,
      reason: "dockerfile must name a path inside the repository: relative, with no '..' or '.' segment",
    })
    expect(
      await ask(writer, 'image.add.plan', aValues({ dockerfile: 'acme/app:../app.Dockerfile' })),
    ).toStrictEqual({
      ok: false,
      reason: "dockerfile must name a path inside the repository: relative, with no '..' or '.' segment",
    })
    // The tags: a list of the CLI's own tag shape, at most the CLI's own count.
    expect(await ask(writer, 'image.add.plan', aValues({ tags: 'node,Redis' }))).toStrictEqual({
      ok: false,
      reason:
        "tags must be a comma-separated list of tags: at most 32 of a-z, 0-9, '.', '_' and '-', each starting with a letter or a digit",
    })
    expect(
      await ask(writer, 'image.add.plan', aValues({ tags: Array.from({ length: 33 }, (_, i) => `t${i}`).join(',') })),
    ).toStrictEqual({
      ok: false,
      reason:
        "tags must be a comma-separated list of tags: at most 32 of a-z, 0-9, '.', '_' and '-', each starting with a letter or a digit",
    })
    // The payload carries the four values and no others.
    expect(await ask(writer, 'image.add.plan', aValues({ apply: true }))).toStrictEqual({
      ok: false,
      reason: 'image.add.plan takes no apply',
    })
    // ...and the four flags of the other definitions are words of no operation:
    // `--inline` would be executable content from a renderer's text, and the
    // other three are definitions this surface does not carry at all.
    expect(await ask(writer, 'image.add.plan', aValues({ inline: 'FROM node\n' }))).toStrictEqual({
      ok: false,
      reason: 'image.add.plan takes no inline',
    })
    expect(calls).toHaveLength(0)
    // The shape's other end, from a writer of its own: a name of exactly the 40
    // characters the rule allows is a plan rather than a refusal, and the grammar
    // is a bound rather than a trend.
    const boundary = `a${'b'.repeat(39)}`
    const longest = aWriter({
      feeds: [
        {
          stdout: aDocumentText({
            entry: { name: boundary, tags: [], description: '', default: false, source: SOURCE },
          }),
        },
      ],
    })
    expect(await ask(longest.writer, 'image.add.plan', aValues({ name: boundary }))).toStrictEqual({
      ok: true,
      data: { ok: true, sentence: 'the plan is waiting for your answer in its window' },
    })
    expect(longest.calls).toHaveLength(1)
  })

  it('puts the entry’s name behind the terminator, so a value spelled like a flag stays a value', async () => {
    // The name's own rule starts with a letter or a digit, so no name this
    // surface accepts can begin with `-` -- and that is exactly why the name is
    // the positional behind `--` rather than a bare word: the shape a future
    // grammar admits cannot turn into a flag. `spawn.test.ts` pins the argv's
    // own half (each of `--inline`, `--image`, `--default` and `--branch` behind
    // the terminator is not refused); what is pinned here is that the write's
    // argv really has one.
    const { writer, calls } = aWriter()
    await ask(writer, 'image.add.plan', aValues())
    const argv = commandLine(calls[0] as SpawnCall)
    const terminator = argv.indexOf('--')
    expect(terminator).toBeGreaterThan(0)
    expect(argv[terminator + 1]).toBe(NAME)
    expect(argv.indexOf(NAME)).toBe(argv.length - 1)
    expect(argv.slice(0, terminator)).toContain(`--dockerfile=${DOCKERFILE}`)
  })

  it('refuses `image rm` and `image promote`, and grants this write’s two operations by name', () => {
    // The never table is what keeps the group's writers off the surface, and the
    // write this task adds is the one named exception (AL extended `except` to
    // command rows; MW2 extends it to the `image`/`add` argv row the same way).
    const forbidden = (name: string, command: string): boolean =>
      isNeverReachable({ name, command, flags: [] }) !== null

    expect(forbidden('image.rm', 'image rm')).toBe(true)
    expect(forbidden('image.promote', 'image promote')).toBe(true)
    expect(forbidden('image.build', 'image build')).toBe(true)
    // The word alone is refused, and so is the command path -- which is what
    // catches a future operation whose command is `image add <name>` rather than
    // the command `image add`.
    expect(forbidden('something.else', 'image add')).toBe(true)
    expect(forbidden('something.else', 'image rm')).toBe(true)

    // ...and the two rows of this write are the exception, by name -- asked
    // about **the rows the surface really states**, so this is the grant the
    // console would really get and not a spelling written out here.
    expect(isNeverReachable(rowFor(IMAGE_ADD_PLAN_OPERATION))).toBeNull()
    expect(isNeverReachable(rowFor(IMAGE_ADD_OPERATION))).toBeNull()
    // The group's one read stays reachable beside them.
    expect(isNeverReachable(rowFor('image.catalog.ls'))).toBeNull()
    // A **third** operation driving the word is refused even beside them, which
    // is the whole of what an exception may not do.
    expect(forbidden('image.other', 'image add --no-apply')).toBe(true)

    // The rows themselves stay in the table: an exception is a grant to two
    // named operations, never a row taken away.
    const values = NEVER_REACHABLE.map((row) => row.value)
    expect(values).toContain('image add')
    expect(values).toContain('image rm')
    expect(values).toContain('image promote')
  })

  it('files the window under the installation the write lands in', () => {
    // The identity `close` reaches the window by, and the one a re-chosen
    // installation closes: the project, not the entry.
    expect(imageAddConfirmID(PROJECT)).toBe(`image-add:${PROJECT}`)
  })
})
