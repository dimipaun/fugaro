// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The real handlers behind the console improvements' operations (task I-OPS;
// the contract is `docs/console-improvements/contract-ops.md`, and the flows are
// `src/source/new-reads.ts`, `src/main/wizardrun.ts`, `src/main/modelsadd.ts`,
// `src/main/imageadd.ts` and `src/main/settingsedit.ts`).
//
// **What this module is.** The join the four flows left for one task: thirteen
// rows -- the contract's nine (`NEW_OPERATION_NAMES`) and the models and image
// writes' four, which are operations of `CONSOLE_OPERATIONS` and deliberately
// outside the contract -- each over the installation the console is driving.
// `index.ts` spreads them into the `gated` table, so every one of them passes
// through `reducedModeHandlers` and `lapseHandlers` exactly as the rows they
// replace did: a refused console still registers no channel at all, and a lapsed
// one still refuses `setup.apply`, `models.add` and `image.add` before a handler
// -- and so before a child -- is reached. The gate on a row is not the whole of
// the lapse, though, and the three flows carry the rest of it: each of them asks
// the licence again where its child really starts -- the window's own yes, which
// no handler is on -- so a plan made after the lapse, kept and shown, spawns
// nothing (`wizardrun.ts`, `modelsadd.ts` and `imageadd.ts` say so at the field,
// and `index.ts` builds the one predicate all of them ask).
//
// **Nothing is registered here.** This module builds rows and hands them back;
// `src/main/index.ts` is the one place the surface is assembled, and a module
// that called `ipcMain.handle` itself would be a second place the allow-list
// could be widened from.
//
// **One resolution, at the first message of the set.** Every row here but the
// two setup ones needs two facts the payload does not carry: which `fugaro` to
// spawn and where. Both are the installation's, and this module resolves them
// the way the console's own source does -- the record's optional override first
// (design section 3.4 rule 4), then the well-known prefixes -- through the same
// function and the same arguments as `CliSource.create` (`src/source/cli.ts`):
// the override, the app's own environment, and the checkout, or the console's
// private working directory when the record names none (rule 5). A resolution
// that failed is a `SourceRefusal` carrying the resolver's own sentence, which
// is what a screen draws. **The two setup rows are the exception, and
// deliberately**: the wizard is what the no-installation onboarding state shows
// in place of a command line, so they resolve the CLI through the
// no-installation route instead of refusing for the lack of one (`buildWizard`
// and `loadSetup` below say how).
//
// It is done **lazily, once per installation, and cached by the fields that
// decide what the child is and where it runs** (`bindingKey`'s five, the same
// ones the source caches a source by): the bound source pays one resolution per
// binding too, and a second resolution here would be a second walk of the
// filesystem answering a question the first one already answered -- possibly
// differently, if the machine changed in between. The resolved binary's own
// version line is what every flow is built with as `cliVersion`, which is the
// fact each of their floor refusals is decided on.
//
// **The two things this module deliberately does not carry**, each because a
// second spelling of it would be a defect rather than a convenience:
//
//   - **No licence key.** `licenceKeyFor` reaches a plan at exactly two call
//     sites (`src/main/launch.ts` and `src/source/cli.ts`), and both are the
//     paths that spawn a child the key is for. None of the operations here
//     spawns one: the reads are reads, the writes' children are `--no-apply`
//     dry runs and their applies, and `settings.editor.open` spawns nothing at
//     all (it drives `settings.edit`, which the source plans with the key). A
//     third `licenceKey` argument would be a third place the key text is read
//     and one more place it could be written into an environment.
//   - **No `distro`.** The WSL route's facts live on the *launch* record and
//     reach a plan through `resolveHandshakeBinary`'s own `distro`
//     (`src/main/index.ts`'s launch path), which is where a WSL installation's
//     rows are planned today. The bound source carries none either: `sourceFor`
//     hands `rule 4` the record's override and nothing else, so a console
//     driving a WSL installation reads its dashboard through the same gap. This
//     module mirrors the source it sits beside rather than inventing a second,
//     disagreeing route; the console-windows plan owns the route that would fill
//     it in for both.
//
// **The setup answers cross one seam that does not typecheck by itself**, and it
// is named here rather than hidden: `setup.plan`'s payload carries the five
// answers as an object, and the allow-list (`ipc.ts`) validates each one and
// hands the **serialised** answers file over as one string -- main's own bytes,
// which is what keeps a path and an unvalidated member out of the child's
// document. The wizard (`wizardrun.ts`) takes the answers as the object it
// validates and serialises itself (`serialiseSetupAnswers`), so the join is
// `setupAnswersFromFile` below: main's own text, parsed back into the five
// values, and validated by the wizard's own reader before anything is spawned.

