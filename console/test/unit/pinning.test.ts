// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The pinning rule, enforced rather than documented (desktop-console plan Task
// 1.0; design section 5.2, and the precedent in
// internal/budget/rules/emulator/package.json).
//
// The rule: every specifier in console/package.json is an EXACT version. No
// "^", no "~", no "*", no "latest", no range, no tag, no "file:" — an exact
// version and nothing else, in `dependencies` and `devDependencies` alike
// (design section 5.2).
//
// It holds one more pin that is not a specifier: `engines.node`, which is the
// version CI installs for the console job (Task 1.4) and is therefore an exact
// version here too, with its own table of refusals ("*", "latest", ">=0",
// ">=22.12.0"). A range there would have been the one float left.
//
// The runtime `dependencies` section holds one package, and that is asserted
// below, twice over. Task 1.0 pinned electron-updater there for Task 4.4's
// auto-update; Task 4.1's review found it shipped in the bundle with nothing
// importing it while this release has no auto-update, so it was removed (Task
// 4.1, review round 2), and Task 4.4 adds it back -- exactly pinned, with the
// code that uses it, which is the allow-list edit below and the import boundary
// the second test asserts. Design D9's "no framework runtime, no charting
// library" is about what the renderer bundles, so the test allows a
// main-process runtime dependency by name rather than forbidding one outright.
//
// The test is written in two halves on purpose. The first half is the checker
// against a table of specifiers it must accept and specifiers it must refuse:
// that is what makes the second half — the real package.json being clean —
// mean something, because a checker that accepted everything would pass it too.
// Adding "^" to one dependency is the plan's red step for this test, and the
// planted "^1.2.3" case below is the same mutation held as a fixture.
//
// The lockfile half of Task 1.0 ("one that asserts the lockfile exists and is
// in sync") is test/unit/lockfile.test.ts; `npm ci` in the task's command is
// what checks the tree itself.

import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

// consolePackageJSON is console/package.json, read from this file's own
// location rather than from the process's working directory, so `vitest` can be
// started from anywhere (the repository root, console/, an IDE) and still read
// the same file.
const consolePackageJSON = fileURLToPath(new URL('../../package.json', import.meta.url))

/** SOURCE_ROOT is `console/src`, walked by the import boundary below. */
const SOURCE_ROOT = fileURLToPath(new URL('../../src', import.meta.url))

/** SourceFile is one file the import boundary looks at, by its path under
 * `console/src/`. */
interface SourceFile {
  readonly path: string
  readonly source: string
}

/** codeOnly is a file's source with its comments removed: the scan is about
 * what the code imports, and `src/main/updater-electron.ts` discusses
 * electron-updater at length on purpose. */
function codeOnly(source: string): string {
  return source.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

/** sourceFiles reads every `.ts` file under `console/src/`. */
function sourceFiles(): readonly SourceFile[] {
  const walk = (directory: string, prefix: string): SourceFile[] => {
    const out: SourceFile[] = []
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const relative = prefix === '' ? entry.name : `${prefix}/${entry.name}`
      if (entry.isDirectory()) out.push(...walk(join(directory, entry.name), relative))
      else if (entry.name.endsWith('.ts')) {
        out.push({ path: relative, source: readFileSync(join(directory, entry.name), 'utf8') })
      }
    }
    return out
  }
  return walk(SOURCE_ROOT, '')
}

// The dependency sections package.json is allowed to carry. All four are held
// to the same rule: a peer or optional dependency that floats is the same
// unpinned tree as a floated dev dependency, and npm resolves it at install
// time all the same.
const dependencySections = [
  'dependencies',
  'devDependencies',
  'optionalDependencies',
  'peerDependencies',
] as const

// exactVersionRE is an exact semantic version, with the two optional parts that
// are still exact: a prerelease (-beta.1) and build metadata (+build.5). It is
// deliberately not npm's full version grammar -- no ranges, no tags, no "x",
// no "latest", no "file:", no "workspace:".
const exactVersionRE = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/

/**
 * pinViolation says why a specifier is not an exact pin, or null when it is
 * one. It names the markers the plan names -- "^", "~", "*", "latest" -- when
 * they are present, because the failure message for a caret should say "a caret
 * range" rather than "not an exact version", and falls back to the grammar for
 * everything else a range can be spelled as (">=1.2.3", "1.2.x", "1.x",
 * "1.2.3 || 2.0.0", "1.2.3 - 2.0.0", "file:../x").
 */
export function pinViolation(specifier: string): string | null {
  if (specifier.includes('^')) return 'a caret range'
  if (specifier.includes('~')) return 'a tilde range'
  if (specifier.includes('*')) return 'a wildcard'
  if (/(^|[^0-9A-Za-z-])latest([^0-9A-Za-z-]|$)/.test(specifier)) return 'the "latest" tag'
  if (!exactVersionRE.test(specifier)) return 'not an exact version'
  return null
}

