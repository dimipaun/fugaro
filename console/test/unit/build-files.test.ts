// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The packaged app carries the files it loads (desktop-console plan Task 3.4b,
// and the handoff to Task 4.1; design sections 5.1 rule 3 and 8.2).
//
// Task 3.4b adds a **second document** (`src/renderer/installations.html`) and a
// **second preload** (`src/preload/installations.ts`) to the console. Task 4.1
// (#540) is the task that packages the console: `console/package.json` gains an
// electron-builder `build` block, and its `files` list decides what is inside
// the app. **#540 had not merged when this task was written** -- `package.json`
// has no `build` key at all -- so this test is written to be useful on both
// sides of that merge:
//
//   - **before `build.files` exists**, it asserts the requirement is written
//     down where Task 4.1 will read it: `console/README.md` names every file
//     the packaged console has to carry, by path;
//   - **the day `build.files` exists**, it asserts each of those files is
//     covered by a pattern in it. A `build.files` that omitted
//     `installations.html`, or the picker's preload, or the compiled tree,
//     fails here by name rather than shipping an app that draws an empty window.
//
// The list is **read off the tree** rather than written out, so the next
// document or the next preload is covered the day it is added: every `.html` under
// `src/renderer` is served by the `app://` handler, every `.ts` under
// `src/preload` compiles to a preload a window loads by absolute path, and every
// `.ts` under `src/` compiles into `dist/` because that is what
// `tsconfig.build.json` says. A file that works in the development tree and is
// missing from the shipped bundle is exactly the failure this test exists to
// prevent, and it is not one the development tree can show you.

import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import { FONT_FILES } from '../../tools/bundle-renderer'

const consoleDir = fileURLToPath(new URL('../..', import.meta.url))

/** filesNamed lists the entries of one directory with the given suffix. */
function filesNamed(relative: string, suffix: string): string[] {
  return readdirSync(join(consoleDir, relative))
    .filter((name) => name.endsWith(suffix))
    .sort()
}

/** compiledModules walks `src/` and returns the `dist/` path each module
 * compiles to -- `tsconfig.build.json`'s `rootDir: "src"`, `outDir: "dist"`. */
function compiledModules(relative = 'src', prefix = 'dist'): string[] {
  const out: string[] = []
  for (const entry of readdirSync(join(consoleDir, relative)).sort()) {
    const source = `${relative}/${entry}`
    const built = `${prefix}/${entry}`
    if (statSync(join(consoleDir, source)).isDirectory()) out.push(...compiledModules(source, built))
    else if (entry.endsWith('.ts')) out.push(`${built.slice(0, -3)}.js`)
  }
  return out
}

/** documents are the pages the protocol handler serves, and the picker's is the
 * one this task adds. */
const documents = filesNamed('src/renderer', '.html').map((name) => `src/renderer/${name}`)

/** preloads are the bridge files a window loads by absolute path: the
 * dashboard's (`dist/preload/index.js`, which Task 3.4a sets) and the picker's
 * (`dist/preload/installations.js`, which Task 3.4b sets). */
const preloads = filesNamed('src/preload', '.ts').map((name) => `dist/preload/${name.slice(0, -3)}.js`)

/** compiled is the whole build output the main process and the renderer run
 * from, and the bundler's five outputs are what the documents actually load:
 * the dashboard's entry, the picker's entry, the confirmation window's entry
 * (V2.5.1a), the stylesheet every document
 * links (Task 6.2), and elkjs's layout worker (GV2.2). The stylesheet and the
 * worker are the ones this list catches by name, and that is the whole point of
 * naming them here: a `build.files` that carried the two bundles and not
 * `console.css` would package an app that opens a window with no style in it,
 * which is the failure this test exists for. The notices file is not a build
 * output, but it ships beside the worker whose licence it carries, so it is
 * named here too, and so is the console's own licence (`LICENSE.md`, D311),
 * which the packed app carries from 0.113.0. */
const compiled = compiledModules()
const bundlerOutputs = [
  'LICENSE.md',
  'THIRD_PARTY_NOTICES.md',
  'dist/bundle/confirm.html',
  'dist/bundle/confirm.js',
  'dist/bundle/console.css',
  'dist/bundle/console.js',
  'dist/bundle/elk-worker.min.js',
  'dist/bundle/installations.js',
  ...FONT_FILES.map(({ file }) => `dist/bundle/${file}`),
]

