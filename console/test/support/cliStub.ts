// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The stub `fugaro` an end-to-end spec hands the real console (desktop-console
// plan Task 3.3).
//
// Task 3.3 wires the version handshake into startup, so **the console's first
// act is to resolve and read a `fugaro`** (design section 3.4 rule 4, D7). That
// makes every e2e spec that launches the app depend on which CLI the machine
// has: with none installed the app now opens the refusal document, and a spec
// that mounts a screen into the dashboard document has nothing to mount into.
//
// There is no `FUGARO_BIN`, and this task does not add one -- an environment
// variable that chooses the binary the console runs is its own SECURITY-SENSITIVE
// change. So a spec chooses the CLI the only way the resolution allows: it puts
// a binary where the resolution looks. `src/main/spawn.ts`'s `binDirectories`
// walks the configured path (the store's own optional override, which
// `seedActiveInstallation` writes) and then the platform's well-known
// directories (`src/main/spawn.ts`'s `locationsFor`, and which ones those are
// depends on `process.platform`). It does
// **not** walk the app's inherited `PATH` -- that is rule 4's whole point, the
// owner's ruling of 2026-10-20 -- so the app's `PATH` below is a decoy on
// purpose: a resolution that searched it would find the stub there and a spec
// asserting the *machine's* answer would instead be asserting the environment's.
// This module therefore does two things:
//
//   - copies `test/fixtures/bin/fugaro` into a scratch directory and returns the
//     environment that makes it the winner -- `PATH` is that directory and
//     nothing else (so the decoy is the same stub, and a spec that wanted to
//     prove the `PATH` is not searched installs a *different* one there), and
//     `HOME` is a scratch home whose `~/.local/bin` holds the same stub, because
//     `~/.local/bin` is one of the well-known prefixes and the operator's real
//     one would otherwise be searched first;
// Since Task 3.4c the stub is also the CLI a *dashboard* reads: it answers
// `watch --json --project P` with the committed synthetic recording and
// `doctor --json` with a minimal document, and refuses anything else -- and any
// read that does not carry the seeded project -- with exit 2. That is why this
// module writes five files beside the stub and not one: `version.txt` (the
// handshake), `project.txt` (the project every read must name, rewritten by
// `seedInstallations`), `frames.ndjson` (the recording `watch` prints, rendered
// from `test/fixtures/synthetic.ndjson` through `loadFixture`, so the stub can
// only replay a fixture this build vouches for), `plan-status.json` (the
// document `plan status --json` prints, the Graph view's own read, GV2.3b) and
// `coordinator-status.json` (the document `coordinator status
// --from-store --all --json` prints, the project view's own read, T1.2).
//
//   - offers `resolveConsoleBinary(env)`, which runs the console's own
//     `resolveBinary` with **exactly the arguments `src/main/index.ts` uses**, so
//     a spec can ask the same question the app is about to ask and find out
//     which binary will actually answer. That is what makes the specs honest on
//     a machine with a real `fugaro` in `/usr/local/bin`: a well-known location
//     is searched by the rule itself, so a spec that assumed its scratch stub
//     always won would be asserting a fact about the machine rather than about
//     the console.
//
// **What this cannot fix, stated plainly.** On a machine whose `/usr/local/bin`
// or `/opt/homebrew/bin` holds a real `fugaro` outside the supported range, the
// app will refuse it and the specs that need a dashboard document will fail --
// because the app is doing exactly what D7 says. CI has no such binary, so the
// stub wins there; a developer who hits this is being told something true about
// their machine, and `resolveConsoleBinary` below is what lets a spec say so
// instead of failing on a missing element.

