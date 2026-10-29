// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The fake `fugaro` the screenshot run gives the built console, and the
// installation store it starts it with (owner ruling 2026-10-20, step 2).
//
// **Nothing here is the real CLI and nothing here can reach one.** The console
// under the camera is the *built* application, launched exactly as the e2e
// suite launches it, so it runs its real startup handshake, its real resolver
// and its real `watch` supervisor -- and every answer it gets comes from the
// fake child this file installs, in its own temporary directory, from the
// documents in `test/fixtures/screenshots/`. §1 of `isolation.ts` is why the
// fake always wins the resolution; this file is why it is the *only* thing that
// can answer.
//
// The fake is the unit suite's own (`test/unit/stub.ts`), installed here rather
// than a second one written for this tool: it answers a command by name from a
// scenario document, refuses anything its command table does not name (so a
// console that asks for a verb this run did not prepare fails loudly rather than
// being answered with an empty document), and writes a report of every spawn --
// the child's own `argv`, `PATH` and environment, which is the evidence this run
// prints at the end.
//
// **The documents are read, not re-typed.** `live.json` is the frame list
// `watch` prints (one compacted document per line, which is what the feed
// reads), `budget.json`/`report.json`/`diagnose.json` are the three one-document
// answers, and `logs.ndjson`/`log-url.txt` are the two log reads. The store this
// file writes is the console's *own* file shape, built through the console's own
// `serializeInstallationFile` rather than by a template here -- a hand-written
// file would be a second spelling of a format the console already has, and the
// first thing it would get wrong is the version.

import { chmodSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

import {
  INSTALLATIONS_FILE_NAME,
  INSTALLATION_FILE_VERSION,
  serializeInstallationFile,
} from '../../src/main/installations'
import { CLI_VERSION_BUILT_AGAINST } from '../../src/shared/version'
import { installStub } from '../../test/unit/stub'
import type { Stub, StubReport, StubScenario } from '../../test/unit/stub'
import type { Isolation } from './isolation'

/** FIXTURES_RELATIVE is the fixture directory, from the console's root: the
 * documents are data of the *test* suite's, next to the recordings and the stub
 * the e2e specs read, and not a private corner of this tool. */
export const FIXTURES_RELATIVE: readonly string[] = Object.freeze(['test', 'fixtures', 'screenshots'])

/** SEED_FILE_NAME is the three installations the pictures show: an id and a
 * project each, and **no binary path** -- the path is the fake this run installs
 * in its scratch tree, which is a different path on every run and is therefore
 * the one field the run supplies rather than the fixture. */
export const SEED_FILE_NAME = 'installations.seed.json'

/** FIXTURE_COMMANDS is every command the fake answers, and the file each answer
 * comes from. It is written out so the run's own log can name what it prepared:
 * a screenshot of a screen whose read was refused is a picture of a refusal, and
 * the reader of that picture should be able to see which document was missing
 * rather than guess. */
export const FIXTURE_COMMANDS = Object.freeze({
  watch: 'live.json',
  'budget show': 'budget.json',
  report: 'report.json',
  diagnose: 'diagnose.json',
  logs: 'logs.ndjson',
  'logs --url': 'log-url.txt',
} as const)

/** INSTALLATION_ID_RE and PROJECT_RE mirror the console's own store rules (16
 * hex characters, a project name with no whitespace), because this file writes
 * a file the console reads: a fixture that violated them would start the console
 * with a refused store and a picker that says so, and the run would take
 * pictures of that instead. The check is here rather than in a schema module
 * because the fixture is this tool's input, and a fixture typo should fail
 * before a window exists. */
const INSTALLATION_ID_RE = /^[0-9a-f]{16}$/
const PROJECT_RE = /^[A-Za-z0-9._/-]+$/

/** SeedInstallation is one row of the seed file. */
export interface SeedInstallation {
  readonly id: string
  readonly project: string
}

/** ScreenshotFixture is the fake CLI, the fixture directory, and the two
 * per-launch writes this run makes through it. */
export interface ScreenshotFixture {
  /** binary is the fake's absolute path: the path every seeded installation
   * names, and the path the run's own resolution check has to find. */
  readonly binary: string
  /** fixtureDirectory is where the documents are read from. */
  readonly fixtureDirectory: string
  /** installations is the seed file's rows, in the order the pictures show
   * them. */
  readonly installations: readonly SeedInstallation[]
  /** newProfile makes a fresh, empty `--user-data-dir` under this run's scratch
   * tree. Fresh per launch, because Chromium locks a profile to one process. */
  newProfile(): string
  /** seedInstallations writes the console's own `installations.json` into
   * `profile`, naming the fake for every row and `active` as the chosen one (or
   * nothing chosen, which is the console's first run). */
  seedInstallations(profile: string, active: string | null): void
  /** reports is the fake child's own account of every spawn so far: its `argv`,
   * its `PATH` and the keys of its environment. */
  reports(): readonly StubReport[]
}

/** readFixture reads one fixture file as text, naming it in the error when it
 * is not there -- a missing document is a broken run, not a screen to capture
 * without. */
function readFixture(directory: string, name: string): string {
  const path = join(directory, name)
  try {
    return readFileSync(path, 'utf8')
  } catch {
    throw new Error(`the screenshot run is missing its fixture ${name} (${path})`)
  }
}

/** compact is one JSON document as the CLI writes it on the wire: a single
 * line, no indentation. The files on disk are indented so a reader can read
 * them; what a command answers is the same document in the shape `readDocument`
 * decodes. */
function compact(text: string, name: string): string {
  try {
    return JSON.stringify(JSON.parse(text))
  } catch {
    throw new Error(`the screenshot run cannot read its fixture ${name}: it is not JSON`)
  }
}

/** framesOf reads the `watch` fixture: the list of documents the feed prints,
 * one per line. It is a list rather than one document because the Live screen's
 * sparkline is drawn from the frames it has seen, and a feed of one frame draws
 * a single bar. */
function framesOf(directory: string): readonly string[] {
  const text = readFixture(directory, FIXTURE_COMMANDS.watch)
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    throw new Error(`the screenshot run cannot read its fixture ${FIXTURE_COMMANDS.watch}: it is not JSON`)
  }
  const frames =
    typeof parsed === 'object' && parsed !== null && !Array.isArray(parsed)
      ? (parsed as Record<string, unknown>)['frames']
      : null
  if (!Array.isArray(frames) || frames.length === 0) {
    throw new Error(
      `the screenshot run needs ${FIXTURE_COMMANDS.watch} to hold a non-empty "frames" list: one document per line, in the order the feed arrived`,
    )
  }
  return frames.map((frame) => JSON.stringify(frame))
}

