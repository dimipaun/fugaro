// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The new operations, held to the surface, to the CLI and to each other
// (console-improvements plan I-OPS; the contract is
// `docs/console-improvements/contract-ops.md`).
//
// `new-ops.test.ts` is the contract against itself: the names, the payloads and
// the answer shapes. This file is the contract against **everything else**, and
// it is the one the plan's acceptance names -- four claims, in this order:
//
//   1. **every name of the contract is an operation of the allow-list**, with the
//      method the preload exposes and exactly the payload keys the row's own
//      argument schema closes, required where the request type is not optional.
//      The two tables are written by different tasks (C-OPS and AL) that import
//      nothing from each other, so this is the only place they are read together:
//      a name in one and not the other is a consumer that can reach nothing, a
//      key in one and not the other is a payload refused at the door or a field
//      the child never sees, and a *required* key in one and not the other is a
//      row that cannot be called at all.
//   2. **the four writes MW1 and MW2 added are outside the contract and on the
//      surface.** The check is one-directional by design: an operation added by a
//      later task grows the surface and not this file.
//   3. **the handlers behind the contract are the real ones**, reached through
//      the real door -- `registerConsoleHandlers`, with `isSenderOnDocument` in
//      front of `validateArguments` and the handler behind it -- and never with
//      the stub's `not available in this build`. With nothing chosen the answer
//      is `NO_ACTIVE_INSTALLATION`; with an installation the resolver will not
//      take it is the resolver's own sentence. Both are facts only a handler that
//      really ran can produce, and the second is chosen to be a case that is the
//      same on every machine (a **relative** override, which `resolveBinary`
//      refuses before it walks anything).
//   4. **the fixtures are the CLI's own recordings**, rendered back into the
//      framing the CLI writes them in -- NDJSON lines, or the two-space document
//      -- and compared byte for byte with the golden file each stands for. This
//      is the half of the acceptance a fixture cannot satisfy by being plausible.
//
// Plus the one seam that does not typecheck on its own: the answers `setup.plan`
// carries cross from the allow-list (where they are an object, serialised by main
// into the answers **file**) to the wizard (where they are the object again), and
// the two ends are held to each other here -- including why `setupAnswersFromFile`
// drops `version`, which is the wizard's own refusal of a key that is not one of
// its questions.

import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  CONSOLE_OPERATION_NAMES,
  CONSOLE_OPERATIONS,
  NO_ACTIVE_INSTALLATION,
  SETUP_ANSWER_KEYS,
  SETUP_BUDGET_MAX as IPC_SETUP_BUDGET_MAX,
  SETUP_BUDGET_MIN as IPC_SETUP_BUDGET_MIN,
  registerConsoleHandlers,
  validateArguments,
} from '../../src/main/ipc'
import type {
  ConsoleHandler,
  ConsoleHandlers,
  ConsoleReply,
  IpcMainLike,
} from '../../src/main/ipc'
import {
  NEW_OPERATION_HANDLER_NAMES,
  SETUP_ANSWERS_UNREADABLE,
  newOperationHandlers,
  setupAnswersFromFile,
} from '../../src/main/handlers'
import type { NewOperationDeps, NewOperationHandlerName } from '../../src/main/handlers'
import { SETUP_ANSWERS_UNKNOWN_KEY, SETUP_NO_PLAN_REFUSAL, setupFloorSentence, validateSetupAnswers } from '../../src/main/wizardrun'
import type { SetupAnswersHeld } from '../../src/main/wizardrun'
import { SETUP_CLI_FLOOR } from '../../src/shared/version'
import type { LaunchConfirmer } from '../../src/main/launch'
import type { SettingsEditorRunner } from '../../src/main/settingsedit'
import { imageAddConfirmID } from '../../src/main/imagesview'
import { modelsAddConfirmID } from '../../src/main/modelsview'
import { setupConfirmID } from '../../src/main/setupview'
import {
  IMAGE_ADD_OPERATION,
  IMAGE_ADD_PLAN_OPERATION,
  MODELS_ADD_OPERATION,
  MODELS_ADD_PLAN_OPERATION,
} from '../../src/main/spawn'
import type { ChildSpawner, SpawnedChildLike } from '../../src/main/spawn'
import { APP_ENTRY_URL } from '../../src/main/protocol'
import type { BoundInstallation } from '../../src/source/cli'
import {
  NEW_OPS_NOT_AVAILABLE,
  NEW_OPERATION_METHODS,
  NEW_OPERATION_NAMES,
  NEW_OPERATION_PAYLOAD_KEYS,
  SETUP_ANSWER_KEYS as CONTRACT_SETUP_ANSWER_KEYS,
  SETUP_BUDGET_MAX,
  SETUP_BUDGET_MIN,
  decodeNewOperationAnswer,
} from '../../src/shared/new-ops'
import type { NewOperationName, NewOperationRequest } from '../../src/shared/new-ops'
import { newReadFloorSentence } from '../../src/source/new-reads'
import { NEW_OP_ANSWERS, SETUP_REFUSED_LINES } from '../support/newOpAnswers'
import { itHostRoute } from '../support/hostRoute'

