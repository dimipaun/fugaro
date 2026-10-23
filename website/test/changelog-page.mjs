// website/test/changelog-page.mjs — the changelog page against docs/releases/ (W1.9).
//
// The page is a rendering of `docs/releases/vX.Y.Z.md` and of `website/data/release-summaries.yaml`,
// and of nothing else, so the source of truth of every assertion below is **those two files** —
// read here, never the generator's own output and never a git tag (D100, finding 44). Six
// things are checked:
//
//   1. **The page's release set equals the directory's, in both directions.** A release
//      file the page omits fails, and a tag the page lists that no file backs fails. D100
//      calls this the whole of the drift protection, because the page is the first
//      reader-facing index of those files (the README keeps no changelog section).
//   2. **The newest entry is the newest file**, by the version in the filename.
//   3. **The page adds no sentence of its own beyond those two files**, and drops no line of
//      any release file.** Every sentence the page renders is in a release file or in the
//      summary the data file carries for that release, and every content line of every
//      release file appears on the page. The only words the page writes itself are its
//      title, the notes block's label, the "not yet released" marker, and the tags, which
//      come from the filenames.
//   4. **Every entry links to its GitHub release**, at a URL built from site.yaml's
//      repository rather than typed into the page, and the page states no date — the files
//      carry none, and the date a reader wants is the one GitHub states (D100).
//   5. **Each release's summary is the one the data file carries for it** (the owner's brief
//      of 2026-10-19): the page renders one summary per release, from
//      `website/data/release-summaries.yaml`, and the release file's own Highlights are the
//      block a reader opens below it. Every release file has an entry in that file, none is
//      empty, no entry outlives the file it names, and the words of an entry are the site's
//      rather than an internal id or a repository path.
//   6. **The page wears the bold look, and wears no words to do it** (the owner's brief of
//      2026-10-21, which applies the design the owner approved on the home page —
//      `website/design-ref/HomeV3.dc.html` — to this page, which has no board of its own).
//      The page's own look is a fact about the artifact like any other, and it is the one
//      thing the cases above cannot see: they read the page's text, and the redesign moved
//      no word of it. So five cases read the built HTML and the stylesheet inlined in it —
//      the display scale's headline with its word in the accent's gradient, one card per
//      release spaced by a step of the spacing scale, the hierarchy inside a card, the
//      boards' background behind the page's head (the grid and the two soft glows `base.css`
//      draws once, `.hero-glow`, which the home page's hero opens with), and the code chips
//      a release note is full of.
//
//      **The chips are the case that shows what the other four cannot.** A rule can be in
//      the stylesheet, name the right elements and paint nothing, and a case that asks
//      whether a rule with those fragments exists answers yes: the notes arrive through
//      `set:html`, Astro's scope attribute never reaches the markup a release file wrote, and
//      the scoped `.release-notes code` this page shipped was built as a selector no chip
//      could match — every chip kept `base.css`'s own `--surface`, the card's own tone, and
//      four readings passed it green. So that case reads **reachability**: the rule is
//      matched against the chips the page actually rendered, and a rule that can reach none
//      of them is refused by name. The case after it plants the defect — it rewrites the
//      built rule into exactly the shape Astro builds for a scoped one — and shows the
//      refusal firing, because a look nothing refuses is a look that drifts back with the
//      next edit.
//
// **How it builds, and why the copy's plugin version is lowered.** A page is observable only
// as HTML, so this file builds the site — in a **copy of the repository in a temp directory**,
// with `node_modules` symlinked rather than installed, the way W1.3's layout test builds for
// the same reason: a build must not be run against the checkout it is asserted over, and the
// assertion needs a checkout whose shape this file controls. The copy carries **no `.git`**,
// which is what makes the released state deterministic without any test reading a tag: which
// releases are published is decided by the plugin's own version
// (`plugin/.claude-plugin/plugin.json`, the owner's brief of 2026-10-19), a file this copy
// carries. The copy's manifest is written with **the second-newest release file's version**,
// so the build under test is the state of a tree one release ahead of its version bump:
// exactly the newest release file is not yet released, and every older one is. A build whose
// "not yet released" came from the checkout's tags could not produce that answer at all.
//
// The build is network-free: `npm ci` is not run here, and the only executable it starts is
// the site's own installed Astro.

