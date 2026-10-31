// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The console's Electron main process entry point (desktop-console plan Task
// 1.1; the IPC wiring is Task 3.4a).
//
// The plan's Files column for Task 1.1 names `src/main/window.ts` and
// `src/main/protocol.ts`; this file is here because those two are libraries and
// something has to call them. Without an entry point the `app://` scheme is
// never registered, `protocol.handle` never runs, and the e2e spec has no
// application to launch -- the window flags would be a set of options nothing
// constructs. It keeps to the minimum that makes the two modules live, and it
// is the only place in the console that imports Electron at runtime: every
// decision it could get wrong is in `window.ts` or `protocol.ts`, where the
// unit test can reach it under plain Node.
//
// **What Task 3.4a adds here, and why it is here rather than in a module the
// unit suite can import.** Three things, all of them one-liners over functions
// that *are* unit-tested: the served root is the built bundle (`rendererRoot`),
// the dashboard's preload path is named once (`consolePreloadPath`), and the
// handshake's answer is turned into a registered IPC surface (`onDecided`).
// What is left in this file is the wiring between them, and the wiring is the
// part that cannot be imported under `vitest`: this module imports the Electron
// *runtime*, so `require('electron')` here is a process and not a path string.
// `test/unit/startup.test.ts` reads this file as **text** and asserts the shapes
// that matter (the order, the once-only registration, the refusal a console with
// no installation answers), and `test/e2e/entry.spec.ts` drives the real thing.
//
// Note where the documents are read from. `tsc` emits JavaScript only, so the
// bundle's files are written by `tools/bundle-renderer/index.ts` into
// `dist/bundle/` -- the compiled entry's whole closure as one script, and the two
// documents copied verbatim -- and the handler's root is that directory under the
// app's own path. Task 4.1's builder is the one that gets to decide where a
// packaged console keeps those files, and it will have to pack them; until then
// there is no packaging step to disagree with.

import { join } from 'node:path'

import { app, BrowserWindow, Menu, ipcMain, protocol, session, shell } from 'electron'

import { APP_ENTRY_URL, handleAppRequests, registerAppScheme } from './protocol'
import { consoleIconPath, installDevDockIcon } from './icon'
import { installInstallationPicker } from './picker'
import type { InstallationPicker } from './picker'
import {
  createConsoleLifecycle,
  installWebContentsSecurity,
  openExternalValidated,
  versionReadFrom,
} from './window'
import type { ConsoleWindowDeps, VersionRead } from './window'
import {
  cliSentence,
  planFor,
  privateWorkingDirectory,
  resolveHandshakeBinary,
  runToCompletion,
} from './spawn'
import type { SpawnTarget } from './spawn'
import { createSpawnLog } from './spawnlog'
import type { SpawnLogWriter } from './spawnlog'
import { parseInstallationsDocument } from './installlist'
import { createLaunchRunner, DECLINING_CONFIRMER } from './launch'
import type { FrozenLaunch, LaunchRequest, TargetLookup } from './launch'
import { startUpdates } from './updater-electron'
import { createUpdateSettings } from './updatestore'
import type { UpdateController } from './updates'
import { CONFIRM_ENTRY_URL, createConfirmWindow } from './confirm'
import { launchFingerprint } from './launchrules'
import type { LaunchRecordFacts } from './launchrules'
import type { InstallationsListReply } from './onboarding'
import { reducedModeHandlers, registerConsoleHandlers, startupCompatibility } from './ipc'
import type { ConsoleHandlers, ValidatedArguments } from './ipc'
import { handlersFor } from '../source/source'
import { createBoundSource } from '../source/cli'
import type { BoundInstallation } from '../source/cli'
import {
  createInstallationFileStore,
  installationStorePath,
  resolveLaunchBinary,
  withResolvedBinary,
} from './installations'
import type { StoredInstallation } from './installations'
import type { ActiveInstallation } from './installations'
import type { CliCompatibility } from '../shared/version'
import { NO_INSTALLATION_SURFACE } from '../source/source'
import type { InstallationSurface } from '../source/source'

// Before `whenReady` resolves, which is the only window Electron gives for it:
// a scheme registered later is not a standard or secure one.
registerAppScheme(protocol)

// Every webContents Electron ever creates gets the three denials, not just the
// console's own window -- so a webContents that appears for a reason this file
// does not know about is still one that cannot navigate, open a window or
// attach a `<webview>`. `createConsoleWindow` installs them on the window it
// builds as well: a window that is correct on its own does not depend on the
// global hook having been registered.
app.on('web-contents-created', (_event, contents) => {
  installWebContentsSecurity(contents)
})

/**
 * rendererRoot is the directory the protocol handler serves, and the only
 * directory it will ever read a file from.
 *
 * It is the **build's** output and not the source tree (Task 3.4a, step 4):
 * `npm run build` compiles the renderer's entry into `dist/renderer/`, bundles it
 * and its whole closure into `dist/bundle/console.js`, and copies the two
 * documents beside it. `SERVED_FILES` in `src/main/protocol.ts` is the list of
 * what that directory holds, and `test/unit/bundle.test.ts` holds the two equal
 * in both directions -- so this line is the one place the served root is named,
 * and the directory it names is the one a test checks.
 */
function rendererRoot(): string {
  return join(app.getAppPath(), 'dist', 'bundle')
}

/**
 * consolePreloadPath is the compiled preload the dashboard window loads: the
 * file `tsc` emits from `src/preload/index.ts`.
 *
 * It is inside the app's own path, like the served root, and it is named here
 * because this is the module that knows where the app is. The rule that the
 * renderer may not name a path is unaffected: the *renderer* never sees this
 * one -- the window is built with it before any document loads, and what crosses
 * into the renderer is the frozen object the preload puts on `window`
 * (`src/preload/index.ts`).
 */
function consolePreloadPath(): string {
  return join(app.getAppPath(), 'dist', 'preload', 'index.js')
}

