// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// FugaroSource -- the whole data plane (desktop-console plan Task 1.3).
//
// Every screen in this console talks to one interface and to nothing else. It
// **yields `JSONDoc`-shaped snapshots and emits change events, and it knows
// nothing about how they were produced**: v1 produces them by spawning the
// installed `fugaro` (`src/source/cli.ts`, design D1's Option A), the tests
// produce them by replaying a recording (Task 1.4's `fixture.ts`), and the
// designed next step produces them by speaking HTTP to a loopback port (Option
// C). None of those three implementations is visible from here, and that is the
// point: **this one interface is why the auth decision (design section 3) is
// cheap to revisit**, and why a screen never learns whether a document came from
// a child process, a file or a socket.
//
// Two shapes leave a source, and there are no others:
//
//   - **`Snapshot`** -- a document, plus the console's own stamp: when it
//     arrived, which one it is in the series, which day it belongs to and
//     whether that day just changed. The document is `JSONDoc`: *the CLI's
//     document as it arrived*, undecoded. Decoding it into the console's typed
//     shapes is Task 2.0's `shared/schema.ts`, and this file deliberately does
//     not know a single field of it -- a field read here would be a field that
//     had to be kept in step with `console/docs/json-contract.md` by hand.
//   - **`TextSnapshot`** -- the two reads that are a sentence rather than a
//     document: `fugaro version` (one human line, section 6.7) and
//     `logs --url` (the one link, and the reason it is its own operation is that
//     the CLI refuses `--url` together with `--json`).
//
// Three things this interface deliberately does not have:
//
//   - **no path, no URL, no command in a snapshot.** A snapshot holds the CLI's
//     document and nothing the renderer could build a request from. The one link
//     is a *text* snapshot, and the surface that opens it validates it against
//     `ALLOWED_EXTERNAL_HOSTS` in the main process (`src/main/window.ts`) rather
//     than trusting it because it came from a `--url` read.
//   - **no mutation.** There is no `write`, no `run`, no `cancel`: the console is
//     read-only in v1 by decision (design section 2), and the way to make that
//     true is to give the screens no method that could do otherwise.
//   - **no credential, and no parameter that could hold one.** A source is
//     constructed with an installation the operator chose (D13) and never with a
//     token: the child authenticates as the operator does in a terminal, using
//     the operator's own home directory, which the console passes through the
//     environment and never reads (design section 3.1).
//
// The last section of the file is `handlersFor`, which is the join between this
// interface and Task 1.2's IPC allow-list: it answers the whole surface with one
// handler per operation, each one receiving *validated* arguments and returning
// something already rendered. A refusal is an exception whose message is the
// CLI's own sentence, because `registerConsoleHandlers` passes a thrown message
// through verbatim (design section 6.3: a refused read shows the CLI's sentence,
// not a generic apology).

import type { ConsoleHandlers, ValidatedArguments } from '../main/ipc'
import { NO_ACTIVE_INSTALLATION } from '../main/installations'
import type { InstallationArguments, InstallationOperationName } from '../main/installations'
import type { Notice } from '../main/spawn'

/**
 * JSONDoc is the CLI's document as it arrived: a JSON object, undecoded. It is
 * the type the whole console passes around, and the reason it is not a union of
 * the thirteen shapes is that a source is not allowed to decide which shape a
 * document is -- that is Task 2.0's job, in one place, against the contract.
 */
export type JSONDoc = Readonly<Record<string, unknown>>

/**
 * InstallationRef is the resolved installation the header draws: the absolute
 * path of the binary, the version it reported, the project name and where the
 * resolution found it. The header shows the first two (design section 3.4 rule
 * 4), and `source` is here so the environment view can say *how* the binary was
 * chosen -- a version and a path with no provenance is what makes "why is this
 * the wrong fugaro?" unanswerable.
 */
export interface InstallationRef {
  readonly binaryPath: string
  readonly version: string
  readonly project: string
  readonly source: 'configured' | 'well-known'
  /** `checkout` is the installation's directory when it has one, which is also
   * the child's working directory (rule 5). */
  readonly checkout?: string | undefined
}

/**
 * Snapshot is one document, with the console's own stamp. `notices` are the
 * stdout lines that were not JSON objects (Task 0.2's reader rule): the degraded
 * mode writes a plain sentence on the same stream as the documents, so a snapshot
 * carries them next to the document rather than pretending the stream was clean.
 */