/** CONSOLE_ROOT and REPO_ROOT are this package and the repository it sits in:
 * five of the fixtures are the Go tree's own goldens, and a fixture here is held
 * to the file the CLI records. */
const CONSOLE_ROOT = join(__dirname, '..', '..')
const REPO_ROOT = join(CONSOLE_ROOT, '..')

/** THE_NINE is the contract's own list, in its own order. */
const THE_NINE: readonly NewOperationName[] = NEW_OPERATION_NAMES

/** THE_FOUR is what MW1 and MW2 added: operations of the allow-list, and
 * deliberately outside the contract (`new-ops.ts` says so from the other side,
 * and so does `handlers.ts`). */
const THE_FOUR: readonly NewOperationHandlerName[] = [
  MODELS_ADD_PLAN_OPERATION,
  MODELS_ADD_OPERATION,
  IMAGE_ADD_PLAN_OPERATION,
  IMAGE_ADD_OPERATION,
]

/** PAYLOADS is one **valid** payload per operation, so every case below reaches
 * a handler rather than the allow-list's own refusal. Each is the smallest
 * payload the row admits: the required keys of the contract's request type, with
 * values inside the row's own grammar. */
const PAYLOADS: Readonly<Record<NewOperationHandlerName, unknown>> = Object.freeze({
  'runs.stream': { since: '24h' },
  'spend.stream': { since: '30d' },
  'budget.stream': {},
  whoami: {},
  'image.catalog.ls': {},
  'recipes.ls': {},
  'setup.plan': {
    answers: {
      gcp_project: 'fugaro-dev',
      region: 'europe-west1',
      name: 'fugaro',
      rigor: 'startup',
      bake_repos: true,
    },
  },
  'setup.apply': {},
  'settings.editor.open': { key: 'agent.models.coder' },
  'models.add.plan': { id: 'deepseek/deepseek-v4', route: 'deepseek' },
  'models.add': { id: 'deepseek/deepseek-v4', route: 'deepseek' },
  'image.add.plan': { name: 'node22', dockerfile: 'acme/images:images/node22/Dockerfile' },
  'image.add': { name: 'node22', dockerfile: 'acme/images:images/node22/Dockerfile' },
})

/**
 * REQUIRED_BY_REQUEST is the keys each row must mark required: the non-optional
 * members of `NewOperationRequest[name]`, written out.
 *
 * The `satisfies` keeps this table honest about the request types rather than
 * about itself: every listed key has to be `keyof` that operation's request, a
 * typo'd or renamed key is a compile error, and a name of the contract missing
 * from the table -- or one that is not a name of the contract at all -- fails the
 * mapped type. It cannot check an *empty* list, because `[]` is assignable to
 * any array type; that half is what the runtime case below is for, comparing
 * these keys with the row's own `required` column.
 */
const REQUIRED_BY_REQUEST = {
  'runs.stream': ['since'],
  'spend.stream': [],
  'budget.stream': [],
  whoami: [],
  'image.catalog.ls': [],
  'recipes.ls': [],
  'setup.plan': ['answers'],
  'setup.apply': [],
  'settings.editor.open': ['key'],
} satisfies {
  readonly [Name in NewOperationName]: readonly (keyof NewOperationRequest[Name] & string)[]
}

/** anInstallation is one bound installation, with the fields this file does not
 * care about left to the case. */
function anInstallation(overrides: Partial<BoundInstallation>): BoundInstallation {
  return { id: 'inst-1', project: 'fugaro-dev', ...overrides }
}

/** reasonOf is a reply's refusal sentence -- or a sentence saying it did not
 * refuse at all, so a case that expects a refusal and is handed `ok` says so
 * rather than comparing `undefined` with a string. */
function reasonOf(reply: ConsoleReply): string {
  if (reply.ok) return `it answered ok (${JSON.stringify(reply.data)})`
  return reply.reason
}

/** Surface is the console's own door with this module's thirteen rows behind it. */
interface Surface {
  /** the thirteen rows, as `handlers.ts` built them. */
  readonly rows: ReturnType<typeof newOperationHandlers>['rows']
  /** call sends one message through `registerConsoleHandlers`: the sender gate,
   * then `validateArguments`, then the row. */
  call(name: NewOperationHandlerName, payload: unknown): Promise<ConsoleReply>
}

/**
 * surfaceWith builds the thirteen rows over `deps` and registers them the way
 * `index.ts` does, over a recording `ipcMain`.
 *
 * The allow-list's other rows are this file's own and throw if they are ever
 * reached, and the row set is held to the surface's names here rather than to a
 * count because `registerConsoleHandlers` refuses to build a table with one
 * missing. What is under test is the thirteen, reached by the path a renderer's
 * message takes -- `isSenderOnDocument`, then `validateArguments`, then the
 * handler -- rather than by calling a row directly: "the row is a function" is
 * not "the row answers a message", and that difference is the point.
 */
