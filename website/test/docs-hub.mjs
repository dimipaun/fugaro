// website/test/docs-hub.mjs — the docs hub (website plan, Task W1.7; reconciliation
// finding 37, decision D98, Global Constraint 6).
//
// The docs hub is the page that could most easily become a copy of the documentation it
// exists to point at: seven documents, each one tempting to summarise, and a summary is
// the copy that drifts first (finding 37). So the checks here are the mechanical form of
// "this is a hub", and the second is the one the task was written for:
//
//   1. **Every link resolves.** The hub points at GitHub, at the file it names, and both
//      halves of that URL have to be true in this checkout: the path has to be a file the
//      repository holds, and an anchor has to be a heading in that file. This is the
//      file-level half of W1.11's link check, run against the hub's own GitHub URLs.
//   2. **Nothing is copied.** No paragraph of more than one sentence that appears
//      verbatim in a document under `docs/` (or in `README.md`, which the hub also links)
//      may appear on the page, and no entry's own line may be a sentence of the document
//      it links. A page that restates a guide is a second copy of it: the one readers
//      land on, and the one no test can hold to the first.
//   3. **The generated reference is rendered, not described.** The CLI reference is
//      generated from the command tree (W0.4), so the hub renders it rather than linking
//      it — a command that lands in the tree appears on this page the day the file is
//      regenerated, and the page's command sections have to equal the file's, one for
//      one, or the build's own check fails.
//   4. **The look is the site's, and not this page's** (the bold redesign of 2026-10-21,
//      the owner's approved home page in PR #779). The page is drawn in the shapes
//      `base.css` writes once for the whole site — the display scale, the accent's
//      gradient, the card — and the last case here reads the built markup and the page's
//      own stylesheet to hold it there: a headline with a size of its own or a card this
//      page painted itself is the page that stops looking like the site, which is the one
//      thing a redesign of one page can get wrong.
//
// How it reads the page: `npm run check` builds the site and then runs every file in this
// directory, so the built HTML is in `website/dist/` by the time these cases run. Nothing
// here fetches: the GitHub URLs are checked against the files in this repository, not
// against the network (Global Constraint 4).

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { existsSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

// The page's own anchor rule, imported rather than re-written: a check that carries its own
// approximation of the rule it is checking agrees with the page's bug instead of catching
// it (the module's header says what the two approximations got wrong).
import { headingAnchor } from '../src/lib/github-slug.mjs'
import { copyRepository } from './lib/build.mjs'
import { cssDeclarations, cssRules } from './lib/html.mjs'

const SITE = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const REPO = path.dirname(SITE)
const BUILT_PAGE = path.join(SITE, 'dist', 'docs', 'index.html')
const REFERENCE = 'website/content/cli-reference.md'

// The attribute the page carries on the section that holds the rendered CLI reference.
// Everything before it is the hub's own words, which is what the no-copied-prose case
// reads; everything after it is a generated document, which is allowed to be one.
const REFERENCE_MARKER = `data-generated-document="${REFERENCE}"`

// The documents the hub is a hub for (the plan's Task W1.7, step 1), as repository paths
// with the anchor each entry names. The list is written here rather than derived from the
// page: a hub that quietly stopped linking a guide would otherwise pass the case below,
// and this list is the plan's, not the page's.
const LINKED = [
  'README.md#getting-started',
  'docs/gcp-setup.md',
  'docs/recipes.md',
  'docs/project-layer.md',
  'docs/multi-model.md',
  'docs/git-providers.md',
]

/** The built docs page: the artifact these cases are about. */
function builtPage() {
  assert.ok(
    existsSync(BUILT_PAGE),
    `${path.relative(REPO, BUILT_PAGE)} is not there, and this check reads the artifact rather than the source: run \`npm run check\`, which builds the site first, rather than this file alone`,
  )
  return readFileSync(BUILT_PAGE, 'utf8')
}

/** The hub's own markup: the page up to the rendered, generated reference. */
function hubMarkup(page) {
  const parts = page.split(REFERENCE_MARKER)
  assert.equal(parts.length, 2, `the built page does not carry the attribute that separates the hub's own words from the rendered CLI reference, and this check reads the hub's words alone:\n${REFERENCE_MARKER}`)
  return parts[0]
}

/** A file of this repository, by its path relative to the repository root. */
function repoFile(relative) {
  const file = path.join(REPO, relative)
  assert.ok(existsSync(file), `${relative} is not a file in this repository, and the page links it`)
  return readFileSync(file, 'utf8')
}

/** One fact of `website/data/site.yaml`, read the way the site's pages read the file. */
function siteFact(key) {
  const match = new RegExp(`^${key}:\\s*(\\S+)$`, 'm').exec(repoFile('website/data/site.yaml'))
  assert.ok(match, `website/data/site.yaml carries no ${key}`)
  return match[1]
}

const HEADING = /^(#{1,6})\s+(.*)$/

/**
 * Every heading of a markdown file, as its depth, the text it renders as and the anchor it
 * is reachable at. The anchor comes from the same module the page imports
 * (`src/lib/github-slug.mjs`): this file used to carry its own approximation of GitHub's
 * rule, which is how a check comes to agree with the page's bug rather than with the
 * `id`s a reader's browser finds.
 */
function headingsOf(markdown) {
  return markdown
    .split('\n')
    .map((line) => HEADING.exec(line.trim()))
    .filter((match) => match !== null)
    .map((match) => ({ depth: match[1].length, text: match[2].trim(), anchor: headingAnchor(match[2]) }))
}

/**
 * The text a fragment of built HTML reads as: tags dropped, entities decoded, quotes
 * straightened and whitespace collapsed — the form both sides of the copied-prose
 * comparison are reduced to, so that the comparison is about the words and not about
 * which of the two spellings of an apostrophe each side happens to use.
 */
function normalise(html) {
  return html
    .replace(/<[^>]*>/g, ' ')
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/[*_`]/g, '')
    .replace(/&nbsp;/g, ' ')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&amp;/g, '&')
    .replace(/[‘’]/g, "'")
    .replace(/[“”]/g, '"')
    .replace(/\s+/g, ' ')
    .trim()
}

/** The text of every paragraph the given markup renders. */
function paragraphsOf(html) {
  return [...html.matchAll(/<p\b[^>]*>([\s\S]*?)<\/p>/g)].map((match) => normalise(match[1]))
}

/**
 * The prose paragraphs of a markdown document: its blank-line-separated blocks, minus
 * headings, tables and fenced code, which are a document's structure rather than its
 * prose. A paragraph shorter than six words is kept out of the comparison below: phrases
 * ("Usage:" notes, a two-word line) recur across documents legitimately, and matching them
 * would make this a check about English rather than about copying.
 */
function markdownParagraphs(markdown) {
  const blocks = []
  let block = []
  let fenced = false
  const flush = () => {
    if (block.length > 0) blocks.push(block)
    block = []
  }
  for (const line of markdown.split('\n')) {
    const trimmed = line.trim()
    if (trimmed.startsWith('```')) {
      fenced = !fenced
      flush()
      continue
    }
    if (fenced) continue
    if (trimmed === '' || HEADING.test(trimmed) || trimmed.startsWith('|') || /^-{3,}$/.test(trimmed)) {
      flush()
      continue
    }
    block.push(trimmed.replace(/^([-*+]|\d+\.)\s+/, ''))
  }
  flush()
  return blocks.map((lines) => normalise(lines.join(' '))).filter((paragraph) => paragraph.split(/\s+/).length >= 6)
}

/** How many sentences a paragraph holds, by the punctuation a reader would count. */
function sentences(paragraph) {
  return paragraph.split(/(?<=[.!?])\s+/).filter((sentence) => sentence.trim() !== '').length
}

/** Every markdown document of this repository under the given directory. */
function markdownUnder(dir) {
  const found = []
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const file = path.join(dir, entry.name)
    if (entry.isDirectory()) found.push(...markdownUnder(file))
    else if (entry.name.endsWith('.md')) found.push(file)
  }
  return found.sort()
}

