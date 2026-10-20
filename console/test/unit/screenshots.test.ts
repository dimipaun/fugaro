// The screenshot run's own rules, held without a window (owner ruling
// 2026-10-20, step 5).
//
// The run itself launches Electron, and no unit test here does: what is held is
// everything the run decides *before* and *after* a window exists --
//
//   1. **the refusals** (`tools/screenshots/isolation.ts`): a resolution that is
//      not the fixture binary, and a launch environment or profile that names
//      anything outside this run's scratch tree. These are the two rules that
//      make "a screenshot could not have been of a real installation" a checked
//      fact, so each is driven with the planted value and asserted to name it;
//   2. **the environment allow-list**: a credential-shaped key in the ambient
//      environment does not reach the child, and the three paths the run owns do;
//   3. **the fixtures**: every document in `test/fixtures/screenshots/` goes
//      through the console's **own** decoder, and the ids and project names the
//      pictures depend on are asserted to agree across files. A fixture the
//      console would refuse is a picture of a refusal, and this is the only
//      place that is cheap to notice;
//   4. **the manifest** (`tools/screenshots/manifest.ts`): the shape the website
//      reads, and the five ways a manifest can be readable-but-wrong;
//   5. **the blank-image check** (`tools/screenshots/png.ts`): a uniform
//      rectangle is refused, a drawn one is not, and a capture at the wrong size
//      is refused -- on PNGs built by the encoder below, because the check has to
//      be driven with a picture whose pixels are known;
//   6. **the failure path** (`tools/screenshots/failure.ts`): the sentence a
//      failed run prints, the unhandled rejection that would otherwise end the
//      process on Playwright's own stack, and the exit code that stands. The
//      entry point itself is read here rather than imported -- it launches
//      Electron, so nothing can import it -- and the one thing asserted about it
//      is that it wires this handler in;
//   7. **what the run's own documents promise about it**: the two commands that
//      take the pictures (`scripts/console-screenshots.sh` and the `screenshots`
//      script it documents, which have to do the same work -- including the
//      build, because what the run launches is the built app), the README's
//      picture table (one table, the omission in it), and the shell script's
//      header, which says what the run reaches as well as what it does not;
//   8. **the captures' selectors** (`tools/screenshots/screens.ts`): each one is
//      held to the renderer file that has to draw its class or id, so a renamed
//      renderer fails here rather than as a `waitForSelector` timeout on the
//      owner's Mac.
//
// What is *not* here, stated plainly: the Electron launch, the window's real
// size, and the fact that a screen draws at all. Those need a display, and the
// owner's own run (`scripts/console-screenshots.sh`) is where they are
// exercised -- see `console/README.md`.

import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { deflateSync } from 'node:zlib'

import { afterAll, describe, expect, it } from 'vitest'

import { INSTALLATION_FILE_VERSION, readInstallationFile, serializeInstallationFile } from '../../src/main/installations'
import { decodeDocument, decodeSnapshot } from '../../src/shared/schema'
import { buildRunModel, parseLogWindow } from '../../src/renderer/screens/run'
import { SPEND_ACCOUNTS, buildSpendModel } from '../../src/renderer/screens/spend'
import { parseLogEntries } from '../../src/source/cli'
import type { JSONDoc } from '../../src/source/source'
import { FIXTURE_COMMANDS, installScreenshotFixture, FIXTURES_RELATIVE, SEED_FILE_NAME } from '../../tools/screenshots/fixture'
import { assertFixtureBinary, assertIsolated, assertLaunchIsFixture, launchEnvironment, newIsolation } from '../../tools/screenshots/isolation'
import type { Isolation } from '../../tools/screenshots/isolation'
import { BLANK_STDDEV_FLOOR, assertUsableCapture, decodePng, pixelStdDev } from '../../tools/screenshots/png'
import { CAPTURE_WINDOW, buildManifest, serializeManifest, sha256Of } from '../../tools/screenshots/manifest'
import { CAPTURE_PIXELS, OMISSIONS, PICKER_SCREEN, RUN_ID, SCREENS } from '../../tools/screenshots/screens'
import { FAILURE_SENTENCE, exitCodeFor, failureLine, installFailureHandler } from '../../tools/screenshots/failure'
import type { RunProcess } from '../../tools/screenshots/failure'
import { STUB_COMMAND_KEYS } from './stub'

/** CONSOLE_ROOT is the console package, from this file's own location: the
 * fixtures are read from the tree rather than from a copy of it. */
const CONSOLE_ROOT = fileURLToPath(new URL('../../', import.meta.url))

/** FIXTURES is `test/fixtures/screenshots`. */
const FIXTURES = join(CONSOLE_ROOT, ...FIXTURES_RELATIVE)

function fixture(name: string): string {
  return readFileSync(join(FIXTURES, name), 'utf8')
}

// ---------------------------------------------------------------------------
// 1. The refusals.
// ---------------------------------------------------------------------------