function surfaceWith(deps: NewOperationDeps): Surface {
  const rows = newOperationHandlers(deps).rows
  const table: Record<string, ConsoleHandler> = {}
  for (const operation of CONSOLE_OPERATIONS) {
    table[operation.name] = () => {
      throw new Error(`${operation.name} is not one of the rows this file stands behind`)
    }
  }
  Object.assign(table, rows)
  const channels = new Map<string, (event: unknown, raw: unknown) => unknown>()
  const ipcMain: IpcMainLike = {
    handle: (channel, listener) => {
      channels.set(channel, listener as (event: unknown, raw: unknown) => unknown)
    },
  }
  registerConsoleHandlers(ipcMain, table as unknown as ConsoleHandlers)
  return {
    rows,
    call: async (name, payload) => {
      const listener = channels.get(`fugaro:console/${name}`)
      if (listener === undefined) throw new Error(`${name} registered no channel`)
      const frame = { url: APP_ENTRY_URL }
      return (await listener({ senderFrame: frame, sender: { mainFrame: frame } }, payload)) as ConsoleReply
    },
  }
}

/** Bench is the deps one case runs on, plus the two things it can watch: what
 * the settings runner was handed, and which confirmation windows were closed. */
interface Bench {
  readonly deps: NewOperationDeps
  readonly settingsCalls: readonly unknown[]
  readonly closes: readonly string[]
}

/**
 * bench builds the deps every case starts from: **no installation bound**, a
 * confirmer that records its closes and opens nothing (nothing here is built to
 * open a window), a settings runner that records its payload instead of touching
 * a document, no lapse, and an empty environment -- which is what makes the
 * spawn log the disabled writer (`spawnlog.ts`) rather than one that writes into
 * the operator's home.
 */
function bench(overrides: Partial<NewOperationDeps> = {}): Bench {
  const settingsCalls: unknown[] = []
  const closes: string[] = []
  const confirmer: LaunchConfirmer = {
    open: () => undefined,
    close: (id) => {
      closes.push(id)
    },
  }
  const settingsEditor: SettingsEditorRunner = {
    open: (args) => {
      settingsCalls.push(args)
      return Promise.resolve({ ok: true, sentence: 'editing agent.models.coder for this project' })
    },
    cancelFor: () => undefined,
  }
  const deps: NewOperationDeps = {
    binding: () => null,
    confirmer: () => confirmer,
    settingsEditor: () => settingsEditor,
    lapsed: () => false,
    appEnv: {},
    ...overrides,
  }
  return { deps, settingsCalls, closes }
}

/** PROBE_VERSION is what the second handshake below answers -- the version line
 * `fugaro version` prints for a built binary, and the one the session is built
 * with. */
const PROBE_VERSION = '0.30.0'

/** MID_UPGRADE is what the first handshake's child refuses with, in the CLI's
 * own `fugaro: <sentence>` shape: a refusal a moment later is gone. */
const MID_UPGRADE = 'fugaro: the binary is being upgraded\n'

/**
 * aVersionProbe is a `spawn` that answers the version handshake twice: the first
 * child fails the probe (exit 1, the sentence above on stderr) and the second
 * answers a version line. It exists because the case below must make **one
 * binding** fail one build and answer the next -- a real binary that was
 * mid-upgrade when the first message arrived and works a moment later -- which
 * no one file can be asked to be. `probes` counts the handshakes, so the case can
 * say the resolver was reached again rather than reading a cached rejection.
 */
function aVersionProbe(): { readonly spawner: ChildSpawner; readonly probes: () => number } {
  let probes = 0
  const spawner = (() => {
    probes += 1
    const fails = probes === 1
    const stdout: ((chunk: Buffer | string) => void)[] = []
    const stderr: ((chunk: Buffer | string) => void)[] = []
    const closes: ((code: number | null, signal: string | null) => void)[] = []
    const child: SpawnedChildLike = {
      stdout: {
        on: (_event, listener) => {
          stdout.push(listener)
        },
      },
      stderr: {
        on: (_event, listener) => {
          stderr.push(listener)
        },
      },
      // `spawn` fires at registration, as the other stubs in this tree do; the
      // bytes and the close come a tick later, once `runToCompletion` has
      // subscribed -- `close` is the event a reader settles on, so `exit` alone
      // would leave the probe waiting for its deadline, and an `error` is never
      // fired here (a child that starts is not a spawn that failed).
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
      for (const listener of fails ? stderr : stdout) listener(fails ? MID_UPGRADE : `${PROBE_VERSION}\n`)
      for (const listener of closes) listener(fails ? 1 : 0, null)
    })
    return child
  }) as ChildSpawner
  return { spawner, probes: () => probes }
}

/**
 * aVersionAnswer is a `spawn` that answers every version handshake with
 * `PROBE_VERSION`. The onboarding case below needs one: the resolution it drives
 * walks rule 4's well-known prefixes and must succeed on whatever candidate the
 * machine holds, and which binary that is -- and what a real one reports -- is
 * not a fact a test may depend on.
 */
function aVersionAnswer(): ChildSpawner {
  return (() => {
    const stdout: ((chunk: Buffer | string) => void)[] = []
    const closes: ((code: number | null, signal: string | null) => void)[] = []
    const child: SpawnedChildLike = {
      stdout: {
        on: (_event, listener) => {
          stdout.push(listener)
        },
      },
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
      for (const listener of stdout) listener(`${PROBE_VERSION}\n`)
      for (const listener of closes) listener(0, null)
    })
    return child
  }) as ChildSpawner
}