function reportAndQuit(error: unknown): void {
  console.error('[console] the window could not be created:', error)
  // The other way out of this process, and `app.exit` fires no `before-quit`:
  // the handler below is not reached on this path, so a switch or an `activate`
  // that failed would leave the console's child running exactly as a plain quit
  // would have. The close is bounded (`SIGTERM`, then `SIGKILL`), so a fatal
  // error still exits, and the exit code is the one this path has always used.
  void boundSource
    .close()
    .catch(() => undefined)
    .finally(() => app.exit(1))
}

/**
 * serveBundle puts the protocol handler in place, and there is exactly one of
 * these per process: `protocol.handle` refuses a scheme that is already
 * registered ("Failed to register protocol: app"), so a second call is not a
 * harmless no-op but a thrown error. It is therefore separate from
 * `lifecycle.activate` below, which is the half that may run again.
 */
function serveBundle(): void {
  handleAppRequests(protocol, rendererRoot())
}

/**
 * handshakeInstallation is the stored **active** installation the startup
 * handshake reads (Task 3.4b, steps 3 and 4), or `null` when there is none.
 *
 * With one, its `binaryPath` -- when the store still carries that optional
 * override -- is the first candidate rule 4 asks about, and its project (and
 * config path, when stored) is the spawn target, so the bare `version` spawn
 * records which installation it was for. The installation is the one
 * `openInstallation` settled: the remembered choice when the CLI's list says it
 * can be read, else the first readable row of that list. With no installation
 * at all the console makes **no guess**: not `FUGARO_PROJECT`, not the working
 * directory's checkout -- `resolveHandshakeBinary` runs Task 3.3's project-less
 * read against rule 4's resolution, the dashboard answers
 * `NO_ACTIVE_INSTALLATION`, and the rail draws no row.
 *
 * **Two fields are not the store's answer to "which binary", and they are handed
 * on anyway** (T-D1, T-D3): `distro` and `checkout`. The first is the
 * installation's **identity** on Windows rather than an override --
 * `resolveHandshakeBinary` sends a `distro` read *inside* that distribution,
 * because there is no host walk to fall through to (T-C2) and a distribution
 * that has gone is a stated refusal rather than a silent switch to another one
 * (F7) -- and the second is the directory the CLI starts in there, which travels
 * as the wrapper's `--cd` (`wslDirectory`, D3(a)). Omitting either would leave a
 * stored WSL installation read as a host one: the walk finds no `fugaro` on
 * Windows (T-C2) and the console reports a missing binary for the installation
 * the operator chose, in a distribution it never entered.
 */
function handshakeInstallation(): {
  readonly binaryPath?: string | undefined
  readonly distro?: string | undefined
  readonly checkout?: string | undefined
  readonly target: SpawnTarget
} | null {
  const chosen = picker?.installation()
  if (chosen === undefined || !chosen.chosen) return null
  return {
    ...(chosen.binary === undefined ? {} : { binaryPath: chosen.binary }),
    ...(chosen.distro === undefined ? {} : { distro: chosen.distro }),
    ...(chosen.checkout === undefined ? {} : { checkout: chosen.checkout }),
    target: Object.freeze({
      project: chosen.project,
      ...(chosen.config === undefined ? {} : { configPath: chosen.config }),
    }),
  }
}

/**
 * handshakeProject is the **project** of the installation whose startup
 * handshake has just been read, until `onDecided` hands the answer to the
 * picker's registry (`picker.remember`), so `installation.current` and the
 * picker's rows carry the startup's mode and version rather than a default
 * (review S1). It is consumed once: a later `onDecided` comes from a choose,
 * whose own probe the picker has already remembered.
 *
 * **The project and not the id**, because that is the key the compatibility is
 * held under: `picker.remember` -> `registry.remember` -> `compatibility.set`,
 * and every reader asks for `active.project` / `installation.project`. An id
 * here would store the handshake's answer where nothing looks -- the header
 * drawn as "version not read · unknown" and the active row as `unknown` on every
 * launch, with the answer the startup had just read thrown away.
 */
let handshakeProject: string | null = null

/**
 * readCliVersion is the handshake's reader: `resolveHandshakeBinary`
 * (`src/main/spawn.ts`, where it is unit-tested) and the version line of the
 * one binary it reads.
 *
 * It reads the machine and nothing else -- no state from a previous run, no
 * cached answer. A resolution that fails crosses into the compatibility as a
 * **category** (`versionReadFrom` in `window.ts`), never as `resolution.reason`,
 * which embeds what the binary printed; and a resolution or a probe that
 * *throws* is left to `runVersionHandshake`, which turns it into the refusal
 * with the console's own sentence rather than a crash.
 *
 * The child runs in `privateWorkingDirectory()` (rule 5), and the probe is
 * logged like every spawn (design section 5.1 rule 10): the log is opened for
 * this one read and closed as soon as it is done.
 */
async function readCliVersion(): Promise<VersionRead> {
  const log = createSpawnLog(process.env)
  // The owner's open-first-readable rule, before anything is resolved (ruling of
  // 2026-10-25): the console opens the installation it can read -- the remembered
  // choice when the CLI's own document says so, the first readable one otherwise
  // -- and the handshake below is then the handshake of **that** installation.
  // Doing it here rather than in the renderer is what makes the whole run agree:
  // the header's binary, the source the screens read through and the rail's
  // selected row are one decision.
  //
  // It is awaited before `handshakeInstallation`, which reads the session the
  // call settles, and it never throws (`openInstallation`): a list read that
  // failed leaves the console on the store's own answer, which is what it did
  // before this rule existed.
  await picker?.openInstallation()
  const chosen = handshakeInstallation()
  try {
    const resolution = await resolveHandshakeBinary({
      installation: chosen,
      appEnv: process.env,
      directory: privateWorkingDirectory(),
      log,
    })
    // The startup's own resolution is remembered for this run, exactly as a
    // choose's is: it is the binary this process is about to drive, and the one
    // fact the dashboard's header needs -- which the *store* cannot answer,
    // because this console never stores the path it resolves (owner's ruling of
    // 2026-10-20) and a record whose override has gone resolves somewhere else
    // entirely.
    if (chosen !== null && resolution.ok) resolvedBinaries.set(chosen.target.project, resolution.path)
    return versionReadFrom(resolution)
  } finally {
    handshakeProject = chosen === null ? null : chosen.target.project
    log.close()
  }
}