test('every guide the hub links is a file in this repository, at the anchor it names', () => {
  const page = builtPage()
  const repository = siteFact('repository')
  const prefix = `${repository}/blob/HEAD/`

  const hrefs = [...page.matchAll(/href="([^"]*)"/g)].map((match) => match[1])
  const linked = hrefs.filter((href) => href.startsWith(prefix))
  assert.ok(linked.length > 0, `the docs page links no document of ${repository}: the hub's whole job is to point at the guides this repository holds`)

  for (const href of linked) {
    const [relative, anchor] = href.slice(prefix.length).split('#')
    const markdown = repoFile(relative)

    if (anchor === undefined) continue
    const anchors = headingsOf(markdown).map((heading) => heading.anchor)
    assert.ok(
      anchors.includes(anchor),
      `${path.relative(REPO, BUILT_PAGE)} links ${relative}#${anchor}, and ${relative} has no heading at that anchor (it has ${anchors.join(', ')}): a link to a section that was renamed is a 404 for a reader, and the hub is a hub or it is a copy`,
    )
  }
})

test("the hub links every guide the plan names, by URL built from site.yaml's repository", () => {
  const page = builtPage()
  const repository = siteFact('repository')
  const prefix = `${repository}/blob/HEAD/`

  const relative = [...page.matchAll(new RegExp(`href="${prefix.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}([^"]*)"`, 'g'))].map((match) => match[1])
  assert.deepEqual(
    relative.slice().sort(),
    LINKED.slice().sort(),
    `the docs page links ${JSON.stringify(relative.sort())} and the hub's guides are ${JSON.stringify(LINKED.slice().sort())}: the entry for a guide is added with the guide, and an entry whose file moves changes here in the same commit`,
  )

  // The one entry that is not a link to GitHub is the generated reference, which this page
  // renders rather than links — so it is an anchor of this page, and one that a reader can
  // reach.
  assert.ok(page.includes('href="#cli-reference"'), 'the hub does not link the rendered CLI reference, and the reference is the one guide that lives on this page')
  assert.match(page, /id="cli-reference"/, 'the hub links #cli-reference and the page carries no element at that anchor')

  // Every absolute URL the page prints is composed from a fact rather than typed: the
  // repository from site.yaml, the canonical origin from site.yaml, and a host the site's
  // own list admits (the same list W1.11's link check resolves against).
  const allowed = [...repoFile('website/data/site.yaml').matchAll(/^\s+-\s+(\S+)$/gm)].map((match) => match[1])
  const hosts = [...page.matchAll(/(?:href|src)="(https?:\/\/[^"]*)"/g)].map((match) => new URL(match[1]).host)
  for (const host of hosts) {
    assert.ok(allowed.includes(host), `the docs page points a reader at ${host}, which website/data/site.yaml's allowed_hosts (${allowed.join(', ')}) does not admit`)
  }
})