describe('every name of the contract is an operation of the allow-list', () => {
  it('is a row of the surface, under the method the preload exposes', () => {
    for (const name of THE_NINE) {
      const operation = CONSOLE_OPERATIONS.find((candidate) => candidate.name === name)
      expect(operation, `${name} is not an operation of the allow-list`).toBeDefined()
      expect(operation?.method, `${name}'s bridge method`).toBe(NEW_OPERATION_METHODS[name])
    }
  })

  it('closes exactly the payload keys the contract names, and no other', () => {
    for (const name of THE_NINE) {
      const operation = CONSOLE_OPERATIONS.find((candidate) => candidate.name === name)
      const keys = (operation?.args ?? []).map((spec) => spec.key).sort()
      expect(keys, `${name}'s payload keys`).toEqual([...NEW_OPERATION_PAYLOAD_KEYS[name]].sort())
    }
  })

  it('marks required exactly the fields the request type does not leave optional', () => {
    for (const name of THE_NINE) {
      const operation = CONSOLE_OPERATIONS.find((candidate) => candidate.name === name)
      const required = (operation?.args ?? [])
        .filter((spec) => spec.required)
        .map((spec) => spec.key)
        .sort()
      expect(required, `${name}'s required keys`).toEqual([...REQUIRED_BY_REQUEST[name]].sort())
    }
  })

  it('takes the wizard’s answers as one object and never as a path', () => {
    // The one kind whose payload is not a scalar: the row serialises it itself,
    // and the command reads the file from **stdin** -- so no member of the
    // payload can be a file name, and none of them is `version`.
    const plan = CONSOLE_OPERATIONS.find((candidate) => candidate.name === 'setup.plan')
    expect(plan?.args.map((spec) => spec.kind)).toEqual(['setupAnswers'])
    expect(plan?.command).toBe('setup --plan-only --answers-file - --events')
    expect(plan?.command).not.toContain('--answers-file /')
  })

  it('leaves the settings editor with no command of its own', () => {
    // `settings.editor.open` drives `settings.edit`'s row and spawns nothing as
    // itself, which is why its `command` is `null` and why it is the one of the
    // nine that is also a `refusedAfterLapse` row without an argv.
    const editor = CONSOLE_OPERATIONS.find((candidate) => candidate.name === 'settings.editor.open')
    expect(editor?.command).toBeNull()
    expect(editor?.drivesOperation).toBe('settings.edit')
    expect(editor?.refusedAfterLapse).toBe(true)
  })
})

describe('the operations MW1 and MW2 added are outside the contract', () => {
  it('is the four on the surface and none of them in the contract', () => {
    for (const name of THE_FOUR) {
      expect(CONSOLE_OPERATION_NAMES, `${name} is not an operation of the allow-list`).toContain(name)
      expect(
        NEW_OPERATION_NAMES as readonly string[],
        `${name} was drawn into the contract`,
      ).not.toContain(name)
      expect(NEW_OPERATION_HANDLER_NAMES, `${name} has no handler row`).toContain(name)
    }
  })

  it('is nine plus four rows, no name twice, and every row an operation', () => {
    // The one-directional rule made countable: a later task that adds a fifth
    // operation of its own grows the surface and not this file.
    expect(NEW_OPERATION_HANDLER_NAMES.length).toBe(THE_NINE.length + THE_FOUR.length)
    expect(new Set(NEW_OPERATION_HANDLER_NAMES).size).toBe(NEW_OPERATION_HANDLER_NAMES.length)
    for (const name of NEW_OPERATION_HANDLER_NAMES) {
      expect(CONSOLE_OPERATION_NAMES, `${name} has a handler row and no operation`).toContain(name)
    }
  })
})