/** windowDeps is the Electron surface the windows are built with.
 * `session.defaultSession` is read here rather than at module scope because it
 * does not exist until the app is ready. `preloadPath` is the dashboard window's
 * (Task 3.4a): `buildConsoleWindow` gives it to that window alone. */
function windowDeps(): ConsoleWindowDeps {
  return { BrowserWindow, session: session.defaultSession, preloadPath: consolePreloadPath() }
}

// ---------------------------------------------------------------------------
// The IPC surface (desktop-console plan Task 3.4a, steps 1 to 3)
// ---------------------------------------------------------------------------

/**
 * current is the handshake's answer, and the only thing this file stores.
 *
 * It is written once, by `onDecided` below, before any window exists -- so a
 * handler can only ever read an answer the console has already drawn from -- and
 * it is read through `storedCompatibility` rather than captured, which is what
 * makes `startup.compatibility`'s answer the one the console decided on rather
 * than a copy of it.
 */
let current: CliCompatibility | null = null

/**
 * storedCompatibility is the read the `startup.compatibility` handler answers
 * from. It throws when there is no answer yet, and `dispatch` turns that throw
 * into a reply: an operation called before the handshake finished is a console
 * that does not exist yet, and it is answered as a refusal rather than as
 * `null`, which no screen is written for.
 */
function storedCompatibility(): CliCompatibility {
  if (current === null) throw new Error('the console has not read a fugaro yet')
  return current
}

/**
 * activeBinding is the installation the source is bound to, as the store holds
 * it **now** (Task 3.4c, step 1). `null` is D13's no-installation state, and it
 * is answered with no spawn at all -- the alternative would be a source built
 * from whatever the machine happened to have (the ambient `FUGARO_PROJECT` and
 * the first `fugaro` on `PATH`), which is a console driving something the
 * operator never chose.
 *
 * It is a function rather than a value for the reason `handlersFor`'s
 * `installation` dep is: the store is read at message time, so a choose or a
 * removal is answered by the next read rather than by a value captured when the
 * surface was registered.
 */
/**
 * installationState is D13's tuple as the **header** reads it: the store's
 * record with the binary this run resolved for it.
 *
 * The precedence is the resolution's and not the store's, and that is the whole
 * point of the function. A record's `binaryPath` is an **optional override** that
 * rule 4 merely *asks first* (owner's ruling of 2026-10-20): when the file has
 * gone, the walk carries on to `/opt/homebrew/bin`, `/usr/local/bin`,
 * `~/.local/bin` and `/usr/bin` and drives one of those. A header that read the
 * store alone would name a path this process is not running -- and, for a record
 * this console wrote itself, would name nothing at all and be drawn as "a line
 * the console will not draw", because those records carry no binary. The
 * resolution is remembered in `resolvedBinaries`, which is where both the choose
 * and the startup handshake put it, so this is a fact of the run rather than a
 * second guess at rule 4's order.
 *
 * `chosen: false` (and `undefined`, before the picker exists) is passed through
 * as the store answered it: D13's no-installation sentence, and the store's own
 * refusal when the file could not be read.
 */
function installationState(): ActiveInstallation | undefined {
  const chosen = picker?.installation()
  if (chosen === undefined) return undefined
  // `withResolvedBinary` is where the direction of the join is decided and
  // unit-tested; this file only supplies the two values.
  const resolved = chosen.chosen ? resolvedBinaries.get(chosen.project) : undefined
  return withResolvedBinary(chosen, resolved)
}

/**
 * activeBinding is the installation the source is bound to: the same state, as
 * the source layer's `BoundInstallation`.
 *
 * It reads the store and **only** the store, exactly as `handshakeInstallation`
 * does: `chosen.binary` is the record's optional override -- rule 4's first
 * *candidate*, absent in the ordinary case, which is every record this console
 * wrote -- and nothing else is handed on. The binary the walk finds is the
 * source's own business: `sourceFor` gives this field to the resolver as the
 * configured path, so a path supplied here from `resolvedBinaries` would be read
 * as the operator's own choice. `configuredSource` would then report
 * `configured` for `/usr/local/bin/fugaro`, and the bound source's
 * `InstallationRef.source` -- the provenance word design section 3.4 adds so
 * that "why is this the wrong fugaro?" has an answer -- would say `configured`
 * for every installation this console wrote, which is precisely the case in
 * which the operator configured nothing.
 *
 * So the two readers of this run's resolution are the header's and no one
 * else's: `installationState` names the path this process resolved (the fact the
 * store cannot hold), and the source resolves rule 4 itself from the same input
 * the handshake had -- the override, when a record carries one. The path that
 * runs is therefore named by both, and the only way for the two to differ is the
 * machine changing between the handshake and a read (an upgrade that moves
 * `fugaro`): then the header is stale and the child is right, which is the
 * direction to be wrong in.
 */
function activeBinding(): BoundInstallation | null {
  const chosen = picker?.installation()
  if (chosen === undefined || !chosen.chosen) return null
  return {
    id: chosen.id,
    project: chosen.project,
    ...(chosen.binary === undefined || chosen.binary === '' ? {} : { binaryPath: chosen.binary }),
    ...(chosen.config === undefined ? {} : { configPath: chosen.config }),
    ...(chosen.checkout === undefined ? {} : { checkout: chosen.checkout }),
    ...(chosen.cliVersion === null ? {} : { version: chosen.cliVersion }),
  }
}