test('the docs page repeats no prose from a document it links', () => {
  const page = builtPage()
  const hub = hubMarkup(page)
  const hubText = normalise(hub)

  // (1) No paragraph of more than one sentence that appears verbatim in a document under
  // `docs/` — or in README.md, which the hub's first entry is a section of. One sentence
  // is a phrase a hub may legitimately carry (a title, a name); a whole paragraph is a
  // copy, and the copy is the one that drifts (finding 37, D98).
  const documents = [...markdownUnder(path.join(REPO, 'docs')), path.join(REPO, 'README.md')]
  const copied = []
  for (const document of documents) {
    for (const paragraph of markdownParagraphs(readFileSync(document, 'utf8'))) {
      if (sentences(paragraph) < 2) continue
      if (hubText.includes(paragraph)) copied.push({ document, paragraph })
    }
  }
  assert.equal(
    copied.length,
    0,
    copied.length === 0
      ? ''
      : `docs page repeats a sentence from ${path.relative(REPO, copied[0].document)}: ${JSON.stringify(copied[0].paragraph.slice(0, 160))}. The hub links a document; it never restates one (finding 37, D98, Global Constraint 6)`,
  )

  // (2) And the hub's own line about a guide is the hub's own: an entry's line must not be
  // a sentence of the document that entry links. This is what keeps "link, do not copy"
  // true for the one line each entry is allowed to carry.
  // The entry's element, by the class that marks it rather than by the whole attribute: the
  // bold look gives every card the site's shared `card` class beside the hub's own `guide`
  // (the redesign of this page, 2026-10-21), and a reader that wanted `class="guide"` exactly
  // would find no entry at all and pass this case on an empty reading.
  const entries = [...hub.matchAll(/<li class="guide\b[^"]*">([\s\S]*?)<\/li>/g)].map((match) => ({
    href: /href="([^"]*)"/.exec(match[1])?.[1] ?? '',
    answers: normalise(/<p class="answers">([\s\S]*?)<\/p>/.exec(match[1])?.[1] ?? ''),
  }))
  assert.equal(entries.length, LINKED.length + 1, `the built page carries ${entries.length} hub entries and the hub's guides plus the rendered reference are ${LINKED.length + 1}`)

  const repository = siteFact('repository')
  for (const entry of entries) {
    if (!entry.href.startsWith(`${repository}/blob/HEAD/`)) continue
    const relative = entry.href.slice(`${repository}/blob/HEAD/`.length).split('#')[0]
    const document = normalise(repoFile(relative))
    for (const sentence of entry.answers.split(/(?<=[.!?])\s+/)) {
      const probe = sentence.trim()
      if (probe.split(/\s+/).length < 6) continue
      assert.ok(
        !document.includes(probe),
        `docs page repeats a sentence from ${relative}: ${JSON.stringify(probe)}. The entry about a guide says what the guide answers in the hub's own words; the guide's own sentence is the guide's to make (finding 37, D98)`,
      )
    }
  }
})