describe('the handlers behind the contract are the real ones', () => {
  // The onboarding case points `HOME` at a scratch home -- `binDirectories` reads
  // the process's own home when the caller passes none -- and this gives the
  // environment back whatever that case does.
  afterEach(() => {
    vi.unstubAllEnvs()
  })

  it('builds one row per name, and every row takes the validated arguments alone', () => {
    const surface = surfaceWith(bench().deps)
    expect(Object.keys(surface.rows).sort()).toEqual([...NEW_OPERATION_HANDLER_NAMES].sort())
    for (const [name, row] of Object.entries(surface.rows)) {
      expect(typeof row, `${name}'s row is not a function`).toBe('function')
      // One parameter, because the door hands a handler `validateArguments`'
      // answer and nothing else (`dispatch`, `ipc.ts`): a row that also wanted
      // the event or the installation would be taking it from somewhere the
      // allow-list never put it.
      expect(row.length, `${name}'s row takes more than the validated arguments`).toBe(1)
    }
  })

  it('answers with main’s own sentence and never with the stub’s', async () => {
    // **What this replaces.** Until I-OPS every one of these rows was the
    // console's stub, whose only answer was `not available in this build` -- so a
    // console that had lost a row looked exactly like one whose CLI was too old.
    // These rows read the binding, resolve the installation and then refuse in
    // the flow's own words; with nothing chosen there is nothing to resolve, and
    // the answer is D13's sentence. **Except the two setup rows**, which are the
    // onboarding wizard in this state and are the case below this one.
    const surface = surfaceWith(bench().deps)
    for (const name of NEW_OPERATION_HANDLER_NAMES) {
      if (name === 'setup.plan' || name === 'setup.apply') continue
      const reply = await surface.call(name, PAYLOADS[name])
      expect(reasonOf(reply), `${name} answered what the stub used to`).not.toBe(NEW_OPS_NOT_AVAILABLE)
      if (name === 'settings.editor.open') {
        // The one row that spawns nothing: it is the settings write's window, so
        // it answers with no installation at all (the write's own row reaches the
        // flow through the bound source).
        expect(reasonOf(reply), 'the settings editor answered its own outcome').toContain(
          'editing agent.models.coder',
        )
        continue
      }
      expect(reasonOf(reply), `${name} with no installation chosen`).toBe(NO_ACTIVE_INSTALLATION)
    }
  })

  itHostRoute('shows the onboarding wizard with no installation, and resolves the CLI itself', async () => {
    // **The wizard is the no-installation state's own view**: the plan's SW3b has
    // the onboarding screen show it in place of a command line, and its two rows
    // take no project at all (`spawn.ts`'s `takesProject: false`) -- so a plan
    // refused for the lack of an installation would be a wizard nothing can run
    // where it is shown. These two rows resolve the CLI the way the startup
    // handshake does with nothing chosen: rule 4's walk, with no override.
    //
    // The walk is made a fact of this case rather than of the machine: a scratch
    // home carries a `fugaro` of its own and `HOME` is pointed at it (the idiom
    // `spawn.test.ts` uses), so a machine that has one in an earlier prefix and
    // one that has none both reach a candidate -- and the version probe is this
    // case's own, so no binary on the machine is ever run.
    const home = mkdtempSync(join(tmpdir(), 'fugaro-onboarding-home-'))
    const prefix = join(home, '.local', 'bin')
    mkdirSync(prefix, { recursive: true })
    const stub = join(prefix, 'fugaro')
    writeFileSync(stub, '#!/bin/sh\n')
    chmodSync(stub, 0o755)
    vi.stubEnv('HOME', home)
    const surface = surfaceWith(bench({ spawner: aVersionAnswer() }).deps)
    // The plan reaches the wizard, which is behind its own floor: the sentence is
    // the flow's own, and the stub's `not available in this build` is nobody's.
    const plan = await surface.call('setup.plan', PAYLOADS['setup.plan'])
    expect(reasonOf(plan), 'setup.plan answered what the stub used to').not.toBe(NEW_OPS_NOT_AVAILABLE)
    expect(reasonOf(plan), 'setup.plan with no installation chosen').toBe(
      setupFloorSentence(SETUP_CLI_FLOOR, PROBE_VERSION),
    )
    // The apply is the wizard's other row, and with no plan held it answers the
    // flow's own "no plan is waiting" -- a sentence no stub and no floor
    // produces, and one only a wizard that was really built can give.
    const apply = await surface.call('setup.apply', PAYLOADS['setup.apply'])
    expect(reasonOf(apply), 'setup.apply with no installation chosen').toBe(SETUP_NO_PLAN_REFUSAL)
  })
  it('refuses an installation the resolver will not take, in the resolver’s words', async () => {
    // The other half of "a handler really ran": the binding is read, the override
    // is resolved, and the refusal is the resolution's own sentence. The override
    // is relative, which `resolveBinary` refuses **before** it walks the
    // well-known prefixes -- so this case is the same on every machine, and it
    // covers a read, the wizard and both writes.
    const surface = surfaceWith(
      bench({ binding: () => anInstallation({ binaryPath: 'fugaro' }) }).deps,
    )
    for (const name of ['runs.stream', 'whoami', 'setup.plan', 'models.add', 'image.add'] as const) {
      const reply = await surface.call(name, PAYLOADS[name])
      expect(reasonOf(reply), `${name} did not reach the resolver`).toContain('is not absolute')
    }
  })

  it('builds the call the wizard’s answers cross, and hands the editor its payload', async () => {
    // Two rows whose join is inside this process rather than at a child.
    //
    // `setup.plan`: the allow-list turns the object into one string and the row
    // turns it back (`setupPlanArgs`), so the contract's own plan payload passing
    // the door is half the seam -- the other half is at the bottom of this file,
    // with no child in between.
    const door = validateArguments('setup.plan', PAYLOADS['setup.plan'])
    expect(door.ok, 'the door refused the contract’s own plan payload').toBe(true)
    if (door.ok) {
      expect(typeof door.args['answers']).toBe('string')
      expect(setupAnswersFromFile(String(door.args['answers']))['gcp_project']).toBe('fugaro-dev')
    }
    // `settings.editor.open`: the payload arrives at the runner untouched, which
    // is the whole of the row.
    const { deps: open, settingsCalls: handed } = bench()
    const opened = await surfaceWith(open).call('settings.editor.open', { key: 'agent.models.coder' })
    expect(opened.ok).toBe(true)
    expect(handed).toEqual([{ key: 'agent.models.coder' }])
  })

  it('builds again when a build failed, instead of keeping the refusal for the binding', async () => {
    // **The one thing thirteen messages share.** The session is kept per binding
    // so a screen's reads do not walk rule 4 once each -- but a resolution can
    // fail for a reason that passes: a binary mid-upgrade, a `version` probe that
    // did not answer in time on a loaded machine, an installation chosen after the
    // console started. `bindingKeyOf` carries no version, so a rejection kept
    // under the key would answer every later message with it until the operator
    // switched installations. The first call below fails at the probe and the
    // second is answered by a session that was built -- the reply is the **read's
    // own floor**, which only a session that exists can compose -- and the probe
    // count is what says the resolver was reached again rather than read from a
    // cache. `src/source/cli.ts` drops a failed build for the same reason, and
    // this is the case that holds the two to each other.
    const probe = aVersionProbe()
    const surface = surfaceWith(
      bench({
        binding: () => anInstallation({ binaryPath: process.execPath }),
        spawner: probe.spawner,
      }).deps,
    )
    const failed = await surface.call('runs.stream', PAYLOADS['runs.stream'])
    expect(reasonOf(failed), 'the first build did not fail at the version probe').toContain(
      'exited 1 for `version`',
    )
    const answered = await surface.call('runs.stream', PAYLOADS['runs.stream'])
    expect(reasonOf(answered), 'the failed build was kept as this binding’s session').toBe(
      newReadFloorSentence('runs.stream', null, PROBE_VERSION),
    )
    expect(probe.probes(), 'the resolver was not reached again').toBe(2)
  })

  it('refuses a payload the allow-list closes, before any handler is reached', async () => {
    // The same door, read from the other side: `settings.editor.open` takes
    // `{key}`, so a payload naming a layer -- the layer is main's, not a
    // renderer's (`ipc.ts` says why) -- is refused by the allow-list's own
    // sentence and the runner is never handed it.
    const { deps, settingsCalls } = bench()
    const reply = await surfaceWith(deps).call('settings.editor.open', {
      key: 'agent.models.coder',
      layer: 'project',
    })
    expect(reply.ok).toBe(false)
    expect(reasonOf(reply)).toContain('layer')
    expect(settingsCalls).toEqual([])
  })

  it('closes the three windows a project’s installation has open, as a cancel', async () => {
    // Rule 11's "an installation re-chosen or removed while the window is open
    // closes it as a cancel": the wizard's window and the two writes' are all
    // keyed by the project, and this is where the three are closed together --
    // `index.ts` calls it beside the launch's and the settings edit's.
    const { deps, closes } = bench()
    const ids = {
      setup: setupConfirmID('fugaro-dev'),
      models: modelsAddConfirmID('fugaro-dev'),
      image: imageAddConfirmID('fugaro-dev'),
    }
    expect(new Set(Object.values(ids)).size, 'two subjects share one confirmation id').toBe(3)
    newOperationHandlers(deps).cancelFor('fugaro-dev')
    expect([...closes].sort()).toEqual(Object.values(ids).sort())
  })

  it('is spread into the surface before the console registers it', () => {
    // The wiring claim, read out of the source because `index.ts` imports
    // Electron: the rows are part of the table the two gates wrap, and the table
    // is registered after the spread. A row outside it would be an operation
    // nothing answers for -- and because the surface check refuses a table with a
    // missing handler, the console would stop at startup rather than answer
    // "not available" for it.
    const index = withoutComments(readFileSync(join(CONSOLE_ROOT, 'src', 'main', 'index.ts'), 'utf8'))
    const table = index.indexOf('const gated: ConsoleHandlers = {')
    const spread = index.indexOf('...newOperations.rows')
    const register = index.indexOf('registerConsoleHandlers(')
    expect(table, 'the gated table is gone from index.ts').toBeGreaterThan(-1)
    expect(spread, 'the rows are not spread into the gated table').toBeGreaterThan(table)
    expect(register, 'the surface is registered before the rows are in it').toBeGreaterThan(spread)
    // And the gates are still in the order the module documents: reduced mode
    // inside the lapse, both of them around the table.
    expect(index).toContain(
      'lapseHandlers(reducedModeHandlers(gated, storedCompatibility), licenceReplyFor)',
    )
  })

  it('reads the licence the three flows are handed, per project', () => {
    // The one reader `handlers.ts` builds the wizard and the two writes from.
    // It is read out of the source because `index.ts` imports Electron, and it
    // is pinned at all because nothing else in the tree would notice it going:
    // the flows' own tests build their writers directly, and the gate at the
    // door (`lapseHandlers`) is a different mechanism -- it takes the row away
    // from the renderer, while a window's own yes leaves the reader as the only
    // thing between a lapsed licence and a child. `lapse-wiring.test.ts` drives
    // that path; this is the wiring it drives.
    const index = withoutComments(readFileSync(join(CONSOLE_ROOT, 'src', 'main', 'index.ts'), 'utf8'))
    expect(
      index,
      'the reader is no longer the store read with the project it was asked about',
    ).toContain(
      "lapsed: (project) => licenceStateOf(licenceStore.read(project, new Date())) === 'lapsed',",
    )
  })

  it('has no stub left, and no module that names one', () => {
    const index = withoutComments(readFileSync(join(CONSOLE_ROOT, 'src', 'main', 'index.ts'), 'utf8'))
    expect(index).not.toContain('newOperationStub')
    expect(index).not.toContain('NEW_OPERATIONS_UNBUILT')
    expect(existsSync(join(CONSOLE_ROOT, 'src', 'renderer', 'stubs', 'bridge-stub.ts'))).toBe(false)
    // **The scan is over the sources and not over the one file**: the stub could
    // come back under another name, and what this file is about is that no
    // renderer module ships a fixture of a CLI answer. A source that needs a
    // fixture keeps it under `test/support`, where no renderer can reach it.
    const offenders = everySource(join(CONSOLE_ROOT, 'src')).filter((file) => {
      const text = readFileSync(file, 'utf8')
      return text.includes('stubBridge') || text.includes('bridge-stub') || text.includes('STUB_FIXTURES')
    })
    expect(offenders, 'a source still names the stub bridge').toEqual([])
  })
})