/**
 * resolvedBinaries is where the binary rule 4 resolved for an installation is
 * remembered **for this run**, keyed by project name, and it is deliberately not
 * the store: the store holds the operator's choices, and the path rule 4
 * resolved is a fact of a run (owner's ruling of 2026-10-20). Storing it would
 * pin the console to a binary the operator never named -- and an upgrade that
 * moves `fugaro` would leave the store pointing at nothing.
 *
 * **Two things write it, and they are the two moments a resolution happens**:
 * the startup handshake (`readCliVersion`, for the record's own project) and a
 * choose (the picker hands its path to `onChosen`, after the handshake that made
 * it a fact). Its one reader is `installationState` -- the dashboard header --
 * which is the whole point of the map: the store cannot answer which `fugaro`
 * this run is running, because the console never writes the path it resolves.
 * The source does not read it either: `activeBinding` hands the record's
 * override to `sourceFor` and the source walks rule 4 itself, so the map is one
 * fact with one reader rather than a path passed around as if the operator had
 * chosen it.
 */
const resolvedBinaries = new Map<string, string>()

/**
 * boundSource is the `FugaroSource` the whole surface is built over: the real
 * `CliSource` for the active installation, built by `sourceFor` and cached by
 * the installation's binding -- its id and the three fields the child is built
 * from, the checkout among them (`createBoundSource` in `src/source/cli.ts`,
 * where the whole of the decision is unit-tested). The checkout is in that key
 * because it is the child's working directory: a record whose checkout changed
 * keeps its id, so a source cached by the id alone would keep spawning in the
 * directory the operator just left.
 *
 * **This is the only source the shipped app can construct.** `sourceFor` gives
 * the record's optional override to the resolver as the configured path and
 * walks rule 4's well-known locations otherwise, so the child is the installed
 * `fugaro` and never a binary an environment variable named.
 *
 * The replaying source (`src/source/fixture.ts`) is reachable only through the
 * `create` seam the tests inject, and a grep-style test holds `this file` to
 * that: `test/unit/entry-wiring.test.ts` asserts that nothing in it imports
 * `fixture.ts`, and that no environment variable, argv or setting selects a
 * source.
 *
 * The store is read through `activeBinding`, and the source it builds spawns on
 * the first read rather than here, so a console with nothing chosen -- and a
 * console in a mode that draws no screen -- drives nothing at all.
 */
const boundSource = createBoundSource({ installation: activeBinding, appEnv: process.env })

/**
 * openExternalFromShell is the surface's one operation that is not a read of the
 * CLI: it hands the URL to `openExternalValidated`, which is the console's one
 * call to `shell.openExternal` and its own allow-list (`src/main/window.ts`). The
 * argument is narrowed here and validated there; a value that is not a string
 * cannot be a URL this console opens, so it is refused by the same function
 * rather than by a second check written here.
 */
async function openExternalFromShell(raw: unknown): Promise<void> {
  await openExternalValidated(shell, typeof raw === 'string' ? raw : '')
}

/**
 * launchRequest narrows the operation's five validated fields to the launcher's
 * own request. The validator has already refused everything that is not a string
 * (`ipc.ts`'s kinds), so this is a narrowing and not a check: a value that is
 * somehow not a string is left out, and the launcher's own rules refuse the
 * empty field that leaves behind.
 */
function launchField(args: ValidatedArguments, key: string): string | undefined {
  const value = args[key]
  return typeof value === 'string' ? value : undefined
}

function launchRequest(args: ValidatedArguments): LaunchRequest {
  const title = launchField(args, 'title')
  const planTask = launchField(args, 'planTask')
  return {
    installation: launchField(args, 'installation') ?? '',
    repository: launchField(args, 'repository') ?? '',
    task: launchField(args, 'task') ?? '',
    ...(title === undefined ? {} : { title }),
    ...(planTask === undefined ? {} : { planTask }),
  }
}

/** LAUNCH_NO_RECORDS is the refusal a console with no installation surface at
 * all answers: there are no records to check a name against, and a name that
 * cannot be checked is not one a launch may be built for. */
const LAUNCH_NO_RECORDS = 'this console cannot read its installation records yet, and a launch needs one it can check'

/** storedRecord is this console's own record for one installation, read from the
 * file the picker's registry reads and not from a second source: the record
 * carries the three fields a launch needs from the machine rather than from the
 * CLI -- the `binaryPath` override, the config path and the distribution the CLI
 * runs inside (or `installations.ts`'s `StoredInstallation` for why each is
 * there). `undefined` covers both "no record" and "no store": a console that
 * cannot read its own file has no override to honour, and the ambient
 * resolution the CLI does anyway is the answer that spends nothing.
 *
 * **The whole record is read in one place on purpose.** Reading a field at a
 * time is how a launch ends up resolving the CLI the *host* has while the
 * record it was given points at another one -- inside a distribution, or at a
 * path the operator chose -- and that is a different CLI, a different config and
 * a different credential. */
function storedRecord(project: string): StoredInstallation | undefined {
  let store
  try {
    store = createInstallationFileStore({
      path: installationStorePath({
        userData: app.getPath('userData'),
        env: process.env,
        home: app.getPath('home'),
      }),
    })
  } catch {
    // The console has no place to keep its own records, so it has none to read.
    return undefined
  }
  const read = store.load()
  if (!read.ok) return undefined
  return read.file.installations.find((record) => record.project === project)
}

/** pickerList is the rail's own read, asked of the picker directly: `list` is
 * the picker's name for it and not the surface's (`installation.list`), and the
 * answer is the picker's `ok`/`data` convention unwrapped -- which is what
 * `source.ts`'s `rail` does for the dashboard and what this file must do for
 * itself. `null` is every way the read can fail to produce rows: no picker, a
 * refusal, an answer that is not the shape. */