import { test, after } from 'node:test'
import assert from 'node:assert/strict'
import { cpSync, existsSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { cssDeclarations, cssRules, parse, walk } from './lib/html.mjs'
import { ancestorsOf, classesOf, matchesSelector, subjectOf } from './lib/selectors.mjs'
import { READ_UNDER_PLUGIN, linkNodeModules } from './lib/build.mjs'

// website/test/changelog-page.mjs -> website/test -> website.
const SITE = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
// ... -> the repository the site is built from.
const REPO = path.dirname(SITE)

const RELEASES_DIR = path.join(REPO, 'docs', 'releases')

// The site's own data: the end-user summary of each release (the owner's brief of
// 2026-10-19), and the plugin manifest whose version decides which releases are published.
const SUMMARIES_FILE = path.join(SITE, 'data', 'release-summaries.yaml')
const PLUGIN_FILE = path.join(REPO, 'plugin', '.claude-plugin', 'plugin.json')

// `docs/releases/README.md` describes the format; it is the one file in the directory that
// is not a release, and it is skipped by name rather than by the `vX.Y.Z.md` pattern, so a
// file the generator does not recognise fails the coverage case instead of being filtered
// out of the expectation by the same rule that filtered it out of the answer (W0.6).
const NON_RELEASE = 'README.md'

// The sections a release file may carry after its Highlights, by title: the two headings
// `docs/releases/README.md` names — the operator steps, and the release's known limits —
// both optional, in the order the file writes them. Each heading is a content line the page
// does not render verbatim: it becomes an `<h3>` whose text is the heading's own words, and
// a `### Known limits` line printed as text instead is the defect the page gate below
// catches. The `###` is the file's level; GoReleaser puts the file under its own
// `## Highlights` in the release body, so a `##` in the file would be a second top-level
// heading beside it.
const SECTION_TITLES = ['For operators', 'Known limits']

/** The file line that opens a section, from its title: `For operators` -> `### For operators`. */
function sectionHeading(title) {
  return `### ${title}`
}

// The label on the collapsed block of a release file's own lines (the component's own word,
// and the whole of what the block adds to the page): a reader who wants the Highlights of a
// release opens it, and the summary above says what the release gives them.
const NOTES_LABEL = 'Highlights'

// What a site build cannot read, and therefore what the copy below leaves out: the Go
// module's own tree and test data, the console application, and the installed and built
// directories (the last two are symlinked and rebuilt instead). The same sets W1.3's layout
// test uses — a copy has to be the shape of the repository, minus what no page reads.
const SKIP = new Set(['.git', 'internal', 'cmd', 'console', 'schemas', 'deploy', 'testdata'])
const SKIP_UNDER_WEBSITE = new Set(['node_modules', 'dist', '.astro'])

// ---------------------------------------------------------------------------------------
// The build: one copy, one build, shared by every case in this file.
// ---------------------------------------------------------------------------------------

let built = null

after(() => {
  if (built !== null) rmSync(built.dir, { recursive: true, force: true })
})

/** The copy's build, run once however many cases ask for it. */
function build() {
  if (built !== null) return built

  const dir = mkdtempSync(path.join(tmpdir(), 'fugaro-changelog-'))
  const root = path.join(dir, 'repo')
  cpSync(REPO, root, {
    recursive: true,
    filter: (src) => {
      const name = path.basename(src)
      const parent = path.basename(path.dirname(src))
      if (path.dirname(src) === REPO && SKIP.has(name)) return false
      // `plugin/skills` and `plugin/.claude-plugin` are the parts of `plugin/` a build
      // reads: the slider's "See it" panel quotes the skills, and the changelog decides which releases
      // are published from the plugin's own version (website/test/lib/build.mjs).
      if (parent === 'plugin' && !READ_UNDER_PLUGIN.has(name)) return false
      if (parent === 'website' && SKIP_UNDER_WEBSITE.has(name)) return false
      return true
    },
  })
  // The installed tree is symlinked rather than copied: it is the pinned tree `npm ci`
  // wrote, and this file installs nothing (W1.1).
  linkNodeModules(path.join(root, 'website', 'node_modules'))

  // The plugin's version the copy is built with: the second-newest release file's, so the
  // build under test is a tree one release ahead of its version bump. Exactly the newest
  // release file is then not yet released, and every older one is — an answer a tag-reading
  // rule could not produce, because the copy has no `.git` at all (the header says why).
  const rank = newestFirst()
  assert.ok(rank.length >= 2, 'docs/releases/ carries fewer than two release files, so this file cannot build the state of a tree one release ahead')
  const publishedVersion = rank[1].slice(1)
  writeFileSync(
    path.join(root, 'plugin', '.claude-plugin', 'plugin.json'),
    `${JSON.stringify({ name: 'fugaro', version: publishedVersion }, null, 2)}\n`,
  )

  const site = path.join(root, 'website')
  const result = spawnSync(process.execPath, [path.join(site, 'node_modules', 'astro', 'bin', 'astro.mjs'), 'build'], {
    cwd: site,
    encoding: 'utf8',
    env: { ...process.env, ASTRO_TELEMETRY_DISABLED: '1' },
  })

  const page = path.join(site, 'dist', 'changelog', 'index.html')
  built = {
    dir,
    root,
    site,
    publishedVersion,
    status: result.status,
    output: `${result.stdout ?? ''}${result.stderr ?? ''}`,
    html: existsSync(page) ? readFileSync(page, 'utf8') : null,
  }
  return built
}

/** The built page, for a case that expects the build to have produced one. */
function builtPage() {
  const { status, output, html } = build()
  assert.equal(status, 0, `the site's build failed:\n${output}`)
  assert.ok(html, 'the build produced no website/dist/changelog/index.html, so there is no changelog page to check')
  return html
}

// ---------------------------------------------------------------------------------------
// The source: docs/releases/, read here rather than asked of the generator.
// ---------------------------------------------------------------------------------------

/** Every release file's name: every `*.md` in the directory except the one skipped by name. */
function releaseFiles() {
  return readdirSync(RELEASES_DIR).filter((name) => name.endsWith('.md') && name !== NON_RELEASE)
}

/** The tag an entry carries, taken from the filename alone (the file carries no title). */
function tagOf(fileName) {
  return fileName.slice(0, -'.md'.length)
}

function versionOf(tag) {
  const m = /^v(\d+)\.(\d+)\.(\d+)$/.exec(tag)
  return [Number(m[1]), Number(m[2]), Number(m[3])]
}

/** The release files, newest first by the version in the filename. */
function newestFirst() {
  return releaseFiles()
    .map(tagOf)
    .sort((a, b) => {
      const va = versionOf(a)
      const vb = versionOf(b)
      for (let i = 0; i < 3; i++) {
        if (va[i] !== vb[i]) return vb[i] - va[i]
      }
      return 0
    })
}

/** A release file's text with its authoring notes (an HTML comment, v0.7.5's) left out. */
function fileText(fileName) {
  return readFileSync(path.join(RELEASES_DIR, fileName), 'utf8').replace(/<!--[\s\S]*?-->/g, '')
}

/** A release file's lines as a reader sees them: trimmed, with the blank lines dropped. */
function contentLines(fileName) {
  return fileText(fileName)
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
}

// What a comparison between the page and a file has to ignore, in one place. The page
// renders the files' **inline markup** rather than its markers — `` `code` `` becomes a
// `<code>` element and `**bold**` a `<strong>` one, because a marker is markup and not
// text — so both sides of every comparison below are stripped of the markers, and of the
// bullet marker a list renders for itself. Nothing else is touched: the words, the
// punctuation and the order are the file's.
function plain(text) {
  return text
    .replace(/^\s*[-*]\s+/, '')
    .replace(/`/g, '')
    .replace(/\*\*/g, '')
    .replace(/\s+/g, ' ')
    .trim()
}

/** The normalized text of every release file, in one string: what the page may say. */
function releasesText() {
  return releaseFiles()
    .map((fileName) => plain(fileText(fileName).replace(/^#{1,6}\s+/gm, '')))
    .join('\n')
}

// ---------------------------------------------------------------------------------------
// The site's data: website/data/release-summaries.yaml, read here rather than asked of the
// generator, the way `docs/releases/` is above.
// ---------------------------------------------------------------------------------------

// The one shape an entry in that file may take, exactly as the file's own header writes it
// and as `website/src/content.ts` reads it: two spaces, a `vX.Y.Z` key, `: `, and the
// summary on that one line. A folded value, a quoted key or a second line under an entry is
// refused here rather than read approximately — a test that parsed the file loosely could
// agree with a file the site's own reader refuses.
const SUMMARY_ENTRY = /^ {2}(v\d+\.\d+\.\d+): (.+)$/

/**
 * Every entry in `website/data/release-summaries.yaml`, by tag, with the file's own lines
 * checked first: the reader below understands one shape and says so, so a file it does not
 * understand fails here rather than silently yielding a shorter map.
 */
function summaryEntries() {
  const lines = readFileSync(SUMMARIES_FILE, 'utf8').split('\n')
  const start = lines.indexOf('summaries:')
  assert.notEqual(start, -1, `${path.relative(REPO, SUMMARIES_FILE)} carries no \`summaries:\` line at the start of a line: the file states its entries under it`)

  const entries = new Map()
  for (let i = start + 1; i < lines.length; i++) {
    const line = lines[i]
    if (line.trim() === '' || line.trimStart().startsWith('#')) continue
    if (!line.startsWith(' ')) break // the next top-level key ends the map.
    const match = SUMMARY_ENTRY.exec(line)
    assert.ok(
      match,
      `${path.relative(REPO, SUMMARIES_FILE)}:${i + 1}: ${JSON.stringify(line)} is not \`  vX.Y.Z: one line of text\`: an entry is a tag and its summary on one line, and a value written another way is refused rather than read approximately`,
    )
    const [, tag, summary] = match
    assert.ok(summary.trim().length > 0, `${path.relative(REPO, SUMMARIES_FILE)}:${i + 1}: ${tag} carries an empty summary, and the changelog renders it under the release's heading`)
    assert.equal(entries.has(tag), false, `${path.relative(REPO, SUMMARIES_FILE)}:${i + 1}: ${tag} is written twice, and the file's own reader keeps one entry per tag`)
    entries.set(tag, summary)
  }

  assert.ok(entries.size > 0, `${path.relative(REPO, SUMMARIES_FILE)} carries no entry under \`summaries:\``)
  return entries
}

// ---------------------------------------------------------------------------------------
// The page: the built HTML, read the way a reader reads it.
// ---------------------------------------------------------------------------------------

// The text-bearing elements of the page's main region, one entry per element: a heading, a
// paragraph, a list item or the label of a details block. Reading the page element by
// element is what keeps two adjacent blocks from being glued into one "sentence" that no
// file carries — the page's text is exactly these strings, in this order. The `<summary>`
// label is in the list because it is a word a reader reads, and it is short enough to slip
// past a rule about sentences: the case below holds it to the page's own words.
const TEXT_ELEMENT = /<(h1|h2|h3|p|li|summary)\b[^>]*>([\s\S]*?)<\/\1>/g

function decodeEntities(text) {
  return text
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&amp;/g, '&')
}