import {
  IMAGE_ADD_OPERATION,
  IMAGE_ADD_PLAN_OPERATION,
  MODELS_ADD_OPERATION,
  MODELS_ADD_PLAN_OPERATION,
  NO_INSTALLATION_TARGET,
  planFor,
  privateWorkingDirectory,
  resolveBinary,
} from './spawn'
import type { ChildSpawner, SpawnTarget, Timers } from './spawn'
import { createSpawnLog } from './spawnlog'
import type { SpawnLogWriter } from './spawnlog'
import type { LaunchConfirmer } from './launch'
import { NO_ACTIVE_INSTALLATION, SETUP_ANSWER_KEYS } from './ipc'
import type { ConsoleHandler, ValidatedArgument, ValidatedArguments } from './ipc'
import { createModelsAdd } from './modelsadd'
import type { ModelsAddValues, ModelsAddWriter } from './modelsadd'
import { modelsAddConfirmID } from './modelsview'
import { createImageAdd } from './imageadd'
import type { ImageAddValues, ImageAddWriter } from './imageadd'
import { imageAddConfirmID } from './imagesview'
import { createSetupWizard } from './wizardrun'
import type { SetupSpawnName, SetupWizard } from './wizardrun'
import { setupConfirmID } from './setupview'
import type { SettingsEditorRunner } from './settingsedit'
import { cliNewReads, newReadHandlers, newReadPlanner } from '../source/new-reads'
import type { NewReadHandlers, NewReadName } from '../source/new-reads'
import { SourceRefusal } from '../source/source'
import type { BoundInstallation } from '../source/cli'
import { NEW_OPERATION_NAMES } from '../shared/new-ops'
import type { NewOperationName } from '../shared/new-ops'
import {
  BUDGET_STREAM_CLI_FLOOR,
  IMAGE_ADD_CLI_FLOOR,
  IMAGE_CATALOG_CLI_FLOOR,
  MODELS_ADD_CLI_FLOOR,
  RECIPES_CLI_FLOOR,
  RUNS_STREAM_CLI_FLOOR,
  SETUP_CLI_FLOOR,
  SPEND_STREAM_CLI_FLOOR,
  WHOAMI_CLI_FLOOR,
} from '../shared/version'

// ---------------------------------------------------------------------------
// The thirteen rows, by name.
// ---------------------------------------------------------------------------

/**
 * NewOperationHandlerName is every operation this module builds a row for: the
 * contract's nine, and the models and image writes' four.
 *
 * The four are spelled off `spawn.ts`'s own operation constants rather than as
 * literals, so the row's name and the argv row's name are one spelling -- they
 * are operations of `CONSOLE_OPERATIONS` (MW1 and MW2) and not of the contract,
 * which is why they are not in `NEW_OPERATION_NAMES`.
 */
export type NewOperationHandlerName =
  | NewOperationName
  | typeof MODELS_ADD_PLAN_OPERATION
  | typeof MODELS_ADD_OPERATION
  | typeof IMAGE_ADD_PLAN_OPERATION
  | typeof IMAGE_ADD_OPERATION

/**
 * NewOperationHandlerNames is those thirteen as a value, so the contract test
 * can hold the row set to the surface's names rather than to a count.
 */