export interface Snapshot {
  readonly document: JSONDoc
  /** the console's clock, RFC 3339 UTC -- never the child's. */
  readonly receivedAt: string
  /** counts the documents this feed has delivered, from 1. */
  readonly sequence: number
  /** the document's `day`, when it carries a string one. */
  readonly day: string | null
  /** true when the day differs from the previous document's: a UTC-midnight
   * rollover, which is a **gap** in the series rather than a zero. */
  readonly rolled: boolean
  readonly notices: readonly Notice[]
}

/** TextSnapshot is a read that answers with a sentence rather than a document. */
export interface TextSnapshot {
  readonly text: string
  readonly receivedAt: string
}

/** FeedEndReason says why a live feed stopped. */
export type FeedEndReason =
  /** the console stopped it */
  | 'stopped'
  /** the CLI refused: exit 1 and the operator's sentence (section 10.5) */
  | 'refused'
  /** the child could not be started at all */
  | 'unstartable'

/** FeedEnd is the last event a feed emits, and the one a screen draws as the
 * reason the number stopped moving. */
export interface FeedEnd {
  readonly reason: FeedEndReason
  /** the CLI's own sentence, verbatim, when it printed one. */
  readonly sentence: string | null
  readonly snapshots: number
}

/** Listener is what `on...` returns: the way to stop listening. */
export type Unsubscribe = () => void

/**
 * LiveFeed is the streaming read. Its four members are the four things a screen
 * needs and nothing more: the snapshot it already has, the two ways to be told
 * about a new one, and the way to stop.
 *
 * `current()` is what makes a feed usable as a request/response operation: the
 * IPC surface has no server-pushed channel (Task 1.2 is `invoke` only), so
 * `live.watch` answers with the frame the console has, and a later task that
 * adds a pushed channel reuses these listeners rather than a second reader.
 */
export interface LiveFeed {
  readonly latest: Snapshot | null
  onChange(listener: (snapshot: Snapshot) => void): Unsubscribe
  onNotice(listener: (notice: Notice) => void): Unsubscribe
  onEnded(listener: (end: FeedEnd) => void): Unsubscribe
  current(): Promise<Snapshot>
  stop(): Promise<void>
}

/**
 * FugaroSource is the interface every screen talks to. The fourteen reads are the
 * CLI documents of design section 2.1 that the dashboard draws
 * (`docs/json-contract.md` section 2), one method each, named after the thing on
 * screen rather than after the CLI verb -- so a screen that says `listRuns` does
 * not change when the verb behind it does. (Section 2.1's picker row,
 * `fugaro config list --json`, is the picker window's read and reaches no screen
 * through this interface.)
 *
 * The arguments are the *validated* arguments of Task 1.2's schema, not a
 * renderer payload: a source is downstream of the validation, and an
 * implementation is entitled to assume that a value it is handed is one the
 * schema allows.
 */