test('the hub renders the generated CLI reference, command section for command section', () => {
  const page = builtPage()
  const markdown = repoFile(REFERENCE)

  const generated = headingsOf(markdown)
  const title = generated.find((heading) => heading.depth === 1)
  assert.ok(title, `${REFERENCE} carries no title line, and the hub renders the document's title rather than typing one`)

  // The document's own title is rendered as a section heading of this page: the page's h1
  // is the hub's, so the rendered document sits a level below it, and the anchor the
  // document's title carries survives the move (F40's rule, applied to the built page).
  assert.ok(
    page.includes(`<h2 id="${title.anchor}">`),
    `${path.relative(REPO, BUILT_PAGE)} does not render ${REFERENCE}'s own title ${JSON.stringify(title.text)} as a section heading: the page renders the generated document, and the document's title is part of it`,
  )

  const sections = [...page.matchAll(/<h2 id="([^"]*)">/g)].map((match) => match[1]).filter((id) => id !== title.anchor)
  const commands = generated.filter((heading) => heading.depth === 2)
  assert.equal(
    sections.length,
    commands.length,
    `the built page renders ${sections.length} command sections and ${REFERENCE} carries ${commands.length}: the reference is generated from the command tree, so a command that lands in the tree appears on this page when the file is regenerated — and a page that dropped one is a page that lies about the CLI`,
  )
})