async function pickerList(): Promise<InstallationsListReply | null> {
  const built = picker
  if (built === null) return null
  const answer = (await built.answer('list', {})) as
    | { readonly ok?: unknown; readonly data?: InstallationsListReply }
    | null
    | undefined
  if (answer === null || typeof answer !== 'object' || answer.ok !== true) return null
  return answer.data ?? null
}

/**
 * launchLookUp answers what this process's own records say about one
 * installation, and it is deliberately built from the two reads that already
 * exist rather than from a new one: the rail's own `installation.list` (which is
 * `fugaro config list --json` through the picker) tells this file whether the
 * console's records carry the name at all, and the document that same command
 * prints carries the repositories and their recorded base branches the launch
 * needs. A name the records do not carry -- or that the CLI did not list -- is
 * refused **before** any window is asked about, which is design section 5.1
 * rule 11's own order.
 *
 * **The route and the binary come from the console's own record and not from the
 * rail's reply**, which carries a row's project, its checkouts and its state and
 * neither of those. `storedRecord` is that read and `resolveLaunchBinary`
 * (`installations.ts`) is the resolution: an installation whose record names a
 * `binaryPath` is resolved at that path rather than through the host's walk, and
 * an installation whose record names a `distro` is resolved *inside* that
 * distribution, so the launcher's own by-name refusal of the WSL route
 * (`launch.ts`) is reached with the truth rather than with a host CLI this
 * installation does not run.
 *
 * The record's `configPath` is carried into the target and not passed: every
 * stored `configPath` is refused before a window opens (see the note on that
 * refusal below), and that refusal is what makes the field visible rather than
 * ignored.
 */
async function launchLookUp(project: string): Promise<TargetLookup> {
  // **One log for the whole call, and it is closed here.** The resolution and
  // the records read are two children of one lookup, and rule 10's log is a
  // writer with a descriptor: two writers opened per call and never closed is a
  // descriptor leaked for every launch a renderer asks about -- including the
  // ones that are refused, which is the cheapest way to ask for a great many. So
  // the two children share the one writer, and the `finally` releases it on
  // every path out, the refusals included.
  const log = createSpawnLog(process.env)
  try {
    return await launchLookUpWith(project, log)
  } finally {
    log.close()
  }
}

async function launchLookUpWith(project: string, log: SpawnLogWriter): Promise<TargetLookup> {
  const listed = await pickerList()
  if (listed === null) return { ok: false, reason: LAUNCH_NO_RECORDS }
  const row = listed.rows.find((candidate) => candidate.project === project)
  if (row === undefined || !row.saved) {
    return {
      ok: false,
      reason: `no installation named ${JSON.stringify(project)} is one this console’s records carry`,
    }
  }
  // A row the rail has and this console's own file does not is resolved as the
  // ambient installation the CLI would use: no override, no distribution. The
  // record's three fields are then handed to the launcher as they are read, so
  // the refusal it makes about them is made about the real ones.
  const record = storedRecord(project)
  const distro = record?.distro ?? ''
  const resolution = await resolveLaunchBinary(record ?? { id: '', project }, {
    appEnv: process.env,
    home: app.getPath('home'),
    spawnLog: log,
  })
  if (!resolution.ok) return { ok: false, reason: resolution.reason }
  // The records read runs on the same route as the resolution: under a
  // distribution the CLI is a Linux program and no host path reaches it, so the
  // read goes through `wsl.exe` with that record's distro -- the route
  // `probeInstallation` takes, and the one the launcher refuses for a *launch*
  // before any window opens.
  const plan = planFor('installation.list', {}, {
    target: { project: '' },
    binaryPath: resolution.value.path,
    directory: privateWorkingDirectory(),
    appEnv: process.env,
    ...(distro === '' ? {} : { wsl: { distro } }),
  })
  let run
  try {
    run = await runToCompletion(plan, { log })
  } catch (error) {
    return { ok: false, reason: error instanceof Error ? error.message : 'the CLI could not list the installations' }
  }
  if (run.signal !== null || run.code !== 0) {
    return {
      ok: false,
      reason: cliSentence(run.stderr) ?? `the CLI exited ${String(run.code)} for its installation list`,
    }
  }
  const parsed = parseInstallationsDocument(run.stdout)
  if (!parsed.ok) return { ok: false, reason: parsed.reason }
  const found = parsed.document.installations.find((candidate) => candidate.name === project)
  if (found === undefined) {
    return { ok: false, reason: `the CLI listed no installation named ${JSON.stringify(project)}` }
  }
  const configPath = record?.configPath ?? ''
  return {
    ok: true,
    target: {
      project,
      binaryPath: resolution.value.path,
      cliVersion: resolution.value.version,
      records: found,
      fingerprint: launchFingerprint(launchRecordFacts(record)),
      ...(configPath === '' ? {} : { configPath }),
      ...(distro === '' ? {} : { distro }),
    },
  }
}

/** launchRecordFacts is `launchrules.ts`'s `LaunchRecordFacts` read off this
 * console's own installation file -- and off that file **only**, because the
 * rail's row is not part of a launch's identity: its `listed` and `checkouts`
 * are answers of a `config list` read that can fail and succeed again between
 * the lookup and the answer, and its `saved` flag is true on every path that
 * reaches here (both callers refuse an unsaved row first); see
 * `LaunchRecordFacts`. */
function launchRecordFacts(record: StoredInstallation | undefined): LaunchRecordFacts {
  return {
    id: record?.id ?? '',
    project: record?.project ?? '',
    binaryPath: record?.binaryPath ?? '',
    distro: record?.distro ?? '',
    configPath: record?.configPath ?? '',
    checkout: record?.checkout ?? '',
  }
}

/** launchStillRecorded is the check that makes "an installation removed, or
 * changed, while its window is open closes as a cancel" true rather than
 * promised: it re-reads the rail's own records and this console's own file, and
 * answers whether the installation the frozen object was built for is still
 * **the same one** -- the name still saved, and the fingerprint
 * (`launchFingerprint`) unchanged. A row that disappeared, a name that was
 * re-added, and a record whose fields moved all answer no. */
