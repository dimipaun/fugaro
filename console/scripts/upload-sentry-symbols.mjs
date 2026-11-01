// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The console's symbol upload: the native debug files of a packaged app and the
// main process's own JavaScript and source maps, sent to the project the crash
// reporter reports to, once per packaged build (the owner's ruling of
// 2026-11-01; design section 7.4). It is what turns a scrubbed frame -- a
// function name, an app-relative file and a line -- back into something a
// maintainer can open.
//
// **Dormant by default.** With `SENTRY_AUTH_TOKEN` unset or empty this prints
// one constant line and exits 0 **without invoking the CLI at all**: a
// checkout, a fork's CI and the console's own pull-request job all run the
// build, and none of them has a token or anything to upload to. A missing token
// is the normal state rather than a failure, which is why it is not an error
// and not a warning.
//
// **The token is never read, printed, logged, echoed or passed on an argv.**
// `sentry-cli` takes it from the environment, so this script only ever asks
// whether the variable is empty -- the one read it makes, and the one that
// decides the line above -- and then hands the CLI the environment it was
// started with. The process table is readable by any process of the same user,
// so an argv is not a place a credential may sit, and neither is a log line.
//
// **What it uploads, and from where.** The bundled app's native debug files
// (`debug-files upload`, which walks the bundle for the Electron framework and
// the app's native modules) and the built main process (`sourcemaps inject`
// writes a debug id into each file and its map, `sourcemaps upload` sends them).
// `console/dist/main` is what the packaged app runs, and the two halves are
// separate commands for that reason: the ids have to be in the files the
// archive carries, not only in the copy that was sent.
//
// **The region, the org and the release.** The org, the project and the region
// are constants here rather than environment: the console reports to one
// project, and a run that could point them elsewhere is a run that could send a
// customer's sources to somebody else's account. The release is the console's
// own version -- `SENTRY_SYMBOLS_RELEASE` when the caller knows it (the release
// workflow stamps the tag's version, which is what the app runs) and
// `console/package.json`'s otherwise. It is the *bare* version and not
// `@sentry/electron`'s own `name@version` default, because the scrubber drops
// any release that is not `crash.ts`'s `RELEASE_RE` shape -- and an `@` is not
// in it -- so a `name@version` tag would name a release no event can carry.
// (`crash.ts` carries the shape, and a later task that gives the event a release
// of its own has to keep the two spellings equal.)
//
// **It fails loudly and does nothing else.** The first command that exits
// non-zero ends the script with that exit code, so a build whose symbols did
// not go up is a red step in the release job rather than a run that looked
// fine. Nothing here is retried and nothing is swallowed: a symbol upload that
// silently failed shows up months later as an unreadable stack trace.

import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

/** The line a build with no token prints, and the whole of its output. The unit
 * test pins the exact words, and a maintainer greps the release log for them. */
const SKIP_LINE = 'Sentry symbol upload skipped: no auth token configured'

/** The project the console reports to, and the region its organization is in.
 * Constants rather than environment: see the header. */
const ORG = 'fugaro'
const PROJECT = 'fugaro-console'
const REGION_URL = 'https://de.sentry.io/'

/** The three overrides, each read once below. `SENTRY_SYMBOLS_CLI` is the CLI to
 * run (the pinned devDependency's own binary by default, which is also what
 * makes the script testable without a network), `SENTRY_SYMBOLS_BUNDLE` is the
 * packaged app to walk, and `SENTRY_SYMBOLS_RELEASE` is the version to tag the
 * upload with. */
const CLI_ENV = 'SENTRY_SYMBOLS_CLI'
const BUNDLE_ENV = 'SENTRY_SYMBOLS_BUNDLE'
const RELEASE_ENV = 'SENTRY_SYMBOLS_RELEASE'

/** The console's own directory, from this file's location rather than from the
 * process's working directory, so the script can run from anywhere. */
const CONSOLE_ROOT = fileURLToPath(new URL('..', import.meta.url))

/** Where `electron-builder` puts a `--mac --universal` pack (package.json's
 * `build.directories.output` is `release`): the bundle the release workflow has
 * just signed and stapled, and the one the debug files come from. */
const DEFAULT_BUNDLE = path.join(CONSOLE_ROOT, 'release', 'mac-universal')

/** The main process as `npm run build` leaves it: the JavaScript the archive
 * runs, and the maps beside it. */
const MAIN_JS_DIR = path.join(CONSOLE_ROOT, 'dist', 'main')

/** The version `console/package.json` declares, which is the version on `main`
 * when a console tag is cut. */
function packageVersion() {
  const pkg = JSON.parse(readFileSync(path.join(CONSOLE_ROOT, 'package.json'), 'utf8'))
  return String(pkg.version)
}

/** The three commands, in order, each a full argv array with the binary left
 * off. Built as data rather than written out at the call site so the unit test
 * can pin the exact arguments of all three. */
function commands({ bundle, release }) {
  const project = ['--org', ORG, '--project', PROJECT, '--release', release]
  return [
    ['debug-files', 'upload', ...project, bundle],
    ['sourcemaps', 'inject', ...project, MAIN_JS_DIR],
    ['sourcemaps', 'upload', ...project, MAIN_JS_DIR],
  ]
}

/** Answers the process's exit code. The environment is both where the three
 * overrides come from and what the CLI inherits -- which is the only way the
 * token reaches it. */
function run(env) {
  if ((env.SENTRY_AUTH_TOKEN ?? '').trim() === '') {
    console.log(SKIP_LINE)
    return 0
  }
  const cli = env[CLI_ENV] ?? path.join(CONSOLE_ROOT, 'node_modules', '.bin', 'sentry-cli')
  const release = env[RELEASE_ENV] ?? packageVersion()
  const bundle = env[BUNDLE_ENV] ?? DEFAULT_BUNDLE
  for (const argv of commands({ bundle, release })) {
    const { status } = spawnSync(cli, argv, {
      env: { ...env, SENTRY_ORG: ORG, SENTRY_PROJECT: PROJECT, SENTRY_URL: REGION_URL },
      stdio: 'inherit',
    })
    if (status !== 0) return status ?? 1
  }
  return 0
}

// The guard: importing this file runs nothing, so a test or a later script can
// read its constants without uploading anything.
if (process.argv[1] !== undefined && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exitCode = run(process.env)
}