/** seedOf reads the seed file and holds it to the console's own store rules. */
function seedOf(directory: string): readonly SeedInstallation[] {
  const text = readFixture(directory, SEED_FILE_NAME)
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    throw new Error(`the screenshot run cannot read its fixture ${SEED_FILE_NAME}: it is not JSON`)
  }
  const rows =
    typeof parsed === 'object' && parsed !== null && !Array.isArray(parsed)
      ? (parsed as Record<string, unknown>)['installations']
      : null
  if (!Array.isArray(rows) || rows.length === 0) {
    throw new Error(`the screenshot run needs ${SEED_FILE_NAME} to hold a non-empty "installations" list`)
  }
  const seen = new Set<string>()
  const out: SeedInstallation[] = []
  for (const row of rows) {
    const entry = typeof row === 'object' && row !== null ? (row as Record<string, unknown>) : {}
    const id = entry['id']
    const project = entry['project']
    if (typeof id !== 'string' || !INSTALLATION_ID_RE.test(id)) {
      throw new Error(`an installation in ${SEED_FILE_NAME} has an id that is not sixteen hex characters: ${JSON.stringify(id)}`)
    }
    if (typeof project !== 'string' || !PROJECT_RE.test(project)) {
      throw new Error(`an installation in ${SEED_FILE_NAME} has a project name the console will not store: ${JSON.stringify(project)}`)
    }
    if (seen.has(id)) throw new Error(`two installations in ${SEED_FILE_NAME} share the id ${id}`)
    seen.add(id)
    out.push({ id, project })
  }
  return out
}

