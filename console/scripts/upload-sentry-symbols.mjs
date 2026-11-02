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
// **Two runs, and the pack sits between them.** `inject` is the first: it
// rewrites each file of `console/dist/main` and the map beside it with a debug
// id, and it has to happen after `npm run build`, which writes those files, and
// before `electron-builder` copies them into the app, which is what makes the id
// part of what ships. `npm run package` carries it between its own build and its
// packer for that reason, and the release job's build step runs it as
// `node scripts/upload-sentry-symbols.mjs inject` -- the step the app is then
// packed from, with the packer's own `npm run package:targets`, because a
// *rebuild* between the two runs would throw the ids away.
//
// The second run, with no argument, uploads what the first one stamped. The
// bundled app's native debug files (`debug-files upload`, which walks the bundle
// for the Electron framework and the app's native modules) and the built main
// process (`sourcemaps upload`, which sends the files `inject` stamped and their
// maps). It is one command per half and not one for both, because the id has to
// be in the file as well as in the map, and the unpacked maps are what the
// upload takes: source maps are deliberately *not* packed into the app (see
// `build.files` in `console/package.json`, which carries `dist/main/**/*.js` and
// no `.map`), so this run reads them from the build tree after the pack rather
// than out of the bundle.
//
// **What the two runs make true, and the piece that is still missing.** A
// symbolicated JavaScript frame needs the app's files to carry the debug id the
// uploaded map carries, the map to be filed under the release the event names,
// and the event to say which image it was thrown from. The first two are this
// script's and they now hold: the ids are in the app as it ships, and the maps
// go up under the console's own version. The third is the console's, and it now
// holds too -- every event carries that release (`crash-electron.ts` stamps it
// with the app's own version, the spelling this script uploads under) and keeps
// the debug image a symbolicator looks up (`crash.ts` reduces `debug_meta.images`
// to an id and an app-relative code file). **What nothing produces yet is the
// image itself**: the pinned SDK fills `debug_meta` from
// `globalThis._sentryDebugIds`, which the `@sentry/*` bundler shims write into a
// *bundle*, and this console's main process is unbundled `tsc` output -- so an
// event carries the release and no image, and a later task that gives the main
// process a debug-id runtime is what closes the last step. The native debug
// files are not that step: they would be read by a minidump's frames, and this
// console installs no minidump integration -- it sends stack frames, not process
// memory.
//
// **The two halves are keyed differently.** The source maps go up under the
// console's version -- that is what the two `sourcemaps` commands take the
// release for. The native debug files do not: `debug-files upload` matches them
// by the debug id inside each file, which is what a minidump's frames carry, so
// the directory is all it needs and a release names nothing there.
//
// **The region, the org and the release.** The org, the project and the region
// are constants here rather than environment: the console reports to one
// project, and a run that could point them elsewhere is a run that could send a
// customer's sources to somebody else's account. The release is the console's
// own version, read from `console/package.json` -- or from
// `SENTRY_SYMBOLS_RELEASE`, which a caller that knows the version another way
// may set -- and it names the `sourcemaps` commands and nothing else: the debug
// files are matched by their debug ids. The release job sets neither override:
// it refuses a tag whose X.Y.Z is not `console/package.json`'s `version`, so the
// file on its tree is the tag's version, and the file is what the app was
// stamped with. It is the *bare* version and not
// `@sentry/electron`'s own `name@version` default, because the scrubber drops
// any release that is not `crash.ts`'s `RELEASE_RE` shape -- and an `@` is not
// in it -- so a `name@version` tag would name a release no event can carry.
// **Both sides of the lookup read it the same way, and that is the property the
// two files have to keep equal**: the event's release is the app's own version
// (`app.getVersion()`, which electron-builder stamps from the same tag), and
// this script's is `console/package.json`'s -- the same string, on the same
// tree, because the release job refuses any tag where they differ.
//
// **It fails loudly and does nothing else.** The first command that exits
// non-zero ends the script with that exit code, so a build whose symbols did
// not go up is a red step in the release job rather than a run that looked
// fine. Nothing here is retried and nothing is swallowed: a symbol upload that
// silently failed shows up months later as an unreadable stack trace. The one
// argument it takes is the mode -- `inject` for the run before the pack, nothing
// for the uploads after it -- and anything else is refused *before* the CLI is
// reached, because a misspelled `inject` would otherwise run the uploads, in a
// build that has not been packed yet.

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