export const NEW_OPERATION_HANDLER_NAMES: readonly NewOperationHandlerName[] = Object.freeze([
  ...NEW_OPERATION_NAMES,
  MODELS_ADD_PLAN_OPERATION,
  MODELS_ADD_OPERATION,
  IMAGE_ADD_PLAN_OPERATION,
  IMAGE_ADD_OPERATION,
])

/** NewOperationRows is the table `index.ts` spreads into `gated`: one closed
 * row per name, which is the shape the surface's own `ConsoleHandlers` checks
 * once the spread has happened. */
export type NewOperationRows = { readonly [Name in NewOperationHandlerName]: ConsoleHandler }

/**
 * NewOperationDeps is what the rows are built from. Every member is a **reader**
 * and not a value: the surface is assembled once, at module scope in
 * `index.ts`, while the installation, the window and the licence are facts of
 * the run that change under it -- an installation chosen after the surface is
 * up must be the one the next message drives, and a licence pasted mid-session
 * must be the one the next window's yes is judged on.
 */
export interface NewOperationDeps {
  /** the active installation, read at call time: `null` is D13's
   * no-installation state, answered with the console's own sentence rather than
   * with a source built from whatever the machine happened to have -- for the
   * eleven rows that drive an installation, while the two setup ones answer it
   * with the onboarding wizard (`loadSetup`). */
  readonly binding: () => BoundInstallation | null
  /** the one confirmation window, read at call time (SU1's `createConfirmWindow`
   * is built in `main`, after this surface exists). */
  readonly confirmer: () => LaunchConfirmer
  /** the settings editor's flow behind `settings.editor.open`, read at call time
   * for the confirmer's reason. */
  readonly settingsEditor: () => SettingsEditorRunner
  /** whether the licence has lapsed for one installation. It is asked where the
   * two writes really start -- the window's own yes -- and not only at the door
   * (`modelsadd.ts` says why), so it is a reader here too. */
  readonly lapsed: (project: string) => boolean
  /** the app's own environment, which is what the child's environment is built
   * from (`CHILD_ENV_KEYS` and nothing else reaches the child itself). */
  readonly appEnv: Readonly<Record<string, string | undefined>>
  /** the spawn log, opened for the app's own environment unless a caller hands
   * one over -- `sourceFor`'s own default, and a test passes `appEnv: {}` for
   * the disabled writer. */
  readonly log?: SpawnLogWriter | undefined
  /** the child spawner, for a test that drives the flows without a process. */
  readonly spawner?: ChildSpawner | undefined
  readonly timers?: Timers | undefined
}

/** NewOperations is what the wiring gets back: the rows, and the one call the
 * rail's two "the installation moved" paths make. */
export interface NewOperations {
  readonly rows: NewOperationRows
  /** cancelFor closes the confirmation window an installation has open, as a
   * cancel, for every one of the three subjects whose window is keyed by a
   * project. It is the rows' half of rule 11's "an installation removed or
   * re-chosen while the window is open closes the window as a cancel": this
   * module builds the three of them, so it is where that cancel is spelled, and
   * `index.ts` calls it beside the launch's and the settings edit's. */
  cancelFor(project: string): void
}

/** PlanContext is `planFor`'s own context: the four facts a plan is built from,
 * which are the target main named, the resolved binary, the child's directory
 * and the app's environment. It is spelled off `planFor` rather than restated,
 * so a row that grows a fact is a compile error here rather than a plan this
 * module builds without it. */
type PlanContext = Parameters<typeof planFor>[2]

/** Session is one binding's flows, built over the binary rule 4 resolved for it
 * and the plan context that binary and directory make. */
interface Session {
  readonly read: NewReadHandlers
  readonly setup: SetupWizard
  readonly models: ModelsAddWriter
  readonly image: ImageAddWriter
}

/**
 * SETUP_ANSWERS_UNREADABLE is what the `setup.plan` seam answers when main's own
 * answers text cannot be read back. It is a programming-error guard and not a
 * state an operator reaches: the allow-list built that text out of five values
 * it validated one at a time, and nothing between `dispatch` and here rewrites
 * it.
 */