export interface FugaroSource {
  /** `installation` is the resolved header: path, version, project, provenance. */
  readonly installation: InstallationRef
  version(): Promise<TextSnapshot>
  /**
   * subscribeLive starts the streaming read, and **a call with the same
   * arguments while a feed for them is live returns that same feed** rather
   * than a second one. The contract is here, on the interface, because it is
   * not an optimisation: Task 1.2's surface is `invoke`-only, so a screen
   * reaches `live.watch` once per poll, and an implementation that started a
   * stream per call would leave one child process per poll -- each of them
   * running `fugaro watch` against the operator's project. A feed that has
   * ended is not live: the next call is entitled to start a fresh one, which is
   * how a screen retries after a refusal.
   *
   * `close()` stops every feed this source started, live or not.
   */
  subscribeLive(args: ValidatedArguments): LiveFeed
  showBudget(args: ValidatedArguments): Promise<Snapshot>
  runReport(args: ValidatedArguments): Promise<Snapshot>
  listRuns(args: ValidatedArguments): Promise<Snapshot>
  /**
   * statsRuns is the by-person aggregate (`runs stats --json`, console L5b):
   * the Spend screen's by-person view. Its rows are the ones the *CLI* decided
   * the caller may see -- an operator's every user, a plain launcher's own row
   * alone -- because the operation's schema has no `--all-users` to send
   * (`src/main/ipc.ts`), and the screen draws nothing but the totals when the
   * document does not say who the caller is.
   */
  statsRuns(args: ValidatedArguments): Promise<Snapshot>
  /**
   * planStatus is the plan graph joined to the runs working on it
   * (`plan status --json`, GV2.1). It is the one read whose *directory* is part
   * of the question: the CLI resolves `docs/` from the process's working
   * directory, so the document answers about the installation's own checkout and
   * about nothing else. That directory is never an argument here -- a renderer
   * has no way to name one (`src/main/ipc.ts`), and an implementation must not
   * invent one -- which is why an installation with no checkout is refused
   * rather than read from wherever the console happens to be running.
   */
  planStatus(args: ValidatedArguments): Promise<Snapshot>
  /**
   * coordinatorStatus is the project view: every repository the installation's
   * local config lists, drawn from the view the coordinator's sweep last stored
   * (`coordinator status --from-store --all --json`, Bitbucket credentials
   * T1.2, D364).
   *
   * It is the one read here that asks nothing of the host: the stored view is
   * the caller's own database read (`roles/firebasedatabase.viewer`), so there
   * is no App key, no Bitbucket credential and no host call behind it. A
   * repository the coordinator has never reconciled answers the runs half with
   * `"stored": false` rather than refusing -- which is the state this console
   * draws as "the runs alone", and the reason the read is safe on an
   * installation whose coordinator was never turned on.
   *
   * Its two flags (`--from-store`, `--all`) are words of the spawn row and never
   * arguments: an implementation must not let a caller turn either off, since
   * the question is the *project's* whole repository set and a read without
   * `--from-store` would be a host call this console has no credential for.
   */
  coordinatorStatus(args: ValidatedArguments): Promise<Snapshot>
  diagnoseRun(args: ValidatedArguments): Promise<Snapshot>
  readRunLogs(args: ValidatedArguments): Promise<Snapshot>
  runLogLink(args: ValidatedArguments): Promise<TextSnapshot>
  listSecrets(args: ValidatedArguments): Promise<Snapshot>
  listAccounts(args: ValidatedArguments): Promise<Snapshot>
  environmentReport(args: ValidatedArguments): Promise<Snapshot>
  /** close stops every feed and every child this source started. */
  close(): Promise<void>
}

/**
 * SourceRefusal is how a source says no, and its whole design is its message:
 * **the CLI's own sentence, verbatim**. The CLI is the only thing in the system
 * that knows why a read failed -- a wrong project, an expired token, no checkout
 * -- and design section 6.3 says the screen shows that sentence rather than a
 * generic apology this console would have invented. Task 1.2's
 * `registerConsoleHandlers` passes a thrown message through untouched, so the
 * sentence a source throws here is the sentence on screen.
 */
export class SourceRefusal extends Error {
  /** `sentence` is the raw text, before any trimming the message does. */
  readonly sentence: string

  constructor(sentence: string) {
    super(sentence)
    this.name = 'SourceRefusal'
    this.sentence = sentence
  }
}

/**
 * refusalFrom turns a child's stderr into a `SourceRefusal`. The CLI prints
 * `fugaro: <sentence>` (`cmd/fugaro/main.go`), and the prefix is dropped so the
 * screen shows the sentence itself; a child that said nothing produces a message
 * that names the read, because "the operation failed" is what a console says when
 * it has nothing to say and the operator is owed better than that.
 */
export function refusalFrom(operation: string, sentence: string | null): SourceRefusal {
  const text = sentence === null || sentence.trim() === '' ? `${operation} failed` : sentence.trim()
  return new SourceRefusal(text)
}

/**
 * InstallationSurface is the rail's half of the surface, and it is one method:
 * `picker.ts`'s own dispatcher (`answerFor`), reached from the dashboard's
 * document instead of from the picker window's (owner's design ruling of
 * 2026-10-25).
 *
 * It answers in the picker's own reply shape -- `{ ok: true, data }` or
 * `{ ok: false, reason }` -- rather than the dashboard's, because it is the
 * same dispatcher the picker's channels call: a second shape here would be a
 * second implementation of every one of these six operations. `handlersFor`
 * below is where the two shapes meet, in one place.
 */
export interface InstallationSurface {
  answer(name: InstallationOperationName, args: InstallationArguments): Promise<unknown>
  /** open is the rail's `+` door: the window that adds an installation by
   * checkout path. It is the picker module's own `open`, which focuses the
   * window when it is already there rather than opening a second one. */
  open(): Promise<void>
}

