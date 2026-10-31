// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// THIRD_PARTY_NOTICES.md lists the licence of every package the packed console
// carries, and this file is the check that the list and the package agree
// (desktop-console plan Task 2.1b, fix round A).
//
// **The set is derived, not written down.** Every version of it before this one
// was read off a machine -- `node_modules` as the packaging step of the day left
// it -- which is how a notices file comes to list the Linux-only bindings of a
// package and omit the macOS ones the universal build ships. What ships is a
// property of `package-lock.json` plus two files that decide it: `package.json`'s
// `dependencies` (the packages the main process requires at run time, since a
// devDependency is not packed) and `build.files` (the allow-list, whose
// `!node_modules/.../**` entries take a package's whole tree back out). The file
// is then read the other way: whatever its package sections name has to be in
// that set, so a section cannot outlive the package it describes.
//
// The three sections that are **not** npm packages of the packed tree are named
// separately, because their code ships inside a build output rather than as a
// directory: the elkjs layout worker the bundler writes into `dist/bundle`, and
// the two vendored fonts' licence texts. `elkjs` itself is a devDependency --
// the bundler takes its source and nothing installs it into the app -- so no
// walk of the lockfile finds it, and dropping its section would leave the
// licence of shipped code unreproduced.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const consoleDir = fileURLToPath(new URL('../..', import.meta.url))

/** MAC_UNIVERSAL and LINUX are the platforms the console is packaged for:
 * `--mac --universal` is darwin arm64 and darwin x64 in one bundle, and
 * `package:linux` is linux x64. */
const TARGETS: readonly (readonly [string, string])[] = [
  ['darwin', 'arm64'],
  ['darwin', 'x64'],
  ['linux', 'x64'],
]

/** UNDECLARED_LOADS is empty, and it is here rather than deleted because of what
 * made it non-empty once: `debug` reaches `supports-color` from inside a `try`,
 * an optional `require` no `package.json` declares, so a walk of the lockfile
 * cannot follow that edge -- and round A therefore treated the package as a root
 * of the derivation (with `has-flag` coming back through its `dependencies`).
 * That was the wrong half of the answer. The edge is real, but the console does
 * not need what it loads: `debug` catches the failure and turns colours off. The
 * packer's own collector follows the edge *for the exclusion* as well as the
 * inclusion, so `supports-color` and `has-flag` were being hoisted out of the
 * excluded tree and into the archive no matter what this walk believed, which
 * `scripts/check-packed-notices.mjs` found on the packed app. Both are now
 * excluded by name, and the derivation below is back to following declared
 * dependencies alone -- with the packed-set check as the authority behind it,
 * since this file is still a model of the packer rather than the packer. */
const UNDECLARED_LOADS: readonly string[] = []

/** BUNDLED are the notices sections whose code ships inside a build output. They
 * are not packages of the packed `node_modules`, so the derivation below leaves
 * them out, and they are asserted present separately. */
const BUNDLED: readonly string[] = ['elkjs 0.11.0', 'Instrument Sans', 'IBM Plex Mono']

interface PackageEntry {
  readonly version?: string
  readonly dev?: boolean
  readonly os?: readonly string[]
  readonly cpu?: readonly string[]
  readonly dependencies?: Readonly<Record<string, string>>
  readonly optionalDependencies?: Readonly<Record<string, string>>
}

const lock = JSON.parse(readFileSync(join(consoleDir, 'package-lock.json'), 'utf8')) as {
  readonly packages: Readonly<Record<string, PackageEntry>>
}
const manifest = JSON.parse(readFileSync(join(consoleDir, 'package.json'), 'utf8')) as {
  readonly dependencies?: Readonly<Record<string, string>>
  readonly build?: { readonly files?: readonly string[] }
}
const notices = readFileSync(join(consoleDir, 'THIRD_PARTY_NOTICES.md'), 'utf8')

/** excludedTrees are the package directories `build.files` takes out, each the
 * `<name>` of a `!node_modules/<name>/**` pattern. `<name>` may be a package
 * (`glob`) or a whole scope (`@oxc-parser`), which is why the check below is a
 * prefix test rather than a lookup by name. */
const excludedTrees: readonly string[] = (manifest.build?.files ?? [])
  .filter((pattern) => pattern.startsWith('!node_modules/') && pattern.endsWith('/**'))
  .map((pattern) => pattern.slice('!node_modules/'.length, -'/**'.length))

/** nameAt is the package name a lockfile path holds: everything after its last
 * `node_modules/` segment. */