export const SETUP_ANSWERS_UNREADABLE = 'the setup answers could not be read back'

/**
 * READ_FLOORS is task AL's one floor constant per read, and it is the table the
 * six reads refuse below. Every one of them is `null` today, which is not an
 * oversight: the floor is unset until a CLI release carries the command, and a
 * console that spawned the argv anyway would run a line the installed CLI
 * refuses -- or one it accepts and means something else by.
 */
const READ_FLOORS: Readonly<Record<NewReadName, string | null>> = Object.freeze({
  'runs.stream': RUNS_STREAM_CLI_FLOOR,
  'spend.stream': SPEND_STREAM_CLI_FLOOR,
  'budget.stream': BUDGET_STREAM_CLI_FLOOR,
  whoami: WHOAMI_CLI_FLOOR,
  'image.catalog.ls': IMAGE_CATALOG_CLI_FLOOR,
  'recipes.ls': RECIPES_CLI_FLOOR,
})

/**
 * bindingKeyOf is what "the same binding" means here, and it is the source's own
 * list of fields for the source's own reason: the id is *which* installation
 * this is, while the override is rule 4's first candidate, the checkout is the
 * child's working directory and the config path is the project the child reads.
 * A session cached by the id alone would keep spawning the binary and the
 * directory an installation used to have.
 */
function bindingKeyOf(installation: BoundInstallation): string {
  return JSON.stringify([
    installation.id,
    installation.project,
    installation.binaryPath ?? '',
    installation.configPath ?? '',
    installation.checkout ?? '',
  ])
}

/**
 * setupAnswersFromFile reads main's own answers text back into the object the
 * wizard validates.
 *
 * The string is the answers **file** the allow-list built and main holds in
 * memory (`setupAnswersValue`, `ipc.ts`), and this is the other end of the same
 * seam: `version` is the file's schema member and not one of the questions, so
 * it is dropped here and added again by the wizard's own serialiser when it
 * makes the bytes the child reads. Every member is read with its own type
 * checked, and a document that is not this console's own text is refused rather
 * than half-read: what a child decodes is a claim about a project, a region and
 * a rigor, and half of that document is not a plan anyone agreed to.
 *
 * `budget` is the one member a document may leave out -- the operator answered
 * no amount and the CLI takes its own default -- so an absent key stays absent
 * here rather than being read as an answer of any kind.
 */
export function setupAnswersFromFile(text: string): ValidatedArguments {
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    throw new SourceRefusal(SETUP_ANSWERS_UNREADABLE)
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new SourceRefusal(SETUP_ANSWERS_UNREADABLE)
  }
  const record = parsed as Record<string, unknown>
  const answers: Record<string, ValidatedArgument> = {}
  for (const key of SETUP_ANSWER_KEYS) {
    const member = record[key]
    if (member === undefined && key === 'budget') continue
    // The amount is the one member that is neither text nor a switch, and every
    // other member is one of those two: an amount spelled as text, or a project
    // spelled as a number, is not this console's own answers file.
    if (key === 'budget') {
      if (typeof member !== 'number') throw new SourceRefusal(SETUP_ANSWERS_UNREADABLE)
      answers[key] = member
      continue
    }
    if (typeof member !== 'string' && typeof member !== 'boolean') {
      throw new SourceRefusal(SETUP_ANSWERS_UNREADABLE)
    }
    answers[key] = member
  }
  return Object.freeze(answers)
}

/**
 * setupPlanArgs adapts the allow-list's payload to the wizard's.
 *
 * `ValidatedArguments` is a record of scalars by construction -- a string, a
 * boolean or a pull request number, and never an object (`ipc.ts` says why: a
 * payload of scalars is a payload with no path and no structure to smuggle one
 * in) -- while the wizard's `setup.plan` takes the answers as the object its own
 * reader validates. The cast is the seam, in one place, with the value on both
 * sides of it built by this process: the string is the allow-list's
 * serialisation of an object whose every member it checked against its grammar,
 * and what this function hands over is that object again, read one key at a
 * time.
 */