/** NEVER_SHIPPED are the two compiled files Task 4.1 keeps out of the package by
 * name: the packing hook (it needs a devDependency) and the fixture loader. */
const NEVER_SHIPPED: readonly string[] = ['dist/main/fuses.js', 'dist/source/fixture.js']

/**
 * BUILD_ONLY are the packages the console's production tree reaches only through
 * `@sentry/bundler-plugins` -- the subpath `@sentry/node` carries so that a
 * *bundler* can wrap a webpack, vite, rollup or esbuild build (Task 2.1b, fix
 * round A). The console runs no bundler: the only bundling it does is
 * `npm run build`, on a build machine, and `dist/bundle` is what that writes.
 * The tree was in the asar and never loaded (the probe in the PR description
 * walks `@sentry/electron/main` and the compiled `dist/main/crash-electron.js`
 * and shows fourteen packages load, none of them these), so `build.files`
 * excludes them by name and this list is what pins the exclusion.
 *
 * Every one of them is loadable-only-from-the-bundler-plugins-subpath, read off
 * `package-lock.json`: `glob`, `minimatch`, `brace-expansion` and
 * `balanced-match` arrive with it; `oxc-parser` and its nineteen
 * platform-specific `@oxc-parser/binding-*` packages are how it parses source;
 * `@oxc-project/types` and `magic-string` are that parser's types and code
 * generator; `@jridgewell/sourcemap-codec` is what maps its output back;
 * `sentry` is the newer bundler-side SDK it pulls in beside itself;
 * `lru-cache`, `minipass`, `path-scurry` and `dotenv` are its own dependencies.
 * A scope with no package name (`@oxc-parser`, `@oxc-project`) is excluded as a
 * whole directory, which is what the packed tree holds.
 */
const BUILD_ONLY: readonly string[] = [
  '@jridgewell/sourcemap-codec',
  '@oxc-parser',
  '@oxc-project',
  '@sentry/bundler-plugins',
  'balanced-match',
  'brace-expansion',
  'dotenv',
  'glob',
  'has-flag',
  'lru-cache',
  'magic-string',
  'minimatch',
  'minipass',
  'oxc-parser',
  'path-scurry',
  'sentry',
  'supports-color',
]

/**
 * Two of the names above are the round B finding, and they are excluded for a
 * reason the walk of the lockfile cannot see: `debug` (shipped, required by
 * `electron-updater`) does `require('supports-color')` inside a `try` and
 * swallows the failure -- an undeclared, optional require, so
 * `package-lock.json` records that edge only under `@sentry/bundler-plugins` and
 * `chalk`, and no walk reaches it. The packer's own collector does follow it, so
 * it hoisted `supports-color` and `has-flag` **out of the excluded tree** and
 * into the archive's own `node_modules` -- which is how a package the notices
 * never described shipped anyway (round A's `notices.test.ts` could not see it:
 * it reasons from the lockfile, whose top-level `supports-color` is the dev
 * 7.2.0). Excluding them here is what puts the packed app back on the notices'
 * set, and it is safe because that `require` is the optional-dependency idiom:
 * with the package gone `debug` catches the failure and turns colours off.
 * `scripts/check-packed-notices.mjs` runs the packer and checks the archive
 * itself, so this list is no longer the only thing standing behind the claim.
 */

/** globToRegExp turns one electron-builder `files` pattern into a matcher. `**`
 * spans directories, `*` and `?` do not cross a `/`. */
function globToRegExp(pattern: string): RegExp {
  let out = ''
  for (let index = 0; index < pattern.length; index += 1) {
    const character = pattern[index] ?? ''
    if (character === '*') {
      if (pattern[index + 1] === '*' && pattern[index + 2] === '/') {
        // `**/` spans zero or more directories, as electron-builder's minimatch
        // reads it: `dist/main/**/*.js` carries `dist/main/index.js`.
        out += '(?:.*/)?'
        index += 2
      } else if (pattern[index + 1] === '*') {
        out += '.*'
        index += 1
      } else out += '[^/]*'
    } else if (character === '?') out += '[^/]'
    else out += character.replace(/[.+^${}()|[\]\\]/g, '\\$&')
  }
  return new RegExp(`^${out}$`)
}