/** The page's own text, block by block: every element's text, tags and entities removed. */
function pageChunks(html) {
  const main = /<main\b[^>]*>([\s\S]*)<\/main>/.exec(html)
  const region = main === null ? '' : main[1]
  const chunks = []
  for (const match of region.matchAll(TEXT_ELEMENT)) {
    const text = decodeEntities(match[2].replace(/<[^>]*>/g, '')).replace(/\s+/g, ' ').trim()
    if (text.length > 0) chunks.push(text)
  }
  return chunks
}

/** The page's own text as one normalized string, for a whole-line comparison. */
function pageText(html) {
  return pageChunks(html).map(plain).join('\n')
}

/** The chunks split into sentences, each chunk on its own: see `pageChunks`. */
function pageSentences(html) {
  return pageChunks(html).flatMap((chunk) =>
    chunk
      .split(/(?<=[.!?])\s+/)
      .map((sentence) => plain(sentence))
      .filter((sentence) => sentence.length > 0),
  )
}

/**
 * The tags the page's **entry** headings carry, in the order they appear.
 *
 * An entry heading is an `<h2>` whose `id` is the tag and whose text is the tag: the id is
 * what the entry is linked by, and the text is what a reader reads, so the two are one fact
 * and are asserted to agree. Nothing else on this page is an `<h2>`, and recognising an
 * entry by its id — which is the tag and nothing else — keeps a heading added later from
 * counting as a release.
 */
function tagsOnPage(html) {
  return [...html.matchAll(/<h2\b[^>]*\bid="([^"]*)"[^>]*>([\s\S]*?)<\/h2>/g)].map((match) => {
    const text = decodeEntities(match[2].replace(/<[^>]*>/g, '')).replace(/\s+/g, ' ').trim()
    assert.equal(
      text,
      match[1],
      `an entry heading carries the id ${JSON.stringify(match[1])} and the text ${JSON.stringify(text)}: the id is what the entry is linked by and the text is what a reader reads, and they are one fact (W1.9)`,
    )
    return match[1]
  })
}

/**
 * The summaries the page renders, in the order it renders them: the tag each one names
 * (carried as an attribute, so an entry and its summary are read as one fact) and the text
 * of the line.
 */
function summariesOnPage(html) {
  return [...html.matchAll(/<p class="release-summary"[^>]*\bdata-release-summary="([^"]+)"[^>]*>([\s\S]*?)<\/p>/g)].map(
    (match) => ({
      tag: match[1],
      text: decodeEntities(match[2].replace(/<[^>]*>/g, '')).replace(/\s+/g, ' ').trim(),
    }),
  )
}

/** The tags whose entry carries the "not yet released" marker, in the order they appear. */
function unpublishedOnPage(html) {
  return [...html.matchAll(/<article\b[^>]*>([\s\S]*?)<\/article>/g)]
    .filter((article) => article[1].includes('data-status="not-yet-released"'))
    .map((article) => {
      const heading = /<h2\b[^>]*\bid="([^"]*)"[^>]*>/.exec(article[1])
      assert.ok(heading, 'an entry carries the "not yet released" marker and no heading with a tag in its id, so this reader cannot name it')
      return heading[1]
    })
}

/**
 * One entry's body, element by element, in the order the page renders it: the text of each
 * `<h3>`, `<p>` and `<li>` inside that entry's own `<article>`, with the tags, the entities
 * and the inline markers of both sides stripped by `plain()`.
 *
 * Reading the entry out of its own `<article>` — which opens at the heading its tag carries
 * — is what lets the case below compare one release's rendering against one release file:
 * the sequence of elements is the file's lines, in the file's order, with each section
 * heading as the heading element it is. The "not yet released" marker and the summary are
 * the page's own lines rather than the release file's — the summary is the data file's, and
 * the case below holds it there — so both are left out here, as they are left out of the
 * gate above.
 */
function entryElements(html, tag) {
  const at = html.indexOf(`<h2 id="${tag}"`)
  assert.notEqual(at, -1, `the page carries no entry for ${tag}`)
  const article = html.slice(html.lastIndexOf('<article', at), html.indexOf('</article>', at))

  const out = []
  for (const match of article.matchAll(/<(h3|p|li)\b([^>]*)>([\s\S]*?)<\/\1>/g)) {
    if (match[2].includes('release-status') || match[2].includes('release-summary')) continue
    const text = decodeEntities(match[3].replace(/<[^>]*>/g, '')).replace(/\s+/g, ' ').trim()
    if (text.length > 0) out.push({ kind: match[1], text: plain(text) })
  }
  return out
}