describe('the fixtures are the CLI’s own recordings', () => {
  /** goldens is each recorded file, with the fixture rendered back into it. Five
   * files, six answers: the setup golden is three runs -- the plan, the apply and
   * a refused `--check` -- and the three together are the whole file. */
  function goldens(): readonly (readonly [string, string])[] {
    const plan = linesOf(NEW_OP_ANSWERS['setup.plan'])
    const apply = linesOf(NEW_OP_ANSWERS['setup.apply'])
    return [
      [
        goldenPath('internal/cli/testdata/runs_stream.golden.ndjson'),
        ndjsonOf(linesOf(NEW_OP_ANSWERS['runs.stream'])),
      ],
      [
        goldenPath('internal/cli/testdata/budget_stream.golden.ndjson'),
        ndjsonOf(linesOf(NEW_OP_ANSWERS['budget.stream'])),
      ],
      [
        goldenPath('ee/report/cli/testdata/report_stream.golden.ndjson'),
        ndjsonOf(linesOf(NEW_OP_ANSWERS['spend.stream'])),
      ],
      [
        goldenPath('internal/cli/testdata/setup_events.golden.ndjson'),
        ndjsonOf([...plan, ...apply, ...SETUP_REFUSED_LINES]),
      ],
      [goldenPath('internal/cli/testdata/whoami.golden.json'), documentText(NEW_OP_ANSWERS['whoami'])],
    ]
  }

  it('renders each golden-backed fixture back into the file it was recorded in', () => {
    for (const [path, rendered] of goldens()) {
      expect(rendered, `${path} is not the fixture’s own bytes`).toBe(readFileSync(path, 'utf8'))
    }
  })

  it('decodes every fixture against the contract, and reads the refused run as an error feed', () => {
    for (const name of THE_NINE) {
      const decoded = decodeNewOperationAnswer(name, NEW_OP_ANSWERS[name])
      expect(decoded.ok, `${name}'s fixture does not decode`).toBe(true)
    }
    // The refused `--check` feed is not an operation's answer: it is the third
    // run of the setup golden, kept so the console's own reader can be held to
    // the one line a refusal prints (`new-ops.test.ts` reads it as a failed feed).
    expect(SETUP_REFUSED_LINES).toHaveLength(1)
    expect(SETUP_REFUSED_LINES[0]?.['type']).toBe('error')
  })
})

