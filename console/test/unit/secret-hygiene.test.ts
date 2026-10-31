// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// No line of this repository carries a secret-shaped string (desktop-console
// plan Task 2.0; the recorder's own rule, `src/source/redact.ts`).
//
// `test/support/fakeSecrets.ts` is where a test gets a credential-shaped value,
// and it assembles every one of them from inert fragments at run time. The rule
// it exists to keep is worth a test rather than a habit, because the failure is
// silent: a fake token written as a literal is refused by GitHub's push
// protection (so the commit cannot be pushed at all), flagged by every scanner
// that reads the file, and indistinguishable to an incident responder from a
// leaked one.
//
// This file therefore walks the console's own sources and fails on the first
// line that matches a credential shape. It is the automated form of the check a
// person would otherwise run before a push -- and, like the `innerHTML` scan in
// `sanitize.test.ts`, it is checked against a planted line so that a scan which
// cannot fail is not mistaken for one that passes.
//
// The patterns themselves are assembled from fragments for the same reason the
// values are: a list of prefixes is a file a scanner flags.

import { createHash } from 'node:crypto'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'

import { describe, expect, it } from 'vitest'

/** inert joins fragments. Local rather than imported: this file must not depend
 * on the module whose contents it is checking, and the two lines below are all
 * of it that is needed. */
function inert(...fragments: readonly string[]): string {
  return fragments.join('')
}

const ROOT = new URL('../..', import.meta.url).pathname

/**
 * SHAPES are the credential families a scanner looks for: the forges' token
 * prefixes, the vendors' key prefixes, a JSON Web Token's first segment, a PEM
 * private-key header. Every one is written as fragments, and every one is the
 * beginning of a string that would be a real credential if it went on.
 */
const SHAPES: readonly { readonly what: string; readonly re: RegExp }[] = [
  { what: 'a forge token', re: new RegExp(inert('gh', 'p_', '|', 'github', '_pat_')) },
  { what: 'a Google access token', re: /ya29\./ },
  { what: 'a Google API key', re: new RegExp(inert('AI', 'za', '[0-9A-Za-z_-]{20}')) },
  { what: 'an assistant API key', re: new RegExp(inert('s', 'k', '-[A-Za-z0-9]{20}')) },
  { what: 'a Slack token', re: new RegExp(inert('xo', 'x[baprs]-')) },
  { what: 'an AWS access-key id', re: new RegExp(inert('AK', 'IA', '[0-9A-Z]{12}')) },
  { what: 'a PEM private key', re: new RegExp(inert('BEGIN ', '[A-Z ]*PRIVATE KEY-----')) },
  { what: 'a Google refresh token', re: /1\/\/0[0-9A-Za-z_-]{8}/ },
  { what: 'a JSON Web Token', re: new RegExp(inert('ey', 'J[A-Za-z0-9_-]{8,}', '\\.', '[A-Za-z0-9_-]{8,}')) },
]

/**
 * DSN_SHAPE is a Sentry DSN: the scheme, the 32-character public key, the host
 * and the project id. It is a shape rather than a credential -- a DSN can only
 * send events to one project and can read nothing -- and it is written as
 * fragments for the same reason every other pattern here is: a file that spells
 * one out is a file a scanner flags.
 *
 * It is deliberately **not** part of `SHAPES` above. `SHAPES` is scanned over
 * the whole tree, and the tests that need a DSN-shaped value in hand (this file,
 * `crash.test.ts`, `crash-electron.test.ts`) assemble one at run time -- which
 * is inert to a reader and to a scanner, but still matches a regex over the
 * file's *text*. The rule this shape belongs to is narrower and is the owner's
 * ruling of 2026-10-31: the console's own DSN is a public constant of the build,
 * it lives in exactly one file under `src/`, and no second one may appear
 * anywhere under `src/` again.
 */
const DSN_SHAPE = new RegExp(inert('https', '://', '[0-9a-f]{32}', '@', '[A-Za-z0-9.-]+', '/', '[0-9]+'))

/** DSN_ALLOWED is the whole allow-list, and it is one entry naming one file --
 * not a directory and not a pattern, so a new file, a renamed one or a second
 * literal in this one all fail `holds the DSN in exactly one file` below. The
 * reason is the ruling that put it there. */
