// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// `scripts/upload-sentry-symbols.mjs`, held to the two things that matter about
// it: that a build with no token does nothing at all, and that a build with one
// runs exactly three commands, with the arguments the crash reporter's frames
// need, and with the token in the environment rather than on an argv.
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
// The three commands are pinned whole rather than by shape. An argument that
// drifts -- org, project, release, one of the two directories -- is a release
// whose symbols are filed where nothing looks for them, which is a failure that
// only shows up as an unreadable stack trace in a crash report.

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
 * the test. */
function runScript(env: Record<string, string>): { readonly status: number | null; readonly stdout: string } {
  const result = spawnSync(process.execPath, [script], { env, encoding: 'utf8' })
  return { status: result.status, stdout: result.stdout }
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
})

describe('the symbol upload with a token', () => {
  it('runs the three commands, in order, with the exact arguments', () => {
    const dir = scratch()
    const { env, record } = uploadEnv(dir)
    const { status } = runScript(env)
    expect(status).toBe(0)
    const mainJs = path.join(consoleRoot, 'dist', 'main')
    expect(recorded(record).filter((line) => line.startsWith('argv:'))).toEqual([
      `argv: debug-files upload --org fugaro --project fugaro-console --release 9.9.9 ${path.join(dir, 'release', 'mac-universal')}`,
      `argv: sourcemaps inject --org fugaro --project fugaro-console --release 9.9.9 ${mainJs}`,
      `argv: sourcemaps upload --org fugaro --project fugaro-console --release 9.9.9 ${mainJs}`,
    ])
  })

  it('reaches the CLI with the token in its environment and nowhere else', () => {
    const dir = scratch()
    const { env, record } = uploadEnv(dir)
    runScript(env)
    const lines = recorded(record)
    expect(lines.filter((line) => line.startsWith('token_in_env:'))).toEqual([
      'token_in_env:yes',
      'token_in_env:yes',
      'token_in_env:yes',
    ])
    // The other half, and the one the process table would expose: no operand of
    // any of the three commands equals or contains the token.
    const argv = lines.filter((line) => line.startsWith('argv:'))
    expect(argv).toHaveLength(3)
    for (const line of argv) expect(line).not.toContain(PLACEHOLDER_TOKEN)
  })

  it('sets the org, the project and the EU region beside the token', () => {
    const dir = scratch()
    const { env, record } = uploadEnv(dir)
    runScript(env)
    expect(recorded(record).filter((line) => line.startsWith('org:'))).toEqual([
      'org:fugaro project:fugaro-console url:https://de.sentry.io/',
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
    for (const line of recorded(record).filter((one) => one.startsWith('argv:'))) {
      expect(line).toContain(`--release ${declared}`)
    }
  })

  it('stops at the first failure and exits with the CLI’s own code', () => {
    const dir = scratch()
    const { env, record } = uploadEnv(dir, 3)
    const { status } = runScript(env)
    expect(status).toBe(3)
    // And it stopped there: the two commands after the failing one never ran, so
    // a release never uploads maps for a build whose debug files did not go up.
    expect(recorded(record).filter((line) => line.startsWith('argv:'))).toEqual([
      `argv: debug-files upload --org fugaro --project fugaro-console --release 9.9.9 ${path.join(dir, 'release', 'mac-universal')}`,
    ])
  })
})