describe('the wizard’s answers cross from the allow-list to the wizard', () => {
  const GIVEN = Object.freeze({
    gcp_project: 'fugaro-dev',
    region: 'europe-west1',
    name: 'fugaro',
    rigor: 'startup',
    budget: 100,
    bake_repos: true,
  })

  it('keeps the allow-list’s six questions and the contract’s six equal', () => {
    // Two copies of one fact, held equal here because the allow-list keeps its
    // own list rather than importing the contract's (`ipc.ts` says why: the door
    // is the surface's own module). A question one of them grew alone would be a
    // key the door refuses or the wizard never sees.
    expect([...SETUP_ANSWER_KEYS]).toEqual([...CONTRACT_SETUP_ANSWER_KEYS])
    expect([...SETUP_ANSWER_KEYS]).toEqual([
      'gcp_project',
      'region',
      'name',
      'rigor',
      'budget',
      'bake_repos',
    ])
  })

  it('holds the amount’s bounds to the schema’s, in main and in the renderer', () => {
    // Two copies of one fact again, and the third is the schema's own
    // (`schemas/setup-answers.schema.json`, which `internal/cli` decodes and
    // `managed.Budget.Check` enforces). An amount outside the range is refused
    // by the run after a child exists, so each copy is read here rather than
    // trusted: a door that took one the run refuses would spawn the child that
    // fails.
    const schema = JSON.parse(
      readFileSync(goldenPath('schemas/setup-answers.schema.json'), 'utf8'),
    ) as {
      properties: { budget: { minimum: number; maximum: number } }
    }
    expect(schema.properties.budget.minimum).toBe(SETUP_BUDGET_MIN)
    expect(schema.properties.budget.maximum).toBe(SETUP_BUDGET_MAX)
    expect(IPC_SETUP_BUDGET_MIN).toBe(SETUP_BUDGET_MIN)
    expect(IPC_SETUP_BUDGET_MAX).toBe(SETUP_BUDGET_MAX)
  })

  it('serialises the payload into the file, and hands the wizard the six questions back', () => {
    // The seam, end to end and with no child: the door's validation of what a
    // renderer sends, the string main holds, and the wizard's own reader on the
    // other side of it.
    const door = validateArguments('setup.plan', { answers: GIVEN })
    expect(door.ok).toBe(true)
    if (!door.ok) return
    const file = door.args['answers']
    expect(typeof file).toBe('string')
    const held: SetupAnswersHeld = validateSetupAnswers(setupAnswersFromFile(String(file)))
    expect(held).toEqual({
      gcp_project: 'fugaro-dev',
      region: 'europe-west1',
      name: 'fugaro',
      rigor: 'startup',
      budget: 100,
      bake_repos: true,
    })
  })

  it('carries an amount nobody typed as no amount at all', () => {
    // The other half of the pair: a renderer that sent no `budget` -- the field
    // it drew empty -- must reach the wizard with no amount, so the CLI applies
    // its own default. An absent member read back as `0`, or as the default, is
    // an alert nobody asked for.
    const { budget: _omitted, ...noBudget } = GIVEN
    const door = validateArguments('setup.plan', { answers: noBudget })
    expect(door.ok).toBe(true)
    if (!door.ok) return
    const held = validateSetupAnswers(setupAnswersFromFile(String(door.args['answers'])))
    expect(held.budget).toBeUndefined()
    expect(Object.keys(setupAnswersFromFile(String(door.args['answers'])))).toEqual([
      'gcp_project',
      'region',
      'name',
      'rigor',
      'bake_repos',
    ])
  })

  it('drops the file’s version, which the wizard refuses by name as not a question', () => {
    const text = JSON.stringify({ version: 1, ...GIVEN })
    expect(Object.keys(setupAnswersFromFile(text)).sort()).toEqual([...SETUP_ANSWER_KEYS].sort())
    expect(() => validateSetupAnswers({ version: 1, ...GIVEN })).toThrow(SETUP_ANSWERS_UNKNOWN_KEY)
  })

  it('refuses text that is not this console’s own answers file', () => {
    for (const text of [
      '',
      'not json',
      '[]',
      '{}',
      '{"version":1}',
      JSON.stringify({ ...GIVEN, name: 7 }),
      JSON.stringify({ ...GIVEN, budget: '100' }),
    ]) {
      expect(() => setupAnswersFromFile(text), `${JSON.stringify(text)} was read back`).toThrow(
        SETUP_ANSWERS_UNREADABLE,
      )
    }
  })
})

