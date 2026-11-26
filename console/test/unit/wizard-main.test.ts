// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The setup wizard's main-process half (the console-improvements plan, task
// SW3a: D2904, O6, F15, F27; `console/src/main/wizardrun.ts`,
// `console/src/main/setupview.ts`).
//
// This is the only test that runs the wizard under plain Node: the two children
// a plan and an apply spawn are a recording stub, the window is a fake
// `LaunchConfirmer`, and the working directory is one the test names -- so
// nothing here reaches a network, a home directory or a real cloud.
//
// Each case names the mutation it catches:
//
//   - apply **re-serialising the answers** instead of reusing the bytes the
//     window showed fails the headline case, which compares the two children's
//     stdin byte for byte and counts the serialiser's calls;
//   - a window shown the **renderer's own object** rather than main's parsed
//     answers fails "shows the answers main parsed", which hands the confirmer
//     a `SetupView` built from what `validateSetupAnswers` returned;
//   - a renderer that names a **path** or a flag as an answer fails the
//     validation cases, which refuse an unknown member and a value outside the
//     CLI's own grammar -- and the plan case that refuses `{answers, path}`;
//   - a **renderer frame starting a setup** fails "an apply with no yes spawns
//     nothing", which goes straight at the `setup.apply` handler;
//   - an **unsanitised event line** fails the case that feeds a `left_for_you`
//     sentence carrying a URL and an absolute path and reads the placeholders
//     back;
//   - a console that spawns a **CLI below the floor** (or with a floor still
//     unset) fails the floor cases, which assert the command to run by hand and
//     that no child and no directory were made;
//   - a console that passes a **confirming word** fails the argv case, which
//     compares both spawned command lines with task AL's own rows and refuses
//     `--yes`, `--ask`, `--non-interactive` by name;
//   - a **second plan replacing a pending one** fails "one wizard at a time",
//     and a second apply that spawns a second child fails the join case;
//   - a window that drew the setup rows as **anything but the five drawn
//     answers, the digest, the two commands and the four sentences** fails the
//     renderer case at the end, which runs `mountConfirm` over a fake document;
//   - a console that **sends the budget to a CLI that cannot read it** -- or
//     drops it for one that can -- fails the budget cases, which drive
//     `setupAnswersForCli` on both sides of the floor.