/**
 * NO_INSTALLATION_SURFACE is what the rail's operations answer in a console
 * built without one (a unit test's, a `refused` console's). It is a state and
 * not a guess, the same way `NO_ACTIVE_INSTALLATION` is: nothing is wrong with
 * the machine, this process simply has no store behind the operation.
 */
export const NO_INSTALLATION_SURFACE = 'this console has no installation store'

/**
 * NO_PLAN_CHECKOUT is what `plan.status` answers for an installation that
 * records no checkout. The read is *about* a directory -- the CLI resolves
 * `docs/` from its working directory -- so with no checkout there is no
 * question to ask, and the console says which fact is missing instead of
 * running the child somewhere arbitrary. It is the console's own sentence
 * because the CLI is never asked: this is a state of the installation store,
 * not a refusal from a command.
 */
export const NO_PLAN_CHECKOUT = 'this installation records no checkout, and the plan read is about its docs/ directory'

/**
 * handlersFor answers the whole IPC surface from one source. It is a function
 * rather than a class because the surface is a table: one handler per operation,
 * each a one-line call.
 *
 * `openExternal` is the twelfth operation and the only one that is not a read of
 * the CLI: it takes the URL the *console* would open anyway and hands it to
 * `openExternalValidated` (`src/main/window.ts`) -- the same function the
 * allow-list's own test uses, imported rather than re-implemented, so the host
 * list cannot drift between the surface and the window.
 *
 * `live.watch` answers with the frame the feed already has: Task 1.2's surface is
 * `invoke`-only, so a live update reaches a screen through the feed's listeners
 * that a later task wires to a push channel. The handler calls `subscribeLive`
 * on every invocation -- one per poll -- so **the idempotency that keeps that
 * from being a child process per poll is the source's**, stated on
 * `FugaroSource.subscribeLive` and implemented in `src/source/cli.ts`, not
 * something this mapping can add by caching: a mapping-level cache would have to
 * key on the arguments it was handed, which is a second copy of the source's own
 * idea of what "the same subscription" is.
 *
 * The rail's six operations (`installation.*`, owner's design ruling of
 * 2026-10-25) are not reads of the CLI either, with one exception:
 * `installation.list` spawns `fugaro config list --json` and is the only one of
 * them that does. All six go through `deps.installations`, which is the picker
 * module's dispatcher, so the dashboard and the picker window answer from the
 * same code; the dep is **optional** for the reason `installation` is -- a
 * console with no store refuses them with a sentence rather than failing to
 * register.
 */
