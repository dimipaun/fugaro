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
// The runtime `dependencies` section is empty, and that is asserted below.
// Task 1.0 pinned electron-updater there for Task 4.4's auto-update, but Task
// 4.1's review found it shipped in the bundle with nothing importing it while
// this release has no auto-update; it was removed (Task 4.1, review round 2),
// and Task 4.4 re-adds it, exactly pinned, when it writes the code that uses
// it -- an edit to the allow-list below. Design D9's "no framework runtime, no
// charting library" is about what the renderer bundles, so the test allows a
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

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

// consolePackageJSON is console/package.json, read from this file's own
// location rather than from the process's working directory, so `vitest` can be
// started from anywhere (the repository root, console/, an IDE) and still read
// the same file.
const consolePackageJSON = fileURLToPath(new URL('../../package.json', import.meta.url))

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
    expect(
      pkg.scripts?.['test:e2e'],
      'test:e2e must build first (the spec runs what `main` points at, which only `npm run build` writes) and must still pass --pass-with-no-tests, so a tree with no e2e spec stays green',
    ).toBe('npm run build && playwright test --pass-with-no-tests')
  })

  // The runtime dependency set, as an allow-list rather than a prohibition.
  // Design D9 is a claim about the RENDERER -- it bundles no framework runtime
  // and no charting library -- and section 5.2 applies the pinning rule to
  // "dependencies and devDependencies alike", so a runtime dependency is
  // allowed as long as it is exact and belongs to the main process. Today there
  // is none: everything a `dependencies` entry names is packed into the shipped
  // asar, and the one Task 1.0 pinned (electron-updater, for Task 4.4) had no
  // importer, so it shipped dead code that downloads and installs code. Task
  // 4.4 adds it back here, with the code that uses it.
  const runtimeDependencyAllowList: string[] = []

  it('ships exactly the main process runtime dependencies it names', () => {
    const runtime = Object.keys((pkg.dependencies ?? {}) as Record<string, string>).toSorted()
    expect(
      runtime,
      `console/package.json's runtime dependencies are ${JSON.stringify(runtime)}. The list is ${JSON.stringify(runtimeDependencyAllowList)}: every runtime dependency is packed into the shipped bundle, the renderer bundles no framework runtime and no charting library (design D9), and the update client arrives with Task 4.4. A new one is a deliberate edit here, with the case for it written down.`,
    ).toEqual(runtimeDependencyAllowList.toSorted())
  })

  it('ships no update client until Task 4.4 writes the code that uses it', () => {
    // Named separately from the list above: the one package whose whole job is
    // fetching and installing a new bundle must not ride along unused.
    const runtime = (pkg.dependencies ?? {}) as Record<string, string>
    expect(runtime).not.toHaveProperty('electron-updater')
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