// enginesNodeRE is an exact Node version and nothing else: three dotted numbers,
// no operator, no wildcard, no tag, no whitespace. It is deliberately narrower
// than the semver `engines.node` normally carries -- ">=22.12.0" is a perfectly
// ordinary engines field -- because this field is what CI's `actions/setup-node`
// installs from (Task 1.4) and a range there means the tests run on whichever
// Node the range happened to resolve to, which is the float this whole task
// exists to refuse.
const enginesNodeRE = /^\d+\.\d+\.\d+$/

/**
 * enginesNodeViolation says why engines.node is not an exact pin, or null when
 * it is one. A non-empty string is not enough: "*", "latest", ">=0" and ">=20"
 * are all non-empty strings, and all four are exactly what a pinned toolchain
 * must refuse.
 */
export function enginesNodeViolation(value: unknown): string | null {
  if (typeof value !== 'string' || value === '') return 'not a non-empty string'
  if (value.trim() !== value) return 'padded with whitespace'
  if (value.includes('*')) return 'a wildcard'
  if (/(^|[^0-9A-Za-z-])latest([^0-9A-Za-z-]|$)/.test(value)) return 'the "latest" tag'
  if (!enginesNodeRE.test(value)) return 'not an exact dotted version'
  return null
}

describe('the engines.node rule itself', () => {
  // The probe, mutation by mutation, because the assertion it guards used to be
  // "engines.node is a non-empty string" -- which "*", "latest" and ">=0" all
  // satisfied inside the suite whose subject is refusing floats.
  const refused: ReadonlyArray<readonly [string, string]> = [
    ['*', 'a wildcard'],
    ['latest', 'the "latest" tag'],
    ['>=0', 'not an exact dotted version'],
    ['>=20', 'not an exact dotted version'],
    ['>=22.12.0', 'not an exact dotted version'],
    ['^22.12.0', 'not an exact dotted version'],
    ['~22.23.3', 'not an exact dotted version'],
    ['22.x', 'not an exact dotted version'],
    ['22', 'not an exact dotted version'],
    ['22.23', 'not an exact dotted version'],
    ['22.23.3 || 24.0.0', 'not an exact dotted version'],
    [' 22.23.3', 'padded with whitespace'],
    ['', 'not a non-empty string'],
  ]

  it.each(refused)('refuses %s', (value, why) => {
    expect(enginesNodeViolation(value)).toBe(why)
  })

  it.each(['22.23.3', '20.0.0', '24.1.0'])('accepts the exact version %s', (value) => {
    expect(enginesNodeViolation(value)).toBeNull()
  })

  it('refuses a field that is not a string at all', () => {
    expect(enginesNodeViolation(undefined)).toBe('not a non-empty string')
    expect(enginesNodeViolation(22)).toBe('not a non-empty string')
  })
})

describe('the pinning rule itself', () => {
  // The probe: a checker that refuses nothing would make the package.json half
  // below pass on a tree of floating ranges, so the checker is shown a table it
  // must get right, marker by marker.
  const refused: ReadonlyArray<readonly [string, string]> = [
    ['^1.2.3', 'a caret range'],
    ['^0.1.0', 'a caret range'],
    ['~1.2.3', 'a tilde range'],
    ['*', 'a wildcard'],
    ['1.2.*', 'a wildcard'],
    ['latest', 'the "latest" tag'],
    ['next', 'not an exact version'],
    ['>=1.2.3', 'not an exact version'],
    ['>1.2.3 <2.0.0', 'not an exact version'],
    ['1.2.x', 'not an exact version'],
    ['1.x', 'not an exact version'],
    ['1', 'not an exact version'],
    ['1.2', 'not an exact version'],
    ['1.2.3 || 2.0.0', 'not an exact version'],
    ['1.2.3 - 2.0.0', 'not an exact version'],
    ['workspace:*', 'a wildcard'],
    ['file:../shared', 'not an exact version'],
    ['npm:other-package@1.2.3', 'not an exact version'],
    ['', 'not an exact version'],
  ]

  it.each(refused)('refuses %s', (specifier, why) => {
    expect(pinViolation(specifier)).toBe(why)
  })

  const accepted = ['1.2.3', '0.0.0', '44.7.2', '1.2.3-beta.1', '1.2.3-rc.4', '1.2.3+build.5']

  it.each(accepted)('accepts the exact version %s', (specifier) => {
    expect(pinViolation(specifier)).toBeNull()
  })
})

