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
// walks the configured path (nothing here -- no config file is read at startup),
// then `WELL_KNOWN_BINARY_PREFIXES`, then every absolute entry of the app's own
// `PATH`. This module therefore does two things:
//
//   - copies `test/fixtures/bin/fugaro` into a scratch directory and returns the
//     environment that makes it the winner -- `PATH` is that directory and
//     nothing else, and `HOME` is a scratch home whose `~/.local/bin` holds the
//     same stub, because `~/.local/bin` is one of the well-known prefixes and
//     the operator's real one would otherwise be searched first;
//   - offers `resolveConsoleBinary(env)`, which runs the console's own
//     `resolveBinary` with **exactly the arguments `src/main/index.ts` uses**, so
//     a spec can ask the same question the app is about to ask and find out
//     which binary will actually answer. That is what makes the specs honest on
//     a machine with a real `fugaro` in `/usr/local/bin`: that one wins over any
//     `PATH` entry, and a spec that assumed otherwise would be asserting a fact
//     about the machine rather than about the console.
//
// **What this cannot fix, stated plainly.** On a machine whose `/usr/local/bin`
// or `/opt/homebrew/bin` holds a real `fugaro` outside the supported range, the
// app will refuse it and the specs that need a dashboard document will fail --
// because the app is doing exactly what D7 says. CI has no such binary, so the
// stub wins there; a developer who hits this is being told something true about
// their machine, and `resolveConsoleBinary` below is what lets a spec say so
// instead of failing on a missing element.

import { chmodSync, copyFileSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { INSTALLATIONS_FILE_NAME, INSTALLATION_FILE_VERSION, serializeInstallationFile } from '../../src/main/installations'
import { privateWorkingDirectory, resolveBinary } from '../../src/main/spawn'
import type { BinaryResolution, SpawnTarget } from '../../src/main/spawn'
import { CLI_VERSION_BUILT_AGAINST } from '../../src/shared/version'

/** CLI_STUB is the fixture this module installs: a one-line POSIX script that
 * prints the `version.txt` beside it and refuses any argv but `version`. */
const CLI_STUB = join(__dirname, '..', 'fixtures', 'bin', 'fugaro')

/** HANDSHAKE_TARGET is the target the startup read runs with, copied from
 * `src/main/index.ts`: no project, no configured path, so the resolution is the
 * well-known prefixes and the app's `PATH` and nothing else. */
const HANDSHAKE_TARGET: SpawnTarget = Object.freeze({ project: '' })

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
  /** userData is a scratch `--user-data-dir` (Task 3.4b): the store the app
   * reads `installations.json` from. Every launch passes it (`launchArgs`), so
   * no spec reads or writes the machine's own console profile. It starts
   * empty, which is D13's first run: no installation, and the picker opens. */
  readonly userData: string
  /** launchArgs is `electron.launch`'s `args`: the app directory, and the
   * scratch `--user-data-dir`. */
  launchArgs(consoleDir: string): string[]
  /** seedActiveInstallation writes one installation naming this stub as the
   * active one, so the handshake's configured path is the stub (rule 4 puts it
   * first, so it wins even over a machine's own `fugaro`) and the picker does
   * not open. It is for the specs whose subject is the dashboard, not the first
   * run. */
  seedActiveInstallation(project?: string): void
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
  for (const where of [dir, wellKnown]) {
    mkdirSync(where, { recursive: true })
    copyFileSync(CLI_STUB, join(where, 'fugaro'))
    // The fixture is committed executable; this is for a checkout that lost the
    // bit (a zip download, a Windows-authored copy), because `isExecutableFile`
    // is what decides whether the resolution will look at it at all.
    chmodSync(join(where, 'fugaro'), 0o755)
  }
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
      return [consoleDir, `--user-data-dir=${userData}`]
    },
    seedActiveInstallation(project = 'e2e'): void {
      const id = '0123456789abcdef'
      writeFileSync(
        join(userData, INSTALLATIONS_FILE_NAME),
        serializeInstallationFile({
          version: INSTALLATION_FILE_VERSION as 1,
          active: id,
          installations: [{ id, project, binaryPath: join(dir, 'fugaro') }],
        }),
        { mode: 0o600 },
      )
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
 */
export async function resolveConsoleBinary(env: NodeJS.ProcessEnv): Promise<BinaryResolution> {
  return resolveBinary({
    appEnv: env,
    directory: privateWorkingDirectory(),
    target: HANDSHAKE_TARGET,
  })
}
