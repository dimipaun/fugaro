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

const specs = readdirSync(e2eDir)
  .filter((name) => name.endsWith('.spec.ts'))
  .map((name) => ({ name, registry: registryOf(readFileSync(join(e2eDir, name), 'utf8')) }))
  .filter((spec): spec is { name: string; registry: readonly string[] } => spec.registry !== null)

test.describe('the hand-built module registries', () => {
  test('there are registries to check, and this guard names every one of them', () => {
    // **Derived, not listed.** Round 2 of Task 3.3 wrote this as
    // `arrayContaining(['degraded.spec.ts', 'live.spec.ts', 'skew.spec.ts',
    // 'spend.spec.ts'])`, which named four of the six specs that declare a
    // registry: the other two were checked by the loop below and *unasserted*
    // here, so a seventh spec could have joined them silently. The set is
    // therefore read off the directory -- every spec in it that declares a
    // `MODULES` list, this guard file excepted -- and compared exactly.
    const declaring = readdirSync(e2eDir)
      .filter((name) => name.endsWith('.spec.ts'))
      .filter((name) => name !== basename(__filename))
      .filter((name) => /const MODULES[^=]*=\s*Object\.freeze\(\[/.test(readFileSync(join(e2eDir, name), 'utf8')))
      .sort()
    expect(specs.map((spec) => spec.name)).toEqual(declaring)
    // ...and the floor, so a directory that somehow read as empty fails here
    // rather than making the assertion above vacuously true.
    expect(declaring.length).toBeGreaterThanOrEqual(6)
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
