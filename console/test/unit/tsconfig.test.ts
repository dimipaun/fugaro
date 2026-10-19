// tsconfig.json's `include`, held to the one thing it can silently get wrong
// (desktop-console plan Task 1.0, review round on PR #434).
//
// Task 1.0 writes the config the type checker runs from, and `npm run
// typecheck` is only as wide as `include`. A TypeScript file that is not
// included is not typechecked at all, and the file most likely to slip out is
// one at the console's ROOT, outside the three directories `include` names --
// playwright.config.ts, whose `testDir` and `testMatch` are the guard that
// keeps Playwright from collecting the vitest unit tests (test/unit/*.test.ts).
// A typo there is invisible: nothing else in the suite reads that file, so the
// guard would simply stop guarding.
//
// So the rule this test holds is structural: **every .ts file at the console's
// root is matched by `include`**. It is not a list of file names, so a new root
// config file is covered the day it is added; and the checker is shown a config
// it must accept and one it must refuse, so "every file is covered" cannot pass
// on a matcher that matches everything.

import { readFileSync, readdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

import { describe, expect, it } from 'vitest'

const consoleRoot = fileURLToPath(new URL('../../', import.meta.url))
const tsconfigPath = path.join(consoleRoot, 'tsconfig.json')

/**
 * stripJSONComments removes `//` and block comments that sit OUTSIDE string
 * literals. tsconfig.json is JSONC, which TypeScript reads and JSON.parse does
 * not, and a plain regex strip would also eat the `//` in a URL -- a comment
 * rule that mangles a value is worse than no comment support at all.
 */
function stripJSONComments(text: string): string {
  let out = ''
  let inString = false
  let inLine = false
  let inBlock = false
  for (let i = 0; i < text.length; i++) {
    const c = text[i] ?? ''
    const next = text[i + 1] ?? ''
    if (inLine) {
      if (c === '\n') {
        inLine = false
        out += c
      }
      continue
    }
    if (inBlock) {
      if (c === '*' && next === '/') {
        inBlock = false
        i++
      }
      continue
    }
    if (inString) {
      out += c
      if (c === '\\') {
        out += next
        i++
      } else if (c === '"') {
        inString = false
      }
      continue
    }
    if (c === '"') {
      inString = true
      out += c
      continue
    }
    if (c === '/' && next === '/') {
      inLine = true
      i++
      continue
    }
    if (c === '/' && next === '*') {
      inBlock = true
      i++
      continue
    }
    out += c
  }
  return out
}

/**
 * includeMatches reports whether a TypeScript `include` pattern matches a
 * slash-separated path relative to the config's directory. It covers the two
 * glob forms an `include` here uses -- `**` across directories and `*` within
 * one -- plus an exact file name, and nothing else: a pattern this does not
 * understand matches nothing, which is the direction that makes the test below
 * fail loudly rather than pass vacuously.
 */
function includeMatches(pattern: string, rel: string): boolean {
  const source = pattern
    .replace(/[.+^${}()|[\]\\]/g, '\\$&')
    .replace(/\*\*\//g, '(?:.*/)?')
    .replace(/\*\*/g, '.*')
    .replace(/\*/g, '[^/]*')
  return new RegExp(`^${source}$`).test(rel)
}

/**
 * rootTypeScriptFiles is every `.ts` file directly under console/ -- the files
 * no directory pattern in `include` reaches. node_modules and the dot
 * directories are stepped over: they are not this project's sources.
 */
function rootTypeScriptFiles(): string[] {
  return readdirSync(consoleRoot, { withFileTypes: true })
    .filter((entry) => entry.isFile() && entry.name.endsWith('.ts'))
    .map((entry) => entry.name)
    .toSorted()
}

describe('the tsconfig include matcher', () => {
  // The probe, so the assertion below is not made by a matcher that says yes to
  // everything. The refusal case is the real one: it is exactly the config this
  // repository had before the review -- src/, test/, tools/ and no root file.
  it('matches the patterns an include uses', () => {
    expect(includeMatches('playwright.config.ts', 'playwright.config.ts')).toBe(true)
    expect(includeMatches('src/**/*.ts', 'src/main/window.ts')).toBe(true)
    expect(includeMatches('test/**/*.ts', 'test/unit/pinning.test.ts')).toBe(true)
    expect(includeMatches('**/*.ts', 'playwright.config.ts')).toBe(true)
  })

  it('refuses a root file the directories do not reach', () => {
    expect(includeMatches('src/**/*.ts', 'playwright.config.ts')).toBe(false)
    expect(includeMatches('test/**/*.ts', 'playwright.config.ts')).toBe(false)
    expect(includeMatches('tools/**/*.ts', 'playwright.config.ts')).toBe(false)
    expect(includeMatches('src/*.ts', 'src/main/window.ts')).toBe(false)
  })
})

describe('console/tsconfig.json', () => {
  const config = JSON.parse(stripJSONComments(readFileSync(tsconfigPath, 'utf8'))) as {
    include?: unknown
    compilerOptions?: Record<string, unknown>
  }
  const include = Array.isArray(config.include) ? (config.include as string[]) : []

  it('declares an include list this test can read', () => {
    // An absent or empty `include` means "everything under the config's
    // directory" to TypeScript, so there would be nothing to check and this
    // test would pass by looking at nothing. That is a loud failure instead.
    expect(
      include.length,
      'console/tsconfig.json declares no include list: an absent include means "every file", which this test cannot check and which no reader can see the edges of',
    ).toBeGreaterThan(0)
  })

  it('includes every TypeScript file at the console root', () => {
    const rootFiles = rootTypeScriptFiles()
    expect(
      rootFiles,
      'no .ts file at the console root: this test would pass by looking at nothing',
    ).not.toEqual([])
    const uncovered = rootFiles.filter((rel) => !include.some((pattern) => includeMatches(pattern, rel)))
    expect(
      uncovered,
      `console/tsconfig.json's include ${JSON.stringify(include)} does not cover ${JSON.stringify(uncovered)}: a TypeScript file outside include is not typechecked by \`npm run typecheck\` at all, and the root config files are the ones no directory pattern reaches (playwright.config.ts's testDir/testMatch keep Playwright off the vitest unit tests). Name the file in include, or move it under src/, test/ or tools/.`,
    ).toEqual([])
  })

  it('typechecks the config file it is checked with', () => {
    // Named as well as covered by the loop above, because this one file is the
    // reason the rule exists.
    expect(rootTypeScriptFiles()).toContain('playwright.config.ts')
    expect(include.some((pattern) => includeMatches(pattern, 'playwright.config.ts'))).toBe(true)
  })
})