/**
 * What that entry must be, read from the release file alone: every content line of the file
 * in order, each one the element the page renders it as — a `### ` heading as an `<h3>`, a
 * bullet as a list item, any other line as a paragraph of its own (W1.9 step 3). This is
 * the file's shape rather than the generator's, so a page that drops a line, reorders the
 * sections or prints a heading's `###` as text differs from it.
 */
function fileElements(fileName) {
  const out = []
  for (const line of contentLines(fileName)) {
    const title = SECTION_TITLES.find((candidate) => line === sectionHeading(candidate))
    if (title !== undefined) {
      out.push({ kind: 'h3', text: plain(title) })
      continue
    }
    const bullet = /^[-*]\s+(.*)$/.exec(line)
    out.push(bullet === null ? { kind: 'p', text: plain(line) } : { kind: 'li', text: plain(bullet[1]) })
  }
  return out
}

// ---------------------------------------------------------------------------------------
// The look: the built page's own stylesheet, read the way a reader receives it.
// ---------------------------------------------------------------------------------------

/**
 * The built page's stylesheet: every `<style>` block in it, joined. `astro.config.mjs` sets
 * `inlineStylesheets: 'always'`, so a page carries the whole of its CSS and this file reads
 * what a browser receives — nothing under a `dist/` the build under test did not write, and
 * no `<link>` followed to a file that might be from another build (the header says why the
 * build is a copy in a temp directory).
 */
function pageCSS(html) {
  return [...html.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((match) => match[1]).join('\n')
}

/**
 * The declarations of the first built rule whose selector carries every one of the given
 * fragments, or `null` when the stylesheet has no such rule.
 *
 * **Fragments rather than a selector written out**, because Astro scopes a component's rules:
 * the source's `.release-notes summary` is built as
 * `.release-notes[data-astro-cid-…] summary[data-astro-cid-…]`, and a reader that asked for
 * the source's selector exactly would find nothing — and a `null` a check happened to pass on
 * is worse than no check at all. The fragments are the parts of the selector the page's own
 * source writes, so a rule that moved off the elements it is about stops being found.
 */
function declarationsOf(css, ...fragments) {
  for (const [, selector, body] of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    if (fragments.every((fragment) => selector.includes(fragment))) return body
  }
  return null
}

/** A rule's declarations, or a failure naming the selector a check wanted when there is none. */
function declarationsFor(css, what, ...fragments) {
  const body = declarationsOf(css, ...fragments)
  assert.ok(
    body !== null,
    `the page's stylesheet carries no rule for ${what}: the built CSS has no selector carrying ${fragments.map((fragment) => JSON.stringify(fragment)).join(' and ')}`,
  )
  return body
}

/** The text of a built element, tags and entities removed and whitespace collapsed. */
function elementText(html) {
  return decodeEntities(html.replace(/<[^>]*>/g, '')).replace(/\s+/g, ' ').trim()
}

// ---- a rule, read against the elements the page rendered ---------------------------------
//
// **A rule is in force only where its selector can reach an element the page rendered**, and
// that is a fact about the built artifact rather than about the source. Astro appends a
// component's `data-astro-cid-…` to **every** compound of a scoped selector, so the source's
// `.release-notes code` is built as `.release-notes[cid] code[cid]` — and the `<code>`
// elements a release note carries arrive through `set:html`, which the scope attribute never
// reaches. The rule was dead: every chip in the notes kept `base.css`'s own `--surface`,
// which is the card's own tone, and the case that named it passed, because it asked whether a
// rule with those fragments existed and never whether the rule could match anything. A
// selector that names a scope no element carries is the same defect `base-styles.mjs` reads on
// the homepage's blocks (its review round 6), read here for the chip rule.

// The matcher itself — `classesOf`, `ancestorsOf`, `matchesSelector` and `subjectOf` — is
// `website/test/lib/selectors.mjs`, which the run diagram's paint gate reads too: "can this
// rule reach this element" is one question and one answer.

/**
 * The notes' code chips and the rule that paints them on the ground, as the sentences for the
 * ways that rule cannot reach a chip — `[]` when the chips are painted by a rule that can
 * match them.
 *
 * The chips are the `<code>` elements the page rendered inside a `.release-notes` block, and
 * the rule is the one the page's own stylesheet declares with a `code` subject and a
 * `background: var(--ground)` — the chip's own tone, which is the whole point of the rule. A
 * rule that reaches none of the chips is refused by name, with the scope it requires and the
 * scopes the chips carry, because the two are one line apart in the built stylesheet and the
 * fix is to make the rule reachable (`:global()`), not to move the chips.
 */
function chipFailures(css, html) {
  const doc = parse(html)
  const chips = [...walk(doc)].filter(
    (el) => el.name === 'code' && ancestorsOf(el).some((ancestor) => classesOf(ancestor).includes('release-notes')),
  )
  if (chips.length === 0) {
    return ['the page renders no code chip inside the notes: a release note is full of them, and the rule this case reads is about the chips a reader sees in one']
  }
  const scopes = [...new Set(chips.flatMap((chip) => [...chip.attrs.keys()].filter((name) => name.startsWith('data-astro-cid-'))))]
  const painting = cssRules(css).filter(
    (rule) =>
      rule.selector.split(',').some((part) => subjectOf(part).name === 'code') &&
      cssDeclarations(rule.body).some(
        ([property, value]) => /^background(-color)?$/.test(property) && value.replace(/\s+/g, '') === 'var(--ground)',
      ),
  )
  if (painting.length === 0) {
    return ['no rule of the page’s stylesheet paints a code chip on the ground: the card is `--surface` and `base.css` paints a chip `--surface` too, so a chip with no rule of its own is a chip a reader cannot see']
  }
  return painting
    .filter((rule) => !rule.selector.split(',').some((part) => chips.some((chip) => matchesSelector(part, chip))))
    .map((rule) => {
      const wanted = [...new Set([...rule.selector.matchAll(/\[(data-astro-cid-[^\]]+)\]/g)].map(([, scope]) => scope))]
      return `the notes’ code chips are painted by \`${rule.selector}\`, and no selector of that rule can match any of the ${chips.length} code chips the page renders: the rule requires ${wanted.join(', ') || 'no scope attribute'} where the chips carry ${scopes.join(', ') || 'none'}. A chip arrives through \`set:html\`, so Astro never appends the component’s scope attribute to it — and a rule the chips cannot be matched by is a rule they never get, so every chip in the notes keeps \`base.css\`’s own \`--surface\`, which is the card’s own tone: a chip the same tone as the card it sits on is a chip nobody sees`
    })
}