function setupPlanArgs(args: ValidatedArguments): ValidatedArguments {
  const file = args['answers']
  if (typeof file !== 'string') throw new SourceRefusal(SETUP_ANSWERS_UNREADABLE)
  const answers = setupAnswersFromFile(file)
  return Object.freeze({ answers: answers as unknown as ValidatedArgument })
}

/**
 * modelsAddArgs and imageAddArgs are the writes' values as the argv rows read
 * them: **an optional value the window left empty is an absent key**, because
 * the CLI's own flag is optional and `--price-in=` is not the same statement as
 * no rate at all. It is the shape `ipc.ts` validates at the door (a payload that
 * carries no `priceIn` is a payload with no rate), so a plan's argv and the argv
 * its apply re-runs are the same line either way.
 */
function modelsAddArgs(values: ModelsAddValues): ValidatedArguments {
  const args: Record<string, ValidatedArgument> = { id: values.id, route: values.route }
  if (values.priceIn !== '') args['priceIn'] = values.priceIn
  if (values.priceOut !== '') args['priceOut'] = values.priceOut
  return Object.freeze(args)
}

/** imageAddArgs is `modelsAddArgs` for the image write, which has two optional
 * values rather than a pair. */
function imageAddArgs(values: ImageAddValues): ValidatedArguments {
  const args: Record<string, ValidatedArgument> = { name: values.name, dockerfile: values.dockerfile }
  if (values.tags !== '') args['tags'] = values.tags
  if (values.description !== '') args['description'] = values.description
  return Object.freeze(args)
}

/**
 * newOperationHandlers builds the thirteen rows over one lazily-resolved
 * installation.
 *
 * The order of the work is the whole of the design, and it is one sentence:
 * **a row reads the binding, then the session, then the flow** -- so an
 * operation that no installation answers for is refused with the console's own
 * sentence before anything is walked, resolved or spawned, and the flows' own
 * refusals (a floor, a payload, a lapse) stay where their modules put them. The
 * two setup rows read the binding too, and answer the no-installation state with
 * the onboarding wizard rather than with a refusal (`loadSetup`); everything else
 * about them is the same three steps.
 */
