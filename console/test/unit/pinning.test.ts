// The pinning rule, enforced rather than documented (desktop-console plan Task
// 1.0; design section 5.2, and the precedent in
// internal/budget/rules/emulator/package.json).
//
// The rule: every specifier in console/package.json is an EXACT version. No
// "^", no "~", no "*", no "latest", no range, no tag, no "file:" — an exact
// version and nothing else, in `dependencies` and `devDependencies` alike
// (design section 5.2).
//
// The runtime `dependencies` section is one entry long and that entry is
// asserted by name below: electron-updater, the main process's auto-update
// client, whose pin Task 4.4 assigns to this task. It is not a renderer
// dependency — design D9's "no framework runtime, no charting library" is about
// what the renderer bundles — and the test says so rather than forbidding a
// runtime dependency outright.
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

  it('declares the Node the toolchain is pinned to', () => {
    // Design section 5.2 and Task 1.4: the console job takes its Node version
    // from this field, so an absent or empty one is a job that runs on whatever
    // the runner happens to ship.
    expect(typeof pkg.engines?.node).toBe('string')
    expect(String(pkg.engines?.node).trim()).not.toBe('')
  })

  it('carries the two scripts every later task runs', () => {
    expect(pkg.scripts?.['test:unit']).toBeTruthy()
    expect(pkg.scripts?.['test:e2e']).toBeTruthy()
  })

  // The runtime dependency set, as an allow-list rather than a prohibition.
  // Design D9 is a claim about the RENDERER -- it bundles no framework runtime
  // and no charting library -- and section 5.2 applies the pinning rule to
  // "dependencies and devDependencies alike", so a runtime dependency is
  // allowed as long as it is exact and belongs to the main process. Exactly one
  // does: the auto-update client, whose pin Task 4.4 assigns to this task
  // ("The pinned electron-updater version is part of Task 1.0's exact pins,
  // since it is the component that downloads and installs code"). Anything else
  // arriving here -- above all a renderer framework or a charting library -- is
  // a design change, so it has to be a deliberate edit to the list below.
  const runtimeDependencyAllowList = ['electron-updater']

  it('ships exactly the main process runtime dependencies it names', () => {
    const runtime = Object.keys((pkg.dependencies ?? {}) as Record<string, string>).toSorted()
    expect(
      runtime,
      `console/package.json's runtime dependencies are ${JSON.stringify(runtime)}. The list is ${JSON.stringify(runtimeDependencyAllowList)}: the renderer bundles no framework runtime and no charting library (design D9), so the only runtime dependency is the main process's update client. A new one is a deliberate edit here, with the case for it written down (plan Task 4.4 names electron-updater's pin as Task 1.0's).`,
    ).toEqual(runtimeDependencyAllowList.toSorted())
  })

  it('pins electron-updater, the component that downloads and installs code', () => {
    // Named separately from the list above, so the one dependency whose whole
    // job is fetching and installing a new bundle cannot leave the pins by a
    // quiet edit to the allow-list.
    const runtime = (pkg.dependencies ?? {}) as Record<string, string>
    expect(runtime['electron-updater']).toBeTruthy()
    expect(pinViolation(runtime['electron-updater'] ?? '')).toBeNull()
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
    for (const name of ['electron', 'typescript', 'vitest', '@playwright/test', '@types/node']) {
      expect(Object.keys(dev), `${name} is not pinned in devDependencies`).toContain(name)
    }
  })
})