/**
 * covers reports whether one `files` pattern carries a path. It accepts the
 * three spellings electron-builder treats as inclusion -- the exact path, a
 * glob that matches it, and a **directory** (`dist` or `dist/`, which means
 * everything under it) -- and it never counts a negation (`!...`) as inclusion:
 * a pattern that excludes the file is not a pattern that carries it.
 */
function covers(pattern: string, path: string): boolean {
  if (pattern.startsWith('!')) return false
  const bare = pattern.endsWith('/') ? pattern.slice(0, -1) : pattern
  if (!bare.includes('*') && !bare.includes('?')) {
    return path === bare || path.startsWith(`${bare}/`)
  }
  return globToRegExp(bare).test(path)
}

/** uncovered returns the required paths no pattern carries -- the files a
 * `build.files` list would leave out of the shipped app. */
function uncovered(patterns: readonly string[], required: readonly string[]): readonly string[] {
  return required.filter((path) => !patterns.some((pattern) => covers(pattern, path)))
}

/** readme is where the requirement is written down until Task 4.1 reads it. */
const readme = readFileSync(join(consoleDir, 'README.md'), 'utf8')

/** packageJson is the file Task 4.1 edits. */
const packageJson = JSON.parse(readFileSync(join(consoleDir, 'package.json'), 'utf8')) as {
  readonly build?: { readonly files?: unknown }
}

/** buildFiles is `build.files`, in either spelling electron-builder accepts (a
 * list, or one string), or `null` when Task 4.1 has not landed. */
function buildFiles(): readonly string[] | null {
  const files = packageJson.build?.files
  if (typeof files === 'string') return [files]
  if (Array.isArray(files) && files.every((entry) => typeof entry === 'string')) {
    return files as readonly string[]
  }
  return null
}

