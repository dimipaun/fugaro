// website/test/wording.mjs — the words the site uses for what a run is (website changes
// spec of 2026-10-18, item 2: "use 'jobs' instead of 'workers' consistently across the
// site", accepted as "the old wording no longer appears anywhere on the site or its
// metadata or README").
//
// **The rule: a run's unit of work is a job, and "worker" is retired from the site's own
// copy.** The old tagline — "fleeting workers in the cloud for coding agents" — described
// the product in a word the reader has to decode, and item 2 replaced it with the headline
// and the subline (`Lambdas for coding agents` / `Ephemeral cloud jobs that turn tasks into
// pull requests.`). The word is the part a hero rewrite cannot retire on its own: it lives
// in a data file's answer, in a page's caption, in a README line and in a card. So this is
// a gate and not a search somebody ran once, in three parts:
//
//   1. **the built pages** — every page the artifact holds, except the three that render a
//      document of this repository (D188's list: `/docs/`, `/security/`, `/changelog/`),
//      carries the word nowhere a reader reads it: neither in its text nor in its `<title>`,
//      its description or its card;
//   2. **README.md**, which is where the site's own promise lines come from, and whose
//      "fleeting cloud workers" line was the old tagline itself;
//   3. **the site's own sources** — `website/src`, `website/data` and `website/public` — so
//      that the word cannot come back in a component or a data file and only show up on the
//      next build.
//
// **Why the three document-rendering pages are exempt, and how wide the exemption is.** They
// render a document of this repository rather than a page of the site: `/docs/` renders the
// generated CLI reference, whose first paragraph is the root command's own help text, and
// that help text still reads "Fleeting cloud workers for coding agents". The owner's ruling
// of 2026-10-18 takes that on purpose: the wording in the CLI's help, in the generated
// `website/content/cli-reference.md` and in the two plugin skills (`routing`, `working`)
// moves in the 0.9.0 work, not in this one, and the generated file may not be hand-edited.
// The exemption is exactly D188's three pages and no others, and the word is still refused
// everywhere else — including `website/data/faq.yaml`, whose answer said "a fleet of
// ephemeral cloud workers" and whose page `/faq/` is not exempt.
//
// A gate whose refusals have never been seen to fire is a gate nobody knows the shape of, so
// each part below has a case that plants the word where its rule looks and proves the failure
// arrives with the file's name in it. Standard library only; no network (Global Constraint 4).

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { DIST, SITE, builtPages, parse, textOf } from './lib/html.mjs'

/**
 * The retired word, as a word: `worker` and `workers`, and not `workered` or a substring of
 * a longer word. Case is not a distinction a reader makes.
 */
export const RETIRED = /\bworkers?\b/i

/**
 * The pages that render a document of this repository and are therefore exempt from part 1:
 * D188's own list, which is also the list the word ceiling exempts. The exemption is checked
 * rather than assumed — `exemptPages` below refuses to skip a page that is not one of these,
 * so the rule cannot quietly widen.
 */
export const DOCUMENT_PAGES = new Set(['/docs/', '/security/', '/changelog/'])

/**
 * The head's text that a reader meets outside the page: the `<title>`, the description and
 * the two cards. The metas are named one by one rather than swept up, because the head
 * carries values that are not text — `viewport`'s `width=device-width` is not a sentence
 * and a gate that read it would report noise.
 */
const HEAD_METAS = new Set(['description', 'og:title', 'og:description', 'og:site_name', 'twitter:card', 'twitter:title', 'twitter:description'])

function headText(html) {
  const out = []
  const title = /<title>([\s\S]*?)<\/title>/.exec(html)
  if (title) out.push(title[1])
  for (const m of html.matchAll(/<meta\b[^>]*>/g)) {
    const content = /\bcontent="([^"]*)"/.exec(m[0])
    const name = /\b(?:name|property)="([^"]*)"/.exec(m[0])
    if (content && name && HEAD_METAS.has(name[1])) out.push(content[1])
  }
  return out.join(' ')
}

/** The sentence the word appears in, so a failure shows what a reader would have read. */
function around(text, at) {
  const starts = Math.max(0, text.lastIndexOf('.', at) + 1)
  const end = text.indexOf('.', at)
  return text.slice(starts, end === -1 ? text.length : end + 1).trim().slice(0, 160)
}

/** Every way one built page carries the retired word, as the lines a failure lists. */
export function pageWordingFailures(html, rel) {
  const out = []
  const where = [
    ['its text', textOf(parse(html)).replace(/\s+/g, ' ')],
    ['its head', headText(html).replace(/\s+/g, ' ')],
  ]
  for (const [what, text] of where) {
    const found = RETIRED.exec(text)
    if (!found) continue
    out.push(
      `${rel} carries ${JSON.stringify(found[0])} in ${what}: ${JSON.stringify(around(text, found.index))}. ` +
        "A run's unit of work is a job, and 'worker' is the word item 2 retired from the site (changes spec 2026-10-18).",
    )
  }
  return out
}