async function launchStillRecorded(launch: FrozenLaunch): Promise<boolean> {
  const listed = await pickerList()
  if (listed === null) return false
  const row = listed.rows.find((candidate) => candidate.project === launch.project)
  if (row === undefined || !row.saved) return false
  return launchFingerprint(launchRecordFacts(storedRecord(launch.project))) === launch.fingerprint
}

/**
 * dashboardWindow is the window the confirmation window is modal over: the
 * console's dashboard, or none when there is not one -- a refused console, whose
 * window is the refusal document, and a startup that has not opened a window
 * yet. `confirmOptions` handles a `null` parent, so this answering nothing is a
 * state rather than a failure.
 *
 * It identifies the dashboard by its **document** rather than by taking the one
 * window that happens to be open, because the picker's window and an open
 * confirmation window are `BrowserWindow`s of this app as well: parenting a
 * confirmation to either would make it modal over the wrong window (and, for
 * another confirmation, over itself). Only the dashboard loads
 * `APP_ENTRY_URL`; the refusal document, the picker's and the confirmation's own
 * are other URLs. `getURL` is read inside the `try` because Electron's getters
 * throw on a window that is already destroyed, and a window this process cannot
 * read is not the dashboard.
 */
function dashboardWindow(): BrowserWindow | null {
  for (const window of BrowserWindow.getAllWindows()) {
    try {
      if (window.webContents.getURL() === APP_ENTRY_URL) return window
    } catch {
      continue
    }
  }
  return null
}

/**
 * updates is the running update policy, or null on everything that is not a
 * packaged macOS app. It is module scope rather than a local of `main` because
 * `before-quit` is not inside `main`: the timer it holds has to be stopped by
 * the quit, and the launcher's idle listener reaches it through this name.
 */
let updates: UpdateController | null = null

/**
 * launchRunner is the launcher the surface's `run.launch` answers through, and
 * the handler reads it **at message time** rather than closing over it. That is
 * what makes this the fail-closed default rather than the shipped one: its
 * confirmer is `DECLINING_CONFIRMER`, so a launch that somehow arrives before
 * `main` has built the confirmation window is refused by name
 * (`DECLINED_UNBUILT`) and nothing reaches `spawn`.
 *
 * `main` replaces it with the same launcher over the window's own confirmer
 * before any renderer can reach the operation: the surface goes on `ipcMain`
 * only from `onDecided`, which only `lifecycle.start()` and `choose` reach, both
 * from inside `main`.
 */
let launchRunner = createLaunchRunner({
  lookUp: launchLookUp,
  stillRecorded: launchStillRecorded,
  confirmer: DECLINING_CONFIRMER,
  appEnv: process.env,
  log: createSpawnLog(process.env),
})

/**
 * gated is the whole surface, and it is the join the two modules describe:
 * `handlersFor` answers the twelve operations that read the CLI -- eleven
 * before console L5b added `runs.stats` (`src/source/source.ts`'s
 * `FugaroSource` lists them) -- over
 * `boundSource`, so each of them reaches the active installation's own binary
 * and nothing else), and `startup.compatibility` is joined in here because it is
 * the one operation whose answer is the console's own state rather than a read
 * (`CliBackedHandlers` in `src/source/source.ts` says so from the other side).
 *
 * `reducedModeHandlers` wraps the result in `registerHandlers` below rather than
 * here, so that what this constant holds is the surface and what the wrapper
 * holds is the gate over it.
 */
const gated: ConsoleHandlers = {
  ...handlersFor(boundSource, {
    openExternal: openExternalFromShell,
    // `installation.current` (Task 3.4b): the picker's store joined with the
    // binary this run resolved for it, read at message time (see
    // `installationState`). Before the picker exists the answer is
    // `handlersFor`'s own no-installation state.
    installation: () => installationState(),
    // The installation rail's own surface (owner's design ruling of
    // 2026-10-25): the picker module's dispatcher, read at message time for the
    // same reason `installation` is -- this constant is built before
    // `whenReady` and the picker is built after it.
    installations: () => railSurface(),
  }),
  'startup.compatibility': startupCompatibility(storedCompatibility),
  // **The launch, and the console's one write** (V2.5.1a, design section 5.1
  // rule 11, D22). It is joined here for the same reason
  // `startup.compatibility` is: it is not a read of the *active* installation's
  // source, so `handlersFor` leaves it out (`CliBackedHandlers` in
  // `src/source/source.ts` says so from the other side).
  //
  // **A `run` child is spawned only on a yes from the confirmation window's own
  // top frame.** The two things that make that true are elsewhere: the sender
  // gate is `createConfirmWindow`'s (`src/main/confirm.ts`), and the launch is
  // built, frozen and spawned by `launch.ts` only from the answer that window's
  // own main-side path resolves. What is here is the handler, and it reads
  // `launchRunner` at message time -- so before `main` has built the window the
  // declining launcher above answers every launch with `DECLINED_UNBUILT`, and
  // this operation has no way to a child at all.
  'run.launch': (args) => launchRunner.launch(launchRequest(args)),
}

/**
 * railSurface is the rail's half of the surface as `handlersFor` reaches it:
 * the picker module's own answers, so the dashboard's document and the picker
 * window's are one implementation rather than two.
 *
 * A console whose picker was never built -- a unit test's, a build with no
 * store -- gets a surface that refuses every one of the six with
 * `NO_INSTALLATION_SURFACE`, which is a state and not a guess, and is what
 * `handlersFor` answers with when the dep is absent altogether.
 */
function railSurface(): InstallationSurface {
  const built = picker
  if (built === null) {
    const absent = async (): Promise<never> => {
      throw new Error(NO_INSTALLATION_SURFACE)
    }
    return { answer: absent, open: absent }
  }
  return {
    answer: (name, args) => built.answer(name, args),
    open: () => built.open(),
  }
}

/** registered is whether this process has put the surface on `ipcMain`. */
let registered = false