import { createHash } from 'node:crypto'
import { existsSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import type {
  ChildSpawner,
  JSONObject,
  SpawnOptionsLike,
  SpawnPlan,
  SpawnedChildLike,
} from '../../src/main/spawn'
import type { ConfirmSubject, LaunchConfirmer } from '../../src/main/launch'
import type { ValidatedArguments } from '../../src/main/ipc'
import { FUGARO_SETUP_COMMAND } from '../../src/main/onboarding'
import {
  SETUP_BAKE_DEFAULT,
  SETUP_BAKE_NO,
  SETUP_BAKE_YES,
  SETUP_NOTES,
  setupConfirmID,
  setupView,
} from '../../src/main/setupview'
import {
  SETUP_ANSWERS_MISSING,
  SETUP_ANSWERS_NOT_AN_OBJECT,
  SETUP_ANSWERS_UNKNOWN_KEY,
  SETUP_APPLY_UNKNOWN_ARGUMENT,
  SETUP_BAKE_REFUSAL,
  SETUP_BYTES_CHANGED_REFUSAL,
  SETUP_DIR_MODE,
  SETUP_DEADLINE_MS,
  SETUP_FAILED_WITHOUT_A_REASON,
  SETUP_FLOOR_UNSET,
  SETUP_NAME_REFUSAL,
  SETUP_NO_PLAN_REFUSAL,
  SETUP_NO_YES_REFUSAL,
  SETUP_PLAN_PENDING_REFUSAL,
  SETUP_PLAN_UNKNOWN_ARGUMENT,
  SETUP_PROJECT_REFUSAL,
  SETUP_REGION_REFUSAL,
  SETUP_RIGOR_REFUSAL,
  SETUP_WIZARD_WORD,
  createSetupWizard,
  makeSetupDirectory,
  readEventLines,
  removeSetupDirectory,
  serialiseSetupAnswers,
  setupAnswersDigest,
  setupAnswersForCli,
  setupFailureSentence,
  setupFeedFailure,
  setupFloorSentence,
  validateSetupAnswers,
} from '../../src/main/wizardrun'
import type { SetupAnswersHeld, SetupBounds, SetupPlanner } from '../../src/main/wizardrun'
import { LAPSE_REFUSAL } from '../../src/main/licence-state'
import { NEW_READ_BOUNDS } from '../../src/source/new-reads'
import {
  SETUP_ANSWER_KEYS,
  SETUP_ANSWERS_FILE_KEYS,
  SETUP_BUDGET_MAX,
  SETUP_BUDGET_MIN,
  SETUP_RIGOR_MODES,
} from '../../src/shared/new-ops'
import { SourceRefusal } from '../../src/source/source'
import { LITERAL_PLACEHOLDER } from '../../src/shared/sanitize'
import { UNREADABLE_VERSION } from '../../src/shared/version'
import {
  CANCEL_LABEL,
  NONE,
  SETUP_LABEL,
  TITLE_SETUP,
  mountConfirm,
} from '../../src/renderer/confirm'
import type {
  ConfirmBridge,
  ConfirmAnswer,
  ConfirmReply,
} from '../../src/preload/confirm'
import type {
  ConfirmDocument,
  ConfirmElement,
  ConfirmEvent,
  ConfirmSubjectFields,
} from '../../src/renderer/confirm'

// ---------------------------------------------------------------------------
// The answers, and the two rows task AL will add
// ---------------------------------------------------------------------------

/** PROJECT, REGION, NAME, RIGOR and BUDGET are one well-formed set of the six
 * answers. The project is a project ID, the region a Google Cloud region, the
 * name a project name, all three inside the CLI's own grammars, and the budget a
 * whole amount inside the alert's own bounds. */
const PROJECT = 'project-a1'
const REGION = 'us-east5'
const NAME = 'sandbox-install'
const RIGOR = 'startup'
const BUDGET = 250

/** anAnswers is the object the renderer sends for a form whose budget field was
 * left **empty**: the six questions, one of them -- `budget` -- with no member at
 * all, and no `version` (main adds that when it serialises the file, because
 * `version` is the file's own schema version and not a question the wizard
 * asks). A case that wants an amount answers `{ budget: BUDGET }` over it. */
function anAnswers(overrides: Partial<Record<string, unknown>> = {}): Record<string, unknown> {
  return {
    gcp_project: PROJECT,
    region: REGION,
    name: NAME,
    rigor: RIGOR,
    bake_repos: true,
    ...overrides,
  }
}

/** BINARY is the resolved CLI the plan's first word is. */
const BINARY = '/usr/local/bin/fugaro'

/**
 * PLAN_ARGV and APPLY_ARGV are task AL's two rows as its acceptance writes them
 * (`docs/console-improvements/implementation-plan.md`):
 *
 *   - `setup.plan`  = `setup --plan-only --answers-file - --events --json`
 *   - `setup.apply` = `setup --answers-file - --events --json`
 *
 * `--answers-file -` is stdin, which is the whole point: main holds the bytes and
 * no file exists for another process to swap between the check and the child's
 * read. **AL has not landed yet** -- the two rows are not in `spawn.ts`'s allow
 * list or in `ipc.ts`'s table -- so the planner below is a stub that answers
 * these rows itself, and the argv cases assert the console spawns them verbatim.
 * What is pinned here is therefore that main adds **no word of its own** (above
 * all no confirming word) to the row the allow list will define; the row's own
 * pinning is AL's (`ipc.test.ts`).
 */
const PLAN_ARGV: readonly string[] = Object.freeze([
  BINARY,
  'setup',
  '--plan-only',
  '--answers-file',
  '-',
  '--events',
  '--json',
])
const APPLY_ARGV: readonly string[] = Object.freeze([
  BINARY,
  'setup',
  '--answers-file',
  '-',
  '--events',
  '--json',
])

/** CONFIRMING_WORDS are the words a console must never pass a setup child: a
 * yes is the operator's go to run the CLI and never a consent the CLI accepts
 * (O6), and `--ask` and `--non-interactive` are the two other ways a command
 * line tries to answer for a person (`docs/no-prompt-policy/`). */
const CONFIRMING_WORDS = ['--yes', '-y', '--ask', '--non-interactive', '--force']

// ---------------------------------------------------------------------------
// A child, and a spawner that feeds it
// ---------------------------------------------------------------------------

interface StubChild extends SpawnedChildLike {
  /** `bytes` is every chunk written to this child's stdin, **as bytes**: the
   * headline case compares two children's stdin and a decode would hide the one
   * difference it is looking for. */
  readonly bytes: readonly Uint8Array[]
  readonly ended: readonly boolean[]
  emitStdout(text: string): void
  emitStderr(text: string): void
  exit(code: number | null, signal: string | null): void
}

/** stubChild is one child whose stdin, stdout and exit this test drives, and
 * whose `spawn` event fires at registration -- which is the ordering
 * `runToCompletion` needs, because that is when it writes the held bytes. */
function stubChild(): StubChild {
  const stdout: ((chunk: Buffer | string) => void)[] = []
  const stderr: ((chunk: Buffer | string) => void)[] = []
  const exits: ((code: number | null, signal: string | null) => void)[] = []
  const closes: ((code: number | null, signal: string | null) => void)[] = []
  const bytes: Uint8Array[] = []
  const ended: boolean[] = []
  const child = {
    stdout: {
      on: (_event: string, listener: (chunk: Buffer | string) => void) => stdout.push(listener),
    },
    stderr: {
      on: (_event: string, listener: (chunk: Buffer | string) => void) => stderr.push(listener),
    },
    stdin: {
      write: (chunk: Uint8Array) => bytes.push(new Uint8Array(chunk)),
      end: () => ended.push(true),
      on: (_event: 'error', listener: (error: Error) => void) => listener,
    },
    on(event: 'spawn' | 'error' | 'exit' | 'close', listener: (() => void) | ((...args: unknown[]) => void)): undefined {
      if (event === 'spawn') (listener as () => void)()
      else if (event === 'exit') exits.push(listener as (code: number | null, signal: string | null) => void)
      else if (event === 'close') closes.push(listener as (code: number | null, signal: string | null) => void)
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
    bytes,
    ended,
  }
  return child as unknown as StubChild
}

/** SpawnCall is what the console handed the spawner: the command, the words
 * after the wrapper, and the spawn options (whose `cwd` is the working
 * directory the wizard made). */
interface SpawnCall {
  readonly command: string
  readonly args: readonly string[]
  readonly options: SpawnOptionsLike
  readonly child: StubChild
}

/** commandLine is one call as the whole command line it is: the program the OS
 * starts and every word after it. `SpawnPlan.argv` carries the program word and
 * the spawner is handed the words after it (`wrapperArgs`), so this is the one
 * place the two are put back together. */
function commandLine(call: SpawnCall): readonly string[] {
  return [call.command, ...call.args]
}

/** Recorder is the spawner and the calls it saw. */
interface Recorder {
  readonly spawner: ChildSpawner
  readonly calls: readonly SpawnCall[]
}

/**
 * aSpawner answers each child with the next feed, one turn later, so
 * `runToCompletion` sees the bytes and then the close. A call whose feed is
 * `null` gets a child that never answers, which is how a test holds a run in
 * flight (the wiring task's own case: the console has no cancel path here).
 */
function aSpawner(feeds: readonly (string | null)[], code = 0): Recorder {
  const calls: SpawnCall[] = []
  const spawner = ((command: string, args: readonly string[], options: SpawnOptionsLike) => {
    const child = stubChild()
    calls.push({ command, args, options, child })
    const feed = feeds[calls.length - 1]
    if (feed !== null && feed !== undefined) {
      queueMicrotask(() => {
        child.emitStdout(feed)
        child.exit(code, null)
      })
    }
    return child
  }) as unknown as ChildSpawner
  return { spawner, calls }
}

/** aFeed is one event line as NDJSON. The shapes are the ones ST2's golden
 * records (`internal/cli/testdata/setup_events.golden.ndjson`): a `stage` line
 * as a stage settles, a `left_for_you` line for each thing only a person can do,
 * and one last line that ends the feed. */
function aFeed(lines: readonly JSONObject[]): string {
  return lines.map((line) => `${JSON.stringify(line)}\n`).join('')
}

/** DONE is the line that ends a feed: a run that left steps for the operator
 * ends `done` with exit 1 and **not** `error` (ST2), so the exit code decides
 * nothing and this line does. */
const DONE: JSONObject = Object.freeze({ type: 'done', exit_code: 0 })

// ---------------------------------------------------------------------------
// The window, and the planner
// ---------------------------------------------------------------------------

/** aConfirmer is the seam SU1's window fills, recording what it was shown. It
 * never decides by itself: the test calls `decide`, which is what only the
 * window's own main frame may do (`confirm.ts`'s sender and frame gate). */
function aConfirmer(): {
  readonly confirmer: LaunchConfirmer
  readonly opened: { readonly subject: ConfirmSubject; readonly decide: (yes: boolean, reason?: string) => void }[]
  readonly closed: readonly string[]
} {
  const opened: { subject: ConfirmSubject; decide: (yes: boolean, reason?: string) => void }[] = []
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

/** aPlan is one setup run's child. Only the fields a child's start reads matter
 * here: the argv, the working directory and the environment. */
function aPlan(argv: readonly string[], cwd: string): SpawnPlan {
  return {
    // The plan's own `operation` is the allow list's word for the row that built
    // it; nothing in `wizardrun.ts` reads it, and AL's rows are what make the
    // real one a `NewOpName`.
    operation: 'runs.list',
    command: BINARY,
    prefix: [],
    argv,
    wrapper: null,
    cwd,
    env: {},
    project: 'fugaro',
    backoff: { minMs: 400, maxMs: 8000 },
  }
}

/** plannerFor is a planner that answers AL's two rows and records the working
 * directory it was handed for each (see `PLAN_ARGV` for why the rows are spelled
 * here rather than taken from the allow list). */
function plannerFor(rows: { readonly plan?: readonly string[]; readonly apply?: readonly string[] } = {}): {
  readonly planner: SetupPlanner
  readonly directories: readonly string[]
} {
  const directories: string[] = []
  const planner: SetupPlanner = (name, directory) => {
    directories.push(directory)
    if (name === 'setup.plan') return aPlan(rows.plan ?? PLAN_ARGV, directory)
    return aPlan(rows.apply ?? APPLY_ARGV, directory)
  }
  return { planner, directories }
}

/** aWizard is one wizard built over everything a test drives. `made` and
 * `removed` record the working directories main asked for, so a case can assert
 * that a refused plan made none and a settled run gave its own back -- and
 * `serialised` counts the serialiser's calls, which is how "serialised once" is
 * a fact rather than a promise. */
function aWizard(
  options: {
    readonly feeds?: readonly (string | null)[]
    readonly floor?: string | null
    readonly cliVersion?: string
    readonly answers?: () => Record<string, unknown>
    readonly plan?: (answers: SetupAnswersHeld) => Uint8Array
    readonly digest?: (bytes: Uint8Array) => string
    readonly bounds?: SetupBounds
    readonly code?: number
    /** the licence: valid unless a case says otherwise. */
    readonly lapsed?: () => boolean
  } = {},
) {
  const confirmer = aConfirmer()
  const rows = plannerFor()
  const recorder = aSpawner(
    options.feeds ?? [aFeed([{ type: 'stage', name: 'installation', state: 'will-do' }, DONE]), aFeed([DONE])],
    options.code ?? 0,
  )
  const made: string[] = []
  const removed: string[] = []
  let serialised = 0
  const deps = {
    confirmer: confirmer.confirmer,
    plan: rows.planner,
    cliVersion: options.cliVersion ?? '0.120.0',
    floor: options.floor ?? null,
    // The licence is valid unless a case says otherwise: the lapse's gate is at
    // the door (`lapseHandlers`) *and* here, where the apply really starts.
    lapsed: options.lapsed ?? (() => false),
    spawner: recorder.spawner,
    directories: rows.directories,
    // The serialiser is counted on every wizard, so "serialised once" is a fact
    // this test can assert whether it supplies a buffer of its own or not.
    serialise: (answers: SetupAnswersHeld): Uint8Array => {
      serialised += 1
      return options.plan === undefined ? serialiseSetupAnswers(answers) : options.plan(answers)
    },
    makeDirectory: () => {
      const path = mkdtempSync(join(tmpdir(), 'wizard-main-test-'))
      made.push(path)
      return path
    },
    removeDirectory: (path: string) => {
      removed.push(path)
      rmSync(path, { recursive: true, force: true })
    },
    ...(options.bounds === undefined ? {} : { bounds: options.bounds }),
    ...(options.digest === undefined ? {} : { digest: options.digest }),
  }
  const wizard = createSetupWizard(deps)
  return {
    wizard,
    confirmer,
    calls: recorder.calls,
    made,
    removed,
    serialiseCalls: (): number => serialised,
  }
}

/** planArgs is what a `setup.plan` handler is called with. The cast is the seam
 * task AL fills: `ValidatedArgument` carries no object today, and AL's own
 * argument kind is what carries the answers object through `validateArguments`
 * -- the handler reads it and validates it either way. */
function planArgs(answers: unknown): ValidatedArguments {
  return { answers } as unknown as ValidatedArguments
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

// ---------------------------------------------------------------------------
// Validation: what a renderer may send
// ---------------------------------------------------------------------------

describe('the answers main accepts', () => {
  it('takes the six questions, and freezes them', () => {
    const answers = validateSetupAnswers(anAnswers({ budget: BUDGET }))
    expect(answers).toEqual({
      gcp_project: PROJECT,
      region: REGION,
      name: NAME,
      rigor: RIGOR,
      budget: BUDGET,
      bake_repos: true,
    })
    expect(Object.isFrozen(answers)).toBe(true)
    // The six, in the order the contract names them, and no `version`: that is
    // the file's own schema version, which main adds when it serialises.
    expect(Object.keys(answers).sort()).toEqual([...SETUP_ANSWER_KEYS].sort())
    expect(SETUP_ANSWER_KEYS).not.toContain('version')
    // An **empty** budget field is held as `undefined` and not refused: main's
    // own reader takes the absent member as unanswered (`setupAnswers.answer`),
    // which is the run's default amount (D2212) and not a zero.
    expect(validateSetupAnswers(anAnswers()).budget).toBeUndefined()
  })

  it('refuses anything that is not an object', () => {
    for (const raw of [null, undefined, 'answers', 7, true, ['a'], []]) {
      expect(() => validateSetupAnswers(raw), JSON.stringify(raw) ?? 'undefined').toThrow(
        SETUP_ANSWERS_NOT_AN_OBJECT,
      )
    }
  })

  it('refuses a sixth member by name, so a path or a flag cannot ride along', () => {
    // The two shapes a hostile or careless renderer would try: a path where the
    // CLI would read a file, and a member of the answers file's own schema that
    // is main's to write.
    expect(() => validateSetupAnswers(anAnswers({ answers_file: '/tmp/answers.json' }))).toThrow(
      `${SETUP_ANSWERS_UNKNOWN_KEY}: answers_file`,
    )
    expect(() => validateSetupAnswers(anAnswers({ version: 1 }))).toThrow(
      `${SETUP_ANSWERS_UNKNOWN_KEY}: version`,
    )
    expect(() => validateSetupAnswers(anAnswers({ path: '../../etc/passwd' }))).toThrow(
      `${SETUP_ANSWERS_UNKNOWN_KEY}: path`,
    )
  })

  it('holds the project and the region to the grammars the CLI enforces', () => {
    // A path, a flag and a bare word are each refused by the project's own rule:
    // a value the console passes is a value the operator typed into a field that
    // asked for a project, and never a path or a word of its own choosing.
    for (const project of [
      '/tmp/answers.json',
      '--answers-file',
      'Project-A1',
      'abc',
      `${'a'.repeat(31)}`,
      'project_a1',
      'project-a1-',
      '',
    ]) {
      expect(() => validateSetupAnswers(anAnswers({ gcp_project: project })), project).toThrow(
        SETUP_PROJECT_REFUSAL,
      )
    }
    for (const region of ['--region', 'us', 'us-east', 'US-EAST5', 'us_east5', 'europe-west4-']) {
      expect(() => validateSetupAnswers(anAnswers({ region })), region).toThrow(SETUP_REGION_REFUSAL)
    }
    // And the shapes the CLI does accept are accepted: the fields are a grammar
    // and not a fixed list.
    expect(validateSetupAnswers(anAnswers({ gcp_project: 'acme-devel-7' })).gcp_project).toBe(
      'acme-devel-7',
    )
    expect(validateSetupAnswers(anAnswers({ region: 'europe-west4' })).region).toBe('europe-west4')
  })

  it('refuses an answer a question left out, except the two that may be absent', () => {
    for (const key of ['gcp_project', 'region', 'rigor']) {
      const answers = anAnswers()
      delete answers[key]
      expect(() => validateSetupAnswers(answers), key).toThrow(SETUP_ANSWERS_MISSING)
    }
    expect(() => validateSetupAnswers(anAnswers({ gcp_project: 7 }))).toThrow(SETUP_ANSWERS_MISSING)
    // `name` and `bake_repos` may be left out, and the two absences are not the
    // same statement: an unanswered `name` is the empty string (the CLI refuses
    // it itself -- it cannot ask on this door -- and main invents none, because
    // a suggestion would be the wizard's own `fugaro-setup-XXXXXX` directory
    // name), and an unanswered `bake_repos` stays **undefined**, which is the
    // CLI's default: bake, since only a written `false` turns it off.
    const bare = validateSetupAnswers({ gcp_project: PROJECT, region: REGION, rigor: RIGOR })
    expect(bare).toEqual({
      gcp_project: PROJECT,
      region: REGION,
      name: '',
      rigor: RIGOR,
      bake_repos: undefined,
    })
    expect(validateSetupAnswers(anAnswers({ name: null })).name).toBe('')
    // A **blank** name is the same statement as an absent one (`''` is what a
    // renderer sends for the field it drew empty, and the CLI's reader takes it
    // as unanswered: `setupAnswers.answer`), so it is held as empty rather than
    // refused as a name outside the grammar.
    expect(validateSetupAnswers(anAnswers({ name: '' })).name).toBe('')
    expect(validateSetupAnswers(anAnswers({ bake_repos: null })).bake_repos).toBeUndefined()
    // The three states survive validation: absent is neither yes nor no.
    expect(validateSetupAnswers(anAnswers({ bake_repos: true })).bake_repos).toBe(true)
    expect(validateSetupAnswers(anAnswers({ bake_repos: false })).bake_repos).toBe(false)
  })

  it('refuses a rigor nothing may be written as, `hotfix` included', () => {
    // D900: rigor has no default, and D834: `hotfix` is an operator's own
    // per-run mode and no layer sets it, so the answers file's schema's enum is
    // the four. The wizard's vocabulary is the wire's five; what may be *given*
    // as this answer is the four.
    expect([...SETUP_RIGOR_MODES]).toEqual(['poc', 'startup', 'pro', 'critical', 'hotfix'])
    for (const rigor of ['hotfix', '', 'fast', 'POC', 'pro ', 7, {}]) {
      expect(() => validateSetupAnswers(anAnswers({ rigor })), JSON.stringify(rigor) ?? 'undefined').toThrow(
        SETUP_RIGOR_REFUSAL,
      )
    }
  })

  it('refuses a name that is not a project name, and a bake switch that is not a switch', () => {
    for (const name of ['/tmp', 'Name', '-n', 'a-', 'x'.repeat(41), 7]) {
      expect(() => validateSetupAnswers(anAnswers({ name })), JSON.stringify(name) ?? 'undefined').toThrow(
        SETUP_NAME_REFUSAL,
      )
    }
    for (const bake of ['yes', 1, 'true', {}]) {
      expect(() => validateSetupAnswers(anAnswers({ bake_repos: bake }))).toThrow(SETUP_BAKE_REFUSAL)
    }
  })
})

// ---------------------------------------------------------------------------
// The bytes, the digest, and the working directory
// ---------------------------------------------------------------------------

describe('the bytes the child is given', () => {
  const answers: SetupAnswersHeld = Object.freeze({
    gcp_project: PROJECT,
    region: REGION,
    name: NAME,
    rigor: RIGOR,
    budget: BUDGET,
    bake_repos: true,
  })

  it('is the schema\'s document, `version` first and no other key', () => {
    const document = JSON.parse(new TextDecoder().decode(serialiseSetupAnswers(answers))) as Record<
      string,
      unknown
    >
    expect(Object.keys(document)).toEqual([...SETUP_ANSWERS_FILE_KEYS])
    expect(document['version']).toBe(1)
    expect(document['rigor']).toBe(RIGOR)
    expect(document['bake_repos']).toBe(true)
  })

  it('writes the budget when the operator answered one, and leaves the member out when the field was empty', () => {
    // The schema's member is optional, and the two states are different
    // statements: an amount is an amount, while an **absent** member is the
    // field nobody answered -- which the CLI reads as its own default of 100 USD
    // (D2212), not as no alert at all. A written `0` is neither: it is an amount
    // the installation refuses later.
    const empty = JSON.parse(new TextDecoder().decode(serialiseSetupAnswers(answers))) as Record<
      string,
      unknown
    >
    expect(empty['budget']).toBe(BUDGET)
    const unanswered = JSON.parse(
      new TextDecoder().decode(serialiseSetupAnswers({ ...answers, budget: undefined })),
    ) as Record<string, unknown>
    expect(unanswered).not.toHaveProperty('budget')
  })

  it('sends no budget to a CLI below the release that takes an amount without its currency', () => {
    // The console answers an amount and never `budget_currency` (D2211), and
    // before 0.29.0 nothing wrote the currency the document left out back from
    // the default: the amount alone reached the run's own trio rule and the whole
    // plan or apply ended there. So a CLI below the release that reads a
    // half-answered pair as a whole one is sent the answers the file has always
    // carried, and the run records its own default amount (D2212) -- the question
    // was still asked and still shown, and what is dropped is the bytes and never
    // the form.
    for (const version of ['0.28.9', '0.29.0-rc.1', 'dev', '']) {
      expect(
        setupAnswersForCli(answers, version).budget,
        `${version} was sent a budget`,
      ).toBeUndefined()
    }
    for (const version of ['0.29.0', '0.29.1', '0.30.0']) {
      expect(setupAnswersForCli(answers, version).budget).toBe(BUDGET)
    }
    // Below the floor the answers are the ones the file has always carried, and
    // nothing else about them changes.
    expect(setupAnswersForCli(answers, '0.28.9')).toEqual({ ...answers, budget: undefined })
    // A field the operator left empty is the same object on either side: there
    // is no member to drop, and the gate is a function of the answers and the
    // version line and nothing else.
    const unanswered = Object.freeze({ ...answers, budget: undefined })
    expect(setupAnswersForCli(unanswered, '0.1.0')).toBe(unanswered)
    expect(setupAnswersForCli(unanswered, '0.30.0')).toBe(unanswered)
  })

  it('writes a no as `false` and leaves only an unanswered switch out', () => {
    // The blocker this pins: an absent `bake_repos` means **yes** to every
    // reader of the field (`internal/imagebake`'s plan, `localcfg`'s
    // `BakeReposEffective`), so an operator who declined and had the key left
    // out would get their repositories baked into the image their jobs run --
    // while the window's own row said they would not be. A written `false` is
    // the only no there is (`internal/cli/setup_answers.go` honours it,
    // `init.go`'s `carryBakeAnswer` writes it for exactly this reason).
    const declined = JSON.parse(
      new TextDecoder().decode(serialiseSetupAnswers({ ...answers, name: '', bake_repos: false })),
    ) as Record<string, unknown>
    expect(Object.keys(declined)).toEqual([
      'version',
      'gcp_project',
      'region',
      'rigor',
      'budget',
      'bake_repos',
    ])
    expect(declined['bake_repos']).toBe(false)
    // A name nobody answered is left out rather than written blank: the CLI's
    // reader takes both spellings as unanswered (`setupAnswers.answer`), so the
    // file keeps one of them.
    expect(declined).not.toHaveProperty('name')
    // A switch nobody answered is left out too, and that is a different
    // statement: it is the CLI's own default, which is yes.
    const unanswered = JSON.parse(
      new TextDecoder().decode(
        serialiseSetupAnswers({ ...answers, name: '', bake_repos: undefined }),
      ),
    ) as Record<string, unknown>
    expect(Object.keys(unanswered)).toEqual([
      'version',
      'gcp_project',
      'region',
      'rigor',
      'budget',
    ])
    expect(unanswered).not.toHaveProperty('bake_repos')
  })

  it('is a function of the answers and nothing else: the same answers, the same SHA-256', () => {
    const first = setupAnswersDigest(serialiseSetupAnswers(answers))
    const second = setupAnswersDigest(serialiseSetupAnswers({ ...answers }))
    expect(first).toBe(second)
    expect(first).toBe(
      createHash('sha256').update(serialiseSetupAnswers(answers)).digest('hex'),
    )
    expect(first).toMatch(/^[0-9a-f]{64}$/)
  })

  it('makes a private 0700 directory that holds nothing, and takes it away', () => {
    // The answers travel on the child's **stdin** (task AL, ST1's `--answers-file
    // -`), so the directory is not where the answers live: it is the child's
    // working directory, empty, and mode 0700 so nothing another account on the
    // machine can read or write is where a setup runs. The plan's original
    // "answers file, mode 0600" is superseded by that decision.
    const base = mkdtempSync(join(tmpdir(), 'wizard-main-base-'))
    const path = makeSetupDirectory(base)
    try {
      expect(path.startsWith(base)).toBe(true)
      expect(path).toContain('fugaro-setup-')
      // Windows keeps no POSIX permission bits: stat reports 0o666 for a directory there, so the mode is read only where one exists.
      if (process.platform !== 'win32') expect(statSync(path).mode & 0o777).toBe(SETUP_DIR_MODE)
      expect(SETUP_DIR_MODE).toBe(0o700)
      expect(readdirSync(path)).toEqual([])
    } finally {
      removeSetupDirectory(path)
      removeSetupDirectory(base)
    }
    expect(existsSync(path)).toBe(false)
    // Removing nothing is not an error, and neither is removing twice: the flow
    // reaches this on paths that made none.
    expect(() => {
      removeSetupDirectory('')
    }).not.toThrow()
  })
})

// ---------------------------------------------------------------------------
// The feed reader
// ---------------------------------------------------------------------------

describe('reading the events feed', () => {
  it('reads one document a line, and ends the read at a line it cannot read', () => {
    const good = aFeed([{ type: 'stage' }, DONE])
    expect(readEventLines(good, NEW_READ_BOUNDS)).toEqual([{ type: 'stage' }, DONE])
    // Blank lines are not documents, and a trailing newline is not an empty
    // answer: a feed ends with one and the last line is the run's own account.
    expect(readEventLines('\n\n{"type":"done"}\n\n', NEW_READ_BOUNDS)).toEqual([{ type: 'done' }])
    // A line that is not a document, and one that is not an object, **end** the
    // read where it is: that is the interrupted read the contract names (no
    // `done` line, and a screen draws Retry), never a refusal that would throw
    // away the stages that arrived.
    expect(readEventLines('{"type":"done"}\nnot json\n{"type":"error"}', NEW_READ_BOUNDS)).toEqual([
      { type: 'done' },
    ])
    expect(readEventLines('{"type":"done"}\n[1,2]\n', NEW_READ_BOUNDS)).toEqual([{ type: 'done' }])
    expect(readEventLines('"a string"\n', NEW_READ_BOUNDS)).toEqual([])
  })

  it('ends the read at the row cap and at the line ceiling', () => {
    const many = Array.from({ length: NEW_READ_BOUNDS.maxRows + 5 }, () => '{"type":"stage"}').join('\n')
    expect(readEventLines(many, NEW_READ_BOUNDS)).toHaveLength(NEW_READ_BOUNDS.maxRows)
    const long = `{"type":"stage","detail":"${'x'.repeat(NEW_READ_BOUNDS.maxLineLength)}"}`
    expect(readEventLines(`${long}\n{"type":"done"}`, NEW_READ_BOUNDS)).toEqual([])
    // The bound is the **reads' own**, not a second one: the contract gives the
    // four streams and this feed the same three, and `source-new-reads.test.ts`
    // is what pins their values (10,000 rows and 16 MiB).
    expect(NEW_READ_BOUNDS.maxRows).toBe(10_000)
    expect(NEW_READ_BOUNDS.maxBytes).toBe(16 * 1024 * 1024)
  })
})

// ---------------------------------------------------------------------------
// The floor
// ---------------------------------------------------------------------------

describe('a CLI the two operations are not in', () => {
  it('shows the command to run by hand and spawns nothing', async () => {
    // A floor still unset (`null`) is the state until a release carries
    // `--answers-file` and `--events`: the wizard is not offered, and the one
    // command that does the same thing by hand is.
    const unset = aWizard({ floor: null })
    const unsetSentence = await refusalOf(() => unset.wizard['setup.plan'](planArgs(anAnswers())))
    expect(unsetSentence).toBe(`${SETUP_WIZARD_WORD} ${SETUP_FLOOR_UNSET}; run ${FUGARO_SETUP_COMMAND} in your own terminal instead`)
    expect(unsetSentence).toContain(FUGARO_SETUP_COMMAND)

    const old = aWizard({ floor: '0.120.0', cliVersion: '0.119.0' })
    expect(await refusalOf(() => old.wizard['setup.plan'](planArgs(anAnswers())))).toBe(
      `${SETUP_WIZARD_WORD} needs fugaro 0.120.0 and this CLI is 0.119.0; run ${FUGARO_SETUP_COMMAND} in your own terminal instead`,
    )

    const unreadable = aWizard({ floor: '0.120.0', cliVersion: '' })
    expect(await refusalOf(() => unreadable.wizard['setup.plan'](planArgs(anAnswers())))).toContain(
      UNREADABLE_VERSION,
    )

    // Nothing was spawned, no directory was made, and no window was shown: a
    // refused plan is a sentence and not the beginning of a run.
    for (const refused of [unset, old, unreadable]) {
      expect(refused.calls).toEqual([])
      expect(refused.made).toEqual([])
      expect(refused.confirmer.opened).toEqual([])
    }
    // And an apply with no plan behind it says so rather than spawning: a
    // console below the floor has nothing to apply either.
    expect(await refusalOf(() => unset.wizard['setup.apply']({}))).toBe(SETUP_NO_PLAN_REFUSAL)
    expect(unset.calls).toEqual([])
  })

  it('is the floor sentence for a release too old and for a floor nobody set', () => {
    expect(setupFloorSentence(null, '0.1.0')).toContain(SETUP_FLOOR_UNSET)
    expect(setupFloorSentence('0.120.0', '')).toContain(UNREADABLE_VERSION)
    expect(setupFloorSentence('0.120.0', '0.121.0')).toContain('0.121.0')
    expect(setupFloorSentence('0.120.0', '0.121.0')).toContain(FUGARO_SETUP_COMMAND)
  })
})

// ---------------------------------------------------------------------------
// Plan: validate, serialise once, hold, show
// ---------------------------------------------------------------------------

describe('setup.plan', () => {
  it('refuses a payload that is not the answers and nothing else', async () => {
    // The two shapes a renderer supplies a path or a flag in: a member beside
    // the answers (a path to an answers file of its choosing) and no answers at
    // all. Both are refused before anything is made or spawned.
    const fresh = aWizard({ floor: '0.120.0' })
    expect(
      await refusalOf(() =>
        fresh.wizard['setup.plan']({
          answers: anAnswers(),
          path: '/tmp/answers.json',
        } as unknown as ValidatedArguments),
      ),
    ).toBe(SETUP_PLAN_UNKNOWN_ARGUMENT)
    // No `answers` member at all: the answers are read as "not an object". A
    // payload this shape never reaches the handler once task AL's own
    // `validateArguments` declares `answers` required -- this is the handler's
    // own half of the same refusal, and it spawns nothing either way.
    expect(await refusalOf(() => fresh.wizard['setup.plan']({} as ValidatedArguments))).toBe(
      SETUP_ANSWERS_NOT_AN_OBJECT,
    )
    expect(fresh.calls).toEqual([])
    expect(fresh.made).toEqual([])
    expect(fresh.confirmer.opened).toEqual([])
    // ...and the answers themselves are held to their grammars by the same
    // door, so a path cannot arrive as a *value* either.
    expect(
      await refusalOf(() =>
        fresh.wizard['setup.plan'](planArgs(anAnswers({ answers_file: '/tmp/answers.json' }))),
      ),
    ).toBe(`${SETUP_ANSWERS_UNKNOWN_KEY}: answers_file`)
    expect(await refusalOf(() => fresh.wizard['setup.plan'](planArgs('answers.json')))).toBe(
      SETUP_ANSWERS_NOT_AN_OBJECT,
    )
    expect(fresh.calls).toEqual([])
    expect(fresh.made).toEqual([])
  })

  it('runs the plan row with the held bytes on the child\'s stdin', async () => {
    const { wizard, calls, made } = aWizard({
      floor: '0.120.0',
      feeds: [aFeed([{ type: 'stage', name: 'installation', state: 'will-do' }, DONE])],
    })
    const answer = await wizard['setup.plan'](planArgs(anAnswers()))
    expect(calls).toHaveLength(1)
    const call = calls[0] as SpawnCall
    expect(call.command).toBe(BINARY)
    expect(commandLine(call)).toEqual([...PLAN_ARGV])
    // The child's working directory is the fresh private one main made, and the
    // row's `--answers-file -` is what tells the CLI to read its stdin.
    expect(call.options.cwd).toBe(made[0])
    expect(call.args).toContain('--answers-file')
    expect(call.args[call.args.indexOf('--answers-file') + 1]).toBe('-')
    // The bytes really are the answers file, and the child's stream was ended:
    // a CLI reading stdin to EOF waits for ever on a stream left open.
    expect(call.child.bytes).toHaveLength(1)
    expect(JSON.parse(new TextDecoder().decode(call.child.bytes[0] as Uint8Array))).toMatchObject({
      version: 1,
      gcp_project: PROJECT,
      region: REGION,
      rigor: RIGOR,
    })
    expect(call.child.ended).toEqual([true])
    expect(answer.document.lines).toEqual([
      { type: 'stage', name: 'installation', state: 'will-do' },
      DONE,
    ])
  })

  it('shows the answers main parsed, not the object the renderer sent', async () => {
    const { wizard, confirmer, made } = aWizard({ floor: '0.120.0' })
    await wizard['setup.plan'](planArgs(anAnswers({ name: undefined, budget: BUDGET, bake_repos: undefined })))
    expect(confirmer.opened).toHaveLength(1)
    const subject = confirmer.opened[0]?.subject
    // The view is the main half's own builder over the parsed answers, and it is
    // a *copy*: what the window reads cannot be changed under it between the
    // paint and the answer.
    expect(subject).toEqual(
      setupView({
        project: PROJECT,
        region: REGION,
        // The renderer left the name out, so main parsed it as the empty string,
        // which the window draws as `none` -- the CLI refuses a setup that
        // names no project, rather than suggesting one. The renderer's own
        // object reaches no row.
        name: '',
        rigor: RIGOR,
        // The switch was left out too, and the window says what that means
        // (the CLI bakes) rather than drawing the absence as a `no`.
        bakeRepos: undefined,
        digest: subject?.kind === 'setup' ? subject.digest : '',
        planArgv: PLAN_ARGV,
        applyArgv: APPLY_ARGV,
      }),
    )
    expect(subject?.kind).toBe('setup')
    if (subject?.kind !== 'setup') throw new Error('the window was not shown a setup')
    expect(subject.project).toBe(PROJECT)
    expect(subject.region).toBe(REGION)
    expect(subject.name).toBe('')
    expect(subject.rigor).toBe(RIGOR)
    // Not `no`: nobody answered the switch, and the CLI's rule for an absent
    // `bake_repos` is that it bakes, so the row has to say what will happen.
    expect(subject.bake).toBe(SETUP_BAKE_DEFAULT)
    expect(subject.digest).toMatch(/^[0-9a-f]{64}$/)
    // The window shows both command lines field by field, so what it shows is
    // what is spawned and not a summary of it.
    expect(subject.planArgv).toEqual([...PLAN_ARGV])
    expect(subject.applyArgv).toEqual([...APPLY_ARGV])
    expect(subject.notes).toEqual(SETUP_NOTES)
    expect(Object.isFrozen(subject)).toBe(true)
    // The window's identity is the project the answers name, so two setups in
    // flight are two windows (`confirmSubjectID`).
    expect(setupConfirmID(PROJECT)).toBe(`setup:${PROJECT}`)
    // The digest is the SHA-256 of the very bytes the child was given.
    expect(subject.digest).toBe(
      setupAnswersDigest(
        serialiseSetupAnswers({
          gcp_project: PROJECT,
          region: REGION,
          name: '',
          rigor: RIGOR,
          budget: BUDGET,
          bake_repos: undefined,
        }),
      ),
    )
    expect(made).toHaveLength(1)
  })

  it('says yes as `yes` for a bake switch the answers set', async () => {
    const { wizard, confirmer } = aWizard({ floor: '0.120.0' })
    await wizard['setup.plan'](planArgs(anAnswers({ bake_repos: true })))
    const subject = confirmer.opened[0]?.subject
    if (subject?.kind !== 'setup') throw new Error('the window was not shown a setup')
    expect(subject.bake).toBe(SETUP_BAKE_YES)
    expect(subject.name).toBe(NAME)
  })

  it('says no as `no`, and hands the child a written `false` for it', async () => {
    const { wizard, confirmer, calls } = aWizard({ floor: '0.120.0' })
    await wizard['setup.plan'](planArgs(anAnswers({ bake_repos: false })))
    const subject = confirmer.opened[0]?.subject
    if (subject?.kind !== 'setup') throw new Error('the window was not shown a setup')
    expect(subject.bake).toBe(SETUP_BAKE_NO)
    // The row and the bytes have to agree, and it is the bytes the CLI reads: an
    // absent `bake_repos` is *yes* to it, so a declined switch that was left out
    // of the file would bake while the window said it would not.
    const document = JSON.parse(
      new TextDecoder().decode((calls[0] as SpawnCall).child.bytes[0] as Uint8Array),
    ) as Record<string, unknown>
    expect(document['bake_repos']).toBe(false)
  })

  it('takes a blank name as unanswered: held empty, no key, and `none` on the row', async () => {
    const { wizard, confirmer, calls } = aWizard({ floor: '0.120.0' })
    await wizard['setup.plan'](planArgs(anAnswers({ name: '' })))
    const subject = confirmer.opened[0]?.subject
    if (subject?.kind !== 'setup') throw new Error('the window was not shown a setup')
    // The renderer sends the field it drew, so a name the operator left **empty**
    // arrives as the empty string -- and that is *unanswered*, not malformed: the
    // CLI's own reader says so (`setupAnswers.answer`: `name` is answered when it
    // is not the empty string), and the answers file's schema makes `name`
    // optional for the same reason. Refusing it would refuse a state the CLI is
    // designed to take, and would take away the `left_for_you` line naming
    // `--name`, which is how a setup nobody named is handed back to the operator.
    expect(subject.name).toBe('')
    // The bytes leave the key out rather than writing it blank, so one state has
    // one spelling in the file.
    const document = JSON.parse(
      new TextDecoder().decode((calls[0] as SpawnCall).child.bytes[0] as Uint8Array),
    ) as Record<string, unknown>
    expect(Object.keys(document)).toEqual(['version', 'gcp_project', 'region', 'rigor', 'bake_repos'])
    expect(document).not.toHaveProperty('name')
    // And the row is drawn through the renderer's own constant: a name row left
    // blank is a field an operator reads as "the console lost it", where `none`
    // says the answers left the naming to the CLI's own line.
    const rendered = fakeDocument()
    await mountConfirm(rendered, fakeBridge(subject).bridge)
    expect(fieldValue(rendered, 'Name')).toBe(NONE)
    expect(NONE).toBe('none')
  })

  it('serialises the answers once, and reuses that buffer', async () => {
    const { wizard, calls, serialiseCalls } = aWizard({ floor: '0.120.0' })
    await wizard['setup.plan'](planArgs(anAnswers()))
    expect(serialiseCalls()).toBe(1)
    // The two children are given the **same** `Uint8Array`: the second spawn
    // hands `runToCompletion` the buffer the record holds, and not a new one.
    const wizard2 = aWizard({ floor: '0.120.0' })
    await wizard2.wizard['setup.plan'](planArgs(anAnswers()))
    expect(wizard2.serialiseCalls()).toBe(1)
    expect(commandLine(calls[0] as SpawnCall)).toEqual([...PLAN_ARGV])
  })

  it('shows no window and answers the sentence when the plan child refused', async () => {
    // A child that printed no line at all is a CLI that died before it could say
    // anything -- a bad flag, a config refusal, a missing binary -- and the
    // sentence is its own, with the console's own only when it said nothing.
    const said = aWizard({ floor: '0.120.0', feeds: [''] })
    const sentence = await refusalOf(() => said.wizard['setup.plan'](planArgs(anAnswers())))
    expect(sentence).toBe('setup printed no events')
    expect(said.confirmer.opened).toEqual([])
    // The directory goes with the refusal, and no record is held: the next plan
    // is a new wizard rather than a pending one.
    expect(said.removed).toEqual(said.made)
    expect(await refusalOf(() => said.wizard['setup.apply']({}))).toBe(SETUP_NO_PLAN_REFUSAL)
  })

  it('shows no window for a plan whose last line is `error`, and answers its sentence', async () => {
    // `setup --json` refuses what it cannot ask about, and a missing `--name` is
    // exactly that: with no terminal `init_inputs.go`'s `canAsk` is false, so the
    // CLI prints one `error` line and exits 1. A window opened over that feed
    // would ask the operator to confirm a run that has already failed, with a yes
    // whose apply fails the same way.
    const failed = aWizard({
      floor: '0.120.0',
      feeds: [
        aFeed([
          {
            type: 'error',
            stage: '',
            error:
              "there is no project config yet: pass --name <name> (the project's name), --gcp-project and --region to create one",
            fix: '',
            exit_code: 1,
          },
        ]),
      ],
      code: 1,
    })
    expect(
      await refusalOf(() => failed.wizard['setup.plan'](planArgs(anAnswers({ name: undefined })))),
    ).toBe(
      "there is no project config yet: pass --name <name> (the project's name), --gcp-project and --region to create one",
    )
    expect(failed.confirmer.opened).toEqual([])
    // The directory goes with the refusal and no record is held, so there is
    // nothing a yes could apply.
    expect(failed.removed).toEqual(failed.made)
    expect(await refusalOf(() => failed.wizard['setup.apply']({}))).toBe(SETUP_NO_PLAN_REFUSAL)
    expect(failed.calls).toHaveLength(1)
  })

  it('reads a plan feed\'s ending: `error` refuses, `done` does not', () => {
    // The sentence reaches the renderer *as a sentence* and not as a feed, so the
    // stage the run stopped at has nowhere else to travel.
    expect(
      setupFailureSentence({
        type: 'error',
        stage: 'project',
        error: 'no billing account is linked',
        fix: 'link one, then run setup again',
        exit_code: 1,
      }),
    ).toBe('project: no billing account is linked (link one, then run setup again)')
    expect(setupFailureSentence({ type: 'error', stage: '', error: '', exit_code: 1 })).toBe(
      SETUP_FAILED_WITHOUT_A_REASON,
    )
    // The last line decides, exactly as it does for `done`: a run that left steps
    // for the operator ends `done` at exit 1 and is not a refusal.
    expect(setupFeedFailure([DONE])).toBeNull()
    expect(setupFeedFailure([])).toBeNull()
    // An `error` line in the middle does not make a finished run a refusal; a
    // feed ends with one terminator (ST2).
    expect(setupFeedFailure([{ type: 'error', stage: '', error: 'a stage failed' }, DONE])).toBeNull()
  })

  it('refuses a second plan while one is still waiting for its answer', async () => {
    const { wizard, calls, made, confirmer } = aWizard({ floor: '0.120.0' })
    await wizard['setup.plan'](planArgs(anAnswers()))
    expect(calls).toHaveLength(1)
    expect(confirmer.opened).toHaveLength(1)
    // One wizard at a time: the bytes the first window showed are the bytes the
    // operator agreed to, so they may not be replaced by a second plan's.
    expect(await refusalOf(() => wizard['setup.plan'](planArgs(anAnswers({ gcp_project: 'other-b2' }))))).toBe(
      SETUP_PLAN_PENDING_REFUSAL,
    )
    expect(calls).toHaveLength(1)
    expect(made).toHaveLength(1)
    expect(confirmer.opened).toHaveLength(1)
  })

  it('ends the run on the CLI\'s own exit, never on the 90 s read deadline', () => {
    // A `--plan-only` run computes the Terraform plan and an apply runs it; both
    // are the minutes a cloud project takes, so the wizard carries a deadline of
    // its own.
    expect(SETUP_DEADLINE_MS).toBe(60 * 60 * 1000)
  })
})

// ---------------------------------------------------------------------------
// Apply: the window's yes, and the digest
// ---------------------------------------------------------------------------

describe('setup.apply', () => {
  it('spawns nothing before the window has said yes', async () => {
    const { wizard, calls, confirmer } = aWizard({ floor: '0.120.0' })
    await wizard['setup.plan'](planArgs(anAnswers()))
    // The renderer's door, with no answer from the window: a frame cannot start
    // a setup, and neither can the channel alone.
    expect(await refusalOf(() => wizard['setup.apply']({}))).toBe(SETUP_NO_YES_REFUSAL)
    expect(calls).toHaveLength(1)
    expect(confirmer.opened).toHaveLength(1)
  })

  it('refuses a renderer that sends anything at all with it', async () => {
    // The payload is empty by contract: apply reuses the answers and the digest
    // main held at plan time, so a renderer that sent `{yes: true}`, or new
    // answers, or a path, must spawn nothing.
    const { wizard, calls, confirmer } = aWizard({ floor: '0.120.0' })
    await wizard['setup.plan'](planArgs(anAnswers()))
    confirmer.opened[0]?.decide(true)
    await flush()
    for (const payload of [
      { yes: true },
      { answers: anAnswers() },
      { path: '/tmp/answers.json' },
      { ticked: true },
    ]) {
      expect(await refusalOf(() => wizard['setup.apply'](payload as unknown as ValidatedArguments))).toBe(
        SETUP_APPLY_UNKNOWN_ARGUMENT,
      )
    }
    // The yes the *window* gave is what spawned the one apply, and it is the
    // only child after the plan.
    expect(calls).toHaveLength(2)
  })

  it('runs the apply row with the very bytes the plan showed', async () => {
    // **The red step.** While apply re-serialised the answers this comparison
    // fails: the two children's stdin would be two buffers that happen to be
    // equal today, and the point of holding the bytes is that there is only one.
    const { wizard, calls, confirmer, serialiseCalls } = aWizard({ floor: '0.120.0' })
    await wizard['setup.plan'](planArgs(anAnswers()))
    confirmer.opened[0]?.decide(true)
    await flush()
    const answer = await wizard['setup.apply']({})
    expect(calls).toHaveLength(2)
    const planCall = calls[0] as SpawnCall
    const applyCall = calls[1] as SpawnCall
    expect(commandLine(applyCall)).toEqual([...APPLY_ARGV])
    expect(applyCall.options.cwd).toBe(planCall.options.cwd)
    // Byte for byte, as bytes: a decode would hide exactly the difference this
    // is looking for.
    expect(Array.from(applyCall.child.bytes[0] as Uint8Array)).toEqual(
      Array.from(planCall.child.bytes[0] as Uint8Array),
    )
    const subject = confirmer.opened[0]?.subject
    if (subject?.kind !== 'setup') throw new Error('the window was not shown a setup')
    expect(setupAnswersDigest(applyCall.child.bytes[0] as Uint8Array)).toBe(subject.digest)
    // Serialised once for the whole wizard, and this is the assertion that makes
    // "once" a fact: a second call is the re-serialising the red step names.
    expect(serialiseCalls()).toBe(1)
    expect(answer.document.lines).toEqual([DONE])
  })

  it('spawns no second child when the renderer asks after the yes already ran it', async () => {
    const { wizard, calls, confirmer } = aWizard({ floor: '0.120.0' })
    await wizard['setup.plan'](planArgs(anAnswers()))
    confirmer.opened[0]?.decide(true)
    const first = await wizard['setup.apply']({})
    expect(first.document.lines).toEqual([DONE])
    expect(calls).toHaveLength(2)
    // The record is spent, and nothing is offered twice: the next plan is a new
    // wizard rather than this one's plan.
    expect(await refusalOf(() => wizard['setup.apply']({}))).toBe(SETUP_NO_PLAN_REFUSAL)
    expect(calls).toHaveLength(2)
  })

  it('refuses `plan, plan, yes` and applies nothing', async () => {
    // The sequence the brief names: a second plan is refused while the first is
    // pending, and the yes the second call would have carried -- a renderer's
    // `{yes: true}`, since the second plan never got a window -- applies nothing.
    // The first window's own yes is the only thing that applies (the case above).
    const { wizard, calls, confirmer } = aWizard({ floor: '0.120.0' })
    await wizard['setup.plan'](planArgs(anAnswers()))
    expect(await refusalOf(() => wizard['setup.plan'](planArgs(anAnswers({ rigor: 'pro' }))))).toBe(
      SETUP_PLAN_PENDING_REFUSAL,
    )
    expect(await refusalOf(() => wizard['setup.apply']({ yes: true } as unknown as ValidatedArguments))).toBe(
      SETUP_APPLY_UNKNOWN_ARGUMENT,
    )
    expect(calls).toHaveLength(1)
    expect(confirmer.opened).toHaveLength(1)
    // The first wizard is still the one held, and its own yes still applies --
    // with the bytes of *its* answers, and never the refused second plan's.
    confirmer.opened[0]?.decide(true)
    await flush()
    const answer = await wizard['setup.apply']({})
    expect(calls).toHaveLength(2)
    expect(answer.document.lines).toEqual([DONE])
    expect(new TextDecoder().decode((calls[1] as SpawnCall).child.bytes[0] as Uint8Array)).toContain(
      `"rigor":"${RIGOR}"`,
    )
  })

  it('refuses a byte that changed under the window, and spawns nothing', async () => {
    // The one attack the held bytes exist for: something writes to the buffer
    // between the digest the window showed and the run. Nothing in this console
    // can; a buffer that changed is a buffer something else wrote to.
    const holder: { held: Uint8Array | null } = { held: null }
    const { wizard, calls, confirmer, removed } = aWizard({
      floor: '0.120.0',
      plan: (answers) => {
        const bytes = serialiseSetupAnswers(answers)
        holder.held = bytes
        return bytes
      },
      digest: (held) => setupAnswersDigest(held),
    })
    await wizard['setup.plan'](planArgs(anAnswers()))
    const held = holder.held
    if (held === null) throw new Error('the serialiser was not called')
    // One byte, changed under the window.
    held[0] = 0x20
    confirmer.opened[0]?.decide(true)
    await flush()
    expect(await refusalOf(() => wizard['setup.apply']({}))).toBe(SETUP_BYTES_CHANGED_REFUSAL)
    expect(calls).toHaveLength(1)
    // The directory goes with the refusal rather than staying behind a record
    // nothing will use.
    expect(removed).toHaveLength(1)
  })

  it('stops the apply the window’s own yes starts when the licence has lapsed', async () => {
    // **The lapse is asked where the child is spawned, and not only at the
    // door.** The window's yes reaches `runApply` from `decide`, and no IPC
    // handler is on that path at all -- so `lapseHandlers`' gate on
    // `setup.apply` (the row's own `refusedAfterLapse` column) is not what stops
    // a plan the operator made after the lapse, which is kept and opens its
    // window. The plan is a dry run and stays reachable; the apply it stands for
    // is the row the lapse takes away, and this is that column on the window's
    // path.
    let lapsed = true
    const feeds = [aFeed([DONE]), aFeed([DONE]), aFeed([DONE])]
    const { wizard, calls, confirmer, removed } = aWizard({
      floor: '0.120.0',
      lapsed: () => lapsed,
      feeds,
    })
    await wizard['setup.plan'](planArgs(anAnswers()))
    // The plan is a child of its own and runs: the lapse takes the apply, not the
    // plan the operator reads before deciding.
    expect(calls).toHaveLength(1)
    confirmer.opened[0]?.decide(true)
    await flush()
    // **Nothing was spawned**, and the renderer's own door answers the lapse's
    // sentence -- the one sentence a lapsed console gives (`licence-state.ts`) --
    // rather than the apply's. The working directory goes with the refusal
    // instead of staying behind a record nothing will use.
    expect(calls).toHaveLength(1)
    expect(await refusalOf(() => wizard['setup.apply']({}))).toBe(LAPSE_REFUSAL)
    expect(removed).toHaveLength(1)

    // **The answer is read per apply and not captured once**: a renewal pasted
    // during the session gives the apply back at the next plan, which is why the
    // wiring asks the predicate instead of holding its answer.
    lapsed = false
    await wizard['setup.plan'](planArgs(anAnswers()))
    confirmer.opened[1]?.decide(true)
    await flush()
    expect(calls).toHaveLength(3)
    expect((await wizard['setup.apply']({})).document.lines).toEqual([DONE])
  })
  it('gives the record and the directory up on a no, and applies nothing', async () => {
    const { wizard, calls, confirmer, removed } = aWizard({ floor: '0.120.0' })
    await wizard['setup.plan'](planArgs(anAnswers()))
    confirmer.opened[0]?.decide(false)
    await flush()
    expect(removed).toHaveLength(1)
    expect(calls).toHaveLength(1)
    expect(await refusalOf(() => wizard['setup.apply']({}))).toBe(SETUP_NO_PLAN_REFUSAL)
    expect(calls).toHaveLength(1)
    // A second plan after a no is a new wizard: the decline gave the record up
    // rather than leaving it pending for ever.
    await wizard['setup.plan'](planArgs(anAnswers()))
    expect(calls).toHaveLength(2)
  })

  it('never passes a confirming word, and spawns the allow list\'s own argv', async () => {
    // O6, and the whole of what this window's yes may not be: `--json` confirms
    // no step, so nothing the console passes may confirm one either. The argv is
    // compared with AL's rows word for word, so a word main added would show up
    // here as a difference.
    const { wizard, calls, confirmer } = aWizard({ floor: '0.120.0' })
    await wizard['setup.plan'](planArgs(anAnswers()))
    confirmer.opened[0]?.decide(true)
    await flush()
    await wizard['setup.apply']({})
    expect(calls).toHaveLength(2)
    expect(commandLine(calls[0] as SpawnCall)).toEqual([...PLAN_ARGV])
    expect(commandLine(calls[1] as SpawnCall)).toEqual([...APPLY_ARGV])
    for (const call of calls) {
      for (const word of commandLine(call)) {
        for (const forbidden of CONFIRMING_WORDS) {
          expect(word, `${forbidden} reached the command line`).not.toBe(forbidden)
        }
      }
    }
    // And the words that could only ever confirm are absent from the answers
    // main serialised as well: there is nowhere for a flag to hide.
    const held = new TextDecoder().decode((calls[1] as SpawnCall).child.bytes[0] as Uint8Array)
    for (const forbidden of CONFIRMING_WORDS) expect(held).not.toContain(forbidden)
  })
})

// ---------------------------------------------------------------------------
// Every event line, and every sentence, through its class
// ---------------------------------------------------------------------------

describe('the events the console answers with', () => {
  it('puts every named field through its class, and rebuilds the document it arrived in', async () => {
    // A `left_for_you` line is the CLI's own sentence and it can carry anything
    // a cloud project put in it. Every field the contract names goes through
    // `NEW_OPERATION_TEXT_CLASSES` before it reaches the renderer.
    const hostile: JSONObject = {
      type: 'left_for_you',
      stage: 'installation',
      kind: 'prompt',
      text: `run it: https://evil.invalid/x now /Users/ana/plan.md`,
      commands: ['fugaro setup'],
    }
    const zeroWidth: JSONObject = {
      type: 'stage',
      name: 'installation',
      state: 'needs-you',
      // A zero-width space, which the sentence class removes: a value that used
      // one to smuggle a second spelling of a name is not the value that is
      // drawn.
      detail: 'a\u200btitle',
      after: '',
    }
    const { wizard } = aWizard({
      floor: '0.120.0',
      feeds: [aFeed([zeroWidth, hostile, DONE])],
    })
    const answer = await wizard['setup.plan'](planArgs(anAnswers()))
    const lines = answer.document.lines as readonly JSONObject[]
    expect(lines).toHaveLength(3)
    const drawnHostile = lines[1] as JSONObject
    // The URL and the path are drawn as written: neither carries a credential,
    // and this layer replaces a value only for the credential in it (D3213), so
    // the sentence the CLI wrote reaches the window whole.
    expect(drawnHostile['text']).toBe('run it: https://evil.invalid/x now /Users/ana/plan.md')
    expect(drawnHostile['text']).toContain('evil.invalid')
    expect(drawnHostile['text']).toContain('/Users/ana')
    // A field with no class is carried as it arrived (`kind` is not in the
    // table), and the walk still rebuilt the document: nothing the renderer
    // reads is the object the child printed.
    expect(drawnHostile['kind']).toBe('prompt')
    expect(drawnHostile).not.toBe(hostile)
    expect(lines[0]).toMatchObject({ detail: 'atitle' })
    expect(JSON.stringify(answer)).not.toContain('​')
    // The console's own placeholder is what a refused field is drawn as, and
    // never the refused value: the wire reads a missing member as "the CLI has
    // nothing", so the field has to be there with something in it.
    expect(JSON.stringify(answer)).not.toContain(LITERAL_PLACEHOLDER)
  })

  it('reads the CLI\'s own event golden without losing a line', async () => {
    // ST2's fixture is the shapes this feed really carries. Reading it here
    // pins that every one of them parses and draws: a class that refused a field
    // the CLI really writes would show up as a placeholder in a line an operator
    // needs, and this is where it would be visible.
    const golden = readFileSync(
      fileURLToPath(new URL('../../../internal/cli/testdata/setup_events.golden.ndjson', import.meta.url)),
      'utf8',
    )
    const raw = golden.split('\n').filter((line) => line.trim() !== '')
    // The fixture holds three runs, each ended by its own end-of-feed line
    // (`done`, `done`, `error`), and they are read one run at a time: a feed is
    // one run, and the last line is what says how it ended.
    const runs: JSONObject[][] = []
    let run: JSONObject[] = []
    for (const line of raw) {
      const parsed = JSON.parse(line) as JSONObject
      run.push(parsed)
      if (parsed['type'] === 'done' || parsed['type'] === 'error') {
        runs.push(run)
        run = []
      }
    }
    expect(run).toEqual([])
    expect(runs).toHaveLength(3)

    for (const done of [runs[0] as JSONObject[], runs[1] as JSONObject[]]) {
      const finished = aWizard({ floor: '0.120.0', feeds: [aFeed(done)] })
      const answer = await finished.wizard['setup.plan'](planArgs(anAnswers()))
      const lines = answer.document.lines as readonly JSONObject[]
      // Every line is there, in order, with its own `type`: a class that refused
      // a field would not lose the line, and this is the assertion that says the
      // feed is answered whole.
      expect(lines.map((line) => line['type'])).toEqual(done.map((line) => line['type']))
      expect(lines.at(-1)).toMatchObject({ type: 'done' })
      expect(finished.confirmer.opened).toHaveLength(1)
      // ...and every sentence the golden wrote is drawn as the CLI's own words:
      // a bucket is the operator's own resource and not a credential (D3213), so
      // the layer line that names `gs://fugaro-runs-proj-1234` arrives whole
      // rather than masked, and so does every other line an operator reads.
      for (const [at, line] of done.entries()) {
        if (line['type'] !== 'stage') continue
        expect(lines[at]?.['detail'], JSON.stringify(line['detail'])).toBe(line['detail'])
      }
    }

    // The third run ends in `error`, and that is the plan's own refusal: the
    // CLI's sentence, and no window to confirm it in.
    const refused = aWizard({
      floor: '0.120.0',
      feeds: [aFeed(runs[2] as JSONObject[])],
      code: 1,
    })
    const sentence = await refusalOf(() => refused.wizard['setup.plan'](planArgs(anAnswers())))
    expect(sentence).toBe((runs[2] as JSONObject[]).at(-1)?.['error'])
    expect(refused.confirmer.opened).toEqual([])
  })
})

// ---------------------------------------------------------------------------
// The window's document
// ---------------------------------------------------------------------------

/** FakeElement is one node of the confirmation document: the text, the four
 * properties the view touches, a child list, and the listeners a test fires. It
 * has **no markup slot** -- a view that reached for one is recorded and throws,
 * which is the window's own rule (`test/unit/confirm-view.test.ts` holds the
 * full account of that). */
class FakeElement implements ConfirmElement {
  textContent: string | null = null
  tabIndex = 0
  checked = false
  disabled = false
  value = ''
  readonly attributes = new Map<string, string>()
  readonly children: FakeElement[] = []
  focused = false
  readonly markup: string[] = []
  private readonly listeners = new Map<string, ((event: ConfirmEvent) => void | Promise<void>)[]>()

  constructor(readonly tag: string) {}

  set innerHTML(markup: string) {
    this.markup.push(markup)
    throw new Error('a value of the confirmation window is a text node, never markup')
  }

  addEventListener(type: string, listener: (event: ConfirmEvent) => void | Promise<void>): void {
    const held = this.listeners.get(type) ?? []
    held.push(listener)
    this.listeners.set(type, held)
  }

  setAttribute(name: string, value: string): void {
    this.attributes.set(name, value)
  }

  appendChild(child: ConfirmElement): unknown {
    this.children.push(child as FakeElement)
    return child
  }

  replaceChildren(...nodes: ConfirmElement[]): void {
    this.children.length = 0
    for (const node of nodes) this.children.push(node as FakeElement)
  }

  focus(): void {
    this.focused = true
  }

  async fire(type: string, detail = 0): Promise<void> {
    for (const listener of this.listeners.get(type) ?? []) await listener({ detail })
  }

  button(label: string): FakeElement {
    const found = this.children.find((child) => child.tag === 'button' && child.textContent === label)
    if (found === undefined) throw new Error(`no button labelled ${JSON.stringify(label)}`)
    return found
  }
}

class FakeDocument implements ConfirmDocument {
  readonly byId = new Map<string, FakeElement>()
  createElement(tag: string): ConfirmElement {
    return new FakeElement(tag)
  }
  getElementById(id: string): ConfirmElement | null {
    return this.byId.get(id) ?? null
  }
}

/** fakeDocument builds the five containers the view looks up by name, so the
 * seam between the document and the view is held. */
function fakeDocument(): FakeDocument {
  const document = new FakeDocument()
  for (const id of [
    'confirm-status',
    'confirm-fields',
    'confirm-task',
    'confirm-actions',
    'confirm-title',
    'confirm-task-heading',
  ]) {
    document.byId.set(id, new FakeElement('div'))
  }
  return document
}

/** setupFields is the subject the window is served: exactly what
 * `wizardrun.ts` hands `confirmer.open`, which is `setupView`'s own output. */
function setupFields(): ConfirmSubjectFields {
  return setupView({
    project: PROJECT,
    region: REGION,
    name: NAME,
    rigor: RIGOR,
    bakeRepos: true,
    digest: 'a'.repeat(64),
    planArgv: PLAN_ARGV,
    applyArgv: APPLY_ARGV,
  })
}

/** fakeBridge answers the two channels with the subject and records the
 * answers, so a test reads back exactly what the main process receives. */
function fakeBridge(subject: ConfirmSubjectFields): {
  readonly bridge: ConfirmBridge
  readonly answers: readonly ConfirmAnswer[]
} {
  const answers: ConfirmAnswer[] = []
  return {
    bridge: {
      fields: async (): Promise<ConfirmReply> => ({ ok: true, data: subject }),
      answer: async (answer: ConfirmAnswer): Promise<ConfirmReply> => {
        answers.push(answer)
        return { ok: true, data: { answered: true } }
      },
    },
    answers,
  }
}

/** fieldValue is the `<dd>` a `<dt>` labels, with a `<code>` child's text. */
function fieldValue(document: FakeDocument, label: string): string | null {
  const fields = document.byId.get('confirm-fields') as FakeElement
  const index = fields.children.findIndex((child) => child.tag === 'dt' && child.textContent === label)
  if (index === -1) return null
  const cell = fields.children[index + 1]
  if (cell === undefined) return null
  return cell.textContent ?? cell.children.map((child) => child.textContent ?? '').join('')
}

/** wordsUnder are the `<dd>`s directly under one `<dt>`: the two command lines
 * are drawn one word per row. */
function wordsUnder(document: FakeDocument, label: string): readonly (string | null)[] {
  const fields = document.byId.get('confirm-fields') as FakeElement
  const start = fields.children.findIndex((child) => child.tag === 'dt' && child.textContent === label)
  const words: (string | null)[] = []
  for (let index = start + 1; index < fields.children.length; index += 1) {
    const child = fields.children[index]
    if (child === undefined || child.tag !== 'dd') break
    words.push(child.textContent)
  }
  return words
}

describe('the window the setup view is drawn in', () => {
  it('draws the five answers, the digest, both commands and the four sentences', async () => {
    const document = fakeDocument()
    const { bridge } = fakeBridge(setupFields())
    await mountConfirm(document, bridge)
    expect((document.byId.get('confirm-title') as FakeElement).textContent).toBe(TITLE_SETUP)
    // The `<pre>` above the rows holds a launch's task and nothing for a setup:
    // a heading over an empty box is a field an operator looks for a value in.
    expect((document.byId.get('confirm-task-heading') as FakeElement).textContent).toBe('')
    // The five answers **as main parsed them**, the digest last of the rows.
    expect(fieldValue(document, 'Project')).toBe(PROJECT)
    expect(fieldValue(document, 'Region')).toBe(REGION)
    expect(fieldValue(document, 'Name')).toBe(NAME)
    expect(fieldValue(document, 'Rigor')).toBe(RIGOR)
    expect(fieldValue(document, 'Bake repositories')).toBe(SETUP_BAKE_YES)
    expect(fieldValue(document, 'SHA-256 of the answers')).toBe('a'.repeat(64))
    // Both command lines, field by field, under their own labels: one window
    // spawns two children in turn, and one label for both would leave the
    // operator guessing which line is which.
    expect(wordsUnder(document, 'Plan command')).toEqual([...PLAN_ARGV])
    expect(wordsUnder(document, 'Apply command')).toEqual([...APPLY_ARGV])
    // The four sentences of what a yes does, one to a line, and every one of
    // them is the main process's own.
    const fields = document.byId.get('confirm-fields') as FakeElement
    const start = fields.children.findIndex((child) => child.tag === 'dt' && child.textContent === 'What this does')
    const notes = fields.children.slice(start + 1).map((child) => child.textContent)
    expect(notes).toEqual([...SETUP_NOTES])
    expect(notes).toHaveLength(4)
    // No element of a markup kind was built: a value of this window is a text
    // node, and this fake throws if the view tries to write markup.
    for (const markups of [document.byId.get('confirm-fields') as FakeElement]) {
      expect(markups.markup).toEqual([])
    }
  })

  it('offers the pointer-only writing button and keeps Cancel focused', async () => {
    const document = fakeDocument()
    const { bridge, answers } = fakeBridge(setupFields())
    await mountConfirm(document, bridge)
    const actions = document.byId.get('confirm-actions') as FakeElement
    const confirm = actions.button(SETUP_LABEL)
    expect(SETUP_LABEL).toBe('Run setup')
    // Out of the tab order, and a keyboard-synthesized click is not an answer:
    // the window's own half of "starting a setup is a deliberate act".
    expect(confirm.tabIndex).toBe(-1)
    const cancel = actions.button(CANCEL_LABEL)
    expect(cancel.focused).toBe(true)
    expect(answers).toEqual([])
    await confirm.fire('click', 0)
    expect(answers).toEqual([])
    await confirm.fire('click', 1)
    expect(answers).toEqual([{ answer: 'yes' }])
    // A yes is drawn with no tick: a setup's view carries no tick words, so the
    // member is absent rather than `false` ("no box was drawn" and "the box is
    // clear" are two facts).
    const answered = answers[0]
    expect(answered?.answer === 'yes' ? answered.ticked : 'not a yes').toBeUndefined()
  })

  it('draws `none` for a name the answers left to the CLI', async () => {
    const document = fakeDocument()
    const { bridge } = fakeBridge(
      setupView({
        project: PROJECT,
        region: REGION,
        name: '',
        rigor: RIGOR,
        bakeRepos: false,
        digest: 'b'.repeat(64),
        planArgv: PLAN_ARGV,
        applyArgv: APPLY_ARGV,
      }),
    )
    await mountConfirm(document, bridge)
    expect(fieldValue(document, 'Name')).toBe(NONE)
  })

  it('draws the refusal sentence and no writing button when there is no window to show', async () => {
    const document = fakeDocument()
    const bridge: ConfirmBridge = {
      fields: async (): Promise<ConfirmReply> => ({ ok: false, reason: 'the read threw' }),
      answer: async (): Promise<ConfirmReply> => ({ ok: true, data: { answered: true } }),
    }
    await mountConfirm(document, bridge)
    expect((document.byId.get('confirm-status') as FakeElement).textContent).toBe('the read threw')
    const actions = document.byId.get('confirm-actions') as FakeElement
    expect(actions.button(CANCEL_LABEL).focused).toBe(true)
    expect(actions.children.filter((child) => child.textContent === SETUP_LABEL)).toEqual([])
  })
})
