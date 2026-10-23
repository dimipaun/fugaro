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