describe('the packaged console carries what it loads', () => {
  it('reads the list off the tree, so a new document or preload is covered', () => {
    // Non-vacuity first: a walk that found nothing would make every assertion
    // below true and mean nothing.
    expect(documents).toContain('src/renderer/installations.html')
    expect(documents).toContain('src/renderer/refusal.html')
    expect(preloads).toEqual([
      'dist/preload/confirm.js',
      'dist/preload/index.js',
      'dist/preload/installations.js',
    ])
    expect(compiled).toContain('dist/main/picker.js')
    expect(compiled).toContain('dist/renderer/installations.js')
    expect(compiled.length).toBeGreaterThan(15)
  })

  it('names every one of them in the README, where Task 4.1 reads it', () => {
    // The half that holds **today**. `build.files` is Task 4.1's (#540), and it
    // had not merged when this task was written, so the list lives in the
    // README until it does -- and this test is what keeps it there.
    for (const path of [...documents, ...preloads]) {
      expect(readme, `the README must name ${path}`).toContain(path)
    }
    expect(readme).toContain('dist/**')
  })

  it('carries each of them once `build.files` exists (Task 4.1, #540)', () => {
    const patterns = buildFiles()
    if (patterns === null) {
      // Task 4.1 has not landed. The assertion above is the one that holds the
      // requirement in the meantime; this test says so rather than passing
      // silently on an empty check.
      expect(packageJson.build).toBeUndefined()
      return
    }
    // Since Task 3.4a the documents are served from the bundler's output, not
    // from `src/renderer`: each source document ships as its copy in
    // `dist/bundle/`, and the renderer's compiled modules ship inside the two
    // bundles rather than as `dist/renderer/*.js` (test/unit/bundle.test.ts
    // holds each bundle to its closure). Task 4.1 excludes the two compiled
    // files the app never loads, by name; they are named here too, and asserted
    // excluded, so the required list below is everything else `tsc` emits.
    const served = documents.map((path) => path.replace(/^src\/renderer\//, 'dist/bundle/'))
    const runtime = compiled.filter(
      (path) => !path.startsWith('dist/renderer/') && !NEVER_SHIPPED.includes(path),
    )
    expect(runtime.length, 'the runtime set is empty: the check below would be vacuous').toBeGreaterThan(10)
    const missing = uncovered(patterns, [
      ...served,
      ...preloads,
      ...runtime,
      ...bundlerOutputs,
    ])
    expect(missing, 'these files are loaded at runtime and are not in build.files').toEqual([])
    for (const excluded of NEVER_SHIPPED) {
      expect(compiled, `${excluded} is not emitted, so its exclusion names nothing`).toContain(excluded)
      expect(patterns, `${excluded} must be excluded by name`).toContain(`!${excluded}`)
    }
    // And the sources are not shipped in place of the bundle's copies.
    for (const document of documents) {
      expect(patterns, `${document} is the source, not the served copy`).not.toContain(document)
    }
  })

  it('keeps the build-time packages out of the package (Task 2.1b, fix round A)', () => {
    const patterns = buildFiles() ?? []
    // Non-vacuity: a list that had emptied would make every assertion below true.
    expect(patterns.length, 'build.files must exist for this list to mean anything').toBeGreaterThan(20)
    expect(BUILD_ONLY.length).toBeGreaterThan(10)
    for (const name of BUILD_ONLY) {
      expect(patterns, `${name} is a build-time package and must be excluded by name`).toContain(
        `!node_modules/${name}/**`,
      )
    }
    // And the negations are exactly these: a `!node_modules/...` entry that
    // named something else would be a package going out of the bundle without
    // ever being argued for here. The two compiled files Task 4.1 excludes are
    // the other two negations in the list, and they are named.
    const negations = patterns.filter((pattern) => pattern.startsWith('!'))
    expect(negations.toSorted()).toEqual(
      [...BUILD_ONLY.map((name) => `!node_modules/${name}/**`), ...NEVER_SHIPPED.map((path) => `!${path}`)].toSorted(),
    )
  })

  it('keeps the symbol uploader out of the package (the owner’s ruling of 2026-11-01)', () => {
    // `@sentry/cli` is the uploader `scripts/upload-sentry-symbols.mjs` runs
    // after a build. It is a build tool and not a runtime: the app never runs it,
    // the release job does, and everything in `dependencies` is packed into the
    // shipped archive -- so its home is `devDependencies`, which is what the
    // script's own binary path and the pinned-toolchain rule both assume. Two of
    // the three assertions below are the load-bearing ones: electron-builder
    // packs `dependencies` and not `devDependencies`, so a section that named it
    // would ship it, and a `files` pattern that named it would put the CLI, and
    // the per-platform binaries its postinstall downloads, inside the operator's
    // download. The third is the non-vacuity: a package that is in neither
    // section is one the script could not run.
    const sections = JSON.parse(readFileSync(join(consoleDir, 'package.json'), 'utf8')) as {
      readonly dependencies?: Record<string, string>
      readonly devDependencies?: Record<string, string>
    }
    expect(Object.keys(sections.dependencies ?? {})).not.toContain('@sentry/cli')
    expect(Object.keys(sections.devDependencies ?? {})).toContain('@sentry/cli')
    for (const pattern of buildFiles() ?? []) {
      expect(pattern, `${pattern} names the symbol uploader, which is a build tool and not part of the app`).not.toContain(
        '@sentry/cli',
      )
    }
  })

  it('the matcher itself: a directory, a glob and a negation', () => {
    // The checker is tested against the shapes it must accept and the one it
    // must refuse, so a `covers` that answered `true` to everything could not
    // make the test above pass.
    expect(covers('dist/**', 'dist/preload/installations.js')).toBe(true)
    // `**/` spans zero directories too: the shape Task 4.1's list uses.
    expect(covers('dist/main/**/*.js', 'dist/main/index.js')).toBe(true)
    expect(covers('dist/main/**/*.js', 'dist/main/sub/deep.js')).toBe(true)
    expect(covers('dist/main/**/*.js', 'dist/mainly.js')).toBe(false)
    expect(covers('dist/main/**/*.js', 'dist/preload/index.js')).toBe(false)
    expect(covers('dist', 'dist/main/picker.js')).toBe(true)
    expect(covers('dist/', 'dist/main/picker.js')).toBe(true)
    expect(covers('src/renderer/*.html', 'src/renderer/installations.html')).toBe(true)
    expect(covers('src/renderer/*.html', 'src/renderer/screens/live.html')).toBe(false)
    expect(covers('dist/main/**', 'dist/preload/installations.js')).toBe(false)
    expect(covers('!dist/preload/**', 'dist/preload/installations.js')).toBe(false)
    expect(uncovered(['dist/**'], ['dist/preload/index.js'])).toEqual([])
    expect(uncovered(['dist/main/**'], ['dist/preload/index.js'])).toEqual(['dist/preload/index.js'])
  })
})