// ---------------------------------------------------------------------------------------
// The cases.
// ---------------------------------------------------------------------------------------

test('the changelog page lists every release file and no others', () => {
  // The build's HTML directly rather than `builtPage()`: with no page there are no tags on
  // it, and the failure a reader needs is the one below — which release is missing — not
  // "the page does not exist". The build's own failure is the next case's subject.
  const onPage = tagsOnPage(build().html ?? '')
  const files = releaseFiles().map(tagOf)

  // Newest first, so the release a reader would miss first is the one named: the newest
  // file is the one committed to main before its tag exists (D100).
  for (const tag of newestFirst()) {
    assert.ok(onPage.includes(tag), `page omits ${tag}, docs/releases/ holds it`)
  }
  for (const tag of onPage) {
    assert.ok(files.includes(tag), `page lists ${tag}, docs/releases/ holds no such file`)
  }
  assert.equal(onPage.length, files.length, `page lists ${onPage.length} releases, docs/releases/ holds ${files.length}`)
})

test('the build produces the changelog page', () => {
  const { status, output } = build()
  assert.equal(status, 0, `the site's build failed:\n${output}`)
  assert.ok(build().html, 'the build produced no website/dist/changelog/index.html')
})

test('the changelog page lists the newest release first', () => {
  const onPage = tagsOnPage(builtPage())
  const expected = newestFirst()

  // The newest entry is the newest file, and the whole order is the files' own order: the
  // newest release is the one a reader sees first, and it is the file the version in the
  // filename says it is (D100 — no tag is read for this, and none is needed).
  assert.equal(onPage[0], expected[0], `the first entry is ${onPage[0]}, and the newest release file is ${expected[0]}`)
  assert.deepEqual(onPage, expected, 'the entries are not the release files, newest first')
})

test('the changelog page adds no sentence of its own', () => {
  const html = builtPage()
  const files = releasesText()
  const summaries = [...summaryEntries().values()].map(plain).join('\n')
  // The page's own words, and the whole of them: its title, the notes block's label — the
  // one word of an entry that names what the block is rather than saying anything about a
  // release — the marker a release the plugin's version has not reached carries, and the
  // tags, which come from the filenames. Every other word the page renders comes from a
  // release file or from the release's own summary in website/data/release-summaries.yaml.
  const own = new Set(['Changelog', NOTES_LABEL, 'not yet released', ...releaseFiles().map(tagOf)])

  for (const sentence of pageSentences(html)) {
    if (files.includes(sentence) || summaries.includes(sentence)) continue
    assert.ok(
      own.has(sentence),
      `the page carries a sentence that is in no release file and in no summary: ${JSON.stringify(sentence)}. The page renders the release notes and their summaries; it writes no "what's new" and no editorial of its own (W1.9 step 2).`,
    )
  }
})

test('each release renders the summary its own entry in the data file carries', () => {
  const html = builtPage()
  const entries = summaryEntries()
  const onPage = summariesOnPage(html)
  const releases = newestFirst()

  // One summary per release, in the order the entries are, and none anywhere else: a
  // release whose summary is missing is an entry with nothing under its heading, and a
  // summary for a release the page does not list is a line nothing renders.
  assert.deepEqual(
    onPage.map((item) => item.tag),
    releases,
    'the page does not render one summary per release, in the newest-first order of the entries',
  )

  for (const item of onPage) {
    // The description of the owner's brief of 2026-10-19, in one pair of assertions: every
    // release file has an entry in the data file, and its text is what the page renders.
    assert.ok(
      entries.has(item.tag),
      `the page renders a summary for ${item.tag}; website/data/release-summaries.yaml has no entry for it, and every release file needs one`,
    )
    assert.equal(
      item.text,
      plain(entries.get(item.tag)),
      `the summary rendered under ${item.tag} is not the entry website/data/release-summaries.yaml carries for it`,
    )
    assert.ok(item.text.length > 0, `the summary rendered under ${item.tag} is empty`)
  }

  // And the other direction: every entry in the data file is a release file this page
  // lists, so an entry outliving its release is refused rather than left unrendered.
  const files = new Set(releaseFiles().map(tagOf))
  for (const tag of entries.keys()) {
    assert.ok(files.has(tag), `website/data/release-summaries.yaml carries an entry for ${tag}, and docs/releases/ holds no such file`)
  }
})

test('a release’s summary is the site’s words, not an internal id or a repository path', () => {
  // The owner's brief of 2026-10-19 asks for "product features that end users understand":
  // an internal task id, a decision number or a path into this repository is none of them,
  // and a reader who meets one in the first line under a release has been handed the
  // contributor's view of it. The check is deliberately narrow — the identifiers this
  // repository writes its own work with, and a filename — so it refuses what the brief
  // names and guesses at nothing else a summary might say.
  const INTERNAL_ID = /\b[DWPF]\d+(?:\.\d+)?\b|\bRF\d+\b/
  const REPO_PATH = /\b\S+\.(?:md|json|yaml)\b/

  for (const [tag, summary] of summaryEntries()) {
    assert.doesNotMatch(summary, INTERNAL_ID, `the summary for ${tag} carries an internal id (${INTERNAL_ID.source}): a summary says what the release gives a reader, not what this repository called the work`)
    assert.doesNotMatch(summary, REPO_PATH, `the summary for ${tag} names a file in this repository (${REPO_PATH.source}): a reader of the changelog has no use for the path a change was made in`)
  }
})

test('the release the plugin’s version has not reached is rendered as not yet released', () => {
  const html = builtPage()
  const { publishedVersion } = build()
  // The release files above the version the copy's plugin manifest carries — the same rule
  // the generator applies (`src/lib/changelog.mjs`), computed here from the filenames so the
  // expectation is the directory's rather than the generator's own answer.
  const ahead = newestFirst().filter((tag) => {
    const file = versionOf(tag)
    const published = versionOf(`v${publishedVersion}`)
    for (let i = 0; i < 3; i++) {
      if (file[i] !== published[i]) return file[i] > published[i]
    }
    return false
  })

  // The copy's manifest carries the second-newest release file's version and the copy has no
  // `.git` at all, so a tag cannot be the reason for this answer: the one release file ahead
  // of that version is the one entry the page marks, and every older one is published (the
  // header says why the copy is built this way). Every release file is listed on the page, so
  // "every older one is published" is the whole of the list minus the one marked — a page
  // that marked nothing, or marked everything, differs here.
  assert.equal(
    ahead.length,
    1,
    'the copy is built one release ahead of its version bump, so exactly one release file is above it',
  )
  assert.deepEqual(
    unpublishedOnPage(html),
    ahead,
    `the page marks ${JSON.stringify(unpublishedOnPage(html))} as not yet released, and the release files above the plugin’s ${publishedVersion} are ${JSON.stringify(ahead)}`,
  )
})

