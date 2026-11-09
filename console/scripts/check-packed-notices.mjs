// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The check that THIRD_PARTY_NOTICES.md describes the console the packer
// actually produces, rather than the tree it was built from.
//
// **Why this is not a unit test.** `test/unit/notices.test.ts` derives the
// shipped set from `package-lock.json`, `package.json`'s `dependencies` and
// `build.files`, and that derivation is a model of electron-builder. A model is
// exactly what round A's review found the bug through: the packer hoists
// `supports-color` out of an excluded tree into the asar's own `node_modules`,
// and the walk cannot see the edge that puts it there (`debug` requires it from
// inside a `try`, undeclared). The only way to be sure is to ask the packer:
// this script runs `electron-builder --dir`, opens the resulting `app.asar`,
// lists the package directories inside it, and compares them with the notices'
// section headings **both ways**. A package in the archive with no section is an
// unreproduced licence; a section naming a package that is not in the archive is
// a claim that is no longer true.
//
// It runs where the console's own CI job already runs: `npm run test:e2e`
// (`check:packed`, then Playwright) after `npm run build`. It uses
// `@electron/asar`, which is already in the tree through electron-builder, so
// it adds no dependency.
//
// **It does need the network, and it is not free.** CI installs with
// `npm ci --ignore-scripts` -- the flag that keeps lifecycle scripts out of this
// repository -- and that is also what leaves `node_modules/electron/` with no
// runtime, because the runtime is what electron's own `postinstall` writes. So
// the first thing here that needs Electron fetches it: on this path that is
// electron-builder, packing, and in the Playwright pass after it that is
// `require('electron')` unpacking the same archive (`test/support/
// electronRuntime.ts` has that half, and why the resolution happens before any
// worker exists). Both go through `@electron/get`, which caches the archive, so
// the second is normally a cache hit rather than a second download -- but on a
// cold runner the download happens, and the pack is a cost this check adds to
// every e2e job.
//
// The target is the host's, because the packer can only build the host's own
// platform here: `--mac --dir --arm64` on macOS, `--linux --dir` everywhere
// else. Both produce the same `node_modules` closure -- every package the
// console ships is pure JavaScript, which is why one platform's answer is
// asserted for all three in the unit test -- so a Linux runner checks the same
// set a macOS one would.

import { spawnSync } from 'node:child_process'
import { existsSync, readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const consoleDir = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const require = createRequire(import.meta.url)

/** TARGETS names the packed app's `resources` directory per platform. */
const TARGETS = {
  darwin: { args: ['--mac', '--dir', '--arm64'], appDir: 'mac-arm64/Fugaro Console.app/Contents/Resources' },
  linux: { args: ['--linux', '--dir'], appDir: 'linux-unpacked/resources' },
}

/** BUNDLED are the notices' sections whose code ships inside a build output --
 * the bundler's elkjs worker and the two vendored fonts -- rather than as a
 * directory of `node_modules`. They are not packages of the archive, so the
 * comparison below leaves them out on both sides. */
const BUNDLED = ['elkjs 0.11.0', 'Instrument Sans', 'IBM Plex Mono']

const target = TARGETS[process.platform]
if (target === undefined) {
  console.error(`check-packed-notices: no --dir target for ${process.platform}, so this check cannot run here`)
  process.exit(1)
}
if (!existsSync(join(consoleDir, 'dist', 'main', 'index.js'))) {
  console.error('check-packed-notices: dist/main/index.js is missing -- run `npm run build` first')
  process.exit(1)
}

console.log(`check-packed-notices: packing (electron-builder ${target.args.join(' ')})`)
const packed = spawnSync(join(consoleDir, 'node_modules', '.bin', 'electron-builder'), [...target.args, '--publish', 'never'], {
  cwd: consoleDir,
  stdio: ['ignore', 'inherit', 'inherit'],
  env: { ...process.env, CSC_IDENTITY_AUTO_DISCOVERY: 'false' },
})
if (packed.status !== 0) {
  console.error(`check-packed-notices: electron-builder exited ${String(packed.status)}`)
  process.exit(1)
}

const asarPath = join(consoleDir, 'release', target.appDir, 'app.asar')
if (!existsSync(asarPath)) {
  console.error(`check-packed-notices: no archive at ${asarPath}`)
  process.exit(1)
}

const asar = require('@electron/asar')
/** entries are the archive's paths, as `listPackage` answers them (leading `/`),
 * and `versionAt` reads one package's version back out of the archive -- the
 * archive's own copy, not the tree's, which is the whole point of this check.
 * `extractFile` takes the path without that leading slash. */
const entries = new Set(asar.listPackage(asarPath))
const versionAt = (path) => {
  const packageJson = `${path}/package.json`
  if (!entries.has(packageJson)) return null
  const parsed = JSON.parse(asar.extractFile(asarPath, packageJson.slice(1)).toString('utf8'))
  return typeof parsed.version === 'string' ? parsed.version : null
}

/** archived is `name version` for every package directory inside the archive --
 * at its root `node_modules`, which is where electron-builder hoists what more
 * than one dependent needs, and nested inside another package's own
 * `node_modules`, which is where it keeps a second copy that resolution demands.
 * A package directory is one whose `package.json` is beside it and which is
 * named by its last `node_modules/` segment. */
const archived = new Set()
for (const entry of entries) {
  const match = /\/(?:node_modules\/)((?:@[^/]+\/)?[^/]+)$/.exec(entry)
  if (match === null) continue
  const version = versionAt(entry)
  if (version !== null) archived.add(`${match[1]} ${version}`)
}

/** listed is every `name version` the notices' section headings name, minus the
 * three bundled sections that are not packages of the archive. */
const notices = readFileSync(join(consoleDir, 'THIRD_PARTY_NOTICES.md'), 'utf8')
const listed = new Set(
  notices
    .split('\n')
    .filter((line) => /^## .+ -- /.test(line))
    .map((line) => line.slice(3).split(' -- ')[0].trim())
    .filter((heading) => !BUNDLED.some((bundled) => heading.startsWith(bundled))),
)

const missingSections = [...archived].filter((entry) => !listed.has(entry)).sort()
const staleSections = [...listed].filter((entry) => !archived.has(entry)).sort()

if (archived.size === 0 || listed.size === 0) {
  console.error(
    `check-packed-notices: ${String(archived.size)} packages in the archive and ${String(listed.size)} sections in the notices -- one of them is empty, so the comparison would mean nothing`,
  )
  process.exit(1)
}
if (missingSections.length > 0 || staleSections.length > 0) {
  if (missingSections.length > 0) {
    console.error('check-packed-notices: these packages are in the packed app and have no notices section:')
    for (const entry of missingSections) console.error(`  ${entry}`)
  }
  if (staleSections.length > 0) {
    console.error('check-packed-notices: these notices sections name packages the packed app does not carry:')
    for (const entry of staleSections) console.error(`  ${entry}`)
  }
  process.exit(1)
}

console.log(`check-packed-notices: ${String(archived.size)} packages in ${target.appDir}/app.asar, all of them with a section`)