/**
 * scenarioFor is the whole answer set of the fake, keyed by the command path the
 * child reads off its own `argv` (`test/unit/stub.ts`'s `commandKey`).
 *
 * `default` **refuses** rather than answering: it exits 2 with a sentence, which
 * is the same shape the e2e suite's shell stub uses for a command it does not
 * know. A fake that answered whatever it was asked would let a console that
 * spawned something this run did not prepare be captured as a screen full of
 * `n/a`, and the picture would be wrong in a way no check here could see.
 *
 * `version` is the version the console was built against, so the handshake
 * lands inside the supported range and the console draws its ordinary mode
 * rather than the reduced one -- a reduced console withholds the Spend screen,
 * which is one of the pictures.
 */
function scenarioFor(directory: string, project: string): StubScenario {
  const logs = readFixture(directory, FIXTURE_COMMANDS.logs)
  return {
    commands: {
      version: { stdout: `${CLI_VERSION_BUILT_AGAINST}\n` },
      watch: { lines: framesOf(directory) },
      // The minimal `doctorOutput` the e2e suite's stub answers with (contract
      // section 6.4). Nothing in the dashboard draws it; it is what a *choose*
      // in the picker runs, so leaving it out would make a picture of the
      // picker's refusal instead of its list.
      doctor: { stdout: `${JSON.stringify({ ok: true, checks: [{ id: 'project', ok: true }], project })}\n` },
      'budget show': { stdout: `${compact(readFixture(directory, FIXTURE_COMMANDS['budget show']), 'budget.json')}\n` },
      report: { stdout: `${compact(readFixture(directory, FIXTURE_COMMANDS.report), 'report.json')}\n` },
      diagnose: { stdout: `${compact(readFixture(directory, FIXTURE_COMMANDS.diagnose), 'diagnose.json')}\n` },
      logs: { stdout: logs.endsWith('\n') ? logs : `${logs}\n` },
      'logs --url': { stdout: readFixture(directory, FIXTURE_COMMANDS['logs --url']) },
    },
    default: {
      code: 2,
      stderr: 'the screenshot fixture CLI answers only the commands the screenshot run prepared\n',
    },
  }
}

/**
 * installScreenshotFixture installs the fake CLI into the run's own scratch
 * `bin` and reads the documents beside it.
 *
 * The binary is **inside the isolation's `bin`** and nowhere else: that
 * directory is the whole of the launched console's `PATH`
 * (`launchEnvironment`), so the resolver's last rule can find nothing but this
 * file, and §1 of `isolation.ts` is where the run proves the resolver did not
 * find anything earlier in its order.
 */
export function installScreenshotFixture(options: {
  /** fixtureDirectory is `test/fixtures/screenshots`, absolute. */
  readonly fixtureDirectory: string
  readonly isolation: Isolation
}): ScreenshotFixture {
  const { fixtureDirectory, isolation } = options
  const installations = seedOf(fixtureDirectory)
  // The project the fake answers `doctor` for: the one the dashboard launch has
  // chosen. It is the first row of the seed file by construction, and the run's
  // own launch is what names the active installation -- see `capture.ts`.
  const first = installations[0]
  if (first === undefined) throw new Error(`the screenshot run needs at least one installation in ${SEED_FILE_NAME}`)
  const stub: Stub = installStub(isolation.bin, scenarioFor(fixtureDirectory, first.project))
  return {
    binary: stub.binary,
    fixtureDirectory,
    installations,
    newProfile(): string {
      const profile = mkdtempSync(join(isolation.scratch, 'profile-'))
      // 0700: the profile holds the store this run writes, and the console's own
      // store rules are about a directory only its operator can read.
      chmodSync(profile, 0o700)
      return profile
    },
    seedInstallations(profile: string, active: string | null): void {
      if (active !== null && !installations.some((row) => row.id === active)) {
        throw new Error(`the screenshot run was asked to choose ${active}, which ${SEED_FILE_NAME} does not hold`)
      }
      const file = serializeInstallationFile({
        version: INSTALLATION_FILE_VERSION,
        active,
        installations: installations.map((row) => ({
          id: row.id,
          project: row.project,
          binaryPath: stub.binary,
        })),
      })
      writeFileSync(join(profile, INSTALLATIONS_FILE_NAME), file, { mode: 0o600 })
      chmodSync(join(profile, INSTALLATIONS_FILE_NAME), 0o600)
    },
    reports(): readonly StubReport[] {
      return stub.reports()
    },
  }
}