/**
 * registerHandlers puts the surface on `ipcMain`, **once per process**.
 *
 * The once-only rule is not tidiness: Electron's `ipcMain.handle` throws on a
 * second listener for a channel it already has ("Attempted to register a second
 * handler"), so a second registration would be a startup failure -- and the
 * guard is what makes `onDecided` safe to call from a path that might run twice,
 * which is exactly what `activate` is for the window.
 *
 * The wrapper is `reducedModeHandlers`: the handshake's answer decides which
 * operations refuse before the source is reached, and it is read per call, so a
 * handler registered here can never answer for a mode the console is not in.
 */
function registerHandlers(): void {
  if (registered) return
  registered = true
  registerConsoleHandlers(ipcMain, reducedModeHandlers(gated, storedCompatibility))
}

/**
 * onDecided is what the handshake's answer does to this process: it is stored,
 * and -- unless the console refused -- the IPC surface is registered.
 *
 * **A refused console registers nothing, ever.** That is the plan's step 2 and
 * design D7's strongest form: a refused console loads a document with no
 * renderer entry in it, and there is no handler behind any channel for one to
 * reach even if it did. The refusal is decided here, once, from the mode; a
 * later `activate` reopens the refusal window and does not come back through
 * this function at all.
 *
 * The ordering is the other half of it: `startupWindow` calls this after the
 * version read and **before** the dashboard document is loaded, so a renderer
 * that asks for `startup.compatibility` the moment it runs finds an answer
 * rather than an unregistered channel.
 */
function onDecided(compatibility: CliCompatibility): void {
  current = compatibility
  // The project, which is what `remember` keys the compatibility by: see
  // `handshakeProject` above for what an id here would cost.
  const read = handshakeProject
  handshakeProject = null
  if (read !== null) picker?.remember(read, compatibility)
  if (compatibility.mode === 'refused') return
  registerHandlers()
}

/**
 * lifecycle is the startup and the `activate` path (`createConsoleLifecycle` in
 * `window.ts`, where it is unit-tested): the handshake runs once -- a stored
 * promise, so an `activate` during the handshake waits for it rather than
 * starting a second one -- and `activate` opens the window the stored answer
 * allows, so a refused console comes back as a refusal screen and never as a
 * dashboard, and a dock-icon click cannot change which console the operator is
 * looking at.
 */
const lifecycle = createConsoleLifecycle({
  windowDeps,
  readVersion: readCliVersion,
  openWindowCount: () => BrowserWindow.getAllWindows().length,
  onDecided,
})

/**
 * picker is the installation picker (Task 3.4b): the store in
 * `<userData>/installations.json`, the `Installations…` menu item, and the
 * picker's own window and channels. It is built **after** `whenReady`, because
 * `session.defaultSession` and `app.getPath` do not exist before it, and
 * **before** the startup handshake, which reads the stored active installation
 * (`handshakeInstallation` above).
 *
 * A choose runs the version handshake for that installation in the picker
 * (`probeInstallation`); a `refused` answer is not chosen. Otherwise
 * `onChosen` hands the answer to `lifecycle.choose`, which goes through the
 * same path the startup does: `onDecided` stores it and registers the surface if
 * it was not yet, and the window is reopened through `openWindowFor` -- so a
 * refused-to-ok switch replaces the refusal window with the dashboard.
 */
let picker: InstallationPicker | null = null

/**
 * main is what `whenReady` resolves into: serve the bundle once, then run the
 * startup. Everything a second call would repeat is in `lifecycle.activate`.
 */