const DSN_ALLOWED: readonly { readonly path: string; readonly why: string }[] = [
  {
    path: 'src/main/crash-dsn.ts',
    why: 'the console\'s Sentry DSN, a public client key embedded by the owner\'s ruling of 2026-10-31 (desktop-console Task 2.1b)',
  },
]

/**
 * DSN_SHA256 is the one DSN the console is allowed to carry, pinned by the hash
 * of its value rather than by the value: this file holds the digest, so the DSN
 * itself still appears in exactly one file, and a *different* DSN that satisfies
 * every shape check above -- right scheme, 32 hex characters, an EU host, a
 * numeric project id -- is still refused here. That is the gap the shape checks
 * leave: they say "a DSN and the right region", not "the project the owner
 * created for this console", and a report sent to somebody else's project is
 * lost with no error anywhere.
 */
const DSN_SHA256 = 'e2fed8dff2515876e406f528769bfbb7350a0149763e3986505e79b82a065294'

/** DSN_SCANNED_EXTENSIONS are the kinds of file under `console/src/` a DSN-shaped
 * literal could hide in. `.ts` is where a constant would go; `.html` and `.css`
 * are the renderer's documents and stylesheets, which are shipped as they are and
 * are read by anyone who opens the bundle; `.json` is a data file, which no
 * glob's shape check would catch because it is not a module. Everything else
 * under `src/` is a font, a licence text or a README, and none of them is a place
 * an endpoint is configured. */
const DSN_SCANNED_EXTENSIONS = ['.ts', '.html', '.css', '.json'] as const

/** dsnFindings is every line of `text` that carries a DSN, as `line: a DSN`. It
 * reports the position and never the value: a finding is a thing to go and look
 * at, not a second copy of the string this scan exists to keep to one place. */
function dsnFindings(text: string): string[] {
  const out: string[] = []
  for (const [index, line] of text.split('\n').entries()) {
    if (DSN_SHAPE.test(line)) out.push(`${String(index + 1)}: a DSN`)
  }
  return out
}

/** dsnPlaceholder is a DSN's shape assembled at run time, for the planted-line
 * check below -- the same helper the wiring's own test uses, and local for the
 * same reason `inert` is. */
function dsnPlaceholder(): string {
  return inert('https', '://', 'a'.repeat(32), '@', 'example', '.invalid', '/', '1')
}

/** crashDsnValue is the one DSN, read out of the one file that holds it. It
 * throws rather than answering undefined, because every check that uses it would
 * otherwise pass by reading nothing; and it hands the value to a test rather than
 * to the console, so this file still carries no DSN of its own. */
function crashDsnValue(): string {
  const source = readFileSync(join(ROOT, 'src/main/crash-dsn.ts'), 'utf8')
  const value = /CONSOLE_CRASH_DSN\s*=\s*\n?\s*'([^']+)'/.exec(source)?.[1]
  if (value === undefined) {
    throw new Error('crash-dsn.ts no longer exports a single-quoted constant: the checks below would read nothing')
  }
  return value
}

/** SCANNED are the directories and files this rule covers: everything a reader
 * of this repository could copy a shape out of. `node_modules`, `dist` and the
 * lockfile are build outputs rather than this repository's own writing. */
const SCANNED = ['src', 'test', 'tools', 'docs', 'README.md', 'package.json']
const SKIP = new Set(['node_modules', 'dist', 'coverage', '.record-fixture'])

function filesUnder(path: string): string[] {
  const found: string[] = []
  const stats = statSync(path)
  if (stats.isFile()) return [path]
  for (const entry of readdirSync(path)) {
    if (SKIP.has(entry)) continue
    const child = join(path, entry)
    if (statSync(child).isDirectory()) found.push(...filesUnder(child))
    else found.push(child)
  }
  return found
}

/** findings is every line of `text` matching one of `SHAPES`, as
 * `line: what`. It is a function so the planted-line check below can call it
 * with a string of its own. */
