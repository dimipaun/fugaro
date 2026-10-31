// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The build step that puts the crash reporter's DSN into the build, and the one
// place the release build's variable is named (desktop-console plan Task 2.1b,
// and the owner's ruling of 2026-10-30; design section 7.4 does not describe
// this yet and Task 2.2 amends it).
//
// **Why the DSN has to be written rather than read.** The running console must
// never be handed the DSN by its environment: the app is launched by a person
// from a launcher, a Dock or a menu, with whatever environment that gives it,
// and the value the processor is reached at would then be a setting on the
// machine rather than a property of the build. It is also the one thing in this
// feature that must not appear in the repository -- not in a source file, not in
// a test, not in a committed artifact -- so the build's own output is the only
// place left for it. `npm run build` runs this after `tsc`, and
// `src/main/crash-electron.ts` reads what it wrote.
//
// **What it writes, and what it writes when there is nothing to write.** With
// `FUGARO_CONSOLE_CRASH_DSN` set, `dist/main/crash-dsn.json` holding that one
// value. With it unset or blank -- every developer build, every CI run, every
// release the owner has not given a DSN to -- **no artifact at all**, and any
// artifact an earlier build left in the same tree is removed. That removal is
// the part worth being explicit about: a stale artifact would ship one build's
// DSN inside a bundle whose builder believes reporting is dormant, which is the
// opposite of what "no variable" is supposed to mean. A build without the
// variable is a dormant console, and dormant has to be a property of the build
// rather than of the directory it was built in.
//
// **What it deliberately does not do.** It does not reach the network, so it
// cannot check the DSN, and it does not log the value. A build that reported the
// value back would put it in a build log, which is a place a credential does not
// belong.

import { mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'

/** CRASH_DSN_VARIABLE is the release build's variable, spelled once in this
 * repository and nowhere else. The release workflow sets it on the build that
 * ships; the app never reads it. */
export const CRASH_DSN_VARIABLE = 'FUGARO_CONSOLE_CRASH_DSN'

/** DSN_ARTIFACT_PATH is where the value lands, relative to the console's
 * directory: inside the compiled main process, which is what `build.files` packs
 * and what `src/main/crash-electron.ts` reads back. `dist/` is gitignored, so
 * this is a build artifact in the same sense `dist/main/index.js` is. */
export const DSN_ARTIFACT_PATH = ['dist', 'main', 'crash-dsn.json'] as const

/** writeCrashDsnArtifact writes the artifact, or removes the one an earlier
 * build left. It answers which of the two it did, so the caller can say so
 * without the value ever reaching a log. */
export function writeCrashDsnArtifact(consoleDir: string, dsn: string | undefined): 'written' | 'removed' {
  const artifact = join(consoleDir, ...DSN_ARTIFACT_PATH)
  if (dsn === undefined || dsn.trim() === '') {
    rmSync(artifact, { force: true })
    return 'removed'
  }
  mkdirSync(dirname(artifact), { recursive: true })
  // Owner-only, which is what a file holding one build's endpoint deserves even
  // though `dist/` is on a build machine.
  writeFileSync(artifact, `${JSON.stringify({ dsn })}\n`, { mode: 0o600 })
  return 'written'
}

/** consoleRootHere is the package `npm run build` runs in, checked rather than
 * guessed: `package.json`'s name is the one thing that says a directory is this
 * console, so a build started somewhere else refuses instead of writing a DSN
 * beside whatever it found -- the same check the bundler makes, for the same
 * reason. */
export function consoleRootHere(cwd: string): string {
  let pkg: { name?: unknown }
  try {
    pkg = JSON.parse(readFileSync(join(cwd, 'package.json'), 'utf8')) as { name?: unknown }
  } catch {
    throw new Error(`${cwd} is not the console package: npm run build runs from console/`)
  }
  if (pkg.name !== 'fugaro-console') {
    throw new Error(`${cwd} is not the console package: npm run build runs from console/`)
  }
  return cwd
}

/** isEntryPoint is true when this file was run rather than imported: `argv[1]`
 * is this module, compiled to `index.js` under `.crash-dsn/`. A test importing
 * it -- vitest's entry is `argv[1]` there -- writes nothing. */
export function isEntryPoint(argv1: string | undefined): boolean {
  return argv1 !== undefined && /crash-dsn[\\/]index\.(?:js|cjs|ts)$/.test(argv1)
}

if (isEntryPoint(process.argv[1])) {
  try {
    const outcome = writeCrashDsnArtifact(consoleRootHere(process.cwd()), process.env[CRASH_DSN_VARIABLE])
    // What was done, never the value: a build log is a place a DSN does not
    // belong, and this step is the only one that ever holds one.
    process.stdout.write(
      outcome === 'written'
        ? `crash reporting: ${CRASH_DSN_VARIABLE} was baked into ${DSN_ARTIFACT_PATH.join('/')}\n`
        : `crash reporting: no ${CRASH_DSN_VARIABLE}, so this build carries no DSN\n`,
    )
  } catch (error: unknown) {
    const message = error instanceof Error ? error.message : String(error)
    process.stderr.write(`${message}\n`)
    process.exitCode = 2
  }
}