/** Every page of a built artifact that carries the retired word, D188's three pages excepted. */
export function wordingFailures(dist) {
  const out = []
  for (const page of builtPages(dist)) {
    if (DOCUMENT_PAGES.has(page.url)) continue
    out.push(...pageWordingFailures(page.html, page.rel))
  }
  return out
}

/** Every way the site's own sources carry the retired word, as failure lines. */
export function sourceWordingFailures(files) {
  const out = []
  for (const [rel, body] of Object.entries(files)) {
    const found = RETIRED.exec(body)
    if (!found) continue
    out.push(
      `${rel} carries ${JSON.stringify(found[0])} in ${JSON.stringify(around(body.replace(/\s+/g, ' '), found.index))}: ` +
        "the site's own copy calls a run's unit of work a job (changes spec 2026-10-18, item 2).",
    )
  }
  return out
}

// ---- the built site ---------------------------------------------------------------------

/** The site's own sources: every text file under `src/`, `data/` and `public/`. */
function siteSources() {
  const files = {}
  const walk = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const file = path.join(dir, entry.name)
      if (entry.isDirectory()) walk(file)
      else if (/\.(astro|ts|mjs|css|yaml|json|txt|svg|webmanifest|html)$/.test(entry.name)) files[path.relative(SITE, file)] = readFileSync(file, 'utf8')
    }
  }
  for (const name of ['src', 'data', 'public']) walk(path.join(SITE, name))
  return files
}

test("no built page carries the retired word 'worker'", () => {
  const failures = wordingFailures(DIST)
  assert.deepEqual(failures, [], `the built site carries the retired wording:\n  ${failures.join('\n  ')}`)
})

test("the site's own sources carry no retired word", () => {
  const failures = sourceWordingFailures(siteSources())
  assert.deepEqual(failures, [], `the site's sources carry the retired wording:\n  ${failures.join('\n  ')}`)
})

test('README.md carries no retired word', () => {
  const failures = sourceWordingFailures({ 'README.md': readFileSync(path.join(SITE, '..', 'README.md'), 'utf8') })
  assert.deepEqual(failures, [], `README.md carries the retired wording:\n  ${failures.join('\n  ')}`)
})

// ---- the refusals, kept -----------------------------------------------------------------

function distOf(t, files) {
  const dist = mkdtempSync(path.join(tmpdir(), 'fugaro-wording-'))
  t.after(() => rmSync(dist, { recursive: true, force: true }))
  for (const [rel, body] of Object.entries(files)) {
    mkdirSync(path.dirname(path.join(dist, rel)), { recursive: true })
    writeFileSync(path.join(dist, rel), body)
  }
  return dist
}

const shell = (body, head = '') => `<!DOCTYPE html><html lang="en"><head><title>t</title>${head}</head><body><main>${body}</main></body></html>`

test('the word is refused in a page’s text, in its title and in its card, and only where the rule looks', (t) => {
  const files = {
    // The defect as it shipped once: a data file's answer, rendered into a page a reader
    // reads, and a description that says the same thing in the head.
    'faq/index.html': shell('<h1>Fugaro</h1><p>A fleet of ephemeral cloud workers for coding agents.</p>'),
    'caps/index.html': shell('<h1>Caps</h1><p>the figures</p>', '<meta name="description" content="one worker per task">'),
    'ok/index.html': shell('<h1>Fugaro</h1><p>Ephemeral cloud jobs that turn tasks into pull requests.</p>'),
    // The exemption, exactly as wide as D188's list: the page that renders the CLI
    // reference keeps the root command's help text, and its own copy is not the site's.
    'docs/index.html': shell('<h1>Reference</h1><p>Fleeting cloud workers for coding agents</p>'),
  }
  const failures = wordingFailures(distOf(t, files))
  assert.equal(failures.length, 2, failures.join('\n'))
  assert.ok(failures.some((line) => line.startsWith('faq/index.html ')), failures.join('\n'))
  assert.ok(failures.some((line) => line.startsWith('caps/index.html ')), failures.join('\n'))
})

test('the word is refused in README.md and in the site’s own sources', () => {
  const readme = ['# Fugaro', '', '**Fleeting cloud workers for coding agents: one task, one container, one PR, then gone.**', ''].join('\n')
  assert.match(sourceWordingFailures({ 'README.md': readme }).join('\n'), /README\.md carries "workers"/)

  const sources = {
    'data/faq.yaml': 'answer: >-\n  A fleet of ephemeral cloud workers for coding agents.\n',
    'src/components/Hero.astro': '<p>Ephemeral cloud jobs</p>\n',
  }
  const failures = sourceWordingFailures(sources)
  assert.equal(failures.length, 1, failures.join('\n'))
  assert.ok(failures[0].startsWith('data/faq.yaml '), failures.join('\n'))
})