function findings(text: string): string[] {
  const out: string[] = []
  for (const [index, line] of text.split('\n').entries()) {
    for (const shape of SHAPES) {
      if (shape.re.test(line)) out.push(`${String(index + 1)}: ${shape.what}`)
    }
  }
  return out
}

/**
 * CREDENTIAL_ENV_KEYS are the environment variable names the console must never
 * read: the cloud SDK's chain, the forges' tokens, the registries' and the
 * vendors' keys.
 *
 * The console's environment rule is that it reads exactly the keys on
 * `CHILD_ENV_KEYS` and nothing else (`src/main/spawn.ts`), so the names below are
 * ones no module may reach for: reaching for one is a module that has grown a
 * second rule. They are **names and not values**, and a name is not a secret (the
 * plan's own rule for this file) -- but even the list is assembled from
 * fragments, because a file that lists the names a scanner looks for is a file a
 * scanner flags.
 */
const CREDENTIAL_ENV_KEYS: readonly string[] = [
  inert('AWS_', 'ACCESS_KEY_ID'),
  inert('AWS_', 'SECRET_ACCESS_KEY'),
  inert('AWS_', 'SESSION_TOKEN'),
  inert('GITHUB_', 'TOKEN'),
  inert('GH_', 'TOKEN'),
  inert('GOOGLE_APPLICATION_', 'CREDENTIALS'),
  inert('GOOGLE_API_', 'KEY'),
  inert('CLOUDSDK_AUTH_', 'ACCESS_TOKEN'),
  inert('CI_JOB_', 'TOKEN'),
  inert('NPM_', 'TOKEN'),
  inert('DOCKER_', 'PASSWORD'),
  inert('OPENAI_API_', 'KEY'),
  inert('ANTHROPIC_API_', 'KEY'),
  inert('SLACK_', 'TOKEN'),
]

/**
 * NEVER_READ_KEYS is every name no module may *read*, and `WSLENV` is the one
 * entry that is a rule rather than a credential: it is `wsl.exe`'s share list,
 * which says which Windows variables the distribution receives, so a console
 * that read the operator's own `WSLENV` would be forwarding a decision it did not
 * make (Part E, T-E2; F20, D5(a)). The console **writes** the key -- always
 * empty -- and never reads it, which is why it is scanned for reads rather than
 * for mentions: `export const WSLENV_KEY = 'WSLENV'` is this console stating the
 * list, and it is the only mention of a name here that is allowed.
 */
const NEVER_READ_KEYS: readonly string[] = [...CREDENTIAL_ENV_KEYS, inert('WSL', 'ENV')]

/** codeOf strips the comments a name may honestly appear in: a doc comment that
 * explains why the console does *not* read `AWS_SECRET_ACCESS_KEY` is a sentence
 * worth having, and this scan is about executable lines. */
