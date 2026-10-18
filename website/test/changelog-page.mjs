// website/test/changelog-page.mjs — the changelog page against docs/releases/ (W1.9).
//
// The page is a rendering of `docs/releases/vX.Y.Z.md` and of nothing else, so the source
// of truth of every assertion below is **the directory itself** — read here, never the
// generator's own output and never a git tag (D100, finding 44). Four things are checked:
//
//   1. **The page's release set equals the directory's, in both directions.** A release
//      file the page omits fails, and a tag the page lists that no file backs fails. D100
//      calls this the whole of the drift protection, because the page is the first
//      reader-facing index of those files (the README keeps no changelog section).
//   2. **The newest entry is the newest file**, by the version in the filename.
//   3. **The page adds no sentence of its own**, and drops no line of any file.** Every
//      sentence the page renders appears in a release file, and every content line of
//      every release file appears on the page. The only words the page writes itself are
//      its title, the "not yet released" marker W1.9 step 1 requires, and the tags, which
//      come from the filenames.
//   4. **Every entry links to its GitHub release**, at a URL built from site.yaml's
//      repository rather than typed into the page, and the page states no date — the files
//      carry none and the newest entry has no tag to read one from (D100).
//
// **How it builds, and why the build has no tags.** A page is observable only as HTML, so
// this file builds the site — in a **copy of the repository in a temp directory**, with
// `node_modules` symlinked rather than installed, the way W1.3's layout test builds for the
// same reason: a build must not be run against the checkout it is asserted over, and the
// assertion needs a checkout whose shape this file controls. The copy deliberately carries
// **no `.git`**, so the checkout the build runs in has no tags at all. That is exactly the
// state W0.6 designed the generator for (a shallow CI checkout has none either), and it is
// what makes the "not yet released" state deterministic here **without any test reading a
// tag**: in the copy every entry is unreleased, and the case below asserts the page says so.
// The released half of that pair is deliberately not asserted — it is decided by the tags
// of the checkout the build ran in, and D100's rule is that no test reads one.
//
// The build is network-free: `npm ci` is not run here, and the only executable it starts is
// the site's own installed Astro.

import { test, after } from 'node:test'
import assert from 'node:assert/strict'
import { cpSync, existsSync, mkdtempSync, readFileSync, readdirSync, rmSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { linkNodeModules } from './lib/build.mjs'

// website/test/changelog-page.mjs -> website/test -> website.
const SITE = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
// ... -> the repository the site is built from.
const REPO = path.dirname(SITE)

const RELEASES_DIR = path.join(REPO, 'docs', 'releases')

// `docs/releases/README.md` describes the format; it is the one file in the directory that
// is not a release, and it is skipped by name rather than by the `vX.Y.Z.md` pattern, so a
// file the generator does not recognise fails the coverage case instead of being filtered
// out of the expectation by the same rule that filtered it out of the answer (W0.6).
const NON_RELEASE = 'README.md'

// The heading the files use to separate the operator steps from the user bullets. It is the
// one content line the page does not render verbatim: the page prints it as the block's own
// heading, which is the same words.
const OPERATORS_HEADING = '### For operators'

// What a site build cannot read, and therefore what the copy below leaves out: the Go
// module's own tree and test data, the console application, and the installed and built
// directories (the last two are symlinked and rebuilt instead). The same sets W1.3's layout
// test uses — a copy has to be the shape of the repository, minus what no page reads.
const SKIP = new Set(['.git', 'internal', 'cmd', 'console', 'plugin', 'schemas', 'deploy', 'testdata'])
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
      if (parent === 'website' && SKIP_UNDER_WEBSITE.has(name)) return false
      return true
    },
  })
  // The installed tree is symlinked rather than copied: it is the pinned tree `npm ci`
  // wrote, and this file installs nothing (W1.1).
  linkNodeModules(path.join(root, 'website', 'node_modules'))

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
// The page: the built HTML, read the way a reader reads it.
// ---------------------------------------------------------------------------------------

// The text-bearing elements of the page's main region, one entry per element: a heading, a
// paragraph or a list item. Reading the page element by element is what keeps two adjacent
// blocks from being glued into one "sentence" that no file carries — the page's text is
// exactly these strings, in this order.
const TEXT_ELEMENT = /<(h1|h2|h3|p|li)\b[^>]*>([\s\S]*?)<\/\1>/g

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

/** The tags the page's entry headings carry, in the order they appear. */
function tagsOnPage(html) {
  return [...html.matchAll(/<h2\b[^>]*>([\s\S]*?)<\/h2>/g)].map((match) =>
    decodeEntities(match[1].replace(/<[^>]*>/g, '')).replace(/\s+/g, ' ').trim(),
  )
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
  // The page's own words, and the whole of them: its title, the marker W1.9 step 1
  // requires for a release with no tag, and the tags, which come from the filenames.
  const own = new Set(['Changelog', 'not yet released', ...releaseFiles().map(tagOf)])

  for (const sentence of pageSentences(html)) {
    if (files.includes(sentence)) continue
    assert.ok(
      own.has(sentence),
      `the page carries a sentence no release file does: ${JSON.stringify(sentence)}. The page renders the release notes; it writes no summary, no "what's new" and no editorial of its own (W1.9 step 2).`,
    )
  }
})

test('the changelog page renders every line of every release file', () => {
  const text = pageText(builtPage())

  for (const fileName of releaseFiles()) {
    for (const line of contentLines(fileName)) {
      if (line === OPERATORS_HEADING) continue
      assert.ok(
        text.includes(plain(line)),
        `docs/releases/${fileName} carries a line the page does not render: ${JSON.stringify(line)}`,
      )
    }
  }
})

test('a release with no tag in the checkout is rendered as not yet released', () => {
  const html = builtPage()

  // The copy this build ran in has no `.git`, so it has no tags: every entry is unreleased,
  // which is the state a shallow CI checkout is in too, and the state of the newest file
  // everywhere — it is committed to main before its tag exists (D100).
  const markers = (html.match(/not yet released/g) ?? []).length
  assert.equal(
    markers,
    releaseFiles().length,
    `the page marks ${markers} releases as not yet released, and a checkout with no tags has ${releaseFiles().length}`,
  )
})

test('the changelog page states no date', () => {
  const html = builtPage()

  // No date of the page's own: the files carry none, the site's build is network-free and
  // the newest entry has no tag to read one from, so the date a reader wants is the one the
  // GitHub release page states (D100). A `<time>` element is the way a page states one.
  assert.ok(!/<time\b/i.test(html), 'the page carries a <time> element, and the site states no release date')

  // The dates the release notes themselves mention stay: they are the files' own text, and
  // they are rendered because the line around them is.
  const text = pageText(html)
  const own = pageSentences(html).filter((sentence) => !releasesText().includes(sentence))
  for (const sentence of own) {
    assert.doesNotMatch(sentence, /\b\d{4}-\d{2}-\d{2}\b/, `the page writes a date of its own: ${JSON.stringify(sentence)}`)
  }
  assert.ok(text.length > 0, 'the page renders no text at all')
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