export function handlersFor(
  source: FugaroSource,
  deps: {
    readonly openExternal: (raw: unknown) => Promise<void>
    /** `installation` is the active installation as the store has it (Task
     * 3.4b). It is a *read* of the console's own state, so it is handed in the
     * same way `startup.compatibility` is -- and it is optional, because a
     * console built without a store (a unit test's, a `refused` console's)
     * answers D13's no-installation state rather than failing to register. */
    readonly installation?: () => unknown
    /** `installations` is the rail's surface (owner's design ruling of
     * 2026-10-25). Optional for the same reason `installation` is -- and a
     * *function* for the same reason too: the entry point builds this surface
     * before the store exists and reads it at message time. */
    readonly installations?: () => InstallationSurface
  },
): CliBackedHandlers {
  /**
   * rail turns one picker answer into the dashboard's reply convention: the
   * data on success, and an exception whose message is the picker's own
   * refusal sentence on failure -- which `registerConsoleHandlers` passes
   * through verbatim (design section 6.3), and which `dispatch` bounds and
   * flattens before a renderer sees it.
   */
  const rail = async (name: InstallationOperationName, args: InstallationArguments): Promise<unknown> => {
    const surface = deps.installations?.()
    if (surface === undefined) throw new Error(NO_INSTALLATION_SURFACE)
    const answer = (await surface.answer(name, args)) as
      | { readonly ok?: unknown; readonly reason?: unknown; readonly data?: unknown }
      | null
      | undefined
    if (answer === null || typeof answer !== 'object' || answer.ok !== true) {
      const reason = answer === null || typeof answer !== 'object' ? undefined : answer.reason
      throw new Error(typeof reason === 'string' && reason !== '' ? reason : `${name} failed`)
    }
    return answer.data
  }

  /** project is the one argument every rail row but `list` and `open` takes,
   * read as the string the schema validated it as. */
  const project = (args: ValidatedArguments): string =>
    typeof args['project'] === 'string' ? args['project'] : ''

  // `satisfies` rather than a cast: the mapped type makes a missing handler a
  // compile error, which is the whole reason `ConsoleHandlers` is mapped. A cast
  // here would turn "one operation answers nothing" back into a runtime state.
  return {
    'cli.version': async () => (await source.version()).text,
    'live.watch': async (args) => {
      const feed = source.subscribeLive(args)
      const snapshot = await feed.current()
      return snapshot
    },
    'budget.show': (args) => source.showBudget(args),
    'spend.report': (args) => source.runReport(args),
    'runs.list': (args) => source.listRuns(args),
    'runs.stats': (args) => source.statsRuns(args),
    'plan.status': (args) => source.planStatus(args),
    'coordinator.status': (args) => source.coordinatorStatus(args),
    'run.diagnose': (args) => source.diagnoseRun(args),
    'run.logs': (args) => source.readRunLogs(args),
    'run.logLink': async (args) => (await source.runLogLink(args)).text,
    'secrets.inventory': (args) => source.listSecrets(args),
    'accounts.inventory': (args) => source.listAccounts(args),
    'environment.report': (args) => source.environmentReport(args),
    'link.openExternal': async (args) => {
      await deps.openExternal(args['url'])
      return null
    },
    // The fifteenth operation is not a read of the CLI either: it is the
    // installation the operator chose, as the store holds it. With no store --
    // and with no installation chosen -- it answers D13's no-installation
    // state, which is a state and not a guess.
    'installation.current': () => deps.installation?.() ?? { chosen: false, sentence: NO_ACTIVE_INSTALLATION },
    // The rail's own surface: the list every row is drawn from, and the five
    // things a row or its issue view can do. `checkout`'s directory is omitted
    // from the payload rather than sent as `undefined`, which is the schema's
    // own way of saying "no checkout" (design section 3.4 rule 5).
    'installation.list': () => rail('list', {}),
    'installation.choose': (args) => rail('choose', { project: project(args) }),
    'installation.checkout': (args) => {
      const checkout = args['checkout']
      return rail(
        'checkout',
        typeof checkout === 'string' ? { project: project(args), checkout } : { project: project(args) },
      )
    },
    'installation.remove': (args) => rail('remove', { project: project(args) }),
    'installation.reveal': (args) => rail('reveal', { project: project(args) }),
    // The one rail operation that is not a row's action but the rail's foot:
    // the window the `+` opens. It goes to `open` rather than through `answer`
    // because the picker's own table has no `open` row -- opening the window is
    // not something the window's document may ask for.
    'installation.open': async () => {
      const surface = deps.installations?.()
      if (surface === undefined) throw new Error(NO_INSTALLATION_SURFACE)
      await surface.open()
      return null
    },
  } satisfies CliBackedHandlers
}

/**
 * CliBackedHandlers is the whole surface **minus the three operations that are
 * not reads of the CLI**: `startup.compatibility` (desktop-console plan Task
 * 3.3), whose answer is the handshake `src/main/index.ts` ran before it built
 * any window, `run.launch` (V2.5.1a, design section 5.1 rule 11), which is the
 * console's **one write**: it is not a read of the active installation's source
 * at all -- it takes the installation's name as an argument, resolves its own
 * records, and goes through the confirmation seam (`src/main/launch.ts`) rather
 * than a `CliSource` -- and `link.openPullRequest` (K2a, D815), which reads no
 * CLI either: it builds a URL from three of its own arguments and hands it to
 * the console's one call to `shell.openExternal`. All three are joined in at the
 * entry point, as
 * `{ ...handlersFor(source, deps), 'startup.compatibility': … , 'run.launch': … ,
 * 'link.openPullRequest': … }` with `startupCompatibility` and
 * `openPullRequestHandler` from `src/main/ipc.ts` and the launcher from
 * `src/main/launch.ts`. **Nothing registers the handlers until that task lands**:
 * today `main()` serves the bundle and starts the lifecycle, and no window has a
 * preload to reach a channel with.
 *
 * The `Omit` is still a *closed* type: a key typed here that is not an operation
 * is an excess property, and an operation deleted from `CONSOLE_OPERATIONS`
 * takes its required key with it -- so the exhaustiveness `satisfies` gives the
 * other rows is unchanged. `registerConsoleHandlers` still demands the
 * whole surface, so a console that forgets the join fails to register rather
 * than answering one operation with nothing.
 */
export type CliBackedHandlers = Omit<
  ConsoleHandlers,
  'startup.compatibility' | 'run.launch' | 'link.openPullRequest'
>