function codeOf(text: string): string {
  // A block comment becomes its own whitespace rather than nothing, so the line
  // numbers a finding reports are the file's own.
  return text.replace(/\/\*[\s\S]*?\*\//g, (comment) => comment.replace(/[^\n]/g, ' ')).replace(/^\s*\/\/.*$/gm, '')
}

/**
 * readsOf is every place a line of *code* reads one of `keys` out of an
 * environment object, as `line: name`. It is deliberately loose about how the
 * read is spelled -- the property form `process.env.GITHUB_TOKEN`, the index form
 * `appEnv['GITHUB_TOKEN']`, a quoted name after anything that looks like an
 * environment -- because the failure this catches is a console that reaches past
 * the allow-list, and every spelling of that is the same finding.
 *
 * It is a function so the planted-line check can call it with a string of its
 * own.
 */
function readsOf(text: string, keys: readonly string[]): string[] {
  const out: string[] = []
  const re = new RegExp(
    `(?:process\\s*\\.\\s*env|appEnv|\\benv|\\benvironment)\\s*` +
      `(?:\\.\\s*(${keys.join('|')})\\b|\\[\\s*['"\`](${keys.join('|')})['"\`]\\s*\\])`,
  )
  for (const [index, line] of codeOf(text).split('\n').entries()) {
    const found = re.exec(line)
    if (found !== null) out.push(`${String(index + 1)}: ${found[1] ?? found[2] ?? ''}`)
  }
  return out
}

/** mentionsOf is every credential name a line of code names at all, whether it
 * reads it or not: a module with a reason to *write* one of these names has grown
 * the same second rule, and catching it by mention is catching it earlier. */
function mentionsOf(text: string, keys: readonly string[]): string[] {
  const out: string[] = []
  const re = new RegExp(`\\b(?:${keys.join('|')})\\b`)
  for (const [index, line] of codeOf(text).split('\n').entries()) {
    const found = re.exec(line)
    if (found !== null) out.push(`${String(index + 1)}: ${found[0]}`)
  }
  return out
}

describe('nothing under `console/src/` reads a credential out of the environment', () => {
  it('finds a read in a planted line, so the scan below can fail', () => {
    // The standing rule for a lint-style test: it is checked against the thing
    // it is supposed to catch. The planted lines are assembled here, so this file
    // does not name the shapes it looks for either.
    const token = inert('GITHUB_', 'TOKEN')
    const list = inert('WSL', 'ENV')
    expect(readsOf(`const auth = process.env.environment`, NEVER_READ_KEYS)).toEqual([])
    expect(readsOf(`const auth = process.env.${token}`, NEVER_READ_KEYS)).toEqual([`1: ${token}`])
    expect(readsOf(`const env = appEnv['${list}']`, NEVER_READ_KEYS)).toEqual([`1: ${list}`])
    expect(readsOf(`const env = appEnv.${token}`, NEVER_READ_KEYS)).toEqual([`1: ${token}`])
    expect(readsOf(`/* why we never read ${token} */`, NEVER_READ_KEYS)).toEqual([])
    expect(readsOf('const keys = CHILD_ENV_KEYS', NEVER_READ_KEYS)).toEqual([])
    expect(mentionsOf(`const env = options.appEnv`, CREDENTIAL_ENV_KEYS)).toEqual([])
    expect(mentionsOf(`const name = '${token}'`, CREDENTIAL_ENV_KEYS)).toEqual([`1: ${token}`])
  })

  it('reads none of them, and names none of the credential ones, in the sources', () => {
    // Every `.ts` file under `console/src/`, walked rather than listed: the name
    // claims whole-tree reach, and a module added under a new directory is
    // exactly what a hand-kept list would miss. The share list is scanned for
    // reads only, and for the reason written beside `NEVER_READ_KEYS`.
    const found: string[] = []
    const scanned: string[] = []
    for (const file of filesUnder(join(ROOT, 'src'))) {
      if (!file.endsWith('.ts')) continue
      scanned.push(file)
      const source = readFileSync(file, 'utf8')
      for (const hit of readsOf(source, NEVER_READ_KEYS)) found.push(`${relative(ROOT, file)}:${hit}`)
      for (const hit of mentionsOf(source, CREDENTIAL_ENV_KEYS)) found.push(`${relative(ROOT, file)}:${hit}`)
    }
    // A walk that read nothing would pass the assertion below and mean nothing,
    // the same reason the shape scan asserts its own count.
    expect(scanned.length).toBeGreaterThan(20)
    expect(found).toEqual([])
  })
})

describe('no line of this repository carries a secret-shaped string', () => {
  it('finds one in a planted line, so the scan below can fail', () => {
    // The plan's standing rule for a lint-style test: it is checked against the
    // thing it is supposed to catch. The planted line is assembled here, so this
    // file does not contain the shape it is looking for either.
    const planted = `const token = '${inert('gh', 'p_', 'a'.repeat(36))}'`
    expect(findings(planted)).toHaveLength(1)
    expect(findings('const token = process.env.GITHUB_TOKEN')).toEqual([])
  })

  it('finds none in the sources, the tests, the tools or the docs', () => {
    const found: string[] = []
    const scanned: string[] = []
    for (const target of SCANNED) {
      for (const file of filesUnder(join(ROOT, target))) {
        scanned.push(file)
        for (const hit of findings(readFileSync(file, 'utf8'))) {
          found.push(`${relative(ROOT, file)}:${hit}`)
        }
      }
    }
    // A walk that read nothing would pass the assertion below and mean nothing:
    // the console is more than twenty files, and the count is asserted so that a
    // `SCANNED` entry that stopped existing fails here rather than quietly.
    expect(scanned.length).toBeGreaterThan(20)
    expect(found).toEqual([])
  })
})

describe('the console\'s DSN is one public constant in one file, and the only one', () => {
  it('finds a DSN in a planted line, so the scans below can fail', () => {
    // Checked against the thing it is supposed to catch, like the two scans
    // above: the planted value is assembled here, so this file does not carry
    // the shape it looks for either.
    expect(dsnFindings(`export const CONSOLE_CRASH_DSN = '${dsnPlaceholder()}'`)).toEqual(['1: a DSN'])
    // ...and the shapes that are not a DSN, so the scan is about a DSN rather
    // than about any URL: the console names its own site and its own repository
    // in the plainest way and neither is a reporting endpoint.
    expect(dsnFindings('const site = "https://fugaro.dev"')).toEqual([])
    expect(dsnFindings('const docs = "https://github.com/dimipaun/fugaro"')).toEqual([])
  })

  it('holds the DSN in exactly one file under `console/src/`, as exactly one literal', () => {
    // The owner's ruling of 2026-10-31 gives the console a DSN it may carry in
    // its own source, and this is the price of it: one file, named in
    // `DSN_ALLOWED` above with the reason, and one literal inside it. A walk
    // rather than a list, because the failure this guards against is a *second*
    // place -- a copy into another module, a value moved into a shared constants
    // file, a second one added "for the tests" -- and over every extension a
    // second place could take rather than over `.ts` alone, because the renderer's
    // documents and stylesheets and the data files under `src/` ship verbatim and
    // a DSN in one of them would never be compiled, imported or type-checked.
    const found: string[] = []
    const carriers: string[] = []
    const scanned: string[] = []
    for (const file of filesUnder(join(ROOT, 'src'))) {
      if (!DSN_SCANNED_EXTENSIONS.some((extension) => file.endsWith(extension))) continue
      scanned.push(file)
      for (const hit of dsnFindings(readFileSync(file, 'utf8'))) {
        carriers.push(relative(ROOT, file))
        found.push(`${relative(ROOT, file)}:${hit}`)
      }
    }
    expect(scanned.length).toBeGreaterThan(20)
    const allowed = DSN_ALLOWED.map((entry) => entry.path)
    expect(
      carriers.toSorted(),
      `a DSN-shaped literal is in ${JSON.stringify(carriers)}; the allow-list is ${JSON.stringify(allowed)}, and it names files rather than directories or patterns`,
    ).toEqual([...allowed].toSorted())
    // One literal, not merely one file: a second value in the same file would
    // be a second endpoint nobody meant to add.
    expect(found, `the DSN-shaped literals are ${JSON.stringify(found)}`).toHaveLength(1)
  })

  it('reaches the EU region, and says so by its host', () => {
    // The ruling names the EU region, and `ingest.de.sentry.io` is where that
    // shows: a DSN copied from the console's US project would report to the
    // wrong region and read no differently here.
    const url = new URL(crashDsnValue())
    expect(
      url.hostname.endsWith('.ingest.de.sentry.io'),
      `the DSN's host is ${url.hostname}, which is not the EU region`,
    ).toBe(true)
    // The two parts that make it a *project's* endpoint rather than any other
    // URL with a key in it, so a placeholder or a swapped value fails here.
    expect(url.username).toMatch(/^[0-9a-f]{32}$/)
    expect(url.pathname).toMatch(/^\/[0-9]+$/)
  })

  it('is the exact DSN the owner created, and not another one that fits the shape', () => {
    // The shape checks above are a region and a grammar, and a different project
    // in the same region satisfies both: it would compile, type-check, pass every
    // scan here and send this console's reports to somebody else's project, where
    // nobody reads them and nothing says so. So the value is pinned by its digest
    // rather than by its shape, and the digest lives here rather than the value,
    // which keeps the DSN to the one file that holds it.
    expect(createHash('sha256').update(crashDsnValue(), 'utf8').digest('hex')).toBe(DSN_SHA256)
  })
})