import { chmodSync, copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { INSTALLATIONS_FILE_NAME, INSTALLATION_FILE_VERSION, serializeInstallationFile } from '../../src/main/installations'
import { privateWorkingDirectory, resolveBinary } from '../../src/main/spawn'
import type { BinaryResolution, SpawnTarget } from '../../src/main/spawn'
import { loadFixture } from '../../src/source/fixture'
import { CLI_VERSION_BUILT_AGAINST } from '../../src/shared/version'

/** CLI_STUB is the fixture this module installs: a POSIX script that prints the
 * `version.txt` beside it, answers `doctor --json` and `watch --json` for the
 * seeded project, and refuses everything else with exit 2. */
const CLI_STUB = join(__dirname, '..', 'fixtures', 'bin', 'fugaro')

/** SYNTHETIC_FIXTURE is the committed recording the stub's `watch` replays
 * (Task 3.4c, step 3). It is the same file `live.spec.ts` and `degraded.spec.ts`
 * read, so the Live screen the e2e sees is the one the unit suite's fixture
 * source feeds. */
const SYNTHETIC_FIXTURE = join(__dirname, '..', 'fixtures', 'synthetic.ndjson')

/** PLAN_FIXTURE is the committed `plan status --json` document the stub's `plan
 * status` answers with (plan GV2.1; the Graph view reads it, GV2.3b). It is the
 * document `graph.test.ts` draws its screen from, so the graph an e2e window
 * shows is the graph the unit suite's cases are written against. */
const PLAN_FIXTURE = join(__dirname, '..', 'fixtures', 'plan-status.json')

/** COORDINATOR_FIXTURE is the committed `coordinator status --from-store --all
 * --json` document the stub's `coordinator status` answers with (Bitbucket
 * credentials T1.2, D364; the Coordinator view reads it). It is invented rather
 * than recorded, like the plan document and for the same reason: the two states
 * the screen turns on -- a repository the sweep never reconciled, and a stale
 * marker -- are not in any recording this tree carries, and a document is the
 * only way a spec can hold either. */
const COORDINATOR_FIXTURE = join(__dirname, '..', 'fixtures', 'coordinator-status.json')

/** DEFAULT_PROJECT is the project a stub answers for until a spec seeds one. */
const DEFAULT_PROJECT = 'e2e'

/**
 * syntheticFrames renders the committed fixture as `watch --json` writes it: one
 * compact document per line, and a notice as its own line of text (which is what
 * the CLI's degraded mode writes to the same stream, and what
 * `src/main/spawn.ts`'s `decodeFeedLine` reads as a notice rather than a parse
 * error). The fixture's own header line and the records' arrival stamps are the
 * *recording's* wrapper, not the stream's shape, so neither reaches the stub.
 *
 * It is loaded through `loadFixture` rather than `JSON.parse`d here: the loader
 * is what verifies the digest, the redactor and the audit, so a fixture the
 * console would refuse cannot be replayed by the stub as if it were fine.
 */
function syntheticFrames(): string {
  const text = readFileSync(SYNTHETIC_FIXTURE, 'utf8')
  const { records } = loadFixture(text, 'test/fixtures/synthetic.ndjson')
  const lines = records.map((record) =>
    'document' in record ? JSON.stringify(record.document) : record.notice,
  )
  return `${lines.join('\n')}\n`
}

/** HANDSHAKE_TARGET is the target the startup read runs with, copied from
 * `src/main/index.ts`: no project and no configured path, so the resolution is
 * the well-known prefixes and nothing else -- never the app's `PATH` (rule 4). */
const HANDSHAKE_TARGET: SpawnTarget = Object.freeze({ project: '' })

/** SeededCheckout is one `checkouts[]` row of the installations document, as
 * `internal/cli/config_list.go` marshals it (`console/docs/json-contract.md`
 * §6.8). The launch reads the identity and the recorded branch from it -- the
 * repository a launch may be cut for is exactly what this list names.
 *
 * The row is written the way a real document carries one, which is what makes
 * it worth seeding: **every** row has an absolute `path` -- it is the recorded
 * directory, kept whether or not the path still passes re-validation -- and a
 * row that did not pass is the only one with a `reason`, which is a code from
 * the CLI's closed set and never a sentence. */
export interface SeededCheckout {
  /** the CLI's own `<provider>/<owner>/<name>` identity, verbatim */
  readonly repository: string
  /** the branch this repository's runs are cut from; `''` is the key there and
   * recording none, which is the state the launch refuses */
  readonly baseBranch: string
  /** the absolute directory the CLI recorded this repository at, printed as it
   * holds it: a row that is no longer a checkout still names the path */
  readonly path: string
  /** whether the path passed the CLI's own re-validation on the read; a row
   * that did not carries `reason: 'missing'` (the directory is not there any
   * more), which is one of the codes `config_list.go` reports */
  readonly valid?: boolean
}

/** SeededLaunchRecords is the launch's own half of the installations document:
 * the installation's recorded base branch and the repositories its checkouts
 * name. Both are optional, and **each key is written only when a spec seeds it**
 * -- an omitted key is omitted from the document, which is the older CLI §6.8
 * keeps apart from a key that is there and records `''` (a branch none is
 * recorded for) or `[]` (no path is remembered, which is not the same fact as a
 * CLI that does not know what a checkout is). */
export interface SeededLaunchRecords {
  readonly baseBranch?: string
  readonly checkouts?: readonly SeededCheckout[]
}

/** SeededSpendRead is one seeded Spend read: the document the stub answers with
 * and how long it takes to answer. */
export interface SeededSpendRead {
  readonly document: unknown
  /** the seconds the stub sleeps before it prints the document. Absent is
   * immediate, which is what a spec that is not about timing wants. */
  readonly delaySeconds?: number
}

/** SeededSpend is the Spend view's two reads, as a spec seeds them. Both keys
 * are optional, and a key left out is a read the stub refuses rather than one it
 * answers with an empty document -- see `CliStub.seedSpend`. */
export interface SeededSpend {
  /** one report document per `--since` the screen may ask for */
  readonly reports?: Readonly<Record<string, SeededSpendRead>>
  /** the caps document, which is one for the installation and not per window */
  readonly budget?: SeededSpendRead
}

/** CliStub is an installed stub, and the environment that makes the console
 * find it. */
export interface CliStub {
  /** the directory the stub lives in, which is the whole of the app's `PATH`. */
  readonly dir: string
  /** the scratch home: the app's `HOME`, with `~/.local/bin` holding the stub. */
  readonly home: string
  /** `process.env` with `PATH` and `HOME` replaced, for `electron.launch`.
   * Every value is a string: an environment key the process does not have is
   * absent rather than `undefined`, which is both what a child process actually
   * receives and what Playwright's `env` option takes. */
  readonly env: Record<string, string>
  /** userData is the seed for every launch's scratch `--user-data-dir` (Task
   * 3.4b): `seedInstallations` writes `installations.json` here and
   * `launchArgs` copies it into a fresh profile, so no spec reads or writes the
   * machine's own console profile. Unseeded, it is D13's first run: no
   * installation, and the picker opens. */
  readonly userData: string
  /** launchArgs is `electron.launch`'s `args`: the app directory, and a
   * **fresh** scratch `--user-data-dir` per launch, holding a copy of the seeded
   * `installations.json` (if any). Fresh, because Chromium locks a profile to
   * one process: a launch that reused the profile of an app still shutting down
   * would hand itself to that process and exit, and the spec would wait for a
   * window that never comes. */
  launchArgs(consoleDir: string): string[]
  /** seedActiveInstallation writes one installation naming this stub as the
   * active one, so the handshake's configured path is the stub (rule 4 puts it
   * first, so it wins even over a machine's own `fugaro`) and the picker does
   * not open. It is for the specs whose subject is the dashboard, not the first
   * run. The stub's `project.txt` is rewritten with it, so the reads the
   * dashboard makes carry the project the console was pointed at.
   *
   * `binaryPath: null` is the shape **this console writes** (owner's ruling of
   * 2026-10-20): the record carries an id and a project and no override at all,
   * so rule 4 resolves the stub through the well-known prefixes instead. The
   * default is the older shape, an override naming the stub, because it pins the
   * resolution to this scratch tree whatever the machine has in `/usr/local/bin`
   * -- a spec whose subject is not the resolution should not depend on it. */
  seedActiveInstallation(project?: string, binaryPath?: string | null): void
  /** seedActiveCheckout gives the active installation a checkout: it makes a
   * directory in this scratch tree and records it, and answers its path. The
   * reads that are *about* a directory need one -- `plan.status` resolves
   * `docs/` from the CLI's working directory, which is the checkout, so an
   * installation that records none is refused before any child is spawned
   * (`NO_PLAN_CHECKOUT`) and the Graph draws that refusal instead of a graph.
   * A directory and not a repository: the stub answers its reads from the files
   * beside itself wherever it runs, so what the checkout has to be is a
   * directory that exists. */
  seedActiveCheckout(): string
  /** seedInstallations writes `installations.json` with these entries and
   * `active` (Task 3.4b), for a spec that needs more than one installation. The
   * active entry's project becomes the one the stub answers for. An entry with
   * no `binaryPath` is written without the field, which is the shape this
   * console writes. */
  seedInstallations(
    entries: readonly {
      readonly id: string
      readonly project: string
      readonly binaryPath?: string | null
      readonly checkout?: string
    }[],
    active: string | null,
  ): void
  /** extraStub installs one more stub, printing `line`, in its own directory
   * under this scratch tree -- **not** on the app's `PATH` -- and answers its
   * path. It is a second CLI an installation can name. It carries the same
   * `project.txt` and frames as the installed one. */
  extraStub(name: string, line: string): string
  /** seedInstallationsList writes the installations document the picker draws
   * (`config list --json`, contract section 6.8): one row per name, with the GCP
   * project and region made up from the name, and -- for a name prefixed
   * `unreadable:` -- a row whose config could not be read, with its fields empty
   * and the CLI's own warning line, which the picker must show rather than hide.
   * Unseeded, the file is absent and the stub answers with the **empty
   * document** (`{"installations":[],"warnings":[]}`, `test/fixtures/bin/fugaro`),
   * which is the document with no installations at all: the machine has none, and
   * it is the state the "set up your first installation" screen is drawn for.
   * (Not "no output": a CLI that printed nothing is text the console refuses, and
   * the picker draws its warning line instead of that screen.) `warnings` are
   * further lines the CLI prints after its own, verbatim.
   *
   * `records` is the launch's own half of the document, and each of its two keys
   * is written only when a spec seeds it, so every document a spec already
   * seeded is byte for byte the document it was *and* an absent key keeps
   * meaning "this CLI does not report it". The launch reads an installation's
   * `base_branch` and its `checkouts[]` repositories, and §6.8's states of the
   * two keys are different facts: `baseBranch` left out writes no key at all --
   * an older CLI, which does not report base branches, and a launch refused for
   * that reason -- `baseBranch: ''` writes a key that records none, and a string
   * writes it; `checkouts` left out is the same distinction for the paths, and
   * `checkouts: []` is written as an empty list, which is "none are
   * remembered". Each row is `{repository, path, base_branch, enrolled, valid,
   * reason}` as the CLI prints it, `reason` only on the rows that failed the
   * read: a `valid` row is one of the directories this installation is used
   * from, and any row naming a `repository` is one a launch may be cut for. */
  seedInstallationsList(
    projects: readonly string[],
    warnings?: readonly string[],
    records?: SeededLaunchRecords,
  ): void
  /** seedPlan replaces the `plan status --json` document the stub answers with,
   * in both places the stub was installed. It is the committed fixture until a
   * spec asks for another one, and it is the one document a spec may want to
   * change: a **chip** exists only for a finished release, and the committed
   * document has none (the unit suite patches the same flag). */
  seedPlan(document: unknown): void
  /** seedSpend seeds the Spend view's two reads (console 0.116.1): the report
   * section's document per window, and the caps section's one, each with the
   * seconds the stub waits before it prints.
   *
   * **The delay is the point of the seeder.** The fix's claim is that a control
   * change is answered in the frame it happened in and that the view of the
   * *last* change is the one that stays, whatever order the answers arrive in --
   * and that is only observable if one read can be made to answer after another.
   * `reports` is keyed by the `--since` the picker sends (`7d`, `30d`, `90d`,
   * `12w`, or a UTC day): the stub answers the document seeded under the window
   * the console asked for and refuses every window it has no document for, so a
   * spec that seeds two windows says which two the screen may ask for.
   *
   * Unseeded, no document exists beside the stub and both reads are refused with
   * exit 2, which is the state `test/e2e/no-checkout.spec.ts` relies on: the
   * specs that are not about Spend must keep seeing a refused report. */
  seedSpend(spend: SeededSpend): void
  /** answeredReads is the reads the stub has answered, in the order it printed
   * them: `report 7d`, `budget`, one line per answer, from `reads.log` beside
   * the stub. It is what a spec waits on for an answer that draws **nothing** --
   * the report read the console supersedes and drops -- because the screen has no
   * signal for a thing that must not happen, and a sleep long enough for the
   * slowest machine is a sleep that hides the bug. Empty until the stub has
   * answered a Spend read. */
  answeredReads(): readonly string[]
  /** setVersion rewrites the line the stub prints (`'0.8.1\n'`, `'banana\n'`, a
   * hostile line), in both places the stub was installed. */
  setVersion(line: string): void
  /** remove deletes the scratch tree the stub lives in. */
  remove(): void
}

/**
 * installCliStub puts the stub in a fresh scratch directory, printing `line`
 * (newline appended), and answers with it and the environment to launch the app
 * with.
 */
export function installCliStub(line: string): CliStub {
  const scratch = mkdtempSync(join(tmpdir(), 'fugaro-cli-stub-'))
  const dir = join(scratch, 'bin')
  const home = join(scratch, 'home')
  const userData = join(scratch, 'user-data')
  mkdirSync(userData, { recursive: true, mode: 0o700 })
  const wellKnown = join(home, '.local', 'bin')
  // The project the stub answers for, and the two files beside it that make that
  // answer possible: `project.txt` (what `--project` must say, Task 3.4c's
  // refusal) and `frames.ndjson` (the committed synthetic recording `watch`
  // prints). Both live beside every copy of the stub, because either copy can be
  // the one the resolution picks.
  let project = DEFAULT_PROJECT
  let installationsList: string | null = null
  const frames = syntheticFrames()
  let plan = readFileSync(PLAN_FIXTURE, 'utf8')
  // The project view's document, read once and written beside every copy of the
  // stub: no spec seeds another one, because the states the Coordinator's cases
  // are about are the ones the committed document already carries.
  const coordinator = readFileSync(COORDINATOR_FIXTURE, 'utf8')
  // The last seeded file, kept so `seedActiveCheckout` can add a directory to
  // the active entry without a spec having to name the entry again.
  let seeded: {
    readonly entries: readonly {
      readonly id: string
      readonly project: string
      readonly binaryPath?: string | null
      readonly checkout?: string
    }[]
    readonly active: string | null
  } = { entries: [], active: null }
  const writeSupport = (): void => {
    for (const where of [dir, wellKnown]) {
      writeFileSync(join(where, 'project.txt'), `${project}\n`)
      writeFileSync(join(where, 'frames.ndjson'), frames)
      writeFileSync(join(where, 'plan-status.json'), plan)
      writeFileSync(join(where, 'coordinator-status.json'), coordinator)
      if (installationsList !== null) writeFileSync(join(where, 'installations.ndjson'), installationsList)
    }
  }
  for (const where of [dir, wellKnown]) {
    mkdirSync(where, { recursive: true })
    copyFileSync(CLI_STUB, join(where, 'fugaro'))
    // The fixture is committed executable; this is for a checkout that lost the
    // bit (a zip download, a Windows-authored copy), because `isExecutableFile`
    // is what decides whether the resolution will look at it at all.
    chmodSync(join(where, 'fugaro'), 0o755)
  }
  writeSupport()
  // The app's own environment, with the two keys that decide the resolution
  // replaced and the keys that are not set left out -- `DISPLAY` and the rest
  // are kept, so a Linux run under `xvfb-run` still has a display.
  const env: Record<string, string> = {}
  for (const [key, value] of Object.entries(process.env)) {
    if (typeof value === 'string') env[key] = value
  }
  env['PATH'] = dir
  env['HOME'] = home
  const stub: CliStub = {
    dir,
    home,
    userData,
    env,
    launchArgs(consoleDir: string): string[] {
      const profile = mkdtempSync(join(scratch, 'profile-'))
      chmodSync(profile, 0o700)
      const seededFile = join(userData, INSTALLATIONS_FILE_NAME)
      if (existsSync(seededFile)) {
        copyFileSync(seededFile, join(profile, INSTALLATIONS_FILE_NAME))
        chmodSync(join(profile, INSTALLATIONS_FILE_NAME), 0o600)
      }
      return [consoleDir, `--user-data-dir=${profile}`]
    },
    seedActiveInstallation(nextProject = DEFAULT_PROJECT, binaryPath: string | null = join(dir, 'fugaro')): void {
      const id = '0123456789abcdef'
      stub.seedInstallations([{ id, project: nextProject, ...(binaryPath === null ? {} : { binaryPath }) }], id)
    },
    seedInstallations(entries, active): void {
      // The stub answers for the **active** installation's project (Task 3.4c,
      // step 3): every read carries `--project P`, and a `P` that is not the one
      // the console was pointed at is refused with exit 2 rather than answered
      // with another project's document. With nothing active there is no read to
      // make, so the first entry's project -- or the default -- is written.
      const chosen = entries.find((entry) => entry.id === active) ?? entries[0]
      project = chosen === undefined ? DEFAULT_PROJECT : chosen.project
      seeded = { entries: [...entries], active }
      writeSupport()
      writeFileSync(
        join(userData, INSTALLATIONS_FILE_NAME),
        serializeInstallationFile({
          version: INSTALLATION_FILE_VERSION as 1,
          active,
          installations: entries.map((entry) => ({
            id: entry.id,
            project: entry.project,
            // Absent, not `null` or `''`: `recordOf` in the app writes a record
            // with no override at all, and `JSON.stringify` drops the key.
            ...(entry.binaryPath === undefined || entry.binaryPath === null
              ? {}
              : { binaryPath: entry.binaryPath }),
            ...(entry.checkout === undefined ? {} : { checkout: entry.checkout }),
          })),
        }),
        { mode: 0o600 },
      )
    },
    seedActiveCheckout(): string {
      const where = join(scratch, 'checkout')
      mkdirSync(where, { recursive: true })
      if (seeded.active === null || !seeded.entries.some((entry) => entry.id === seeded.active)) {
        // A checkout belongs to an installation, so this call has an order. It
        // says so rather than writing a file with no entry to hang it on, which
        // would leave the spec waiting for a screen that is drawn for a state
        // nobody asked for.
        throw new Error('seedActiveCheckout needs an active installation: call seedActiveInstallation first')
      }
      // Written back through `seedInstallations`, so the file keeps the one
      // shape this module writes and the project the stub answers for is not
      // disturbed by adding a directory to the record.
      stub.seedInstallations(
        seeded.entries.map((entry) => (entry.id === seeded.active ? { ...entry, checkout: where } : entry)),
        seeded.active,
      )
      return where
    },
    extraStub(name: string, next: string): string {
      const where = join(scratch, name)
      mkdirSync(where, { recursive: true })
      copyFileSync(CLI_STUB, join(where, 'fugaro'))
      chmodSync(join(where, 'fugaro'), 0o755)
      writeFileSync(join(where, 'version.txt'), `${next}\n`)
      // The same support files the installed stub has, so a spec that makes this
      // one active gets the same answers rather than a `cat` of a missing file.
      writeFileSync(join(where, 'project.txt'), `${project}\n`)
      writeFileSync(join(where, 'frames.ndjson'), frames)
      writeFileSync(join(where, 'plan-status.json'), plan)
      writeFileSync(join(where, 'coordinator-status.json'), coordinator)
      return join(where, 'fugaro')
    },
    seedInstallationsList(
      projects: readonly string[],
      extra: readonly string[] = [],
      records?: SeededLaunchRecords,
    ): void {
      // The document, as `internal/cli/config_list.go` marshals it: the
      // installations in the CLI's order, and the warning lines beside them. A
      // name prefixed `unreadable:` is the state that matters most here -- the
      // CLI lists the installation with its fields empty *and* says why, and a
      // picker that hid the row would be hiding an installation the machine
      // has.
      //
      // The launch's two keys are added only when a spec seeded them: with them
      // left out, every document a spec already wrote stays the document it was,
      // and the absent `base_branch` keeps meaning what §6.8 makes it mean.
      const launch = records ?? {}
      const checkouts = (launch.checkouts ?? []).map((row) =>
        Object.freeze({
          repository: row.repository,
          path: row.path,
          base_branch: row.baseBranch,
          enrolled: true,
          valid: row.valid ?? true,
          // `reason` is `omitempty` in `config_list.go` and the CLI writes it only
          // for a row its own re-validation refused; `missing` is the code for a
          // directory that is not there any more.
          ...(row.valid === false ? { reason: 'missing' } : {}),
        }),
      )
      const installations = projects.map((name) =>
        name.startsWith('unreadable:')
          ? { name: name.slice('unreadable:'.length), gcp_project: '', region: '', shared_merged: false }
          : {
              name,
              gcp_project: `fugaro-${name}-123456`,
              region: 'europe-west4',
              shared_merged: true,
              ...(launch.baseBranch === undefined ? {} : { base_branch: launch.baseBranch }),
              ...(launch.checkouts === undefined ? {} : { checkouts }),
            },
      )
      const warnings = [
        ...projects
          .filter((name) => name.startsWith('unreadable:'))
          .map((name) => `/Users/owner/.config/fugaro/projects/${name.slice('unreadable:'.length)}.yaml could not be read: permission denied`),
        ...extra,
      ]
      installationsList = `${JSON.stringify({ installations, warnings })}\n`
      writeSupport()
    },
    seedPlan(document: unknown): void {
      plan = `${JSON.stringify(document)}\n`
      writeSupport()
    },
    seedSpend(spend: SeededSpend): void {
      // Beside **both** copies of the stub, like every other document: either
      // copy can be the one the resolution picks, and a spec must not have to
      // know which. The documents are one JSON object per file and the delays
      // one number per file, because that is what the stub reads them as.
      for (const where of [dir, wellKnown]) {
        for (const [since, read] of Object.entries(spend.reports ?? {})) {
          writeFileSync(join(where, `spend-report-${since}.json`), `${JSON.stringify(read.document)}\n`)
          if (read.delaySeconds !== undefined) {
            writeFileSync(join(where, `spend-report-${since}-delay.txt`), `${read.delaySeconds}\n`)
          }
        }
        if (spend.budget === undefined) continue
        writeFileSync(join(where, 'spend-budget.json'), `${JSON.stringify(spend.budget.document)}\n`)
        if (spend.budget.delaySeconds !== undefined) {
          writeFileSync(join(where, 'spend-budget-delay.txt'), `${spend.budget.delaySeconds}\n`)
        }
      }
    },
    answeredReads(): readonly string[] {
      // The console runs whichever copy its own resolution finds, and the stub
      // writes its log beside itself -- so both places it was installed are
      // read. Neither having one is a run that has answered nothing, not an
      // error.
      for (const where of [wellKnown, dir]) {
        const log = join(where, 'reads.log')
        if (existsSync(log)) return readFileSync(log, 'utf8').split('\n').filter((line) => line !== '')
      }
      return []
    },
    setVersion(next: string): void {
      for (const where of [dir, wellKnown]) {
        writeFileSync(join(where, 'version.txt'), `${next}\n`)
      }
    },
    remove(): void {
      rmSync(scratch, { recursive: true, force: true })
    },
  }
  stub.setVersion(line)
  return stub
}

/**
 * inRangeCliStub is the stub the specs that mount a screen install: a `fugaro`
 * printing a version the console supports, so the handshake reaches `ok` and the
 * window the app opens is the **dashboard document**. Every spec that draws into
 * that document needs this and only this -- with nothing resolvable, or with a
 * version outside the range, the app opens the refusal document instead and
 * there is no dashboard to mount into, which is the whole point of the wiring
 * this stub exists to satisfy.
 */
export function inRangeCliStub(): CliStub {
  return installCliStub(CLI_VERSION_BUILT_AGAINST)
}

/**
 * resolveConsoleBinary asks the question the console is about to ask, with the
 * console's own resolver and the console's own arguments: which `fugaro` will
 * answer this launch, and with what version line. A spec calls it with the
 * environment it passes to `electron.launch` and gets the binary the app will
 * actually find -- the stub when nothing well-known is installed, and the
 * machine's own binary when there is one.
 *
 * **`HOME` comes out of `env`**, because that is where the app reads `~` from:
 * the main process calls the resolver with its own environment, so
 * `~/.local/bin` is *the stub's* scratch home for a launch. Letting
 * `homedir()` answer here would be the spec runner's home instead, and this
 * function would then name a binary the app never looks at -- the opposite of
 * what it exists for.
 */
export async function resolveConsoleBinary(env: NodeJS.ProcessEnv): Promise<BinaryResolution> {
  const home = env['HOME']
  return resolveBinary({
    appEnv: env,
    directory: privateWorkingDirectory(),
    target: HANDSHAKE_TARGET,
    ...(home === undefined || home === '' ? {} : { home }),
  })
}