describe('the run refuses to start against anything but its own fixture', () => {
  const scratch = '/tmp/fugaro-console-screenshots-abc123'
  const isolation: Isolation = Object.freeze({
    scratch,
    home: join(scratch, 'home'),
    xdgConfigHome: join(scratch, 'home', '.config'),
    userData: join(scratch, 'user-data'),
    bin: join(scratch, 'bin'),
    tmp: join(scratch, 'tmp'),
  })
  const cleanEnv: Readonly<Record<string, string>> = Object.freeze({
    PATH: isolation.bin,
    HOME: isolation.home,
    XDG_CONFIG_HOME: isolation.xdgConfigHome,
    TMPDIR: isolation.tmp,
  })

  it('answers nothing but the fixture binary from the resolver', () => {
    const fixtureBinary = join(isolation.bin, 'fugaro')
    // The same path is not a refusal: it is the answer the run wants.
    expect(() => {
      assertFixtureBinary(fixtureBinary, fixtureBinary, 'the console’s startup handshake')
    }).not.toThrow()
  })

  it('refuses a resolved binary that is not the fixture, naming the path it found', () => {
    // The failure this exists for: a machine with a real `fugaro` in
    // `/opt/homebrew/bin`, which the resolver's order reaches before any `PATH`
    // entry. A run that proceeded here would photograph the operator's own runs.
    expect(() => {
      assertFixtureBinary('/opt/homebrew/bin/fugaro', join(isolation.bin, 'fugaro'), 'the startup handshake')
    }).toThrow(/refuses to start.*the startup handshake.*\/opt\/homebrew\/bin\/fugaro/)
  })

  it('refuses a resolution that found no fugaro at all', () => {
    expect(() => {
      assertFixtureBinary(null, join(isolation.bin, 'fugaro'), 'the startup handshake')
    }).toThrow(/no fugaro at all/)
  })

  it('refuses a HOME outside the scratch tree, naming the key and the path', () => {
    expect(() => {
      assertIsolated({
        env: { ...cleanEnv, HOME: '/Users/somebody' },
        isolation,
        userDataDir: null,
        real: { home: '/Users/somebody', xdgConfigHome: null },
      })
    }).toThrow(/refuses to start: HOME is the operator's home directory \(\/Users\/somebody\)/)
  })

  it("refuses the operator's real XDG_CONFIG_HOME even when it is inside a scratch-looking path", () => {
    // The path is under the scratch tree *and* is the real config home: the
    // two rules are separate, and the second one wins, because a directory the
    // operator's CLI writes is a directory that can hold an installation.
    const inside = join(scratch, 'home', '.config')
    expect(inside.startsWith(scratch)).toBe(true)
    expect(() => {
      assertIsolated({
        env: { ...cleanEnv, XDG_CONFIG_HOME: inside },
        isolation,
        userDataDir: null,
        real: { home: '/Users/somebody', xdgConfigHome: inside },
      })
    }).toThrow(/XDG_CONFIG_HOME is the operator's XDG_CONFIG_HOME/)
  })

  it('refuses a TMPDIR outside the scratch tree', () => {
    // The console builds the private working directory its children run in as
    // `$TMPDIR/fugaro-console-<uid>/cwd` (`src/main/spawn.ts`). An ambient
    // `TMPDIR` is therefore a directory the console *writes* outside this run's
    // tree -- and a run that removes everything it made cannot leave one out.
    expect(() => {
      assertIsolated({
        env: { ...cleanEnv, TMPDIR: '/var/folders/xx' },
        isolation,
        userDataDir: null,
        real: { home: '/Users/somebody', xdgConfigHome: null },
      })
    }).toThrow(/TMPDIR is \/var\/folders\/xx, which is outside/)
  })

  it('refuses a profile outside the scratch tree', () => {
    expect(() => {
      assertIsolated({
        env: cleanEnv,
        isolation,
        userDataDir: '/Users/somebody/Library/Application Support/Fugaro Console',
        real: { home: '/Users/somebody', xdgConfigHome: null },
      })
    }).toThrow(/--user-data-dir is \/Users\/somebody\/Library\/Application Support\/Fugaro Console, which is outside/)
  })

  it('refuses an environment with no HOME, no XDG_CONFIG_HOME or no TMPDIR at all', () => {
    const { HOME: _home, ...noHome } = cleanEnv
    expect(() => {
      assertIsolated({ env: noHome, isolation, userDataDir: null, real: { home: '', xdgConfigHome: null } })
    }).toThrow(/sets no HOME/)
    const { XDG_CONFIG_HOME: _xdg, ...noXdg } = cleanEnv
    expect(() => {
      assertIsolated({ env: noXdg, isolation, userDataDir: null, real: { home: '', xdgConfigHome: null } })
    }).toThrow(/sets no XDG_CONFIG_HOME/)
    const { TMPDIR: _tmp, ...noTmp } = cleanEnv
    expect(() => {
      assertIsolated({ env: noTmp, isolation, userDataDir: null, real: { home: '', xdgConfigHome: null } })
    }).toThrow(/sets no TMPDIR/)
  })

  it('accepts the launch this run actually makes', () => {
    expect(() => {
      assertIsolated({
        env: cleanEnv,
        isolation,
        userDataDir: join(scratch, 'profile-abc'),
        real: { home: '/Users/somebody', xdgConfigHome: '/Users/somebody/.config' },
      })
    }).not.toThrow()
  })
})

describe('the environment a launch gets is an allow-list', () => {
  const isolation = newIsolation()

  afterAll(() => {
    rmSync(isolation.scratch, { recursive: true, force: true })
  })

  it('carries a display and a locale, and nothing that can name a credential or a config', () => {
    const env = launchEnvironment(
      {
        DISPLAY: ':0',
        LANG: 'en_GB.UTF-8',
        TMPDIR: '/var/folders/xx',
        // The keys this rule exists for: each one names a file, a socket or a
        // credential a child could reach, and none of them may reach the child.
        GOOGLE_APPLICATION_CREDENTIALS: '/Users/somebody/.config/gcloud/application_default_credentials.json',
        FUGARO_CONFIG: '/Users/somebody/.config/fugaro/config.yaml',
        SSH_AUTH_SOCK: '/private/tmp/com.apple.launchd.abc/Listeners',
        AWS_PROFILE: 'production',
        HOME: '/Users/somebody',
        XDG_CONFIG_HOME: '/Users/somebody/.config',
        PATH: '/opt/homebrew/bin:/usr/bin',
      },
      isolation,
    )
    expect(env['DISPLAY']).toBe(':0')
    expect(env['LANG']).toBe('en_GB.UTF-8')
    // The ambient `TMPDIR` does not reach the child: the run's own is written
    // over it, so the private working directory the console makes for its
    // children is inside the scratch tree this run removes.
    expect(env['TMPDIR']).toBe(isolation.tmp)
    expect(env['TMPDIR']).not.toBe('/var/folders/xx')
    for (const key of [
      'GOOGLE_APPLICATION_CREDENTIALS',
      'FUGARO_CONFIG',
      'SSH_AUTH_SOCK',
      'AWS_PROFILE',
    ]) {
      expect(env[key], `${key} must not reach the launched console`).toBeUndefined()
    }
    // The three paths the run owns win over the operator's, which is what makes
    // the resolution and the store the fixture's rather than the machine's.
    expect(env['HOME']).toBe(isolation.home)
    expect(env['XDG_CONFIG_HOME']).toBe(isolation.xdgConfigHome)
    expect(env['PATH']).toBe(isolation.bin)
  })

  it('drops an ambient key that is empty rather than carrying an empty string', () => {
    // The allow-list copies a value only when it is a non-empty string, and the
    // difference matters: an empty `DISPLAY` is not a display, and a child that
    // receives `DISPLAY=''` fails to open a window rather than falling back to
    // anything. An empty ambient value is therefore an *absent* one.
    const env = launchEnvironment({ DISPLAY: '', LANG: 'en_GB.UTF-8', TERM: '' }, isolation)
    expect(Object.prototype.hasOwnProperty.call(env, 'DISPLAY')).toBe(false)
    expect(Object.prototype.hasOwnProperty.call(env, 'TERM')).toBe(false)
    expect(env['LANG']).toBe('en_GB.UTF-8')
    // The run's own keys are there anyway: an empty ambient value is dropped,
    // and the paths `launchEnvironment` writes are not ambient at all.
    expect(env['HOME']).toBe(isolation.home)
  })

  it('puts the five directories under one scratch tree it created', () => {
    expect(isolation.home.startsWith(isolation.scratch)).toBe(true)
    expect(isolation.xdgConfigHome.startsWith(isolation.home)).toBe(true)
    expect(isolation.userData.startsWith(isolation.scratch)).toBe(true)
    expect(isolation.bin.startsWith(isolation.scratch)).toBe(true)
    expect(isolation.tmp.startsWith(isolation.scratch)).toBe(true)
  })
})

// ---------------------------------------------------------------------------
// 2. The fixtures, through the console's own decoder.
// ---------------------------------------------------------------------------

/** frames reads the `watch` fixture: the documents the fake prints, one per
 * line. */
function frames(): readonly unknown[] {
  const parsed = JSON.parse(fixture(FIXTURE_COMMANDS.watch)) as { frames: readonly unknown[] }
  return parsed.frames
}

/** COUNT_WORDS spells a small count the way a sentence does. The alt texts are
 * prose written for a person who cannot see the picture, and "3 runs" reads
 * like a log line; the test still has to hold the prose to the fixture, so the
 * number is spelled here the same way the sentence spells it. */
const COUNT_WORDS: readonly string[] = ['none', 'one', 'two', 'three', 'four', 'five', 'six']

describe('the fixtures are documents the console accepts', () => {
  it('decodes every live frame through the schema the feed uses', () => {
    const all = frames()
    expect(all.length).toBeGreaterThan(1)
    all.forEach((frame, index) => {
      const decoded = decodeSnapshot(frame, `live.json frames[${String(index)}]`)
      if (!decoded.ok) throw new Error(`frames[${String(index)}]: ${decoded.skew.because}`)
    })
  })

  it('carries the e2e suite’s own keys, and no more', () => {
    // This directory's README said the frames carried "the same seven keys" as
    // the e2e suite's recordings while they carried ten (review of 2026-10-20,
    // low finding). The count is held here to the recording that sentence names,
    // so the two cannot drift apart again: this directory exists to be the
    // *same shape* as a `watch --json` document with better values, and a frame
    // with a key the e2e suite's documents do not have is a shape the console's
    // own decoder never meets in the suite.
    const keys = Object.keys(frames()[0] as object).sort()
    expect(keys).toEqual([
      'connection',
      'day',
      'finished',
      'mode',
      'now',
      'project',
      'ready',
      'repos',
      'runs',
      'total',
    ])
    for (const frame of frames()) expect(Object.keys(frame as object).sort()).toEqual(keys)
    // `../synthetic.ndjson` is the suite's recording of that shape: a header
    // line, then one `{at, document}` per record.
    const recorded = readFileSync(join(CONSOLE_ROOT, 'test/fixtures', 'synthetic.ndjson'), 'utf8')
      .split('\n')
      .filter((line) => line.trim() !== '')
      .map((line) => JSON.parse(line) as { document?: Record<string, unknown> })
      .filter((record) => record.document !== undefined)
    expect(recorded.length).toBeGreaterThan(1)
    for (const record of recorded) {
      expect(Object.keys(record.document ?? {}).sort()).toEqual(keys)
    }
  })

  /** spendModel is the Spend screen's own model built from the two fixtures the
   * screen draws: `report --json` is the curve and the rows, `budget show
   * --json` is the caps panel. */
  function spendModel(): ReturnType<typeof buildSpendModel> {
    return buildSpendModel(
      JSON.parse(fixture(FIXTURE_COMMANDS.report)) as JSONDoc,
      JSON.parse(fixture(FIXTURE_COMMANDS['budget show'])) as JSONDoc,
    )
  }

  it('reads the report and budget fixtures into the Spend screen’s own model', () => {
    // **Two of the five fixtures are documents the schema does not decode, and
    // that is the schema working.** `decodeDocument` admits the feed's shape and
    // the degraded one (`LIVE_MARKERS`, `degraded`); `report --json` carries
    // `degraded: false` and `budget show --json` carries `max_reserve_usd`,
    // `global` and `repos` -- neither is a document `watch` writes, and reading
    // one as a live document would be a guess about what the writer meant. The
    // Spend screen reads both with its own field readers, so the fixtures are
    // held to *those*: the figures the picture and its alt text claim are the
    // figures the screen computes.
    const model = spendModel()
    expect(model.project).toBe('acme-platform')
    expect(model.dimension).toBe('day')
    expect(model.range.since).toBe('2026-09-21')
    expect(model.range.until).toBe('2026-10-20')
    // Thirty days, one row each: the curve the picture is of.
    expect(model.rows.length).toBe(30)
    expect(model.rows[0]?.key).toBe('2026-09-21')
    expect(model.rows[29]?.key).toBe('2026-10-20')
    expect(model.series.map((series) => series.account)).toEqual([...SPEND_ACCOUNTS])
    const caps = model.caps
    expect(caps.available).toBe(true)
    expect(caps.project).toBe('acme-platform')
    expect(caps.day).toBe('2026-10-20')
    expect(caps.mode).toBe('enforce')
    expect(caps.reserve.value).toBe(60)
    expect(caps.global?.dailyCap.value).toBe(120)
    expect(caps.global?.counted.value).toBe(41.82)
    expect(caps.defaults?.repoDailyCap.value).toBe(40)
    expect(caps.repos.map((row) => row.repo)).toEqual(['acme/platform-core', 'acme/platform-cli'])
    expect(caps.repos.map((row) => row.dailyCap.value)).toEqual([80, 40])
    expect(caps.warnings).toEqual([])
  })

  it('reads the diagnose fixture into the Run screen’s own model', () => {
    // The third document the schema does not decode: `diagnose RUN --json` is a
    // `runview` row and its findings, read by the Run screen's own reader. The
    // picture is of the run the Live screen's first row names, so the model is
    // held to that run -- a diagnose fixture for another run would draw a detail
    // screen for a row nobody clicked.
    const read = parseLogEntries(fixture(FIXTURE_COMMANDS.logs))
    const model = buildRunModel({
      diagnosis: JSON.parse(fixture(FIXTURE_COMMANDS.diagnose)),
      logs: { entries: read.entries, total: read.total, refused: read.refused },
      logLink: fixture(FIXTURE_COMMANDS['logs --url']).trim(),
    })
    expect(model.row.runId).toBe(RUN_ID)
    expect(model.row.repo).toBe('acme/platform-core')
    expect(model.row.status).toBe('running')
    expect(model.stages.length).toBeGreaterThan(0)
    expect(model.diagnosis.findings.length).toBeGreaterThan(0)
    expect(model.logs.lines.length).toBeGreaterThan(0)
    expect(model.logs.refused.value).toBe(0)
    expect(model.pullRequest.number).toBe(412)
    expect(model.pullRequest.repository).toBe('acme/platform-core')
  })

  it('reads the log window with no refused line', () => {
    // The fixture is the *stdout* of `logs RUN --json`: one compact `logLine`
    // object per line. It goes through the console's own line reader and then
    // through the Run screen's window reader, which is the order the log read
    // makes (`source/cli.ts`'s `parseLogEntries` answers `{entries, total,
    // refused}` and the screen is handed exactly that). A fixture with a torn
    // line or a missing field would be captured as a screen with a refusal
    // count on it, and this is where that is cheap to notice.
    const read = parseLogEntries(fixture(FIXTURE_COMMANDS.logs))
    const lines = fixture(FIXTURE_COMMANDS.logs)
      .split('\n')
      .filter((line) => line.trim() !== '')
    expect(read.refused).toBe(0)
    expect(read.total).toBe(lines.length)
    const window = parseLogWindow({ entries: read.entries, total: read.total, refused: read.refused })
    expect(window.lines.length).toBe(lines.length)
    expect(window.total).toBe(lines.length)
    expect(window.refused).toBe(0)
    // A bare list carries no refused count, and the screen says so rather than
    // drawing a zero: `null` is unavailable, which is a different fact.
    expect(parseLogWindow(read.entries).refused).toBeNull()
  })

  it('names the same project in every document the dashboard draws', () => {
    // A picture whose header says one project and whose document says another
    // is a picture of a console reading the wrong installation. The fake answers
    // whatever it is asked, so nothing but this assertion holds the documents to
    // one project name.
    //
    // Every document the dashboard draws is in the list, and `diagnose` is in it
    // because it was the one that got left out: the diagnosis document carries
    // the same top-level `project` as `report` and `budget show`
    // (`internal/cli/diagnose.go`'s `Diagnosis.project`, written with no
    // `omitempty` -- `console/docs/json-contract.md` §6.1), and the fixture was
    // missing the key entirely, so a screen that drew it would have drawn
    // whatever it draws for an absent one. A loop over a list is only as good as
    // the list.
    const seed = JSON.parse(fixture(SEED_FILE_NAME)) as { installations: readonly { project: string }[] }
    const active = seed.installations[0]?.project
    expect(active).toBeDefined()
    for (const frame of frames()) {
      expect((frame as { project: string }).project).toBe(active)
    }
    for (const name of [FIXTURE_COMMANDS['budget show'], FIXTURE_COMMANDS.report, FIXTURE_COMMANDS.diagnose]) {
      expect((JSON.parse(fixture(name)) as { project?: string }).project, `${name} names no project`).toBe(active)
    }
  })

  it('answers the run the Live screen names, with the log link for that run', () => {
    const first = frames()[0] as { runs: readonly { run: string }[] }
    const row = (JSON.parse(fixture(FIXTURE_COMMANDS.diagnose)) as { row: { run: string } }).row
    // The run detail is reached by clicking the Live screen's own row, and the
    // screen then draws the diagnosis *it* asked for. Two files naming two runs
    // would draw a run id the operator never clicked.
    expect(first.runs[0]?.run).toBe(RUN_ID)
    expect(row.run).toBe(RUN_ID)
    expect(fixture(FIXTURE_COMMANDS['logs --url'])).toContain(RUN_ID)
  })

  it('prepares only commands the fake can be asked for', () => {
    for (const name of Object.keys(FIXTURE_COMMANDS)) {
      expect(STUB_COMMAND_KEYS, `${name} is not a command this console spawns`).toContain(name)
    }
  })
})

// ---------------------------------------------------------------------------
// 3. The fake CLI and the store it seeds.
// ---------------------------------------------------------------------------

describe('the fixture CLI and the installation store it seeds', () => {
  it('writes a store the console’s own reader accepts, with the fake as every binary', () => {
    const isolation = newIsolation()
    try {
      const installed = installScreenshotFixture({ fixtureDirectory: FIXTURES, isolation })
      const profile = installed.newProfile()
      const active = installed.installations[0]
      expect(active).toBeDefined()
      installed.seedInstallations(profile, active?.id ?? null)
      const read = readInstallationFile(readFileSync(join(profile, 'installations.json'), 'utf8'))
      if (!read.ok) throw new Error(read.reason)
      expect(read.file.active).toBe(active?.id)
      expect(read.file.installations.map((row) => row.project)).toEqual(
        installed.installations.map((row) => row.project),
      )
      for (const row of read.file.installations) {
        // Every row names the *fake*, which is the whole point: an installation
        // pointing anywhere else is an installation this run must not have.
        expect(row.binaryPath).toBe(installed.binary)
        expect(installed.binary.startsWith(isolation.scratch)).toBe(true)
      }
    } finally {
      rmSync(isolation.scratch, { recursive: true, force: true })
    }
  })

  it('seeds a first run with nothing chosen, which is what opens the picker', () => {
    const isolation = newIsolation()
    try {
      const installed = installScreenshotFixture({ fixtureDirectory: FIXTURES, isolation })
      const profile = installed.newProfile()
      installed.seedInstallations(profile, null)
      const read = readInstallationFile(readFileSync(join(profile, 'installations.json'), 'utf8'))
      if (!read.ok) throw new Error(read.reason)
      expect(read.file.active).toBeNull()
      expect(read.file.installations.length).toBe(3)
    } finally {
      rmSync(isolation.scratch, { recursive: true, force: true })
    }
  })

  it('refuses to seed a choice the seed file does not hold', () => {
    const isolation = newIsolation()
    try {
      const installed = installScreenshotFixture({ fixtureDirectory: FIXTURES, isolation })
      const profile = installed.newProfile()
      expect(() => {
        installed.seedInstallations(profile, 'ffffffffffffffff')
      }).toThrow(/does not hold/)
    } finally {
      rmSync(isolation.scratch, { recursive: true, force: true })
    }
  })

  it('answers no command it was not prepared for', () => {
    // The scenario's `default` is a refusal, so a console that spawns something
    // this run did not prepare fails the screen rather than being answered with
    // an empty document. Held here by reading the scenario the fake writes.
    const isolation = newIsolation()
    try {
      const installed = installScreenshotFixture({ fixtureDirectory: FIXTURES, isolation })
      const scenario = JSON.parse(readFileSync(join(isolation.bin, 'scenario.json'), 'utf8')) as {
        commands: Record<string, unknown>
        default: { code?: number }
      }
      expect(scenario.default.code).toBe(2)
      expect(Object.keys(scenario.commands).sort()).toEqual([...Object.keys(FIXTURE_COMMANDS), 'doctor', 'version'].sort())
      // The version is the one the console was built against, so the handshake
      // lands inside the supported range and the Spend screen is offered.
      expect((scenario.commands['version'] as { stdout: string }).stdout.trim()).toBe('0.8.1')
      expect(installed.binary.length).toBeGreaterThan(0)
    } finally {
      rmSync(isolation.scratch, { recursive: true, force: true })
    }
  })
})

// ---------------------------------------------------------------------------
// 3a. The launch's own store, which is the only thing the handshake may read.
// ---------------------------------------------------------------------------

describe('a launch is checked against the store it seeds', () => {
  /**
   * The check under test is the run's first refusal, and it is the fix for the
   * one failure that made this run impossible on the owner's Mac: asking the
   * *machine's* resolver what it would find walks `/opt/homebrew/bin`,
   * `/usr/local/bin`, `~/.local/bin` and `/usr/bin`, and probes the winner by
   * **executing** it -- so the question itself ran the operator's own `fugaro`
   * and then refused the run. The question a launch asks is about the one path
   * its chosen installation names, so these tests drive that: the store is read
   * with the console's own reader, and the path is resolved with the console's
   * own one-path resolution.
   *
   * The fake is really installed here (and really spawned), because the check
   * spawns: `test/unit/stub.ts`'s child is a Node process running the scenario,
   * and its report is how the tests below see the child's own `cwd`.
   */
  function installed(): { isolation: Isolation; fixture: ReturnType<typeof installScreenshotFixture>; env: Readonly<Record<string, string>> } {
    const isolation = newIsolation()
    const fixture = installScreenshotFixture({ fixtureDirectory: FIXTURES, isolation })
    return { isolation, fixture, env: launchEnvironment({}, isolation) }
  }

  it('accepts the store this run seeds, and probes from inside the scratch tree', async () => {
    const { isolation, fixture, env } = installed()
    try {
      const profile = fixture.newProfile()
      const active = fixture.installations[0]
      expect(active).toBeDefined()
      fixture.seedInstallations(profile, active?.id ?? null)
      await expect(
        assertLaunchIsFixture({ profile, expected: fixture.binary, env, isolation }),
      ).resolves.toBeUndefined()
      // The check spawns the fake to ask its version, and that child runs in
      // the directory this run gave it -- inside the scratch tree, which is
      // what makes "the run touches nothing it did not make" a fact about the
      // check itself and not only about the launch.
      const reports = fixture.reports()
      expect(reports.length).toBeGreaterThan(0)
      for (const report of reports) expect(report.cwd).toBe(isolation.userData)
    } finally {
      rmSync(isolation.scratch, { recursive: true, force: true })
    }
  })

  it('refuses a launch whose store chooses no installation, naming the prefixes the handshake would walk', async () => {
    // D13's first run is the state the second launch used to have, and it is
    // the state that must never reach `launchConsole`: with nothing chosen the
    // handshake is `resolveBinary` over rule 4's list, and on the owner's Mac
    // the first entry of that list is their own CLI.
    const { isolation, fixture, env } = installed()
    try {
      const profile = fixture.newProfile()
      fixture.seedInstallations(profile, null)
      await expect(
        assertLaunchIsFixture({ profile, expected: fixture.binary, env, isolation }),
      ).rejects.toThrow(/chooses no installation.*\/opt\/homebrew\/bin, \/usr\/local\/bin, ~\/\.local\/bin, \/usr\/bin/)
    } finally {
      rmSync(isolation.scratch, { recursive: true, force: true })
    }
  })

  it('refuses a store that names another binary, naming the path it resolved', async () => {
    // A store the run did not write: every row names another executable, which
    // is what a store pointing at a real installation looks like from here. The
    // check has to refuse it, and the sentence has to name the path, because
    // that path is the only thing that tells the operator what went wrong.
    //
    // The other binary is a **copy of the fake**, deliberately: naming a real
    // prefix here (`/opt/homebrew/bin/fugaro`) would make this unit test
    // *execute the operator's own CLI* on the machines where it exists -- the
    // exact leak this check exists to prevent, in the one place it would be
    // least expected -- and it would pass on this container, where the path
    // does not exist, for the wrong reason. A copy is somewhere else and is
    // still the fake, so the probe is safe and the refusal is the resolver's
    // answer rather than a missing file.
    const { isolation, fixture, env } = installed()
    try {
      const elsewhere = join(isolation.scratch, 'elsewhere')
      mkdirSync(elsewhere, { recursive: true })
      const other = join(elsewhere, 'fugaro')
      cpSync(fixture.binary, other)

      const profile = fixture.newProfile()
      const active = fixture.installations[0]
      expect(active).toBeDefined()
      writeFileSync(
        join(profile, 'installations.json'),
        serializeInstallationFile({
          version: INSTALLATION_FILE_VERSION,
          active: active?.id ?? null,
          installations: fixture.installations.map((row) => ({
            id: row.id,
            project: row.project,
            binaryPath: other,
          })),
        }),
      )
      await expect(
        assertLaunchIsFixture({ profile, expected: fixture.binary, env, isolation }),
      ).rejects.toThrow(new RegExp(`resolves ${other}, not the fixture CLI at ${fixture.binary}`))
    } finally {
      rmSync(isolation.scratch, { recursive: true, force: true })
    }
  })

  it('refuses a profile with no store, and one the console will not read', async () => {
    const { isolation, fixture, env } = installed()
    try {
      const empty = fixture.newProfile()
      await expect(
        assertLaunchIsFixture({ profile: empty, expected: fixture.binary, env, isolation }),
      ).rejects.toThrow(/holds no installations\.json/)

      const broken = fixture.newProfile()
      writeFileSync(join(broken, 'installations.json'), '{"version":1}')
      await expect(
        assertLaunchIsFixture({ profile: broken, expected: fixture.binary, env, isolation }),
      ).rejects.toThrow(/is not one the console will read/)
    } finally {
      rmSync(isolation.scratch, { recursive: true, force: true })
    }
  })
})

// ---------------------------------------------------------------------------
// 3b. A fixture the run cannot use.
// ---------------------------------------------------------------------------

describe('a fixture the run cannot use', () => {
  /**
   * brokenFixture is a **copy** of `test/fixtures/screenshots` with one thing
   * done to it, and the two reasons it is a copy rather than a path that does
   * not exist: a fixture typo is what actually happens (someone edits a
   * document and gets an id or a key wrong), and the run has to refuse it
   * *before a window exists* rather than start a console whose store is refused
   * and take pictures of the refusal.
   */
  function brokenFixture(edit: (directory: string) => void): { directory: string; isolation: Isolation } {
    const directory = mkdtempSync(join(tmpdir(), 'fugaro-screenshot-fixtures-'))
    cpSync(FIXTURES, directory, { recursive: true })
    edit(directory)
    return { directory, isolation: newIsolation() }
  }

  function withBrokenFixture(edit: (directory: string) => void, check: (error: unknown) => void): void {
    const { directory, isolation } = brokenFixture(edit)
    try {
      let thrown: unknown = null
      try {
        installScreenshotFixture({ fixtureDirectory: directory, isolation })
      } catch (error) {
        thrown = error
      }
      check(thrown)
    } finally {
      rmSync(directory, { recursive: true, force: true })
      rmSync(isolation.scratch, { recursive: true, force: true })
    }
  }

  it('refuses a seed row whose id is not sixteen hex characters', () => {
    withBrokenFixture(
      (directory) => {
        const path = join(directory, SEED_FILE_NAME)
        const seed = JSON.parse(readFileSync(path, 'utf8')) as { installations: { id: string }[] }
        // The console's own store rule: a row that failed it would be dropped
        // by `readInstallationFile`, and the picker would draw two rows.
        if (seed.installations[0] !== undefined) seed.installations[0].id = 'ACME-1'
        writeFileSync(path, JSON.stringify(seed))
      },
      (error) => {
        expect(String(error)).toMatch(/sixteen hex characters/)
      },
    )
  })

  it('refuses a seed with two rows sharing an id', () => {
    withBrokenFixture(
      (directory) => {
        const path = join(directory, SEED_FILE_NAME)
        const seed = JSON.parse(readFileSync(path, 'utf8')) as { installations: { id: string }[] }
        const first = seed.installations[0]
        const second = seed.installations[1]
        if (first !== undefined && second !== undefined) second.id = first.id
        writeFileSync(path, JSON.stringify(seed))
      },
      (error) => {
        expect(String(error)).toMatch(/share the id/)
      },
    )
  })

  it('refuses a feed with no frames in it', () => {
    withBrokenFixture(
      (directory) => {
        // One document rather than a list of them: the shape the feed fixture
        // had before the sparkline needed a history, and a run that read it as
        // frames would draw one bar.
        const path = join(directory, FIXTURE_COMMANDS.watch)
        const feed = JSON.parse(readFileSync(path, 'utf8')) as { frames: unknown[] }
        writeFileSync(path, JSON.stringify(feed.frames[0] ?? {}))
      },
      (error) => {
        expect(String(error)).toMatch(/non-empty "frames" list/)
      },
    )
  })

  it('refuses a fixture directory that is missing a document', () => {
    withBrokenFixture(
      (directory) => {
        rmSync(join(directory, FIXTURE_COMMANDS['budget show']))
      },
      (error) => {
        expect(String(error)).toMatch(/missing its fixture budget\.json/)
      },
    )
  })
})

// ---------------------------------------------------------------------------
// 4. The manifest.
// ---------------------------------------------------------------------------

describe('the manifest', () => {
  const entry = {
    file: 'console-live.png',
    width: 2560,
    height: 1600,
    sha256: 'a'.repeat(64),
    alt: 'The Live screen.',
  }

  it('carries what it is given, with the window every capture is taken at', () => {
    const manifest = buildManifest({ screens: [entry] })
    expect(manifest.version).toBe(1)
    expect(manifest.window).toEqual(CAPTURE_WINDOW)
    expect(manifest.screens).toEqual([entry])
    expect(manifest.omitted).toEqual([])
  })

  it('refuses two entries for one file', () => {
    expect(() => buildManifest({ screens: [entry, { ...entry }] })).toThrow(/lists console-live\.png twice/)
  })

  it('refuses a name with a path in it, and a name that is not a .png', () => {
    expect(() => buildManifest({ screens: [{ ...entry, file: '../console-live.png' }] })).toThrow(/no path in it/)
    expect(() => buildManifest({ screens: [{ ...entry, file: 'console-live.json' }] })).toThrow(/no path in it/)
  })

  it('refuses a pixel size that is not a positive whole number', () => {
    expect(() => buildManifest({ screens: [{ ...entry, width: 0 }] })).toThrow(/not a positive whole number/)
    expect(() => buildManifest({ screens: [{ ...entry, height: 1066.5 }] })).toThrow(/not a positive whole number/)
  })

  it('refuses a digest that is not a digest, and an alt text that is empty', () => {
    expect(() => buildManifest({ screens: [{ ...entry, sha256: 'nope' }] })).toThrow(/carries no sha256/)
    expect(() => buildManifest({ screens: [{ ...entry, alt: '   ' }] })).toThrow(/carries no alt text/)
  })

  it('writes the canonical form a later diff can be read against', () => {
    const text = serializeManifest(
      buildManifest({
        screens: [entry],
        omitted: [...OMISSIONS],
      }),
    )
    expect(text.endsWith('}\n')).toBe(true)
    expect(JSON.parse(text)).toEqual({
      version: 1,
      window: CAPTURE_WINDOW,
      screens: [entry],
      omitted: [...OMISSIONS],
    })
    // The keys are in the order the interface declares them, so two runs'
    // manifests differ in the captures and nothing else.
    expect(text.indexOf('"window"')).toBeLessThan(text.indexOf('"screens"'))
    expect(text.indexOf('"screens"')).toBeLessThan(text.indexOf('"omitted"'))
  })

  it('hashes the bytes it is given', () => {
    // The digest is the one every other tool computes for these bytes: an empty
    // input's sha256, written out rather than computed by the code under test.
    expect(sha256Of(new Uint8Array([]))).toBe('e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855')
  })

  it('names the four captures and the one it does not take', () => {
    expect(SCREENS.map((screen) => screen.file)).toEqual([
      'console-live.png',
      'console-run.png',
      'console-spend.png',
    ])
    expect(PICKER_SCREEN.file).toBe('console-installations.png')
    for (const screen of [...SCREENS, PICKER_SCREEN]) {
      expect(screen.alt.trim().endsWith('.'), `${screen.file} has no alt sentence`).toBe(true)
    }
    // The alt text is *of the fixture*, so the figures in it are held to the
    // fixture: a fixture edited for a better picture and an alt text left behind
    // would describe a screen nobody took a picture of.
    //
    // It is held to what the screen **draws**, and that is the second half of
    // this test. The Live screen draws `runs`, `repos` and `total`
    // (`src/renderer/screens/live.ts`); the frame's `finished[]` list is decoded
    // by the schema and drawn nowhere. An alt that counted it -- the first
    // version of this one said "five runs ... one that opened pull request
    // acme/platform-core#412 and one that failed" -- would put a fact on the
    // website that is in no picture, and it shipped in `MANIFEST.json`. The
    // three assertions below are therefore *absences*: the finished rows' own
    // facts must not appear.
    const last = frames()[frames().length - 1] as {
      total: { percent: number; daily_cap_usd: number }
      runs: readonly unknown[]
      repos: readonly unknown[]
      finished: readonly { pr_url: string | null; pr_number: number | null }[]
    }
    const live = SCREENS[0]?.alt ?? ''
    expect(live).toContain(String(last.total.percent))
    expect(live).toContain(String(last.total.daily_cap_usd))
    expect(live).toContain(COUNT_WORDS[last.runs.length] ?? '')
    expect(live).toContain(COUNT_WORDS[last.repos.length] ?? '')
    // The fixture *does* hold a finished run with a pull request: the absences
    // below are about the alt, not about the fixture.
    const pull = last.finished.find((row) => row.pr_number !== null)
    expect(pull?.pr_number).toBe(412)
    expect(live).not.toContain(`#${String(pull?.pr_number)}`)
    expect(live).not.toContain('pull request')
    expect(live).not.toContain('failed')
    expect(PICKER_SCREEN.alt).toContain('acme-platform')
    expect(PICKER_SCREEN.alt).toContain('acme-web')
    expect(PICKER_SCREEN.alt).toContain('acme-data')
    // The picker's own sentence is `viewOf`'s and `rowText`'s
    // (`src/renderer/installations.ts`), and the run seeds a *chosen*
    // installation: a sentence saying nothing is chosen would describe the
    // first-run state this run no longer launches.
    expect(PICKER_SCREEN.alt).toContain('is the one this console is showing')
    expect(PICKER_SCREEN.alt).not.toContain('none of them chosen')
    // **The picker's rows carry a machine-specific path, and the alt text does
    // not quote it.** The rows draw the fake's path inside this run's scratch
    // tree (`rowText`/`viewOf`, `src/renderer/installations.ts`), which is a
    // different path on every run and a platform's temporary one -- on macOS
    // under `/var/folders/...`. The picture is the owner's to publish or not
    // (`console/README.md` says so and says why); the sentence that travels with
    // it *describes* the path, because one naming
    // `/var/folders/.../fugaro-console-screenshots-XXXXXX/bin/fugaro` would be
    // stale the moment it was written (review of 2026-10-20, low finding).
    expect(PICKER_SCREEN.alt).not.toContain('/')
    expect(PICKER_SCREEN.alt).toContain('its own temporary directory')
    // The omission is a fact the manifest carries: a reader who expected the
    // owner's fifth picture finds the reason rather than a silence.
    expect(OMISSIONS.map((omission) => omission.file)).toEqual(['console-first-run.png'])
    expect(OMISSIONS[0]?.because).toMatch(/#751/)
  })

  it('asks for a capture the size the manifest declares', () => {
    // **Literals, not the expression that defines them.** The first version of
    // this test asserted `CAPTURE_PIXELS` equalled `CAPTURE_WINDOW.width *
    // CAPTURE_WINDOW.deviceScaleFactor` -- which is exactly what `screens.ts`
    // computes it from, so the assertion held whatever the numbers became and
    // the test could not fail (review of 2026-10-20, medium finding). The
    // numbers are written out here instead: 1280x800 CSS pixels at device scale
    // factor 2 is a 2560x1600 PNG, and a run that changed either half of that
    // would be changing the pictures the website draws.
    expect(CAPTURE_WINDOW.width).toBe(1280)
    expect(CAPTURE_WINDOW.height).toBe(800)
    expect(CAPTURE_WINDOW.deviceScaleFactor).toBe(2)
    expect(CAPTURE_PIXELS).toEqual({ width: 2560, height: 1600 })
  })
})

// ---------------------------------------------------------------------------
// 5. The blank-image check, on pictures whose pixels are known.
// ---------------------------------------------------------------------------

// A PNG encoder, for this file only. The check under test reads PNG bytes, and
// the only way to drive it with a picture whose pixels are *known* is to write
// one -- an image library is a dependency the console is not allowed to gain
// (`test/unit/pinning.test.ts`), and a real capture needs a window. It writes
// true PNGs: the signature, IHDR, the deflated scanlines with a filter per row,
// and a CRC per chunk, so what the decoder reads here is what it reads on disk.

const CRC_TABLE = ((): Uint32Array => {
  const table = new Uint32Array(256)
  for (let n = 0; n < 256; n++) {
    let c = n
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
    table[n] = c >>> 0
  }
  return table
})()

function crc32(bytes: Uint8Array): number {
  let c = 0xffffffff
  for (const byte of bytes) c = (CRC_TABLE[(c ^ byte) & 0xff] ?? 0) ^ (c >>> 8)
  return (c ^ 0xffffffff) >>> 0
}

function chunk(type: string, data: Uint8Array): Uint8Array {
  const out = new Uint8Array(12 + data.length)
  const view = new DataView(out.buffer)
  view.setUint32(0, data.length)
  for (let i = 0; i < 4; i++) out[4 + i] = type.charCodeAt(i)
  out.set(data, 8)
  const crcInput = out.subarray(4, 8 + data.length)
  view.setUint32(8 + data.length, crc32(crcInput))
  return out
}

/** png encodes one RGBA image. `filter` is the filter byte written on every
 * row, and `depth`/`interlace` exist so a test can write a PNG the decoder must
 * refuse. */
function png(options: {
  readonly width: number
  readonly height: number
  readonly pixel: (x: number, y: number) => readonly [number, number, number]
  readonly filter?: number
  readonly depth?: number
  readonly interlace?: number
  readonly colourType?: number
}): Uint8Array {
  const { width, height, pixel } = options
  const filter = options.filter ?? 0
  const channels = options.colourType === 2 ? 3 : 4
  const stride = width * channels
  // The bytes on a scanline are the *filter's output*, not the pixels: a row
  // written with filter 1 and absolute values decodes as the sum of its own
  // prefixes, which is a picture nobody drew. The two filters the tests write
  // are the two implemented here, and a third is refused rather than encoded as
  // one of them.
  if (filter !== 0 && filter !== 1) throw new Error(`this encoder writes filter 0 and 1, not ${String(filter)}`)
  const raw = new Uint8Array((stride + 1) * height)
  for (let y = 0; y < height; y++) {
    raw[y * (stride + 1)] = filter
    const base = y * (stride + 1) + 1
    // The pixel as the bytes a row stores it in: three colour channels, and the
    // opaque fourth the RGBA type carries.
    const valueAt = (x: number): readonly number[] => {
      const [r, g, b] = pixel(x, y)
      return [r, g, b, 255]
    }
    for (let x = 0; x < width; x++) {
      const value = valueAt(x)
      for (let c = 0; c < channels; c++) {
        const byte = value[c] ?? 0
        // Sub stores the difference from the **pixel** to the left, byte by
        // byte -- its value, not the difference already written there, which is
        // what a decoder sums its prefixes from. Filter 0 stores the byte.
        const left = filter === 1 && x > 0 ? ((valueAt(x - 1)[c] ?? 0) & 0xff) : 0
        raw[base + x * channels + c] = (byte - left) & 0xff
      }
    }
  }
  const ihdr = new Uint8Array(13)
  const view = new DataView(ihdr.buffer)
  view.setUint32(0, width)
  view.setUint32(4, height)
  ihdr[8] = options.depth ?? 8
  ihdr[9] = options.colourType ?? 6
  ihdr[10] = 0
  ihdr[11] = 0
  ihdr[12] = options.interlace ?? 0
  const parts = [
    new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', ihdr),
    chunk('IDAT', new Uint8Array(deflateSync(raw))),
    chunk('IEND', new Uint8Array([])),
  ]
  const total = parts.reduce((sum, part) => sum + part.length, 0)
  const out = new Uint8Array(total)
  let at = 0
  for (const part of parts) {
    out.set(part, at)
    at += part.length
  }
  return out
}

/** flat is a rectangle of one colour: what a window that painted nothing after
 * its ground would leave. */
function flat(width: number, height: number, colour: readonly [number, number, number]): Uint8Array {
  return png({ width, height, pixel: () => colour })
}

/** drawn is a rectangle with two colours on it -- a ground and ink -- which is
 * what any real drawing of the console is. */
function drawn(width: number, height: number): Uint8Array {
  return png({
    width,
    height,
    pixel: (x, y) => ((x + y) % 7 === 0 ? [235, 235, 245] : [16, 18, 24]),
  })
}

describe('the blank-image check', () => {
  it('reads a PNG back as its pixels', () => {
    const image = decodePng(png({ width: 3, height: 2, pixel: (x, y) => [x * 10, y * 10, 0] }))
    expect(image.width).toBe(3)
    expect(image.height).toBe(2)
    // Row-major RGBA: the pixel at (2, 1) is the sixth.
    const at = (1 * 3 + 2) * 4
    expect([...image.pixels.subarray(at, at + 4)]).toEqual([20, 10, 0, 255])
  })

  it('reverses a Sub-filtered scanline', () => {
    // Filter 1 stores each byte as its difference from its left neighbour, so a
    // decoder that ignored the filter would read a gradient as a black row.
    const bytes = png({
      width: 4,
      height: 2,
      filter: 1,
      pixel: (x) => [x * 20, x * 20, x * 20],
    })
    const image = decodePng(bytes)
    const at = (1 * 4 + 3) * 4
    expect([...image.pixels.subarray(at, at + 3)]).toEqual([60, 60, 60])
  })

  it('refuses a rectangle with nothing on it, and its spread is under the floor', () => {
    const bytes = flat(64, 64, [16, 18, 24])
    expect(pixelStdDev(decodePng(bytes))).toBeLessThan(BLANK_STDDEV_FLOOR)
    expect(() => assertUsableCapture(bytes, { width: 64, height: 64 }, 'console-live.png')).toThrow(
      /console-live\.png is blank/,
    )
  })

  it('accepts a rectangle with a drawing on it', () => {
    const bytes = drawn(64, 64)
    const image = assertUsableCapture(bytes, { width: 64, height: 64 }, 'console-live.png')
    expect(image.width).toBe(64)
    expect(pixelStdDev(image)).toBeGreaterThan(BLANK_STDDEV_FLOOR)
  })

  it('refuses a capture of the wrong size, naming both', () => {
    expect(() => assertUsableCapture(drawn(64, 64), { width: 128, height: 128 }, 'console-run.png')).toThrow(
      /console-run\.png is 64x64 pixels, and this run asked for 128x128/,
    )
  })

  it('refuses a file that is not a PNG, and one whose header it cannot read', () => {
    expect(() => decodePng(new Uint8Array([1, 2, 3]))).toThrow(/shorter than a PNG signature/)
    expect(() => decodePng(new Uint8Array(32))).toThrow(/does not start with the PNG signature/)
    expect(() => decodePng(png({ width: 8, height: 8, depth: 16, pixel: () => [0, 0, 0] }))).toThrow(/bit depth is 16/)
    expect(() => decodePng(png({ width: 8, height: 8, interlace: 1, pixel: () => [0, 0, 0] }))).toThrow(/interlaced/)
    expect(() => decodePng(png({ width: 8, height: 8, colourType: 0, pixel: () => [0, 0, 0] }))).toThrow(
      /colour type is 0/,
    )
  })

  it('reads a three-channel PNG as grey pixels', () => {
    // `page.screenshot` writes RGBA today, and a decoder that reads RGB is what
    // keeps a future Chromium that writes it from being graded as blank: the
    // two colour types are read, and a third is refused above.
    const image = decodePng(png({ width: 2, height: 2, colourType: 2, pixel: () => [10, 20, 30] }))
    expect([...image.pixels.subarray(0, 4)]).toEqual([10, 20, 30, 255])
  })
})

// ---------------------------------------------------------------------------
// 6. The line a failed run ends with, and the exit code that stands.
// ---------------------------------------------------------------------------

/** FakeRun is the stand-in `process` the failure handler is driven with: it
 * records the events it was asked to listen for, hands the reason to those
 * listeners the way `emit` would, and keeps the bytes and the exit code the run
 * would have left. */
interface FakeRun {
  readonly target: RunProcess
  readonly events: readonly string[]
  readonly written: () => string
  readonly exitCode: () => number | string | null | undefined
  readonly emit: (reason: unknown) => void
}

function fakeRun(): FakeRun {
  const listeners = new Map<string, ((reason: unknown) => void)[]>()
  let written = ''
  let exitCode: number | string | null | undefined
  const target: RunProcess = {
    on(event: string, listener: (reason: unknown) => void) {
      listeners.set(event, [...(listeners.get(event) ?? []), listener])
      return target
    },
    stderr: {
      write(chunk: string) {
        written += chunk
        return true
      },
    },
    get exitCode() {
      return exitCode
    },
    set exitCode(value: number | string | null | undefined) {
      exitCode = value
    },
  }
  return {
    target,
    get events() {
      return [...listeners.keys()]
    },
    written: () => written,
    exitCode: () => exitCode,
    emit: (reason: unknown) => {
      for (const listener of listeners.get('unhandledRejection') ?? []) listener(reason)
    },
  }
}

describe('the line a failed run ends with', () => {
  it('names the reason, and is a line of its own', () => {
    // The reason is Playwright's own sentence, passed through rather than
    // summarised: "Electron launch failed" is the one fact the owner needs.
    expect(failureLine(new Error('Process failed to launch!'))).toBe(
      '\nthe screenshot run failed: Process failed to launch!\n',
    )
    // Playwright does not always reject with an `Error`, and a rejection reason
    // that is a string must not be printed as an object.
    expect(failureLine('Target page, context or browser has been closed')).toContain(
      'the screenshot run failed: Target page, context or browser has been closed',
    )
    expect(failureLine(undefined)).toContain('the screenshot run failed: undefined')
    // Both newlines are load-bearing: Playwright's own stderr and this run's
    // report share the stream, and the sentence must not run into either.
    const line = failureLine(new Error('x'))
    expect(line.startsWith('\n')).toBe(true)
    expect(line.endsWith('\n')).toBe(true)
    expect(FAILURE_SENTENCE).toBe('the screenshot run failed: ')
  })

  it('turns an unhandled rejection into that sentence and a failed run', () => {
    // The failure this exists for: Node 22's default `--unhandled-rejections=
    // throw` ends the process on Playwright's first rejection, so `main()`'s own
    // catch never runs. The listener is on `unhandledRejection` -- not
    // `uncaughtException`, and not some event of this run's invention.
    const run = fakeRun()
    installFailureHandler(run.target)
    expect(run.events).toEqual(['unhandledRejection'])
    // Nothing is written, and nothing exits, before a rejection arrives.
    expect(run.written()).toBe('')
    expect(run.exitCode()).toBeUndefined()
    // A listener that threw would leave the process in exactly the state the
    // handler is here to prevent, so `emit` must come back.
    expect(() => {
      run.emit(new Error('Process failed to launch!'))
    }).not.toThrow()
    expect(run.written()).toBe('\nthe screenshot run failed: Process failed to launch!\n')
    expect(run.exitCode()).toBe(1)
  })

  it('does not let a recovered run report success', () => {
    // The launch retry (`test/support/launch.ts`, ETXTBSY) is the case: the
    // failed attempt rejects, the retry succeeds, and `main()` returns 0. The
    // run printed a failure line, so it does not exit 0 -- while a run that
    // failed on its own terms keeps its own code.
    const run = fakeRun()
    installFailureHandler(run.target)
    expect(exitCodeFor(0, run.target)).toBe(0)
    run.emit(new Error('spawn ETXTBSY'))
    expect(exitCodeFor(0, run.target)).toBe(1)
    expect(exitCodeFor(1, run.target)).toBe(1)
    const clean = fakeRun()
    expect(exitCodeFor(1, clean.target)).toBe(1)
    expect(exitCodeFor(0, clean.target)).toBe(0)
  })

  it('is wired into the entry point, on both of its failure paths', () => {
    // `capture.ts` launches Electron, so it is read here rather than imported --
    // and the finding this holds is that its own catch is not enough: a
    // rejection nobody awaited never reaches it. The handler is therefore
    // installed before the work starts, and the sentence is written in one
    // place (`failure.ts`) rather than two.
    const source = readFileSync(join(CONSOLE_ROOT, 'tools/screenshots', 'capture.ts'), 'utf8')
    expect(source).toContain('installFailureHandler(process)')
    expect(source).toContain('failureLine(error)')
    expect(source).toContain('exitCodeFor(code, process)')
    expect(source).not.toContain(FAILURE_SENTENCE)
  })
})

// ---------------------------------------------------------------------------
// 7. What the README and the shell script promise about the run, held to the
//    tree.
// ---------------------------------------------------------------------------

describe('what the run’s own documents promise about it', () => {
  /** readmeLines is `console/README.md` line by line: the claims below are about
   * what that document says and about how it renders. */
  function readmeLines(): readonly string[] {
    return readFileSync(join(CONSOLE_ROOT, 'README.md'), 'utf8').split('\n')
  }

  /** packageScripts is `package.json`'s `scripts`, read rather than imported:
   * the console's build is a JSON document, and the question here is what the
   * commands in it *do*. */
  function packageScripts(): Readonly<Record<string, string>> {
    const parsed = JSON.parse(readFileSync(join(CONSOLE_ROOT, 'package.json'), 'utf8')) as {
      scripts: Record<string, string>
    }
    return parsed.scripts
  }

  it('both build the console the run launches', () => {
    // **The run launches the built app** (`dist/main/index.js`, `package.json`'s
    // `main`), and `dist/` is git-ignored and untracked: a `screenshots` script
    // with no build in it launches a file that is not there on a fresh checkout,
    // or one from a branch ago on the machine of whoever runs it next. The
    // script the README offers from `console/` therefore builds first, exactly
    // as `test:e2e` does, and the build is *inside* the script rather than in a
    // caller: a second command documented as the same run has to be the same
    // run.
    const script = packageScripts()['screenshots'] ?? ''
    expect(script).toContain('npm run build')
    expect(script.indexOf('npm run build')).toBeLessThan(script.indexOf('capture.js'))
  })

  it('says so where it offers them', () => {
    // The finding this holds: the README offered `npm run screenshots` as "the
    // same thing" as the shell script while the two did different work, so the
    // second one launched a `dist/` the reader had no reason to have built.
    const readme = readmeLines().join('\n')
    expect(readme).toContain('npm run screenshots')
    expect(readme).toContain('it builds the console first')
  })

  it('lists every picture, and the omission, in one table', () => {
    // The finding this holds: the `console-first-run.png` row sat *after* two
    // paragraphs of prose, so it rendered as literal pipe text rather than as a
    // row of the table it belongs to. The rows are therefore required to be
    // contiguous -- five of them, nothing but table between the first and the
    // last.
    const lines = readmeLines()
    const rows = lines
      .map((line, index) => ({ line, index }))
      .filter((row) => /^\| `console-[a-z-]+\.png` \|/.test(row.line))
    expect(rows.map((row) => row.line.split('|')[1]?.trim())).toEqual([
      '`console-live.png`',
      '`console-run.png`',
      '`console-spend.png`',
      '`console-installations.png`',
      '`console-first-run.png`',
    ])
    const first = rows[0]?.index ?? 0
    const last = rows[rows.length - 1]?.index ?? 0
    for (const line of lines.slice(first, last + 1)) {
      expect(line.startsWith('|'), `${line} breaks the picture table in two`).toBe(true)
    }
  })

  it('says what the shell script reaches, because it is not nothing', () => {
    // The finding this holds: the script's header claimed "Nothing here writes
    // to the repository except `console/screenshots/`, and nothing here talks to
    // GitHub, the cloud, or the network" -- while the script runs `npm ci`
    // (which writes `console/node_modules/` and reads the npm registry) and the
    // launch fetches Electron's runtime on the first `require('electron')`.
    // A header that says the run touches nothing is a header a reader stops
    // checking.
    const script = readFileSync(join(CONSOLE_ROOT, '..', 'scripts', 'console-screenshots.sh'), 'utf8')
    expect(script).toContain('npm ci')
    expect(script).toContain('registry')
    expect(script).not.toContain('nothing here talks to GitHub')
  })
})

// ---------------------------------------------------------------------------
// 8. The selectors, held to the renderers that have to have them.
// ---------------------------------------------------------------------------

describe('the selectors the run waits for', () => {
  it('are the renderer’s own class names, not this run’s invention', () => {
    // The finding this holds: the four `drawn` selectors were strings in this
    // tool that nothing agreed with -- a renderer that renamed `.live-run` would
    // have turned the run into a `waitForSelector` timeout on the owner's Mac
    // (or, worse, a capture of a skeleton that never drew) instead of a failing
    // test here (review of 2026-10-20, low finding).
    const screens = [...SCREENS, PICKER_SCREEN]
    expect(screens.map((screen) => screen.file)).toEqual([
      'console-live.png',
      'console-run.png',
      'console-spend.png',
      'console-installations.png',
    ])
    for (const screen of screens) {
      const source = readFileSync(join(CONSOLE_ROOT, screen.drawnBy), 'utf8')
      // Every class or id in the selector -- `.live-run`, `#picker-list` -- is
      // looked for as a *quoted literal* in the file named beside it, because
      // that is how each renderer writes one: `const classes = ['live-run']`
      // (`live.ts`), `cell(list, factory, 'li', 'run-log-line')` (`run.ts`),
      // `make(factory, 'li', 'spend-point')` (`spend.ts`), and
      // `<ul id="picker-list">` (`installations.html`).
      const tokens = screen.drawn.match(/[.#][A-Za-z0-9_-]+/g) ?? []
      expect(tokens.length, `${screen.file}'s selector names no class and no id`).toBeGreaterThan(0)
      for (const token of tokens) {
        const name = token.slice(1)
        expect(
          source.includes(`'${name}'`) || source.includes(`"${name}"`),
          `${screen.drawnBy} does not name ${name}, which ${screen.file} waits for`,
        ).toBe(true)
      }
    }
  })
})

describe('the one scratch tree a run owns', () => {
  it('is fresh per run, under a temporary directory, and holds the five directories', () => {
    const first = newIsolation()
    const second = newIsolation()
    try {
      expect(first.scratch).not.toBe(second.scratch)
      expect(first.scratch.startsWith(tmpdir())).toBe(true)
      for (const directory of [first.home, first.xdgConfigHome, first.userData, first.bin, first.tmp]) {
        expect(statSync(directory).isDirectory(), `${directory} was not created`).toBe(true)
        // 0700: the store the run writes lives in one of these, and the
        // operator's own directories are the shape it is kept to.
        expect(statSync(directory).mode & 0o777).toBe(0o700)
      }
    } finally {
      rmSync(first.scratch, { recursive: true, force: true })
      rmSync(second.scratch, { recursive: true, force: true })
    }
  })
})