function nameAt(path: string): string {
  return path.slice(path.lastIndexOf('node_modules/') + 'node_modules/'.length)
}

/** resolveWithin answers the lockfile path `dependency` resolves to as seen from
 * the package at `from`, the way `require` does: the nearest `node_modules`
 * walking up from the requiring package, then the tree root. */
function resolveWithin(from: string, dependency: string): string | null {
  for (let base = from; ; ) {
    const candidate = base === '' ? `node_modules/${dependency}` : `${base}/node_modules/${dependency}`
    if (lock.packages[candidate] !== undefined) return candidate
    if (base === '') return null
    const parent = base.lastIndexOf('/node_modules/')
    base = parent === -1 ? '' : base.slice(0, parent)
  }
}

/** isExcluded reports whether the package at `path` is, or sits inside, one of
 * the trees `build.files` takes out -- including through a `node_modules` nested
 * inside another package, which is where a lockfile keeps a second copy. */
function isExcluded(path: string): boolean {
  for (let candidate = path; ; ) {
    if (candidate.startsWith('node_modules/')) {
      for (const tree of excludedTrees) {
        if (candidate === `node_modules/${tree}` || candidate.startsWith(`node_modules/${tree}/`)) return true
      }
    }
    const parent = candidate.lastIndexOf('/node_modules/')
    if (parent === -1) return false
    candidate = candidate.slice(0, parent)
  }
}

/** runsOn reports whether a package declares itself for one target. A package
 * with neither `os` nor `cpu` is pure JavaScript and runs anywhere; one with
 * either is the platform-specific half of a package that cannot work without it,
 * and npm's `!`-prefixed entries say which platforms are refused. */
function runsOn(entry: PackageEntry, [os, cpu]: readonly [string, string]): boolean {
  // A declaration is a list of the values allowed and, with a `!` in front, the
  // ones refused; absent means every value is allowed.
  const allows = (declared: readonly string[] | undefined, value: string): boolean => {
    if (declared === undefined) return true
    if (declared.includes(`!${value}`)) return false
    const allowed = declared.filter((one) => !one.startsWith('!'))
    return allowed.length === 0 || allowed.includes(value)
  }
  return allows(entry.os, os) && allows(entry.cpu, cpu)
}

/** shippedOn is every package the packed app carries for one platform: the
 * production tree of `package.json`'s `dependencies`, expanded as `require`
 * resolves it, minus the dev-only entries a packer leaves out, minus the trees
 * `build.files` excludes, minus the packages that declare another platform. Each
 * answer is the `name version` a notices heading names. */
function shippedOn(target: readonly [string, string]): readonly string[] {
  const reached = new Set<string>()
  const queue: string[] = []
  const enqueue = (path: string | null): void => {
    if (path !== null && !reached.has(path)) {
      reached.add(path)
      queue.push(path)
    }
  }
  for (const dependency of [...Object.keys(manifest.dependencies ?? {}), ...UNDECLARED_LOADS]) {
    enqueue(resolveWithin('', dependency))
  }
  while (queue.length > 0) {
    const path = queue.shift() ?? ''
    const entry = lock.packages[path]
    if (entry === undefined) continue
    for (const dependency of [
      ...Object.keys(entry.dependencies ?? {}),
      ...Object.keys(entry.optionalDependencies ?? {}),
    ]) {
      enqueue(resolveWithin(path, dependency))
    }
  }
  return [...reached]
    .filter((path) => {
      const entry = lock.packages[path] ?? {}
      return entry.dev !== true && !isExcluded(path) && runsOn(entry, target)
    })
    .map((path) => `${nameAt(path)} ${lock.packages[path]?.version ?? '?'}`)
    .sort()
}

/** headings are the notices file's section headings that name a licence, in the
 * `name version` form the derivation above answers with (the three bundled
 * sections, which name no npm package, are left out here and asserted below). */
