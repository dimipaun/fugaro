// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// `scripts/upload-sentry-symbols.mjs`, held to the three things that matter
// about it: that a run with no token does nothing at all, that the `inject` run
// stamps `dist/main` and nothing else, and that the default run uploads what the
// stamp wrote -- each command whole, with the arguments the crash reporter's
// frames need, and with the token in the environment rather than on an argv.
//
// **Two runs, and their order is the release's.** `inject` rewrites
// `console/dist/main` in place, so it has to run after `npm run build` writes
// those files and before `electron-builder` copies them into the app: a build
// that rebuilds between the two runs throws the ids away, and a pack that
// happens before the first one ships an app with no id in it. The upload run
// comes after the pack, because `debug-files upload` reads the bundle. This file
// pins what each run *runs*; the release workflow's own test pins where the two
// runs sit.
//
// **The CLI is faked, and the fake writes down what it was handed.** It is a
// `/bin/sh` script in a temporary directory that appends its own argv to a file
// and exits with a code the test chose, so the assertions are about what a real
// process would have received -- the argv as an operand list, and whether the
// token was in its environment -- rather than about the script's own idea of
// them. It never receives a real token and never prints one: the test sets the
// variable to a placeholder to take the upload path, and what the fake records
// is whether a non-empty value was there, which is the fact this file is about.
//
// The commands are pinned whole rather than by shape. An argument that drifts --
// org, project, release, one of the two directories -- is a release whose
// symbols are filed where nothing looks for them, which is a failure that only
// shows up as an unreadable stack trace in a crash report.