describe('console/package.json', () => {
  const pkg = JSON.parse(readFileSync(consolePackageJSON, 'utf8')) as {
    private?: unknown
    engines?: Record<string, unknown>
    scripts?: Record<string, unknown>
    [section: string]: unknown
  }

  it('is private: it is never published to a registry', () => {
    expect(pkg.private).toBe(true)
  })

  it('pins the Node the toolchain runs on, exactly', () => {
    // Design section 5.2 and Task 1.4: the console job takes its Node version
    // from this field, so a field that says "anything" would be a job that runs
    // on whatever the runner happens to ship -- and this is the one field in
    // the manifest a reader expects to be a range. It is an exact version here
    // like every other pin, which the table above proves the checker can tell
    // apart from a range.
    const why = enginesNodeViolation(pkg.engines?.node)
    expect(why, `engines.node is ${JSON.stringify(pkg.engines?.node)}: ${why}`).toBeNull()
  })

  it('carries the two scripts every later task runs, flags and all', () => {
    // The commands themselves, not `toBeTruthy()`: the flags ARE the contract.
    // `playwright test` without `--pass-with-no-tests` exits 1 with "Error: No
    // tests found" on a tree with no e2e spec -- the state Task 1.4's staged
    // console workflow runs this script in before Task 1.1 lands the first spec
    // -- so the flag is what makes the e2e step green rather than red. And
    // `test:unit` has to be the unit runner over test/unit (the directory vitest
    // is pointed at), not whatever else a truthy string could be. A later task
    // that needs a different invocation edits this test deliberately, which is
    // the point of asserting the string.
    //
    // Task 1.1 edits the second string, deliberately: the e2e spec launches the
    // app Electron loads from package.json's `main` (`dist/main/index.js`), and
    // `npm run build` is what puts it there, so the script builds first. The
    // flag stays: a tree with no spec yet is still green.
    expect(
      pkg.scripts?.['test:unit'],
      'test:unit must run vitest over test/unit: it is the command the plan names (Task 1.0) and the one the staged console workflow calls (Task 1.4)',
    ).toBe('vitest run --dir test/unit')
    // Task 2.1b's fix round B edits the string again, deliberately: the packed
    // console's notices are held to the archive the packer produces
    // (`scripts/check-packed-notices.mjs`, which packs the app itself), and the
    // e2e script is the step the console's CI job already runs after `npm run
    // build` -- so the check rides there rather than in a step of its own that
    // would mean editing the workflow.
    expect(
      pkg.scripts?.['test:e2e'],
      'test:e2e must build first (the spec runs what `main` points at, which only `npm run build` writes), then check the packed app against the notices, and must still pass --pass-with-no-tests, so a tree with no e2e spec stays green',
    ).toBe('npm run build && npm run check:packed && playwright test --pass-with-no-tests')
    expect(
      pkg.scripts?.['check:packed'],
      'check:packed must run the packed-set check, which is the only thing that reads the packer\'s own answer',
    ).toBe('node scripts/check-packed-notices.mjs')
  })

  // The runtime dependency set, as an allow-list rather than a prohibition.
  // Design D9 is a claim about the RENDERER -- it bundles no framework runtime
  // and no charting library -- and section 5.2 applies the pinning rule to
  // "dependencies and devDependencies alike", so a runtime dependency is
  // allowed as long as it is exact and belongs to the main process. There is
  // two, and each with the case for it written down. Everything a
  // `dependencies` entry names is packed into the shipped asar, so a row here
  // is a package the operator's download carries: electron-updater downloads
  // and installs code, and @sentry/electron is the crash reporter the owner's
  // ruling of 2026-10-30 names, which the wiring starts only when the
  // installation's own switch says on; the DSN it reports to is a constant of
  // the build rather than an input of it (the owner's ruling of 2026-10-31,
  // Task 2.1b). Task 4.4 added
  // the first with the one file that imports it; Task 2.1b adds the second the
  // same way, and the boundary below is what keeps the two to one importer each.
  const runtimeDependencyAllowList: string[] = ['electron-updater', '@sentry/electron']

  it('ships exactly the main process runtime dependencies it names', () => {
    const runtime = Object.keys((pkg.dependencies ?? {}) as Record<string, string>).toSorted()
    expect(
      runtime,
      `console/package.json's runtime dependencies are ${JSON.stringify(runtime)}. The list is ${JSON.stringify(runtimeDependencyAllowList)}: every runtime dependency is packed into the shipped bundle, the renderer bundles no framework runtime and no charting library (design D9), and the update client arrives with Task 4.4. A new one is a deliberate edit here, with the case for it written down.`,
    ).toEqual(runtimeDependencyAllowList.toSorted())
  })

  it('imports the update client from one file, and from no other', () => {
    // Named separately from the list above, because the pinning rule alone
    // would be satisfied by the package sitting in the tree unused: the point
    // of the allow-list is that a runtime dependency is *reached*, and the
    // point of this test is that it is reached from exactly one place. The
    // update client is the package that fetches and installs a new bundle, and
    // `src/main/updater-electron.ts` is where the policy's `UpdaterPort` is
    // filled -- the file that also removes the identifier the library sends.
    // A second importer is a second place a feed URL, a header or a download
    // can be configured, which is the surface Task 4.4 exists to keep to one.
    // The four shapes are every way a module reaches a package: `import x from`,
    // `import { x } from`, a bare `import 'electron-updater'` for its side
    // effects, and `require`/`import()` at call time.
    const importers = sourceFiles()
      .filter((file) =>
        /(?:from|require\(|import\(|import)\s*['"]electron-updater['"]/.test(codeOnly(file.source)),
      )
      .map((file) => file.path)
      .toSorted()
    expect(
      importers,
      `electron-updater is imported by ${JSON.stringify(importers)}; it must be imported by src/main/updater-electron.ts and by nothing else`,
    ).toEqual(['main/updater-electron.ts'])
  })

  it('imports the crash reporter from one file, its main entry, and no other', () => {
    // The same shape as the update client's, and a stricter pin for the same
    // kind of reason. `@sentry/electron` ships three entries -- `main`,
    // `renderer` and `preload` -- and each one wires a *different process*: the
    // renderer entry reports the page's errors, and the preload entry exists to
    // carry the renderer's events across to main. The owner's ruling of
    // 2026-10-30 is main process only, with no preload and no new IPC surface,
    // so `main` and nothing else is imported, from exactly one file. A second
    // importer is a second place a DSN, an integration or an IPC mode can be
    // configured, which is the surface this test keeps to one.
    const specifiers = sourceFiles().flatMap((file) =>
      [
        ...codeOnly(file.source).matchAll(
          /(?:from|require\(|import\(|import)\s*['"](@sentry\/electron(?:\/[^'"]*)?)['"]/g,
        ),
      ].map((match) => `${file.path}: ${match[1]}`),
    )
    expect(
      specifiers,
      `@sentry/electron is reached as ${JSON.stringify(specifiers)}; it must be reached as src/main/crash-electron.ts and '@sentry/electron/main' and nothing else`,
    ).toEqual(['main/crash-electron.ts: @sentry/electron/main'])
  })

  it('keeps the DSN in one file, reached from one file', () => {
    // The owner's ruling of 2026-10-31 puts the console's Sentry DSN in the
    // build as a public constant. One file holds it and one file reads it: a
    // second importer would be a second place reporting is wired, and the value
    // would then have two readers to drift between. `test/unit/secret-hygiene`
    // pins the *value* side of the same ruling -- that it is the only
    // DSN-shaped literal under `src/` -- and this is the reach side.
    const importers = sourceFiles()
      .filter((file) => /(?:from|require\(|import\(|import)\s*['"][^'"]*crash-dsn(?:\.js)?['"]/.test(codeOnly(file.source)))
      .map((file) => file.path)
      .toSorted()
    expect(
      importers,
      `the DSN module is imported by ${JSON.stringify(importers)}; it must be imported by src/main/crash-electron.ts and by nothing else`,
    ).toEqual(['main/crash-electron.ts'])
  })

  // One test per section, each naming its own offender, so "a ^ was added to
  // one dependency" is the failure the plan's red step describes. A section that
  // is absent is skipped rather than failed -- but at least one section must
  // exist, or the scan below would pass by looking at nothing.
  const present = dependencySections.filter((section) => pkg[section] !== undefined)

  it('has at least one dependency section to check', () => {
    expect(present.length).toBeGreaterThan(0)
  })

  it.each(dependencySections)('every specifier in %s is an exact version', (section) => {
    if (pkg[section] === undefined) return
    const deps = pkg[section] as Record<string, string>
    const violations: string[] = []
    for (const [name, specifier] of Object.entries(deps)) {
      const why = pinViolation(specifier)
      if (why !== null) violations.push(`${name}: ${JSON.stringify(specifier)} is ${why}`)
    }
    expect(
      violations,
      `console/package.json must pin exact versions, never ranges (design section 5.2, plan Task 1.0):\n${violations.join('\n')}`,
    ).toEqual([])
  })

  it('pins Electron, TypeScript and the test runners it says it does', () => {
    // Named here rather than only in the README, so removing Electron from the
    // pinned toolchain is a failing test and not a quiet edit.
    const dev = (pkg.devDependencies ?? {}) as Record<string, string>
    for (const name of ['electron', 'electron-builder', 'typescript', 'vitest', '@playwright/test', '@types/node']) {
      expect(Object.keys(dev), `${name} is not pinned in devDependencies`).toContain(name)
    }
  })
})
