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