/** The one argument, and the line an argument that is not it prints. The mode is
 * read from `process.argv` rather than from the environment: it is part of what
 * the caller asked for, the way the other three are configuration this script
 * may default. */
const INJECT = 'inject'
const USAGE_LINE = `usage: upload-sentry-symbols.mjs [${INJECT}]`

/** The three overrides, each read once below. `SENTRY_SYMBOLS_CLI` is the CLI to
 * run (the pinned devDependency's own binary by default, which is also what
 * makes the script testable without a network), `SENTRY_SYMBOLS_BUNDLE` is the
 * packaged app to walk, and `SENTRY_SYMBOLS_RELEASE` is the version the
 * `sourcemaps` commands upload under (the debug files are keyed by their own
 * debug ids, so no version names them). */
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

/** The commands one run makes, in order, each a full argv array with the binary
 * left off. Built as data rather than written out at the call site so the unit
 * test can pin the exact arguments of every one of them.
 *
 * **The stamp and the uploads do not take the same arguments.** `debug-files
 * upload` takes the org, the project and the directory, and **no `--release`**:
 * the pinned `@sentry/cli` 3.8.2 refuses that argument outright (exit 2, before
 * anything is uploaded), and it has nothing to name -- a native debug file is
 * matched by the debug id inside it, which is what a minidump's frames carry,
 * not by a release. The `sourcemaps` commands do take one, and the inject and
 * the upload of a release have to read the *same* version: the id the first
 * writes is the id the second files a map under. */
function commands({ bundle, release }, mode) {
  const project = ['--org', ORG, '--project', PROJECT, '--release', release]
  if (mode === INJECT) return [['sourcemaps', INJECT, ...project, MAIN_JS_DIR]]
  return [
    ['debug-files', 'upload', '--org', ORG, '--project', PROJECT, bundle],
    ['sourcemaps', 'upload', ...project, MAIN_JS_DIR],
  ]
}

/** The mode the arguments ask for, or `null` with the exit code of a refusal.
 * An argument that is not a mode is refused rather than ignored: the two runs
 * write and read the same directory, and the one that runs *before* the app is
 * packed cannot be the one that reads the packaged bundle. */
function modeFrom(argv) {
  const asked = argv.slice(2)
  if (asked.length === 0) return { mode: null, status: 0 }
  if (asked.length === 1 && asked[0] === INJECT) return { mode: INJECT, status: 0 }
  console.error(`${USAGE_LINE}: ${asked.join(' ')} is not a mode this script has`)
  return { mode: null, status: 2 }
}

/** Answers the process's exit code. The environment is both where the three
 * overrides come from and what the CLI inherits -- which is the only way the
 * token reaches it. */
function run(env, mode) {
  if ((env.SENTRY_AUTH_TOKEN ?? '').trim() === '') {
    console.log(SKIP_LINE)
    return 0
  }
  const cli = env[CLI_ENV] ?? path.join(CONSOLE_ROOT, 'node_modules', '.bin', 'sentry-cli')
  const release = env[RELEASE_ENV] ?? packageVersion()
  const bundle = env[BUNDLE_ENV] ?? DEFAULT_BUNDLE
  for (const argv of commands({ bundle, release }, mode)) {
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
  // The mode is read before the token is, so a refusal is the same refusal on a
  // machine that has one and on a machine that does not.
  const { mode, status } = modeFrom(process.argv)
  process.exitCode = status === 0 ? run(process.env, mode) : status
}