import { spawnSync } from 'node:child_process'
import { chmodSync, existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { afterEach, describe, expect, it } from 'vitest'

/** The script under test, and the console directory whose `dist/main` and
 * `package.json` its own arguments are resolved against. */
const script = fileURLToPath(new URL('../../scripts/upload-sentry-symbols.mjs', import.meta.url))
const consoleRoot = fileURLToPath(new URL('../..', import.meta.url))

/** The one line a build with no token prints. Spelled here rather than imported:
 * the string is the claim, and a test that read it from the script would follow
 * the script wherever it went. */
const SKIP_LINE = 'Sentry symbol upload skipped: no auth token configured'

/** The value that stands in for a token. It is not secret-shaped and is never
 * sent anywhere: `SENTRY_AUTH_TOKEN` is read by nothing but the fake, which
 * records whether it was set and never what it was. */
const PLACEHOLDER_TOKEN = 'placeholder'

/** Every temporary directory this file made, removed after each test. */
const made: string[] = []

afterEach(() => {
  while (made.length > 0) rmSync(made.pop() as string, { recursive: true, force: true })
})

/** A fresh temporary directory, registered for cleanup. */
function scratch(): string {
  const dir = mkdtempSync(path.join(tmpdir(), 'sentry-symbols-'))
  made.push(dir)
  return dir
}

/** The fake `sentry-cli`: records its argv, whether the token was in its
 * environment and the three variables the script sets beside it, then exits with
 * `code`. */
function fakeCli(dir: string, code: number): string {
  const file = path.join(dir, 'sentry-cli')
  writeFileSync(
    file,
    [
      '#!/bin/sh',
      '{',
      '  printf "argv:"',
      '  for arg in "$@"; do printf " %s" "$arg"; done',
      '  printf "\\n"',
      '  if [ -n "${SENTRY_AUTH_TOKEN:-}" ]; then printf "token_in_env:yes\\n"; else printf "token_in_env:no\\n"; fi',
      '  printf "org:%s project:%s url:%s\\n" "${SENTRY_ORG:-}" "${SENTRY_PROJECT:-}" "${SENTRY_URL:-}"',
      '} >> "$FAKE_RECORD"',
      `exit ${code}`,
      '',
    ].join('\n'),
    'utf8',
  )
  chmodSync(file, 0o755)
  return file
}

/** Runs the script with exactly the environment given -- never the suite's own,
 * which would put whatever `SENTRY_AUTH_TOKEN` a machine happened to carry into
 * the test -- and with the arguments given, so one helper covers both runs. */
function runScript(
  env: Record<string, string>,
  args: readonly string[] = [],
): { readonly status: number | null; readonly stdout: string; readonly stderr: string } {
  const result = spawnSync(process.execPath, [script, ...args], { env, encoding: 'utf8' })
  return { status: result.status, stdout: result.stdout, stderr: result.stderr }
}

/** The lines the fake recorded, in order, or [] when it was never run. */
function recorded(file: string): readonly string[] {
  if (!existsSync(file)) return []
  return readFileSync(file, 'utf8').trimEnd().split('\n').filter((line) => line !== '')
}

/** The environment that takes the upload path, with the fake CLI in it, and the
 * file the same fake records into. The two are returned together because the
 * record path is what the assertions read and it is not something the script
 * under test should be asked for. */
function uploadEnv(dir: string, code = 0, extra: Record<string, string> = {}): { readonly env: Record<string, string>; readonly record: string } {
  const record = path.join(dir, 'record')
  return {
    env: {
      PATH: process.env.PATH ?? '',
      SENTRY_AUTH_TOKEN: PLACEHOLDER_TOKEN,
      SENTRY_SYMBOLS_CLI: fakeCli(dir, code),
      SENTRY_SYMBOLS_BUNDLE: path.join(dir, 'release', 'mac-universal'),
      SENTRY_SYMBOLS_RELEASE: '9.9.9',
      FAKE_RECORD: record,
      ...extra,
    },
    record,
  }
}

describe('the symbol upload with no token', () => {
  it('prints one line, exits 0 and never invokes the CLI', () => {
    const dir = scratch()
    // The fake is in the environment, so "the CLI was never invoked" is a claim
    // about a CLI that was reachable rather than about one that was missing.
    const record = path.join(dir, 'record')
    const env = { PATH: process.env.PATH ?? '', SENTRY_SYMBOLS_CLI: fakeCli(dir, 0), FAKE_RECORD: record }
    const { status, stdout } = runScript(env)
    expect(status).toBe(0)
    expect(stdout).toBe(`${SKIP_LINE}\n`)
    expect(recorded(record)).toEqual([])
  })

  it('treats an empty or whitespace-only token as absent', () => {
    for (const token of ['', '   ']) {
      const dir = scratch()
      const record = path.join(dir, 'record')
      const env = { PATH: process.env.PATH ?? '', SENTRY_AUTH_TOKEN: token, SENTRY_SYMBOLS_CLI: fakeCli(dir, 0), FAKE_RECORD: record }
      const { status, stdout } = runScript(env)
      expect(status, `a token of ${JSON.stringify(token)} must be the absent case`).toBe(0)
      expect(stdout).toBe(`${SKIP_LINE}\n`)
      expect(recorded(record)).toEqual([])
    }
  })

  it('is dormant in the inject run as well, printing the same line', () => {
    const dir = scratch()
    const record = path.join(dir, 'record')
    const env = { PATH: process.env.PATH ?? '', SENTRY_SYMBOLS_CLI: fakeCli(dir, 0), FAKE_RECORD: record }
    const { status, stdout } = runScript(env, ['inject'])
    expect(status).toBe(0)
    expect(stdout).toBe(`${SKIP_LINE}\n`)
    // The inject rewrites files in place and needs no credential -- `sentry-cli
    // sourcemaps inject` never reaches the network -- but a build with no token
    // is a build nobody asked for symbols from, so it is skipped with the same
    // line rather than run. That is the state a checkout, a fork's CI and the
    // warm-up pack of the release job are in.
    expect(recorded(record)).toEqual([])
  })
})

describe('the inject run', () => {
  it('runs one command, the inject, on the built main process, and nothing else', () => {
    const dir = scratch()
    const { env, record } = uploadEnv(dir)
    const { status } = runScript(env, ['inject'])
    expect(status).toBe(0)
    const mainJs = path.join(consoleRoot, 'dist', 'main')
    // One command, and no upload: this run is the one that happens *before* the
    // pack, when the bundle `debug-files upload` reads does not exist yet, and
    // an upload here would file maps for a build the app has not been made from.
    expect(recorded(record).filter((line) => line.startsWith('argv:'))).toEqual([
      `argv: sourcemaps inject --org fugaro --project fugaro-console --release 9.9.9 ${mainJs}`,
    ])
  })

  it('reaches the CLI with the token in its environment and nowhere else', () => {
    const dir = scratch()
    const { env, record } = uploadEnv(dir)
    runScript(env, ['inject'])
    const lines = recorded(record)
    expect(lines.filter((line) => line.startsWith('token_in_env:'))).toEqual(['token_in_env:yes'])
    const argv = lines.filter((line) => line.startsWith('argv:'))
    expect(argv).toHaveLength(1)
    for (const line of argv) expect(line).not.toContain(PLACEHOLDER_TOKEN)
  })

  it('refuses an argument that is not `inject`, without running the CLI', () => {
    // A typo would otherwise run the *other* run: `inject` misspelled is the
    // upload, which in the release job's first step would read a bundle that has
    // not been packed yet -- or, in a checkout, upload maps nobody asked for.
    for (const args of [['Inject'], ['upload'], ['--inject'], ['inject', 'extra']]) {
      const dir = scratch()
      const record = path.join(dir, 'record')
      const { env } = uploadEnv(dir)
      const { status, stdout, stderr } = runScript(env, args)
      expect(status, `${args.join(' ')} must be refused`).not.toBe(0)
      expect(stdout).toBe('')
      expect(stderr).toContain('inject')
      expect(recorded(record)).toEqual([])
    }
  })
})

describe('the symbol upload with a token', () => {
  it('runs the two uploads, in order, with the exact arguments', () => {
    const dir = scratch()
    const { env, record } = uploadEnv(dir)
    const { status } = runScript(env)
    expect(status).toBe(0)
    const mainJs = path.join(consoleRoot, 'dist', 'main')
    expect(recorded(record).filter((line) => line.startsWith('argv:'))).toEqual([
      // No `--release` on the debug-files line: `debug-files upload` does not
      // take one (3.8.2 refuses the argument outright, exit 2, which would stop
      // the upload before the maps went up), and native debug files are keyed by
      // the debug id inside them rather than by a release.
      `argv: debug-files upload --org fugaro --project fugaro-console ${path.join(dir, 'release', 'mac-universal')}`,
      `argv: sourcemaps upload --org fugaro --project fugaro-console --release 9.9.9 ${mainJs}`,
    ])
  })

  it('uploads and never injects: the stamp is the other run, before the pack', () => {
    // The mutation this catches: `inject` back in this list. Injecting here
    // rewrites the same files the upload has just read, after the app has been
    // packed from them -- so the ids would go into a build that no longer exists
    // and the maps that went up would describe neither the app nor the next
    // build.
    const dir = scratch()
    const { env, record } = uploadEnv(dir)
    runScript(env)
    expect(recorded(record).filter((line) => line.includes('sourcemaps inject'))).toEqual([])
  })

  it('reaches the CLI with the token in its environment and nowhere else', () => {
    const dir = scratch()
    const { env, record } = uploadEnv(dir)
    runScript(env)
    const lines = recorded(record)
    expect(lines.filter((line) => line.startsWith('token_in_env:'))).toEqual([
      'token_in_env:yes',
      'token_in_env:yes',
    ])
    // The other half, and the one the process table would expose: no operand of
    // either command equals or contains the token.
    const argv = lines.filter((line) => line.startsWith('argv:'))
    expect(argv).toHaveLength(2)
    for (const line of argv) expect(line).not.toContain(PLACEHOLDER_TOKEN)
  })

  it('sets the org, the project and the EU region beside the token', () => {
    const dir = scratch()
    const { env, record } = uploadEnv(dir)
    runScript(env)
    expect(recorded(record).filter((line) => line.startsWith('org:'))).toEqual([
      'org:fugaro project:fugaro-console url:https://de.sentry.io/',
      'org:fugaro project:fugaro-console url:https://de.sentry.io/',
    ])
  })

  it('tags the upload with console/package.json’s version when nothing names one', () => {
    const dir = scratch()
    const declared = String((JSON.parse(readFileSync(path.join(consoleRoot, 'package.json'), 'utf8')) as { version: unknown }).version)
    const { env, record } = uploadEnv(dir)
    delete env.SENTRY_SYMBOLS_RELEASE
    runScript(env)
    // The command that takes a release, and the one that does not:
    // `debug-files upload` matches by the debug id inside the files it is
    // handed, so the version names nothing there and the CLI refuses the
    // argument.
    const argv = recorded(record).filter((one) => one.startsWith('argv:'))
    expect(argv.filter((line) => line.includes('sourcemaps'))).toHaveLength(1)
    for (const line of argv.filter((one) => one.includes('sourcemaps'))) {
      expect(line).toContain(`--release ${declared}`)
    }
    for (const line of argv.filter((one) => one.includes('debug-files'))) {
      expect(line).not.toContain('--release')
    }
  })

  it('tags the inject run with the same release, read the same way', () => {
    // The stamp and the upload have to agree: the id `inject` writes into the
    // file and the map is looked up under the release the map was filed under,
    // so a run that read one version and a run that read another would file
    // maps no event can reach.
    const dir = scratch()
    const declared = String((JSON.parse(readFileSync(path.join(consoleRoot, 'package.json'), 'utf8')) as { version: unknown }).version)
    const { env, record } = uploadEnv(dir)
    delete env.SENTRY_SYMBOLS_RELEASE
    runScript(env, ['inject'])
    expect(recorded(record).filter((line) => line.startsWith('argv:'))).toEqual([
      `argv: sourcemaps inject --org fugaro --project fugaro-console --release ${declared} ${path.join(consoleRoot, 'dist', 'main')}`,
    ])
  })

  it('stops at the first failure and exits with the CLI’s own code', () => {
    const dir = scratch()
    const { env, record } = uploadEnv(dir, 3)
    const { status } = runScript(env)
    expect(status).toBe(3)
    // And it stopped there: the command after the failing one never ran, so a
    // release never uploads maps for a build whose debug files did not go up.
    expect(recorded(record).filter((line) => line.startsWith('argv:'))).toEqual([
      `argv: debug-files upload --org fugaro --project fugaro-console ${path.join(dir, 'release', 'mac-universal')}`,
    ])
  })
})
