// website/test/security.mjs — the security page (website plan, Task W1.8).
//
// The page this file checks renders `website/content/security.md`, which W0.5 generates
// from `SECURITY.md` byte for byte below its title. Everything here is therefore checked
// against **SECURITY.md itself** rather than against the generated file or against the
// page's source: a test written against the thing it is meant to hold agrees with whatever
// that thing does, which is the one thing a drift test must never do (plan Review Focus).
//
// Six things are checked, and each one is the mechanical form of a sentence the plan
// states (W1.8's steps and its `Tests` line):
//
//   1. **The headings are the document's, in order** (step 1, F40, D99). The page renders
//      the document; it does not improve a heading on the way. A security page whose
//      heading says "What we protect" where `SECURITY.md` says "Trust model and known
//      limits" is a claim the document does not make, and it is the red step this task
//      was written against.
//   2. **Every anchor the repository's cross-references name is in the built page**
//      (step 2, F40). `SECURITY.md` sends its own readers to a bullet or a heading by
//      name — `see "Narrow git credentials" above` — and the page is what that reader
//      lands on, so the headings and the bold bullet leads have to survive the renderer
//      as ids. The rule is W0.5's anchor rule (a `##`-`####` heading, or a bullet whose
//      bold run leads it), applied here to the built HTML.
//   3. **The page renders the document's text and adds no sentence of its own** (step 1's
//      "no editing"). Every block of the rendered document is a block of `SECURITY.md`,
//      and the rendered region carries nothing else.
//   4. **The page's own text is its lead line and its contents, and both are link lines**
//      (step 1's "the page adds a lead sentence and nothing else ... a link line rather
//      than a claim", step 3's reporting pointer). Neither region may carry a sentence:
//      text inside them is inside a link, and what those links say is a heading of the
//      document or the document's own source path.
//   5. **The contents is generated from the document's headings** (step 1): one entry per
//      heading below the title, in the document's order, each pointing at that heading's
//      own id.
//   6. **Every link the document carries survives the render** (step 1's "no editing"):
//      the target and the label, both. The document's links point into `docs/` with paths
//      that are meaningful from the repository root; the page does not rewrite them,
//      because rewriting a security document's links is editing it.
//
// How it reads the page: the built artifact, `website/dist/security/index.html`, written
// by `npm run build` — which `npm run check` runs before it runs any file here, so the
// page this file checks is the one the build produced rather than the one the source
// hopes to produce. The regions are found by the markers the page's own markup carries
// (`data-security-lead`, `data-security-toc`, `data-security-document`, `data-security-page`),
// and the text of a region is compared with the document's text through one normalisation,
// `flatten`: tags removed, HTML entities decoded, whitespace removed. Removing the
// whitespace is what makes the comparison a comparison of the document's words rather
// than of two renderers' line breaks.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const SITE = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const REPO = path.dirname(SITE)

/** The document the page renders, and the source W0.5 generates it from. */
const SOURCE = 'SECURITY.md'
const GENERATED = 'website/content/security.md'
const BUILT = path.join('dist', 'security', 'index.html')

/** The document's bytes, read from the repository rather than from the build. */
function sourceDocument() {
  return readFileSync(path.join(REPO, SOURCE), 'utf8')
}

/** The built page, or a failure that says how it is produced. */
function builtPage() {
  const file = path.join(SITE, BUILT)
  if (!existsSync(file)) {
    throw new Error(
      `${BUILT} is not there: the page this file checks is the artifact \`npm run build\` writes, and \`npm run check\` builds before it runs these checks (W1.8). Run \`npm run check\` from website/.`,
    )
  }
  return readFileSync(file, 'utf8')
}

// ---------------------------------------------------------------------------------------
// Reading a region out of the built page, and its text out of the region.
// ---------------------------------------------------------------------------------------

