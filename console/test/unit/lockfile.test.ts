// The committed lockfile, and the two questions a reader of it should be able
// to answer without running npm (desktop-console plan Task 1.0; design section
// 5.2).
//
// `npm ci --ignore-scripts --no-audit --no-fund` is the task's command and it
// is what proves the TREE installs from this lockfile. This test is the other
// half: it proves the FILE is the one that belongs to package.json, so a
// lockfile that was hand-edited, regenerated against a different manifest, or
// left behind by a `npm install` of something nobody pinned fails here by name
// rather than at the next `npm ci` with npm's own message.
//
// Four things are asserted, and each is a claim the plan makes about the file:
//
//  1. it exists, at lockfileVersion 3 (the version npm 7+ writes and `npm ci`
//     reads), with the root package entry naming this package;
//  2. the root entry's dependencies and devDependencies are EXACTLY
//     package.json's -- same names, same specifiers, both directions -- which is
//     what "in sync" means for the manifest side;
//  3. every dependency the manifest declares resolves to a locked entry whose
//     version is the pinned version, and that entry carries a `resolved` URL and
//     an `integrity` hash: the proof that it came from a registry install and
//     not from a hand-written file, and the two fields `npm ci` cannot work
//     without;
//  4. nothing in the tree floats: no locked entry has a range where a version
//     belongs.

import { existsSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const consolePackageJSON = fileURLToPath(new URL('../../package.json', import.meta.url))
const consoleLockfile = fileURLToPath(new URL('../../package-lock.json', import.meta.url))

const dependencySections = [
  'dependencies',
  'devDependencies',
  'optionalDependencies',
  'peerDependencies',
] as const

const exactVersionRE = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/

type Manifest = Record<string, Record<string, string> | undefined>

interface LockEntry {
  version?: string
  resolved?: string
  integrity?: string
  link?: boolean
  [key: string]: unknown
}

interface Lockfile {
  name?: string
  lockfileVersion?: number
  requires?: boolean
  packages?: Record<string, LockEntry>
}

const manifest = JSON.parse(readFileSync(consolePackageJSON, 'utf8')) as Manifest

// A missing lockfile is the one failure whose fix is a command rather than an
// edit, so it is reported as itself instead of as a parse error.
if (!existsSync(consoleLockfile)) {
  throw new Error(
    'console/package-lock.json does not exist: the toolchain is pinned with a committed lockfile (design section 5.2, plan Task 1.0), and `npm ci` refuses to install without one. Generate it from the pinned manifest with `npm install --package-lock-only --ignore-scripts` and commit it.',
  )
}

const lock = JSON.parse(readFileSync(consoleLockfile, 'utf8')) as Lockfile
const packages = lock.packages ?? {}
const root = packages[''] ?? {}

// declaredDependencies is every dependency package.json declares, across all
// four sections, as name -> specifier; rootDependencies is the same map read
// off the lockfile's own root entry, which npm writes with one key per section
// the manifest has.
const declaredDependencies: Record<string, string> = {}
const rootDependencies: Record<string, string> = {}
for (const section of dependencySections) {
  for (const [name, specifier] of Object.entries(manifest[section] ?? {})) {
    declaredDependencies[name] = specifier
  }
  for (const [name, specifier] of Object.entries(
    (root[section] as Record<string, string> | undefined) ?? {},
  )) {
    rootDependencies[name] = specifier
  }
}

describe('console/package-lock.json', () => {
  it('exists and is a lockfileVersion 3 lockfile', () => {
    expect(lock.lockfileVersion).toBe(3)
  })

  it('names this package and its pinned manifest in the root entry', () => {
    expect(lock.name).toBe(manifest.name)
    expect(root.name).toBe(manifest.name)
    expect(root.version).toBe(manifest.version)
  })

  it('is in sync with package.json: the same dependency names in both directions', () => {
    // Both directions on purpose. A name in the lockfile and not in the
    // manifest is a leftover from an install nobody pinned; a name in the
    // manifest and not in the lockfile is a `npm ci` that will refuse, or worse
    // a dev machine where `npm install` silently added it.
    const inLock = Object.keys(rootDependencies).toSorted()
    const inDeclared = Object.keys(declaredDependencies).toSorted()
    expect(inLock, 'the root lock entry and package.json must declare the same packages').toEqual(
      inDeclared,
    )
  })

  it('is in sync with package.json: the same exact specifiers for every name', () => {
    for (const [name, specifier] of Object.entries(declaredDependencies)) {
      expect(rootDependencies[name], `${name} in the root lock entry`).toBe(specifier)
    }
  })

  it('locks every declared dependency to the pinned version, from a registry', () => {
    // This is the half a hand-written lockfile cannot fake: a locked entry has
    // to carry the resolved tarball URL and its integrity hash, and `npm ci`
    // verifies the hash of every tarball it downloads against it.
    const problems: string[] = []
    for (const [name, specifier] of Object.entries(declaredDependencies)) {
      const entry = packages[`node_modules/${name}`]
      if (entry === undefined) {
        problems.push(`${name}: no node_modules/${name} entry in the lockfile`)
        continue
      }
      if (entry.version !== specifier) {
        problems.push(`${name}: locked at ${entry.version}, pinned at ${specifier}`)
      }
      if (typeof entry.resolved !== 'string' || !entry.resolved.startsWith('https://')) {
        problems.push(`${name}: no https:// resolved URL`)
      }
      if (typeof entry.integrity !== 'string' || !/^sha(256|384|512)-/.test(entry.integrity)) {
        problems.push(`${name}: no integrity hash`)
      }
    }
    expect(problems, problems.join('\n')).toEqual([])
  })

  it('has a resolved, non-floating tree and no range anywhere in it', () => {
    const entries = Object.entries(packages).filter(([path]) => path !== '')
    expect(entries.length, 'the lockfile holds no packages: it is not this project’s').toBeGreaterThan(1)
    const problems: string[] = []
    for (const [path, entry] of entries) {
      if (entry.link === true) continue
      if (typeof entry.version !== 'string' || !exactVersionRE.test(entry.version)) {
        problems.push(`${path}: version ${JSON.stringify(entry.version)} is not exact`)
      }
      if (typeof entry.resolved !== 'string' || typeof entry.integrity !== 'string') {
        problems.push(`${path}: no resolved URL and integrity hash`)
      }
    }
    expect(problems, problems.join('\n')).toEqual([])
  })
})