export function newOperationHandlers(deps: NewOperationDeps): NewOperations {
  const log = deps.log ?? createSpawnLog(deps.appEnv)
  const confirmer: LaunchConfirmer = {
    open: (subject, decide) => deps.confirmer().open(subject, decide),
    close: (id) => {
      deps.confirmer().close(id)
    },
  }
  // The seams every flow of a session is built with, and the unbound wizard's
  // as well: a test's spawner and timers, and nothing else, because everything
  // the flows need beyond them is a fact of the installation.
  const seams = {
    ...(deps.spawner === undefined ? {} : { spawner: deps.spawner }),
    ...(deps.timers === undefined ? {} : { timers: deps.timers }),
  }
  let session: { readonly key: string; readonly built: Promise<Session> } | null = null
  // The setup flow built with no installation bound, for the onboarding view;
  // `loadSetup` says why it has no key.
  let wizard: { readonly built: Promise<SetupWizard> } | null = null

  /** contextFor is one plan's own facts: the target main named, the binary rule 4
   * resolved, and the directory the child runs in. */
  const contextFor = (
    installation: BoundInstallation,
    binaryPath: string,
    directory: string,
    project: string,
  ): PlanContext => ({
    target: {
      project,
      ...(installation.configPath === undefined ? {} : { configPath: installation.configPath }),
    },
    binaryPath,
    directory,
    appEnv: deps.appEnv,
  })

  /** buildSession resolves the binary and builds the four flows over it. It is
   * the source's own resolution, with the source's own arguments -- the record's
   * override as the configured path, the checkout or the private working
   * directory as the child's directory -- and the resolved version is what every
   * floor is asked of. */
  const buildSession = async (installation: BoundInstallation): Promise<Session> => {
    const directory = installation.checkout ?? privateWorkingDirectory()
    const target: SpawnTarget = {
      project: installation.project,
      ...(installation.configPath === undefined ? {} : { configPath: installation.configPath }),
    }
    const resolution = await resolveBinary({
      ...(installation.binaryPath === undefined || installation.binaryPath === ''
        ? {}
        : { configuredPath: installation.binaryPath }),
      appEnv: deps.appEnv,
      directory,
      target,
      ...(deps.spawner === undefined ? {} : { spawner: deps.spawner }),
      log,
    })
    if (!resolution.ok) throw new SourceRefusal(resolution.reason)
    const context = (where: string, project: string): PlanContext =>
      contextFor(installation, resolution.path, where, project)
    return {
      read: newReadHandlers({
        source: cliNewReads({
          plan: newReadPlanner(context(directory, installation.project)),
          log,
          ...seams,
        }),
        cliVersion: resolution.version,
        floors: READ_FLOORS,
      }),
      // The wizard makes its own working directory (`SETUP_DIR_PREFIX`) and hands
      // its planner that one -- the answers file is written there, and a setup
      // that has not applied yet must not leave anything in the checkout it was
      // run from -- so this planner passes the directory it is given and not the
      // session's.
      setup: createSetupWizard({
        confirmer,
        plan: (name: SetupSpawnName, where: string) => planFor(name, {}, context(where, installation.project)),
        cliVersion: resolution.version,
        floor: SETUP_CLI_FLOOR,
        // The setup runs in the installation's project, which is the one whose
        // licence the apply is judged on -- the same reader the two writes are
        // built with, for the same reason (`wizardrun.ts` says why the window's
        // yes asks it a second time).
        lapsed: () => deps.lapsed(installation.project),
        log,
        ...seams,
      }),
      // The two writes name the project with `--project` and change that
      // project's own config, so both run in the installation's directory
      // (rule 5) and both are handed the project this session is for.
      models: createModelsAdd({
        confirmer,
        plan: (name, values: ModelsAddValues, project: string) =>
          planFor(name, modelsAddArgs(values), context(directory, project)),
        project: installation.project,
        cliVersion: resolution.version,
        floor: MODELS_ADD_CLI_FLOOR,
        lapsed: () => deps.lapsed(installation.project),
        log,
        ...seams,
      }),
      image: createImageAdd({
        confirmer,
        plan: (name, values: ImageAddValues, project: string) =>
          planFor(name, imageAddArgs(values), context(directory, project)),
        project: installation.project,
        cliVersion: resolution.version,
        floor: IMAGE_ADD_CLI_FLOOR,
        lapsed: () => deps.lapsed(installation.project),
        log,
        ...seams,
      }),
    }
  }

  /** load reads the binding and answers the session for it, building one the
   * first time this binding is used. The assignment happens before the first
   * await, so two messages that arrive together share one build rather than
   * starting two walks. */
  const load = async (): Promise<Session> => {
    const installation = deps.binding()
    if (installation === null) throw new SourceRefusal(NO_ACTIVE_INSTALLATION)
    const key = bindingKeyOf(installation)
    if (session !== null && session.key === key) return session.built
    const built = buildSession(installation)
    session = { key, built }
    // A build that failed is not kept as this binding's session: resolution can
    // fail for a reason that passes (a binary mid-upgrade, a `version` probe
    // that did not answer in time on a loaded machine, an installation chosen
    // after the console started), and the binding key carries no version, so a
    // rejection kept here would answer all thirteen operations with it until
    // the operator switched installations. The next message builds again.
    built.catch(() => {
      if (session !== null && session.built === built) session = null
    })
    return built
  }

  /** buildWizard resolves the binary and builds the setup flow **without a
   * binding**. The wizard is what the no-installation onboarding view shows in
   * place of a command line (the console-improvements plan, SW3b), so it may not
   * be refused for the lack of an installation -- and the two rows it drives take
   * no project (`spawn.ts`'s `takesProject: false`), which is what lets a
   * `fugaro setup` child create the installation its answers name.
   *
   * The resolution is rule 4's walk with no override, because there is no record
   * to carry one, over the target the startup handshake reads the CLI through
   * with nothing chosen (`NO_INSTALLATION_TARGET`), and the child's directory is
   * the console's private working directory (rule 5) -- which the wizard replaces
   * with its own working directory anyway (`SETUP_DIR_PREFIX`).
   */
  const buildWizard = async (): Promise<SetupWizard> => {
    const directory = privateWorkingDirectory()
    const resolution = await resolveBinary({
      appEnv: deps.appEnv,
      directory,
      target: NO_INSTALLATION_TARGET,
      ...(deps.spawner === undefined ? {} : { spawner: deps.spawner }),
      log,
    })
    if (!resolution.ok) throw new SourceRefusal(resolution.reason)
    return createSetupWizard({
      confirmer,
      plan: (name: SetupSpawnName, where: string) =>
        planFor(name, {}, {
          target: NO_INSTALLATION_TARGET,
          binaryPath: resolution.path,
          directory: where,
          appEnv: deps.appEnv,
        }),
      cliVersion: resolution.version,
      floor: SETUP_CLI_FLOOR,
      // There is no installation here and so no project whose licence could have
      // lapsed: the store answers a project it holds no key for as `none`, which
      // is not the lapse (`licence-store.ts`), and the onboarding state is
      // exactly the one with no key at all.
      lapsed: () => deps.lapsed(''),
      log,
      ...seams,
    })
  }

  /** loadSetup answers the wizard for the message's binding: the active
   * installation's own flow when there is one -- one resolution, the same session
   * the reads use -- and the unbound wizard when there is none, which is the
   * state the onboarding view shows it in.
   *
   * The unbound wizard is built once and kept, with the same discard-on-failure
   * the session keeps and for the same reason: a CLI installed after the console
   * started must answer the next message rather than being refused under a
   * failure that has passed. There is no binding key to build it under, and none
   * is needed -- there is no installation for one to change.
   */
  const loadSetup = async (): Promise<SetupWizard> => {
    if (deps.binding() !== null) return (await load()).setup
    if (wizard === null) {
      const built = buildWizard()
      wizard = { built }
      built.catch(() => {
        if (wizard !== null && wizard.built === built) wizard = null
      })
    }
    return wizard.built
  }

  const rows: NewOperationRows = {
    'runs.stream': async (args) => (await load()).read['runs.stream'](args),
    'spend.stream': async (args) => (await load()).read['spend.stream'](args),
    'budget.stream': async (args) => (await load()).read['budget.stream'](args),
    whoami: async (args) => (await load()).read.whoami(args),
    'image.catalog.ls': async (args) => (await load()).read['image.catalog.ls'](args),
    'recipes.ls': async (args) => (await load()).read['recipes.ls'](args),
    'setup.plan': async (args) => (await loadSetup())['setup.plan'](setupPlanArgs(args)),
    'setup.apply': async (args) => (await loadSetup())['setup.apply'](args),
    // **The settings editor spawns no child of its own** (SU2b): the runner is
    // the flow, built in `main` over the window, and its three reads are the
    // bound source's -- so this row reads it at message time and hands the
    // payload straight over, exactly as the row it replaces did.
    'settings.editor.open': (args) => deps.settingsEditor().open(args),
    'models.add.plan': async (args) => (await load()).models['models.add.plan'](args),
    'models.add': async (args) => (await load()).models['models.add'](args),
    'image.add.plan': async (args) => (await load()).image['image.add.plan'](args),
    'image.add': async (args) => (await load()).image['image.add'](args),
  }

  return Object.freeze({
    rows: Object.freeze(rows),
    cancelFor: (project: string): void => {
      const window = deps.confirmer()
      window.close(setupConfirmID(project))
      window.close(modelsAddConfirmID(project))
      window.close(imageAddConfirmID(project))
    },
  })
}