test('the docs page states one h1 of its own, and the rendered reference keeps its anchors', () => {
  const page = builtPage()

  const h1s = [...page.matchAll(/<h1\b[^>]*>([\s\S]*?)<\/h1>/g)].map((match) => normalise(match[1]))
  assert.deepEqual(h1s, ['Documentation'], `the built page carries ${h1s.length} h1 headings (${JSON.stringify(h1s)}), and a page of the site carries one: the hub's own title, which the rendered document's title is a heading below (W1.11 asserts the same over every page)`)

  // The reference's own sections keep the anchors the generated file gives them, so a
  // reader who was sent to `#fugaro-budget-set` still lands on that section.
  const markdown = repoFile(REFERENCE)
  const anchors = new Set([...page.matchAll(/id="([^"]*)"/g)].map((match) => match[1]))
  const missing = headingsOf(markdown)
    .filter((heading) => heading.depth === 2)
    .map((heading) => heading.anchor)
    .filter((anchor) => !anchors.has(anchor))
  assert.deepEqual(missing, [], `the built page renders no element at ${JSON.stringify(missing)}, and ${REFERENCE} reachable them as its command sections: a rendered document keeps the anchors its own headings carry`)
})

test("the anchor rule the hub links by is GitHub's own, not an approximation of it", () => {
  // The cases below are the three details an approximation gets wrong, and each one is a
  // heading a guide could carry tomorrow: an underscore (dropped by a rule that treats
  // `\w` as the alphabet), a markdown link (unwrapped by a rule that reads the rendered
  // text and left alone by one that reads the line), and a run of spaces or a tab (where
  // only U+0020 is a hyphen and nothing is collapsed). `src/lib/github-slug.mjs` is the one
  // place the rule is written, and the cases above resolve the generated reference's own
  // command headings through it against the `id`s the built page carries — so a
  // disagreement with the renderer fails here, on the headings a reader is sent to.
  assert.equal(headingAnchor('`fugaro_run_id` in the ledger'), 'fugaro_run_id-in-the-ledger', 'an underscore is a character GitHub keeps in a heading anchor')
  assert.equal(headingAnchor('The hub — and the guides'), 'the-hub--and-the-guides', 'an em dash is punctuation GitHub drops, and the spaces around it stay')
  assert.equal(headingAnchor('[Getting started](#getting-started)'), 'getting-started', 'a markdown link anchors at its text, which is what GitHub renders')
  assert.equal(headingAnchor('A  B'), 'a--b', 'each space becomes one hyphen: two spaces are two hyphens, not one')
  assert.equal(headingAnchor('x\ty'), 'xy', 'a tab is not a space, and does not become a hyphen')
  assert.equal(headingAnchor("Requirements: the C# toolchain"), 'requirements-the-c-toolchain', 'the punctuation of a heading is dropped and the letters around it are not')

  // The two files that need the rule import this one: a page that resolved its own links by
  // one rule and was checked by another is how the check came to agree with the bug.
  for (const file of ['src/pages/docs.astro', 'test/docs-hub.mjs']) {
    assert.match(repoFile(`website/${file}`), /from '.*github-slug\.mjs'/, `${file} does not import the shared anchor rule, so it carries a second copy of it`)
  }
})

// ---------------------------------------------------------------------------------------
// The generated reference is TEXT, never markup (W1.7 follow-up). The reference documents
// placeholders such as `<name>` and `<repo-slug>/<run-id>`; a markdown renderer that passes
// raw HTML through hands the browser unknown elements, and the placeholder vanishes from
// the page. A reference that carried a `<script>` would run it.

/** The tags the markdown renderer itself emits into the reference section, and no others. */
const RENDERED_TAGS = new Set([
  'section', 'h2', 'h3', 'h4', 'p', 'code', 'pre', 'span', 'table', 'thead', 'tbody', 'tr', 'th', 'td',
  'ul', 'ol', 'li', 'a', 'em', 'strong', 'blockquote', 'hr', 'br',
])

/**
 * The section of a built page that holds the rendered reference.
 *
 * The page's own copy control is in there too — one per code block, written by
 * `website/src/lib/copy-control.mjs` (P6) — and the cases below are about the tags the
 * *renderer* emits, so the control's markup is taken back out here. `website/test/copy.mjs`
 * is the file that holds the control to its own shape, on every built page.
 */
function referenceSection(page) {
  // The section is found by its `id` and not by the whole opening tag: the page gives the
  // section a class of its own as well (the bold look, 2026-10-21), and a reader that wanted
  // the tag's exact text would call a page with one more attribute a page with no reference
  // at all — the reading this case is least able to afford.
  const opening = /<section\b[^>]*\bid="cli-reference"[^>]*>/.exec(page)
  const start = opening ? opening.index : -1
  assert.ok(start >= 0, 'the built page has no cli-reference section')
  const section = page.slice(start, page.indexOf('</section>', start))
  assert.match(
    section,
    /<div class="command-controls">/,
    'the reference section carries no copy control, and every command the site prints has one (P6, D183)',
  )
  return section
    .replace(/<div class="command-controls">[\s\S]*?<\/div>/g, '')
    .replace(/<div class="command-block" data-copy[^>]*>/g, '')
    .replace(/<\/pre><\/div>/g, '</pre>')
}

/** The tag names a fragment of HTML opens or closes. */
function tagNames(html) {
  return [...new Set([...html.matchAll(/<\/?([a-zA-Z][\w:-]*)/g)].map((match) => match[1].toLowerCase()))].sort()
}

test('the rendered reference carries only tags the renderer emits, and every placeholder is text', () => {
  const section = referenceSection(builtPage())
  const unexpected = tagNames(section).filter((name) => !RENDERED_TAGS.has(name))
  assert.deepEqual(unexpected, [], `the built reference holds tags that are not the renderer's (${unexpected.join(', ')}): a raw \`<x>\` placeholder was passed through as an element, and a browser drops its text`)

  const placeholders = new Set(
    [...repoFile(REFERENCE).matchAll(/<([a-z][a-z0-9-]*)>/g)].map((match) => match[1]),
  )
  assert.ok(placeholders.size > 0, `${REFERENCE} documents no \`<x>\` placeholder, so this case checks nothing`)
  for (const name of placeholders) {
    assert.ok(section.includes(`&lt;${name}&gt;`), `the built reference does not show \`<${name}>\` as escaped text (&lt;${name}&gt;)`)
  }
})

test('the reference shows its text unaltered: `--out` keeps two hyphens', () => {
  const section = referenceSection(builtPage())
  assert.ok(section.includes('--out'), 'the built reference does not contain the literal flag --out')
  assert.ok(!/[\u2013\u2014]out/.test(section), 'smartypants rewrote `--out` to a dash in the reference')
})

test('markup planted in the reference source is rendered as text and never as a tag', () => {
  const tmp = mkdtempSync(path.join(os.tmpdir(), 'docs-hub-'))
  try {
    // A throwaway copy of what the build reads, so the real reference is untouched: the
    // whole repository but the Go tree, as the other copy-and-build tests take it, because
    // other pages read root files (LICENSE, SECURITY.md) and .github/ (the community page).
    const site = copyRepository(tmp)
    const file = path.join(site, 'content', 'cli-reference.md')
    writeFileSync(
      file,
      readFileSync(file, 'utf8') +
        '\n## `fugaro planted`\n\nPlanted <script>alert(1)</script> and <img src=x onerror=alert(2)> in prose.\n\n<div onclick="alert(3)">block</div>\n\nIn code: `<script>alert(4)</script>`.\n',
    )
    const built = spawnSync('npx', ['astro', 'build'], { cwd: site, encoding: 'utf8', env: { ...process.env, ASTRO_TELEMETRY_DISABLED: '1' } })
    assert.equal(built.status, 0, `the planted build failed: ${built.stdout}${built.stderr}`)
    const section = referenceSection(readFileSync(path.join(site, 'dist', 'docs', 'index.html'), 'utf8'))
    const unexpected = tagNames(section).filter((name) => !RENDERED_TAGS.has(name))
    assert.deepEqual(unexpected, [], `planted markup became tags: ${unexpected.join(', ')}`)
    assert.ok(section.includes('&lt;script&gt;alert(1)&lt;/script&gt;'), 'planted <script> is not shown as text')
    assert.ok(section.includes('&lt;img src=x onerror=alert(2)&gt;'), 'planted <img onerror> is not shown as text')
    assert.ok(!/<(script|img|div)\b/i.test(section) && !/\sonerror=/.test(section.replace(/&lt;[^]*?&gt;/g, '')), 'planted markup reached the page as a tag')
  } finally {
    rmSync(tmp, { recursive: true, force: true })
  }
})

// ---------------------------------------------------------------------------------------
// The bold look (the owner's approved home page, the foundation PR #779; the owner's brief of
// 2026-10-21 for this page).
//
// The redesign of the site is a set of shapes written once in `base.css` — the display scale,
// the gradient, the card — and the whole point of writing them once is that a page uses them
// rather than a shape of its own: a page that drew its own headline size or its own card box
// would be the page that stops looking like the rest of the site, which is the thing the
// owner's redesign is for. That is the property this case holds, and it is checkable where
// the look is not (no gate here can see a page): the classes the built markup carries, and
// the declarations the page's own stylesheet does NOT make.
//
// A gate whose refusal has never been seen to fire is a gate nobody knows the shape of, so
// each assertion below was mutation-proved on the built page: dropping `display-xl` from the
// headline, dropping the gradient's `span`, dropping `card` from an entry, and writing a
// `background` into the page's own `.guide` rule each fail here, naming the page.

/** The built page's own stylesheet: Astro inlines the site's CSS into one `<style>` block. */
function pageStylesheet(page) {
  const blocks = [...page.matchAll(/<style\b[^>]*>([\s\S]*?)<\/style>/g)].map((match) => match[1])
  assert.ok(blocks.length > 0, `${path.relative(REPO, BUILT_PAGE)} carries no stylesheet, so the case below would read nothing`)
  return blocks.join('\n')
}

/** The declarations of the one rule whose selector list is exactly `selector`. */
function declarationsOf(css, selector) {
  const rule = cssRules(css).find((entry) => entry.selector === selector)
  assert.ok(rule, `the built page's stylesheet carries no ${selector} rule, and the case below reads its declarations`)
  return cssDeclarations(rule.body)
}

test("the hub is drawn in the site's shared bold look, and gives the page no shape of its own", () => {
  const page = builtPage()

  // The headline: the display scale's largest step — `base.css`'s `.display-xl`, the same
  // class the homepage's hero carries — with the accent's gradient on the whole of the word.
  // The text inside the gradient is the page's own title and nothing else, so a redesign that
  // wrapped a second word into the headline (or left part of the title outside the gradient)
  // is a change to what the page says, which no styling may make.
  const heading = /<h1\b([^>]*)>([\s\S]*?)<\/h1>/.exec(page)
  assert.ok(heading, `${path.relative(REPO, BUILT_PAGE)} carries no h1, and the hub's headline is the page's own title`)
  assert.match(heading[1], /(?:^|\s)class="[^"]*\bdisplay-xl\b[^"]*"/, `the hub's h1 carries ${JSON.stringify(heading[1])} and no display step: the headline is \`base.css\`'s \`.display-xl\` written once for the site, not a size this page invents (the bold look, PR #779)`)
  assert.match(heading[2], /<span class="gradient-text">/, "the hub's headline carries no gradient: the accent's gradient is `base.css`'s `.gradient-text`, and the headline is where the bold look puts it")
  assert.equal(
    normalise(heading[2]),
    'Documentation',
    `the hub's headline reads ${JSON.stringify(normalise(heading[2]))} inside its gradient and the page's title is "Documentation": the gradient is the headline's own word and not a place for a second one`,
  )

  // The section heading: the display scale's middle step, the same class the homepage's
  // section headings carry.
  const guidesHeading = /<h2\b([^>]*)>/.exec(page)
  assert.ok(guidesHeading, `${path.relative(REPO, BUILT_PAGE)} carries no h2, and the guides section is headed`)
  assert.match(guidesHeading[1], /(?:^|\s)class="[^"]*\bdisplay-md\b[^"]*"/, `the Guides heading carries ${JSON.stringify(guidesHeading[1])} and no display step: a section's heading is the display scale's middle step, written once for the site`)

  // Every entry is one of the site's cards: `base.css`'s `.card`, which is what makes a card
  // on this page the same box as a card on any other.
  const entries = [...page.matchAll(/<li class="([^"]*)">/g)].map((match) => match[1]).filter((classes) => /\bguide\b/.test(classes))
  assert.equal(entries.length, LINKED.length + 1, `the built page carries ${entries.length} guide entries and the hub's guides plus the rendered reference are ${LINKED.length + 1}`)
  for (const classes of entries) {
    assert.match(classes, /(?:^|\s)card(?:\s|$)/, `a guide entry carries ${JSON.stringify(classes)} and not the site's shared \`card\` class: the bold look's card is written once in \`base.css\`, and a card this page drew itself is the page that stops looking like the site`)
  }

  // And the page draws no card of its own: the box (its fill, its hairline, its corner and
  // its padding) is `.card`'s, so the page's own `.guide` rule may lay the card's contents
  // out and may not paint it.
  const css = pageStylesheet(page)
  const forbidden = new Set(['background', 'background-color', 'background-image', 'border', 'border-radius', 'padding'])
  const drawn = declarationsOf(css, '.guide').filter(([property]) => forbidden.has(property))
  assert.deepEqual(
    drawn,
    [],
    `the page's own \`.guide\` rule declares ${JSON.stringify(drawn.map(([p, v]) => `${p}: ${v}`))}: the card's fill, hairline, corner and padding are \`base.css\`'s \`.card\` on this page as on every other, and a page that paints its own box is the drift the shared look exists to stop`,
  )
})

test('the anchors the hub links name headings that are unique in their file', () => {
  // github-slug.mjs has no `-1` suffix for a repeated heading, so a link to one would
  // anchor at the first occurrence by GitHub's rule only by luck. The hub links unique
  // headings only, and this fails the day one of them stops being unique.
  for (const link of LINKED.filter((entry) => entry.includes('#'))) {
    const [file, anchor] = link.split('#')
    const count = headingsOf(repoFile(file)).filter((heading) => heading.anchor === anchor).length
    assert.equal(count, 1, `${file} has ${count} headings anchored at #${anchor}: the hub links it and the slug rule has no duplicate suffix`)
  }
})
