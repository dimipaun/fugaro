// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The hand-built module registries are closed under `require` (desktop-console
// plan Task 3.3, round 2 of its review).
//
// Several specs here inject the compiled renderer into the running window as a
// hand-built CommonJS registry -- a `MODULES` list of `dist/` files and a
// `require` that resolves `./` and `../` and nothing else. The list is written
// out by hand in each spec, so when a renderer module gains an import the list
// has to gain it too. Round 1 of Task 3.3 is what that costs when it does not:
// `renderer/app.ts` began importing `../shared/version`, five specs' lists did
// not name `shared/version.js`, and the whole e2e suite went red in CI on both
// operating systems with an error about a module the registry did not have.
//
// This spec is the guard. For every spec in this directory that declares a
// `MODULES` list, it reads each listed module **as built** (`dist/`, which is
// what the registry injects -- so an import the compiler elided as type-only is
// not counted, and one it kept is) and asserts that every relative `require` in
// it names a module the same list carries. A renderer edit that adds an import
// therefore fails here, by spec and by module, rather than as an opaque runtime
// error in an unrelated screen's test. It runs under `npm run test:e2e`, which
// builds first, so `dist/` is always the tree under test.

import { existsSync, readFileSync, readdirSync } from 'node:fs'
import { basename, join, posix } from 'node:path'

import { expect, test } from '@playwright/test'

const consoleDir = join(__dirname, '..', '..')
const e2eDir = __dirname

/** registryOf is the `MODULES` list a spec declares, read from its source, or
 * `null` when the spec declares none. */
function registryOf(source: string): readonly string[] | null {
  const block = /const MODULES[^=]*=\s*Object\.freeze\(\[([\s\S]*?)\]\)/.exec(source)
  if (block === null) return null
  return [...(block[1] ?? '').matchAll(/'([^']+)'/g)].map((match) => match[1] ?? '')
}

/** relativeRequires is every `./` or `../` specifier a compiled module requires,
 * resolved against the module's own directory into a registry path. */
function relativeRequires(module: string, code: string): readonly string[] {
  const out: string[] = []
  for (const match of code.matchAll(/\brequire\(\s*["'](\.{1,2}\/[^"']+)["']\s*\)/g)) {
    const resolved = posix.normalize(posix.join(posix.dirname(module), match[1] ?? ''))
    out.push(resolved.endsWith('.js') ? resolved : `${resolved}.js`)
  }
  return out
}

// Sorted by name, because `readdirSync` order is unspecified -- ext4 returns
// hash order, APFS returns something else again -- and the non-vacuity check
// below compares this list with a derived set by `toEqual`, which is
// order-sensitive. An unsorted side would make the guard fail on a filesystem
// that happened to enumerate the directory differently, for no reason that has
// anything to do with a registry.
const specSources = readdirSync(e2eDir)
  .filter((name) => name.endsWith('.spec.ts'))
  .map((name) => ({ name, source: readFileSync(join(e2eDir, name), 'utf8') }))

const specs = specSources
  .map((spec) => ({ name: spec.name, registry: registryOf(spec.source) }))
  .filter((spec): spec is { name: string; registry: readonly string[] } => spec.registry !== null)
  .sort((left, right) => (left.name < right.name ? -1 : left.name > right.name ? 1 : 0))

/**
 * declaring is every spec that injects the compiled renderer, found by a scan
 * that does **not** go through `registryOf`: a spec that reads a module straight
 * out of `dist/` is one with a registry, whether or not this file can parse its
 * list.
 *
 * It exists because the first version of the guard below named four specs by
 * hand -- `degraded`, `live`, `skew`, `spend` -- while six declared a registry
 * (`projects` and `run` were the two it did not name). A spec left out of a
 * hand-written list is a spec whose registry can drift unnoticed, which is
 * exactly the defect this whole file exists for, so the list is derived from the
 * directory and the guard asserts that the derived set and the parsed set are
 * the same set. A spec whose `MODULES` block stops being parseable therefore
 * fails here instead of being skipped in silence.
 */
const declaring = specSources
  .filter((spec) => /readFileSync\(\s*join\(consoleDir,\s*'dist'/.test(spec.source))
  // A spec that reads the built main entry (crash-init) injects no renderer
  // module, so it has no registry to drift.
  .filter((spec) => !/readFileSync\(\s*join\(consoleDir,\s*'dist',\s*'main'/.test(spec.source))
  .map((spec) => spec.name)

test.describe('the hand-built module registries', () => {
  test('there are registries to check, and this guard covers every one of them', () => {
    expect(declaring.length, 'no spec reads a compiled module out of dist/').toBeGreaterThan(0)
    // The derived set and the parsed set, both ways: every spec with a registry
    // is checked, and every spec that is checked has a registry this file could
    // read. `toEqual` on sorted copies, so a spec in either list and not the
    // other is a failure naming nothing else.
    expect(specs.map((spec) => spec.name).sort()).toEqual([...declaring].sort())
    for (const spec of specs) {
      expect(spec.registry.length, `${spec.name} declares an empty registry`).toBeGreaterThan(0)
    }
    // **Derived, not listed**, a second way (Task 3.4b): every spec in the
    // directory that declares a `MODULES` list, this guard file excepted, read
    // with a pattern independent of `registryOf`'s, and compared exactly.
    const declaringModules = readdirSync(e2eDir)
      .filter((name) => name.endsWith('.spec.ts'))
      .filter((name) => name !== basename(__filename))
      .filter((name) => /const MODULES[^=]*=\s*Object\.freeze\(\[/.test(readFileSync(join(e2eDir, name), 'utf8')))
      .sort()
    expect(specs.map((spec) => spec.name)).toEqual(declaringModules)
    // ...and the floor, so a directory that somehow read as empty fails here
    // rather than making the assertions above vacuously true.
    expect(declaringModules.length).toBeGreaterThanOrEqual(6)
  })

  for (const spec of specs) {
    test(`${spec.name}'s registry carries every module its modules require`, () => {
      const listed = new Set(spec.registry)
      const missing: string[] = []
      for (const module of spec.registry) {
        const built = join(consoleDir, 'dist', module)
        expect(existsSync(built), `${spec.name} lists ${module}, which the build did not emit`).toBe(true)
        for (const required of relativeRequires(module, readFileSync(built, 'utf8'))) {
          if (!listed.has(required)) missing.push(`${module} requires ${required}`)
        }
      }
      expect(missing, `${spec.name}'s MODULES is missing what its modules require`).toEqual([])
    })
  }

  test('the resolver itself: a nested ../ import lands on the registry path', () => {
    expect(
      relativeRequires('renderer/screens/spend.js', 'const a = require("../app"); const b = require("../../shared/version");'),
    ).toEqual(['renderer/app.js', 'shared/version.js'])
  })
})