function listedPackages(): readonly string[] {
  return notices
    .split('\n')
    .filter((line) => /^## .+ -- /.test(line))
    .map((line) => line.slice(3).split(' -- ')[0] ?? '')
    .filter((heading) => !BUNDLED.some((bundled) => heading.startsWith(bundled)))
    .sort()
}

/** numberWord spells a count the way the notices file's prose does, below a
 * hundred -- the header says "the last thirty-one in this file". */
function numberWord(count: number): string {
  const ones: readonly string[] = [
    'zero', 'one', 'two', 'three', 'four', 'five', 'six', 'seven', 'eight', 'nine',
    'ten', 'eleven', 'twelve', 'thirteen', 'fourteen', 'fifteen', 'sixteen', 'seventeen',
    'eighteen', 'nineteen',
  ]
  if (count < 20) return ones[count] ?? '?'
  const tens: readonly string[] = ['', '', 'twenty', 'thirty', 'forty', 'fifty', 'sixty', 'seventy', 'eighty', 'ninety']
  const tensWord = tens[Math.floor(count / 10)] ?? '?'
  const onesWord = ones[count % 10] ?? '?'
  return count % 10 === 0 ? tensWord : `${tensWord}-${onesWord}`
}

describe('the notices file lists what ships, for every platform', () => {
  it('derives the shipped set from the lockfile, and it is not empty', () => {
    // Non-vacuity: a walk that resolved nothing would make the comparison below
    // true and mean nothing.
    const shipped = shippedOn(['darwin', 'arm64'])
    expect(shipped.length).toBeGreaterThan(25)
    expect(shipped).toContain('electron-updater 6.8.10')
    expect(shipped).toContain('@sentry/electron 8.2.0')
    // `semver` is the case that says the walk **resolves** rather than lists:
    // the copy that ships is the one nested under `electron-updater` (7.7.4),
    // not the tree root's (7.8.5) which only a development dependency needs, and
    // a notices heading names the version that ships.
    expect(shipped).toContain('semver 7.7.4')
    expect(shipped).not.toContain('semver 7.8.5')
  })

  it('names what the build-time trees leave out, and only that', () => {
    // Every exclusion pattern has to name a package the lockfile actually has:
    // a pattern that named nothing would look like a narrowing and pack the tree
    // anyway.
    expect(excludedTrees.length).toBeGreaterThan(10)
    for (const tree of excludedTrees) {
      const holders = Object.keys(lock.packages).filter(
        (path) => nameAt(path) === tree || nameAt(path).startsWith(`${tree}/`),
      )
      expect(holders, `no lockfile package is named ${tree}`).not.toEqual([])
    }
    // And the other way: nothing that loads is excluded by accident -- the four
    // below are build-time, and the two the packer used to hoist out of that
    // tree (`supports-color` and `has-flag`) are excluded with it, because the
    // `require` that reaches them is optional and `debug` runs without them.
    const shipped = shippedOn(['linux', 'x64'])
    for (const gone of [
      'sentry 0.45.0',
      'oxc-parser 0.152.0',
      '@sentry/bundler-plugins 11.7.0',
      'glob 13.0.6',
      'supports-color 8.1.1',
      'has-flag 4.0.0',
    ]) {
      expect(shipped, `${gone} is build-time and must not ship`).not.toContain(gone)
    }
  })

  it('ships the same packages on all three targets', () => {
    // The universal build is one bundle for two architectures and the Linux
    // build is a third. Every package here is pure JavaScript, so the three
    // answers agree; a platform-specific package entering the tree would show up
    // as one target's archive carrying a package another's does not, which is
    // exactly the case a notices file read off one machine gets wrong.
    const answers = TARGETS.map((target) => shippedOn(target))
    for (const answer of answers.slice(1)) expect(answer).toEqual(answers[0])
    expect(answers[0]?.length).toBeGreaterThan(25)
  })

  it('matches the notices file both ways, so no section outlives its package', () => {
    // Both directions at once: a package that ships with no section is an
    // unreproduced licence, and a section for a package that no longer ships is
    // a claim about the package that is no longer true.
    expect(listedPackages()).toEqual(shippedOn(['darwin', 'arm64']))
  })

  it('keeps the sections whose code ships inside a build output', () => {
    // `elkjs` is a devDependency -- the bundler takes its source into
    // `dist/bundle/elk-worker.min.js` -- and the fonts are files under
    // `src/renderer/fonts` that `npm run build` copies. None of the three is a
    // package of the packed tree, so none is in the derived set, and this is
    // what stops them being deleted as unmatched.
    for (const section of [
      '## elkjs 0.11.0 -- Eclipse Public License 2.0',
      '## Instrument Sans -- SIL Open Font License 1.1',
      '## IBM Plex Mono -- SIL Open Font License 1.1',
    ]) {
      expect(notices, `${section} carries the licence of code that ships`).toContain(section)
    }
    // And the count the prose states is the count of package sections, spelled
    // the way the prose spells it, so a reader is told how many to expect.
    expect(notices).toContain(`the last ${numberWord(listedPackages().length)} in this file`)
  })
})