/**
 * The markup of one region of the page, found by the marker attribute its element
 * carries. The document region is a `<div>` whose content is headings, paragraphs and
 * lists, so the first closing `</div>` is its own; the lead is a `<p>` and the contents a
 * `<nav>`, for the same reason.
 */
function region(page, marker, tag) {
  const found = new RegExp(`<${tag}\\b[^>]*${marker}[^>]*>([\\s\\S]*?)</${tag}>`).exec(page)
  assert.ok(found, `the built page carries no <${tag} ${marker}> region: the page's own markup is what this check reads it by (W1.8)`)
  return found[1]
}

/** The page's own region: everything the page adds below the layout's own chrome. */
function pageRegion(page) {
  return region(page, 'data-security-page', 'article')
}

const ESCAPES = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", '#39': "'" }

/** A region's visible text: tags removed, entities decoded. */
function textOf(html) {
  return html
    .replace(/<[^>]*>/g, '')
    .replace(/&([a-z#0-9]+);/gi, (whole, name) => (name in ESCAPES ? ESCAPES[name] : whole))
}

/**
 * Text reduced to the words themselves: no whitespace, so that two renderers' line
 * breaks, indentation and block boundaries cannot make the same sentence look different.
 * It is the only normalisation this file applies, and it applies it to both sides.
 */
function flatten(text) {
  return text.replace(/\s+/g, '')
}

/** The headings of a Markdown document or of a rendered region, in order. */
function headings(text) {
  const markdown = /^(#{1,6})[ \t]+(.*?)[ \t]*$/gm
  const html = /<h([1-6])\b([^>]*)>([\s\S]*?)<\/h\1>/g
  const found = [...text.matchAll(markdown)].map((m) => ({ level: m[1].length, text: m[2].trim(), id: null }))
  if (found.length > 0) return found
  return [...text.matchAll(html)].map((m) => ({
    level: Number(m[1]),
    text: textOf(m[3]).trim(),
    id: /\sid="([^"]+)"/.exec(m[2])?.[1] ?? null,
  }))
}

/** A line of the document with its Markdown inline markup taken off, as a reader reads it. */
function inlineText(line) {
  return line
    .replace(/^\s*#{1,6}[ \t]+/, '')
    .replace(/^\s*-[ \t]+/, '')
    .replace(/`([^`]*)`/g, '$1')
    .replace(/\[([^\]]*)\]\([^)\s]+\)/g, '$1')
    .replace(/\*\*([^*]+)\*\*/g, '$1')
    .replace(/\*([^*]+)\*/g, '$1')
    .trim()
}

/**
 * Every block of the document as a reader reads it: its headings, its paragraphs and its
 * bullet lines, inline markup off. This is the set of sentences the page is allowed to
 * carry, and check 3 is that it carries no others.
 */
function documentBlocks(document) {
  return document
    .split('\n')
    .map((line) => inlineText(line))
    .filter((line) => line !== '')
}

/**
 * The anchor name of a Markdown heading or of a bullet's bold lead, reduced to the id a
 * rendered page gives it: lowercase, inline markup off, punctuation dropped, spaces to
 * hyphens — the convention the built page's own ids are written in, restated here so that
 * the two can be compared. It is deliberately the same rule as `website/src/pages/security.astro`'s
 * slug, and this test is where the two are held together.
 */
function slug(text) {
  return inlineText(text)
    .toLowerCase()
    .replace(/['’]/g, '')
    .replace(/[^\p{L}\p{N} -]/gu, '')
    .trim()
    .replace(/\s+/g, '-')
}

/** The names `SECURITY.md` sends its own readers to: `see "X" above` / `see "X" below`. */
function crossReferences(document) {
  return [...document.matchAll(/see "([^"]+)" (?:above|below)/g)].map((m) => m[1])
}

// ---------------------------------------------------------------------------------------

test("the security page's headings are SECURITY.md's, in order", () => {
  const document = sourceDocument()
  const documentHeadings = headings(document)
  assert.ok(documentHeadings.length >= 8, `read ${documentHeadings.length} headings out of ${SOURCE}: the document has moved, so this check saw nothing`)

  const rendered = headings(region(builtPage(), 'data-security-document', 'div'))
  assert.ok(rendered.length > 0, 'the built page carries no heading inside its document region')

  // The paraphrase first, because it is the edit a security page is always tempted by and
  // the one this check exists for: a heading "improved" on the way to the site is a claim
  // the document does not make.
  const known = new Set(documentHeadings.map((h) => flatten(h.text)))
  for (const heading of rendered) {
    assert.ok(
      known.has(flatten(heading.text)),
      `built page heading "${heading.text}" is not in ${SOURCE}: the page renders the document's own headings, because they are what the repository's own cross-references name (F40, D99, W1.8 step 1)`,
    )
  }

  assert.deepEqual(
    rendered.map((h) => [h.level, flatten(h.text)]),
    documentHeadings.map((h) => [h.level, flatten(h.text)]),
    `${SOURCE}'s headings and the built page's are not the same headings in the same order`,
  )

  // An anchor per heading: the page is what a reader is sent to, so a heading with no id
  // is a heading no cross-reference can reach (check 2 below asserts the same from the
  // other end).
  for (const heading of rendered) {
    assert.ok(heading.id, `the built page renders "${heading.text}" with no id, so nothing can send a reader to it`)
  }
})

test('every anchor the repository sends a reader to is in the built page', () => {
  const document = sourceDocument()
  const references = crossReferences(document)
  assert.ok(references.length >= 5, `read ${references.length} cross-references out of ${SOURCE}: the document has moved, so this check saw nothing`)

  const page = builtPage()
  const ids = [...page.matchAll(/\sid="([^"]+)"/g)].map((m) => m[1])
  assert.ok(ids.length > 0, 'the built page carries no id at all')

  // The document names a destination the way its own text does — a heading, or a bold
  // bullet lead — and a reference may name a prefix of a longer name, which is why this
  // is a prefix rule rather than an equality (W0.5's anchor rule, and the reason it is
  // the same rule here is that the two tests must not disagree about what an anchor is).
  for (const reference of references) {
    const wanted = slug(reference)
    assert.ok(
      ids.some((id) => id === wanted || id.startsWith(wanted)),
      `${SOURCE} sends a reader to "${reference}" and the built page has no anchor "${wanted}": the page is where that reader lands, so a heading or a bullet lead that did not survive the renderer is a dead end (F40, W1.8 step 2)`,
    )
  }
})

test('the page renders the document and adds no sentence of its own', () => {
  const document = sourceDocument()
  const rendered = region(builtPage(), 'data-security-document', 'div')

  // Every block of the rendered document is a block of SECURITY.md. The longest blocks
  // are removed first so that a short block cannot eat into a longer one, and what is
  // left has to be nothing: a sentence the document does not carry is a security claim
  // written by whoever edited the page (D99, F41).
  let left = flatten(textOf(rendered))
  const blocks = [...new Set(documentBlocks(document))].sort((a, b) => b.length - a.length)
  for (const block of blocks) left = left.split(flatten(block)).join('')

  assert.equal(
    left,
    '',
    `the built page carries text ${SOURCE} does not carry, and a security sentence written for the site is a claim the document does not make: ${JSON.stringify(left.slice(0, 200))}`,
  )
})

test("the page's own text is the lead line and the contents, and both are link lines", () => {
  const page = builtPage()
  const lead = region(page, 'data-security-lead', 'p')
  const contents = region(page, 'data-security-toc', 'nav')
  const document = region(page, 'data-security-document', 'div')

  // The page is those three regions and nothing else: a page that adds an introduction, a
  // summary or a "what this means for you" paragraph fails here (W1.8 step 1: "the page
  // adds a lead sentence and nothing else").
  assert.equal(
    flatten(textOf(pageRegion(page))),
    flatten(textOf(lead) + textOf(contents) + textOf(document)),
    'the page carries text outside its lead line, its contents and the document it renders',
  )

  for (const [name, html] of [
    ['the lead line', lead],
    ['the contents', contents],
  ]) {
    assert.match(html, /<a\b[^>]*href="[^"]+"/, `${name} carries no link, and both are rendered as link lines rather than as claims (W1.8 steps 1 and 3)`)
    assert.equal(
      flatten(textOf(html.replace(/<a\b[^>]*>[\s\S]*?<\/a>/g, ''))),
      '',
      `${name} carries text outside a link: the page states nothing of its own about the security model (D99)`,
    )
  }

  // What the lead line's links say is drawn from the document and from the generated
  // file's own header, never written here: the reporting section's own heading, and the
  // path of the file this page is rendered from (W0.5's header, which names its source).
  const generated = readFileSync(path.join(SITE, 'content', 'security.md'), 'utf8').split('\n')
  const sourcePath = /^<!--\s*source:\s*(.+?)\s*-->$/.exec(generated[1])?.[1]
  assert.ok(sourcePath, `${GENERATED} carries no "source:" header line, so the page cannot say what it renders`)
  assert.equal(sourcePath, SOURCE, `${GENERATED} says it was generated from ${JSON.stringify(sourcePath)}, and this page and ${SOURCE} are the same document`)

  const reportingHeading = headings(sourceDocument()).find((h) => h.text === 'Reporting a vulnerability')
  assert.ok(reportingHeading, `${SOURCE} has no "Reporting a vulnerability" heading, so the page has nowhere to send a reader who has found one (W1.8 step 3)`)

  const labels = [...lead.matchAll(/<a\b[^>]*>([\s\S]*?)<\/a>/g)].map((m) => flatten(textOf(m[1])))
  assert.deepEqual(
    labels,
    [flatten(reportingHeading.text), flatten(sourcePath)],
    'the lead line says something other than the reporting section\'s own heading and the document\'s own source path',
  )
  assert.match(
    lead,
    new RegExp(`href="#${slug(reportingHeading.text)}"`),
    'the lead line does not point at the reporting section itself, which is the pointer W1.8 step 3 asks for: the section is in the document below, and the page does not paraphrase it',
  )
})

test("the contents is generated from the document's headings", () => {
  const page = builtPage()
  const contents = region(page, 'data-security-toc', 'nav')
  const below = headings(sourceDocument()).filter((h) => h.level >= 2)
  assert.ok(below.length >= 8, `read ${below.length} headings below the title: the document has moved, so this check saw nothing`)

  assert.deepEqual(
    [...contents.matchAll(/href="#([^"]+)"/g)].map((m) => m[1]),
    below.map((h) => slug(h.text)),
    "the contents' targets are not the document's headings, in the document's order",
  )
  assert.deepEqual(
    [...contents.matchAll(/<a\b[^>]*>([\s\S]*?)<\/a>/g)].map((m) => flatten(textOf(m[1]))),
    below.map((h) => flatten(h.text)),
    "the contents' entries are not the document's own heading text",
  )
})

test('every link the document carries survives the render', () => {
  const document = sourceDocument()
  const rendered = region(builtPage(), 'data-security-document', 'div')
  const page = builtPage()

  const links = [...document.matchAll(/\[([^\]]*)\]\(([^)\s]+)\)/g)]
  assert.ok(links.length >= 8, `read ${links.length} links out of ${SOURCE}: the document has moved, so this check saw nothing`)

  for (const [, label, target] of links) {
    assert.ok(
      page.includes(`href="${target}"`),
      `the built page does not carry the document's link to ${target}: the page renders the document's links as the document writes them (W1.8 step 1)`,
    )
    assert.ok(
      flatten(textOf(rendered)).includes(flatten(inlineText(label))),
      `the built page does not carry the label ${JSON.stringify(label)} of the document's link to ${target}`,
    )
  }
})