async function main(): Promise<void> {
  // The icon first, and before any window exists: an unpackaged console has no
  // bundle to carry `build/icon.png`, so until this runs the dock shows
  // Electron's own icon rather than the website's mark (the owner's brief of
  // 2026-10-19). It is a no-op once packaged -- the bundle's icon is the same
  // file, put there by the packager -- and off macOS, where there is no dock to
  // set. `installDevDockIcon` is where all of that is decided and tested.
  installDevDockIcon({
    platform: process.platform,
    packaged: app.isPackaged,
    dock: app.dock,
    iconPath: consoleIconPath(app.getAppPath()),
  })
  serveBundle()
  // **The confirmation window goes live here, and here only** (V2.5.1a part
  // 2b-3b-1). It is built after `serveBundle`, because this is the run of
  // `main` that has just made the document it loads answerable, and it is built
  // before the picker and `lifecycle.start`, because those two are the paths
  // that put the IPC surface on `ipcMain`: `run.launch` reads `launchRunner` at
  // message time, so a renderer that can reach the operation is a renderer that
  // finds the window's confirmer here rather than the declining default.
  //
  // `session.defaultSession`, never a partition: `protocol.handle` registers the
  // `app://` scheme on the default session and nowhere else, so a window on a
  // partition would issue its document request where nothing answers it and
  // every launch would be declined. `dashboardWindow` is the parent, so the
  // window is modal over the dashboard and never over the picker or over another
  // confirmation.
  launchRunner = createLaunchRunner({
    lookUp: launchLookUp,
    stillRecorded: launchStillRecorded,
    confirmer: createConfirmWindow({
      BrowserWindow,
      ipcMain,
      parent: dashboardWindow,
      appPath: app.getAppPath(),
      entryUrl: CONFIRM_ENTRY_URL,
      session: session.defaultSession,
    }),
    appEnv: process.env,
    log: createSpawnLog(process.env),
  })
  // **The update policy comes up with the launcher it defers to**, and before
  // the surface exists: `startUpdates` answers null on anything but a packaged
  // macOS app, so this is a no-op on the Linux build and in development, and on
  // a Mac it is where the client is hardened and the first check is started.
  //
  // The two halves are the same fact from each side: the policy asks
  // `isIdle()` when it is about to prompt or install, and the flow tells it
  // through `onIdle` when a launch that was in flight settles -- so a download
  // that finished mid-launch is offered the moment the launch ends rather than
  // waiting for the next six-hourly check.
  //
  // **The switch's one source is `updates.json`** (U4, D768 (a) of 2026-10-30).
  // The settings are built first because the policy reads them: `switchOn` is
  // asked afresh before every check, every prompt and every install, so a
  // hand-edited file -- or the checkbox, which writes the same file and then
  // stops or starts this controller -- is what the gate acts on.
  const updateSettings = createUpdateSettings({
    directory: app.getPath('userData'),
    controller: () => updates,
  })
  updates = startUpdates({
    isLaunchFlowIdle: () => launchRunner.isIdle(),
    switchOn: () => updateSettings.auto,
  })
  launchRunner.onIdle(() => {
    updates?.launchFlowIdle()
  })
  picker = installInstallationPicker({
    // `shell` and not `dialog`: the picker has no file dialog any more (owner's
    // ruling of 2026-10-20) and its one external link goes through
    // `openExternalValidated(shell, ...)`, which `installInstallationPicker`
    // closes over.
    electron: { BrowserWindow, session: session.defaultSession, ipcMain, Menu, shell, app },
    appEnv: process.env,
    // The rail's `config list --json` is a dashboard read since the owner's
    // ruling of 2026-10-25, and rule 10's log is one line per child the console
    // spawns: the picker's spawns are written to it like the source's.
    spawnLog: createSpawnLog(process.env),
    // The picker awaits this inside its own serial chain (review S6), so a
    // second choose cannot start until the window this one asked for is up.
    onChosen: (chosen) => {
      // Rule 11's "re-chosen": a launch names its own installation rather than
      // the active one, so the installation being chosen again is the window
      // that must close. Before `lifecycle.choose`, so the cancel is decided
      // while the old installation's window is still the only one open.
      launchRunner.cancelFor(chosen.project)
      resolvedBinaries.set(chosen.project, chosen.resolvedBinaryPath)
      return lifecycle.choose(chosen.compatibility).catch(reportAndQuit)
    },
    // A removal that took the active installation drops the bound source (Task
    // 3.4c; the seam 3.4b left for it). Without this the surface would keep
    // answering from a source built for a binary the store no longer names --
    // and, worse, would keep its child process alive. `forget` is safe with
    // nothing bound (the first run) and safe to call twice, so the wiring is
    // unconditional.
    onRemoved: () => {
      void boundSource.forget()
    },
    // And rule 11's "removed": the launch window's own half of the same fact.
    // `onRemoved` above is not it -- that one fires only when the removal took
    // the **active** installation away, and it is handed a record **id**, while
    // a launch in flight names its own installation and the launcher closes a
    // window by project. So every removal this console performs closes the
    // launch that project has open, active row or not.
    onRecordRemoved: (project) => {
      launchRunner.cancelFor(project)
    },
    // The two update items, handed over **only while a controller is running**:
    // `startUpdates` answered null on everything that is not a packaged macOS
    // app, so the menu carries neither item off a Mac and in development, and
    // nothing here repeats that admission test.
    ...(updates === null
      ? {}
      : {
          updateMenu: {
            auto: updateSettings.auto,
            setAuto: (on) => updateSettings.setAuto(on),
            checkNow: () => updateSettings.checkNow(),
          },
        }),
  })
  // The menu is installed in **every** mode, `refused` included: choosing an
  // installation is how an operator gets out of a refusal, and a console that
  // hid the door would be a console with no way back.
  picker.installMenu()
  await lifecycle.start()
  // **The console does not open the picker window here, and that is the
  // owner's design ruling of 2026-10-25**: there is no picker screen. D13's
  // first run -- no installation chosen -- is drawn by the dashboard's own
  // rail, which lists every installation the CLI found and opens the issue
  // view of the first one when none of them can be read. The picker window is
  // left where the ruling puts it: behind the rail's `+` door
  // (`installation.open`) and the menu item, both of which the operator asks
  // for.
}

app.whenReady().then(main).catch(reportAndQuit)

// The console is a single window: closing it quits, except on macOS, where the
// convention is that the app stays running and `activate` brings the window
// back.
app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit()
})

/**
 * **The console stops what it started before it exits.** The Live screen's poll
 * runs `fugaro watch` through the source bound to the active installation, and
 * that child lives as long as the source does: nothing else would stop it. The
 * menu's Quit is Electron's own `role: 'quit'` with an empty callback and
 * `window-all-closed` only calls `app.quit()`, so without this the child
 * outlives the app that started it -- the opposite of what `forget` protects (an
 * installation the console no longer shows leaves nothing running), and the
 * reason every spawn is written to the spawn log: a child nobody stopped is a
 * child somebody has to find.
 *
 * `CliSource.close()` is bounded -- `SIGTERM`, then `SIGKILL` after
 * `SHUTDOWN_GRACE_MS`, and a drain backstop for a grandchild holding the pipe --
 * so the quit can wait for it. The first `before-quit` is held back for exactly
 * that; the `app.quit()` below comes back through this handler, and that second
 * one falls through, or the quit would hold itself back forever.
 */
let closing = false
app.on('before-quit', (event) => {
  if (closing) return
  closing = true
  event.preventDefault()
  // The update controller goes first, and it is the one thing here that is not
  // a child process: its timer is a live handle and its generation counter is
  // what abandons a check whose listing is still in flight, so a quit that left
  // it running would leave a check that finishes -- and prompts -- into a
  // process on its way out.
  updates?.stop()
  void boundSource
    .close()
    .catch(() => undefined)
    .finally(() => app.quit())
})

// Note which of the two halves this calls. `serveBundle` here would re-register
// the scheme on every dock-icon click -- the window is gone, the process is not
// -- and `protocol.handle` throws on the second registration, which
// `reportAndQuit` would turn into `app.exit(1)`: the console would quit the
// moment the user asked it to come back.
app.on('activate', () => {
  lifecycle.activate().catch(reportAndQuit)
})