// ---------------------------------------------------------------------------
// The readers the cases above share.
// ---------------------------------------------------------------------------

/** everySource is every source file under `dir`, at any depth. The stub scan is
 * over the sources rather than over a list of them, because a stub reintroduced
 * under another name is exactly what a list would miss. */
function everySource(dir: string): readonly string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) return everySource(path)
    return entry.isFile() && /\.(ts|tsx|js|mjs)$/.test(entry.name) ? [path] : []
  })
}

/** withoutComments is a source with its comments blanked, so a scan reads the
 * code and not the prose that quotes it -- this file's own comments name the
 * stub, and so does the wiring's. */
function withoutComments(source: string): string {
  return source.replace(/\/\*[\s\S]*?\*\//g, ' ').replace(/(^|[^:])\/\/[^\n]*/gm, '$1')
}

/** goldenPath is one recorded file in the Go tree. */
function goldenPath(relative: string): string {
  return join(REPO_ROOT, relative)
}

/** documentOf is an answer's `document`, as the object a case reads. */
function documentOf(answer: unknown): Record<string, unknown> {
  const document = (answer as { document?: unknown } | null)?.document
  if (document === null || typeof document !== 'object' || Array.isArray(document)) {
    throw new Error('the fixture carries no document')
  }
  return document as Record<string, unknown>
}

/** linesOf is a feed answer's lines, in the order the CLI wrote them. */
function linesOf(answer: unknown): readonly Record<string, unknown>[] {
  const lines = documentOf(answer)['lines']
  if (!Array.isArray(lines)) throw new Error('the fixture carries no lines')
  return lines as readonly Record<string, unknown>[]
}

/** ndjsonOf renders lines the way the CLI writes a feed: one compact document
 * per line, and a newline after each. */
function ndjsonOf(lines: readonly Record<string, unknown>[]): string {
  return `${lines.map((line) => JSON.stringify(line)).join('\n')}\n`
}

/** documentText renders a one-shot read the way the CLI writes it: two spaces of
 * indentation, and a newline. */
function documentText(answer: unknown): string {
  return `${JSON.stringify(documentOf(answer), null, 2)}\n`
}