test('the changelog page renders each release file, line for line and in the file’s order', () => {
  const html = builtPage()

  // One comparison per release, of the whole entry against the whole file: every content
  // line is rendered, none is added, and the order is the file's — the Highlights, then
  // each section heading as its own `<h3>` with its lines under it, in the order the file
  // writes the two sections. A `### Known limits` line that leaks into the page as text, a
  // section rendered before the one the file puts first, or a line left out all differ from
  // the file here, in the element that differs.
  for (const fileName of releaseFiles()) {
    const tag = tagOf(fileName)
    assert.deepEqual(
      entryElements(html, tag),
      fileElements(fileName),
      `the entry for ${tag} is not docs/releases/${fileName} rendered, line for line and in the file’s order`,
    )
  }

  // And the new section is on the page as a heading rather than only as text somewhere: the
  // 0.8.5 release file carries the release's known limits, and a reader reads them under
  // that heading — which is what the release rule asks the page for.
  const withLimits = releaseFiles()
    .map(tagOf)
    .filter((tag) => contentLines(`${tag}.md`).includes(sectionHeading('Known limits')))
  assert.ok(withLimits.length > 0, 'no release file carries a Known limits section, so this case proves nothing')
  for (const tag of withLimits) {
    assert.deepEqual(
      entryElements(builtPage(), tag).filter((element) => element.kind === 'h3').map((element) => element.text),
      contentLines(`${tag}.md`)
        .filter((line) => SECTION_TITLES.some((title) => line === sectionHeading(title)))
        .map((line) => plain(line.replace(/^#{1,6}\s+/, ''))),
      `the entry for ${tag} does not render its own section headings, in the file’s order`,
    )
  }
})

test('the changelog page states no date', () => {
  const html = builtPage()

  // No date of the page's own: the files carry none, the site's build is network-free and
  // the entries carry no tag to read one from, so the date a reader wants is the one the
  // GitHub release page states (D100). A `<time>` element is the way a page states one.
  assert.ok(!/<time\b/i.test(html), 'the page carries a <time> element, and the site states no release date')

  // The dates a release file's own notes mention stay: they are that file's text, and they
  // are rendered because the line around them is. The summaries are the site's own lines
  // (the owner's brief of 2026-10-19), and they are the other half of what the page says
  // besides the files — so the sentences held to "no date" are the ones that are neither.
  const files = releasesText()
  const summaries = [...summaryEntries().values()].map(plain)
  const own = pageSentences(html).filter((sentence) => !files.includes(sentence) && !summaries.includes(sentence))
  for (const sentence of own) {
    assert.doesNotMatch(sentence, /\b\d{4}-\d{2}-\d{2}\b/, `the page writes a date of its own: ${JSON.stringify(sentence)}`)
  }
  assert.ok(pageText(html).length > 0, 'the page renders no text at all')
})

test('every entry links to its GitHub release, at a URL built from site.yaml', () => {
  const html = builtPage()
  const repository = /^repository:\s*(\S+)$/m.exec(readFileSync(path.join(SITE, 'data', 'site.yaml'), 'utf8'))[1]
  const canonical = /^canonical_url:\s*(\S+)$/m.exec(readFileSync(path.join(SITE, 'data', 'site.yaml'), 'utf8'))[1]

  for (const tag of releaseFiles().map(tagOf)) {
    assert.ok(
      html.includes(`href="${repository}/releases/tag/${tag}"`),
      `the page does not link ${tag} to its GitHub release, which is where its date and its artifacts are: ${repository}/releases/tag/${tag}`,
    )
  }

  // And no URL is typed into the page: every absolute href is built from a fact of
  // site.yaml — the repository the entries link, or the canonical origin the layout
  // composes the page's own URLs from (W1.3). A hard-coded host here is a fact nothing
  // pins, and the page is the one place a release URL could look real while pointing
  // somewhere else.
  for (const match of html.matchAll(/href="([^"]*)"/g)) {
    const href = match[1]
    if (href.startsWith('/') || href.startsWith('#')) continue
    assert.ok(
      href.startsWith(repository) || href.startsWith(canonical),
      `the page carries an absolute link that is not built from site.yaml: ${href}`,
    )
  }
})

// ---------------------------------------------------------------------------------------
// The bold look (the owner's brief of 2026-10-21).
// ---------------------------------------------------------------------------------------

test('the page opens with the display scale’s headline, its word in the accent’s gradient', () => {
  const html = builtPage()

  // One `h1`, and it is the page's own name: `website/test/a11y.mjs` counts the headings, and
  // this word is the whole of what the page writes for itself.
  assert.equal(
    (html.match(/<h1\b/g) ?? []).length,
    1,
    'the page carries more than one <h1>: the bold look’s headline is the page’s one name, and a second one is a page with two first things',
  )
  const h1 = /<h1\b([^>]*)>([\s\S]*?)<\/h1>/.exec(html)
  assert.ok(h1, 'the page carries no <h1>, so it opens with nothing a reader reads first')

  // The headline is `base.css`'s largest display step — the weight, the tracking and the
  // leading the owner's boards set a headline in, shared by every page that wears the look —
  // and not a size this page invented.
  assert.match(
    h1[1],
    /\bclass="[^"]*\bdisplay-xl\b[^"]*"/,
    `the page’s <h1> is not the display scale’s largest step (${JSON.stringify(h1[1])}): the bold look’s headline is \`display-xl\`, and a page that sets its own size is the drift the shared look exists to prevent`,
  )

  const headline = elementText(h1[2])
  assert.equal(headline, 'Changelog', `the page’s headline is ${JSON.stringify(headline)}, and the page’s own word is its name`)

  // The gradient: `base.css`'s own class, drawn on the headline's words. The boards draw part
  // of a headline in it and the rest in the text tone; this headline is one word, so the word
  // is what is drawn in it — and the check is that the gradient carries those words rather
  // than sitting beside them, which is what an emphasis on nothing would look like.
  const gradient = /<span\b[^>]*\bclass="[^"]*\bgradient-text\b[^"]*"[^>]*>([\s\S]*?)<\/span>/.exec(h1[2])
  assert.ok(
    gradient,
    'the headline carries no element in the accent’s gradient: the bold look draws the emphasised words of a headline in it (`base.css`’s `.gradient-text`), and this page’s one headline is where it has them',
  )
  assert.equal(
    elementText(gradient[1]),
    headline,
    'the accent’s gradient is not drawn on the headline’s own words: the gradient is an emphasis, and an emphasis is drawn on the words it emphasises',
  )

  // And the page writes nothing between the headline and its first release. This is the
  // decision the bold look's eyebrow, pill and lead paragraph would each have broken: every
  // sentence on this page comes from `docs/releases/` or from
  // `website/data/release-summaries.yaml` (the case above refuses one that does not), so a
  // page that gave itself an intro would have to write one.
  const firstRelease = html.indexOf('<h2')
  assert.notEqual(firstRelease, -1, 'the page carries no release heading, so there is no first entry for the headline to open onto')
  const between = html.slice(h1.index + h1[0].length, firstRelease)
  assert.equal(
    elementText(between),
    '',
    `the page writes ${JSON.stringify(elementText(between))} between its headline and its first release: the page has no lead, no eyebrow and no pill of its own, because it writes no sentence of its own (W1.9)`,
  )
})

test('every release is a card, and the list is spaced by a step of the scale', () => {
  const html = builtPage()

  // One card per release file, drawn on `base.css`'s own shape: the panel a page is drawn on,
  // one step above the ground with a hairline and the larger radius. The page used to be a
  // list of entries separated by a hairline; the bold look makes each release a thing of its
  // own, and a class of this page's own would be a second card shape for the same site.
  const articles = [...html.matchAll(/<article\b([^>]*)>/g)]
  assert.equal(
    articles.length,
    releaseFiles().length,
    `the page renders ${articles.length} entries and docs/releases/ holds ${releaseFiles().length} release files`,
  )
  for (const [, attrs] of articles) {
    assert.match(
      attrs,
      /\bclass="[^"]*\bcard\b[^"]*"/,
      `an entry is not drawn as a card (${JSON.stringify(attrs)}): every release sits on the board’s panel (base.css’s \`.card\`), and an entry with no box is the hairline-separated list the bold look replaced`,
    )
  }

  const css = pageCSS(html)

  // The list: a grid whose gap is a step of the spacing scale, so two releases read as two
  // things and the room between them is the page's room rather than a number this component
  // invented.
  const list = declarationsFor(css, 'the list of releases', '.changelog[')
  assert.match(list, /display:\s*grid/, `the releases are not laid out as a grid (${list}): the gap between two cards is a grid gap, and a list with no layout of its own is the stack this page used to be`)
  assert.match(
    list,
    /gap:\s*var\(--space-[a-z0-9]+\)/,
    `the room between two releases is not a step of the spacing scale (${list}): a gap written as a number is a value no other block of the site can be compared with`,
  )

  // And the head gives the headline room below it, in a step of the same scale — and **no room
  // above it**: the top of the page is `main`'s own padding, which every page of the site
  // opens at (`--space-lg` there and `--space-xl` from 48 rem up, `base.css`), so a head that
  // pads its own top sets this page's headline a step lower than every other page's. The
  // pages whose first block is a full container of its own drop `main`'s padding instead
  // (`main:has(> .hero, > [data-security-page])`) and this head is not one of those.
  const head = declarationsFor(css, 'the page’s head', '.changelog-head[')
  assert.match(
    head,
    /padding-block:\s*0\s+var\(--space-[a-z0-9]+\)/,
    `the page’s head does not open at zero and give the headline a step of the spacing scale below it (${head}): the room above a page’s first block is \`main\`’s own padding, so a head with a top padding of its own puts the page’s headline a step lower than every other page’s — and a head with no room under it leaves the first card reading as the next line of the title`,
  )
})

test('an entry is read in one hierarchy: the tag, the summary, then the notes', () => {
  const html = builtPage()
  const css = pageCSS(html)

  // The tag is the card's title at the display scale's middle step — the same shared class the
  // page's own headline is set in one step up — and it is still the entry's heading, its anchor
  // and its link to the GitHub release (the case above reads the URL).
  const headings = [...html.matchAll(/<h2\b([^>]*)>/g)]
  assert.ok(headings.length > 0, 'the page carries no entry heading')
  for (const [, attrs] of headings) {
    assert.match(
      attrs,
      /\bclass="[^"]*\bdisplay-md\b[^"]*"/,
      `an entry’s heading is not at the display scale (${JSON.stringify(attrs)}): the tag is the card’s title, and it is set in the same display scale as the page’s headline`,
    )
  }

  // The summary is the loudest prose in the card: one step up the type scale, which is what
  // makes it the line a reader reads first rather than a paragraph among the notes.
  const summary = declarationsFor(css, 'the release summary', '.release-summary[')
  assert.match(
    summary,
    /font-size:\s*var\(--font-size-lg\)/,
    `the release summary is not set a step up the type scale (${summary}): the summary is what a release gives a reader, and it is the line of the card that is read first`,
  )

  // The notes are a control: a filled tone step, the accent's legible tone, and a target
  // the owner's brief of 2026-10-19 measures every control against. The `<details>` is still
  // the mechanism (no script), so what is pinned here is that a reader can see it and reach it.
  const control = declarationsFor(css, 'the notes’ control', '.release-notes[', 'summary')
  assert.match(
    control,
    /min-height:\s*44px/,
    `the notes’ control is not a 44 px target (${control}): the owner’s brief of 2026-10-19 measures every control of the site against it`,
  )
  // Since the owner's polish round of 2026-10-23 a control is marked by a filled tone step (the
  // panel) rather than by the accent's hairline: no 1 px outlined boxes. The accent stays its label.
  assert.match(
    control,
    /background:\s*var\(--panel\)/,
    `the notes’ control is not drawn on the panel's tone step (${control}): a control the reader can operate is a filled shape, not a line of prose`,
  )
  assert.match(
    control,
    /color:\s*var\(--accent-on-dark\)/,
    `the notes’ control is not set in the accent’s legible tone (${control}): it is text a reader operates the page with, and the muted tone is for prose`,
  )

  // The file's own lines under the control are the quietest tone in the card — they are the
  // operator's detail, not what the release gives a reader. The chips those lines are full of
  // are the next case's subject, because a rule that paints them has to be able to reach them.
  const notes = declarationsFor(css, 'the notes’ own lines', '.release-notes[')
  assert.match(
    notes,
    /color:\s*var\(--text-muted\)/,
    `the notes’ own lines are not the quieter tone (${notes}): the collapsed block is the operator’s detail, and it reads below the summary that says what the release gives a reader`,
  )

  // The "not yet released" marker is the board's pill — the shape it draws a badge of one or
  // two words in — and the warning tone, because it is the one line of the card that says
  // "not yet" (the case above reads which release carries it).
  for (const [, attrs] of html.matchAll(/<p\b([^>]*data-status="not-yet-released"[^>]*)>/g)) {
    assert.match(
      attrs,
      /\bclass="[^"]*\bpill\b[^"]*"/,
      `the not-yet-released marker is not the board’s pill (${JSON.stringify(attrs)}): the marker is a badge of two words, and the pill is the shape the boards draw those in`,
    )
  }
  const status = declarationsFor(css, 'the not-yet-released marker', '.release-status[')
  assert.match(
    status,
    /color:\s*var\(--warn\)/,
    `the not-yet-released marker is not in the warning tone (${status}): the tone that means "pay attention" is the one a line saying "not yet" is read in`,
  )
})

test('the notes’ code chips are painted on the ground, by a rule that reaches them', () => {
  const html = builtPage()
  const css = pageCSS(html)

  // A code span in a release note is a command, and a chip the same tone as the card it sits
  // on is a chip nobody sees: the card is `--surface` (`base.css`) and `base.css` paints a
  // chip `--surface` too, so the rule that draws one on the ground is the only thing that
  // makes it read as a chip rather than as a word in the mono face.
  //
  // **What is read is reachability, not the text of a selector.** The notes arrive through
  // `set:html`, Astro's scope attribute never reaches the markup a release file wrote, and the
  // scoped `.release-notes code` this page shipped was built as `.release-notes[cid]
  // code[cid]` — a selector no chip could match. The rule was in the stylesheet, named the
  // right elements, painted nothing, and a case that asked whether a rule with those fragments
  // existed answered yes. So the rule is matched against the chips the page rendered.
  const failures = chipFailures(css, html)
  assert.deepEqual(
    failures,
    [],
    `the notes’ code chips are not painted by a rule that reaches them:\n  ${failures.join('\n  ')}`,
  )
})

test('the chip reading refuses a rule that can match no chip, and passes the page’s own', () => {
  const html = builtPage()
  const css = pageCSS(html)

  // The page's own rule for the chips, as the built stylesheet writes it: the fixture has to
  // edit the rule the page carries rather than write one here, because the defect is the shape
  // Astro builds a scoped selector into.
  const rule = /\.release-notes\[[^{}]*\bcode[^{}]*\{[^{}]*\}/.exec(css)
  assert.ok(rule, 'the changelog stylesheet carries no `.release-notes … code` rule, and the cases below are about the page’s own rule')
  const scope = /\[(data-astro-cid-[^\]]+)\]/.exec(rule[0])
  assert.ok(scope, `the page’s chip rule carries no scope attribute (${rule[0]}), and the plant below reproduces the scoped shape from it`)

  // **The defect as it shipped**: the component's own scoped rule, which Astro builds with the
  // scope attribute on every compound — the `code` one included, where no chip carries it.
  const shipped = rule[0].replace(/(\s)code(\s*\{)/, `$1code[${scope[1]}]$2`)
  assert.notEqual(shipped, rule[0], 'the plant did not change the chip rule, so the case below proves nothing about the scoped shape')

  const cases = [
    // The shape the page shipped: refused, by name, with the scope it requires and the fact
    // that the chips carry none — which is the two lines a reader needs to fix it.
    [
      'the scoped rule Astro builds, which no chip can match',
      css.replace(rule[0], shipped),
      1,
      [/can match any of the \d+ code chips/, /requires data-astro-cid-/, /the chips carry none/],
    ],
    // The other way a chip rule paints nothing: no rule at all, and the chips keep `base.css`'s
    // `--surface`, which is the card's own tone.
    [
      'the chip rule taken away',
      css.replace(rule[0], ''),
      1,
      [/no rule of the page’s stylesheet paints a code chip on the ground/],
    ],
    // The control: the page's own rule is refused not at all, so the two plants above are not
    // passing because the reading refuses every rule it sees.
    ['the page’s own rule', css, 0, []],
  ]
  for (const [what, stylesheet, count, patterns] of cases) {
    const failures = chipFailures(stylesheet, html)
    assert.equal(
      failures.length,
      count,
      `${what}: the reading reported ${failures.length} failures where ${count} were expected:\n  ${failures.join('\n  ')}`,
    )
    for (const pattern of patterns) {
      assert.match(
        failures.join('\n'),
        pattern,
        `${what}: the failure does not say what it is about:\n  ${failures.join('\n  ')}`,
      )
    }
  }
})

test('the page’s head carries the boards’ background, and the headline paints above it', () => {
  const html = builtPage()
  const css = pageCSS(html)

  // The boards draw a grid and two soft glows behind the top of a page, and `base.css` writes
  // that drawing once (`.hero-glow`) for the pages that open with it. This page opens with it
  // rather than with a drawing of its own, and it is decoration: `aria-hidden`, and no text.
  const glow = /<div\b[^>]*\bclass="[^"]*\bhero-glow\b[^"]*"[^>]*>/.exec(html)
  assert.ok(
    glow,
    'the page’s head carries no background: the bold look opens a page with the boards’ grid and glows (`base.css`’s `.hero-glow`, which the home page’s hero opens with)',
  )
  assert.match(
    glow[0],
    /\baria-hidden="true"/,
    `the page’s background is not marked as decoration (${glow[0]}): it is a drawing that says nothing, and a reader given it is given a picture of a grid`,
  )
  const head = /<header\b[^>]*\bclass="[^"]*\bchangelog-head\b[^"]*"[^>]*>([\s\S]*?)<\/header>/.exec(html)
  assert.ok(head, 'the page carries no head element')
  assert.ok(
    head[1].indexOf('hero-glow') < head[1].indexOf('<h1'),
    'the background is not the head’s first child: the layer is behind the headline, and a layer written after it is a drawing over the page’s own name',
  )

  // The two rules the layer needs, and the one the headline needs to paint above it: a
  // positioned box paints after a static one, so a headline left static would be drawn under
  // the glow rather than over it.
  const headRule = declarationsFor(css, 'the page’s head', '.changelog-head[')
  assert.match(headRule, /position:\s*relative/, `the page’s head is not a positioned box (${headRule}): the background is absolutely positioned inside it, and a glow positioned against the page is a glow across the page`)
  assert.match(headRule, /overflow:\s*hidden/, `the page’s head does not clip its background (${headRule}): the drawing is wider than the box it belongs to`)
  const titleRule = declarationsFor(css, 'the page’s headline', '.changelog-title[')
  assert.match(titleRule, /position:\s*relative/, `the page’s headline is not a positioned box (${titleRule}): the background paints over a static box, so the headline would be drawn under the glow it sits on`)
})
