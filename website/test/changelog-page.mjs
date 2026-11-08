// website/test/changelog-page.mjs — the changelog page against docs/releases/ (W1.9).
//
// The page is a rendering of `docs/releases/vX.Y.Z.md` and of `website/data/release-summaries.yaml`,
// and of nothing else, so the source of truth of every assertion below is **those two files** —
// read here, never the generator's own output and never a git tag (D100, finding 44). Six
// things are checked:
//
//   1. **The page's release set is the three newest release files** (D1640): every entry is
//      one of the three newest files, in the order their filenames' versions give, and a tag
//      the page lists that no file backs fails. The cut is a cut — `docs/releases/` holds
//      more files than the page renders — and the files are still the source, read here
//      rather than asked of the generator (D100). Under the entries the page renders the
//      link to GitHub's releases page and one line per other stream (D1641, D1644, D1648).
//      **Only releases are counted**: a file for a version above the CLI stream's `current`
//      (`docs/release-streams.yaml`) is the next release's notes, written ahead of their
//      release, and the page renders — and counts — the three newest releases without it.
//   2. **The newest entry is the newest file**, by the version in the filename.
//   3. **The page adds no sentence of its own beyond those two files**, and drops no line of
//      any release file it renders.** Every sentence the page renders is in a release file
//      or in the summary a data file carries for that release, and every content line of a
//      rendered release file appears on the page. The only words the page writes itself are
//      its title, the notes block's label, the tags (which come from the filenames), the
//      link to the full list, and the label of each stream's line — named in the case below,
//      and nothing else.
//   4. **Every entry links to its GitHub release**, at a URL built from site.yaml's
//      repository rather than typed into the page, and the page states no date — the files
//      carry none, and the date a reader wants is the one GitHub states (D100).
//   5. **Each release's summary is the one the data file carries for it** (the owner's brief
//      of 2026-10-19): the page renders one summary per rendered release, from
//      `website/data/release-summaries.yaml`, and the release file's own Highlights are the
//      block a reader opens below it. Every release file has an entry in that file, none is
//      empty, no entry outlives the file it names, and the words of an entry are the site's
//      rather than an internal id or a repository path. **Every release file, and not only
//      the three the page renders** (F15, D1647): the page validates each `buildChangelog`
//      entry's summary before it slices, so an older release with no line still stops the
//      build — the fixture case near the foot of this file shows it failing.
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
// **How it builds, and what makes the expected set deterministic.** A page is observable only
// as HTML, so this file builds the site — in a **copy of the repository in a temp directory**,
// with `node_modules` symlinked rather than installed, the way W1.3's layout test builds for
// the same reason: a build must not be run against the checkout it is asserted over, and the
// assertion needs a checkout whose shape this file controls. The copy carries **no `.git`**,
// and nothing in it is edited: which releases the page renders is decided by the CLI stream's
// `current` version in `docs/release-streams.yaml`, a file every checkout carries, and never by
// the checkout's tags — which is what the deploy build, checked out without tags, needs (D100).
//
// The build is network-free: `npm ci` is not run here, and the only executable it starts is
// the site's own installed Astro.

import { test, after } from 'node:test'
import assert from 'node:assert/strict'
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { cssDeclarations, cssRules, parse, walk } from './lib/html.mjs'
import { ancestorsOf, classesOf, matchesSelector, subjectOf } from './lib/selectors.mjs'
import { READ_UNDER_PLUGIN, buildCopy, linkNodeModules } from './lib/build.mjs'
import { latestMobileRelease } from '../src/lib/mobile-release.mjs'

// website/test/changelog-page.mjs -> website/test -> website.
const SITE = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
// ... -> the repository the site is built from.
const REPO = path.dirname(SITE)

const RELEASES_DIR = path.join(REPO, 'docs', 'releases')

// The site's own data: the end-user summary of each release (the owner's brief of 2026-10-19).
const SUMMARIES_FILE = path.join(SITE, 'data', 'release-summaries.yaml')

// The release streams catalogue, whose CLI stream's `current` version says which release files
// are releases at all: a file above it is the next release's notes, written ahead of their
// release, and the page does not render it (src/lib/changelog.mjs). Read here from the file
// rather than asked of the generator, as every other fact in this file is.
const STREAMS_FILE = path.join(REPO, 'docs', 'release-streams.yaml')

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

// How many CLI releases the page renders: the three newest files, and no others (D1640). The
// generator still returns every release file (`src/lib/changelog.mjs`), because the home page's
// hero reads the newest published entry out of the same call (F10); the cut is made in the page.
const SHOWN = 3

// The page's own new words (D1644, D1648), named here rather than matched loosely: the link to
// the full list, and the label each stream's line carries. The mobile line's sentence is not
// among them — it is that release's `mobile-vX.Y.Z` line in
// `website/data/mobile-release-summaries.yaml`, a file of this repository, so the case below
// allows it as a summary rather than as a word of the page.
const MORE_LABEL = 'See all releases on GitHub'
const CONSOLE_LABEL = 'Desktop console'
const MOBILE_LABEL = 'Mobile app'

// The two streams beside the CLI, and the rule that names their latest release (D1641, D1644):
// each stream's notes are its own directory under `docs/releases/` (`console/` and `mobile/`,
// beside the CLI's own files), each stream's version line is its own package manifest, and its
// latest release is the newest notes file at or below that version — the rule
// `src/lib/console-release.mjs` applies to the console and `src/lib/mobile-release.mjs` to the
// app. Read here from the tree, never asked of the page: what is asserted is the directory's
// answer, not the page's.
const CONSOLE = {
  name: 'the desktop console',
  notesDir: path.join(RELEASES_DIR, 'console'),
  packageFile: path.join(REPO, 'console', 'package.json'),
  // What its own releases' tags carry in front of the version, and what its notes' files do
  // not: its notes are `v0.120.0.md` and its releases are `console-v0.120.0` (docs/release.md).
  tagPrefix: 'console-',
}
const MOBILE = {
  name: 'the phone app',
  notesDir: path.join(RELEASES_DIR, 'mobile'),
  packageFile: path.join(REPO, 'mobile', 'package.json'),
  tagPrefix: 'mobile-',
}

// The phone app's own end-user summaries, keyed by its tag: the second summaries file, beside
// the CLI's (its header says why it is a file of its own). Nothing reads it yet — no mobile
// release exists — so it is read with no entry in it, and the case below proves that.
const MOBILE_SUMMARIES_FILE = path.join(SITE, 'data', 'mobile-release-summaries.yaml')

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
      // reads: the slider's "See it" panel quotes the skills (website/test/lib/build.mjs).
      if (parent === 'plugin' && !READ_UNDER_PLUGIN.has(name)) return false
      if (parent === 'website' && SKIP_UNDER_WEBSITE.has(name)) return false
      return true
    },
  })
  // The installed tree is symlinked rather than copied: it is the pinned tree `npm ci`
  // wrote, and this file installs nothing (W1.1).
  linkNodeModules(path.join(root, 'website', 'node_modules'))

  // The console application is skipped by the copy above, and its `package.json` is the one
  // file of it a build reads: the console's version line, from which the changelog page names
  // the latest console release (`src/lib/console-release.mjs`, D1641). It is read from the
  // checkout rather than typed, so the line these cases assert over carries the tree's own
  // version — and a copy without it would build a page with no console line at all, which is
  // not the page these cases are about.
  mkdirSync(path.join(root, 'console'))
  cpSync(path.join(REPO, 'console', 'package.json'), path.join(root, 'console', 'package.json'))

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

/**
 * The CLI stream's current release, as the tag of the file it names, read off
 * `docs/release-streams.yaml` by this file — the file the rule names rather than the module
 * that applies it. Every release file above it is the next release's notes, written ahead of
 * its release, and the changelog leaves it out.
 */
function cliCurrent() {
  const lines = readFileSync(STREAMS_FILE, 'utf8').split('\n')
  const start = lines.findIndex((line) => /^\s*-\s*name:\s*cli\s*$/.test(line))
  assert.notEqual(
    start,
    -1,
    'docs/release-streams.yaml carries no `- name: cli` stream, and the changelog reads the CLI’s current release from that stream',
  )
  for (let at = start + 1; at < lines.length; at++) {
    if (/^\s*-\s*name:/.test(lines[at])) break
    const m = /^\s*current:\s*"?(\d+\.\d+\.\d+)"?\s*$/.exec(lines[at])
    if (m !== null) return `v${m[1]}`
  }
  throw new Error('docs/release-streams.yaml carries no `current: x.y.z` line under the cli stream, and the changelog compares every release file’s version with it')
}

/** The release files that are releases: the ones at or below the CLI stream's current version. */
function releasedFirst() {
  const current = versionOf(cliCurrent())
  return newestFirst().filter((tag) => {
    const file = versionOf(tag)
    for (let i = 0; i < 3; i++) {
      if (file[i] !== current[i]) return file[i] < current[i]
    }
    return true
  })
}

/**
 * The three newest release files: the entries the page renders, and nothing below them. The
 * files above the CLI's current version are not among them — a draft of the next release is
 * neither rendered nor counted against the three (src/lib/changelog.mjs).
 */
function shownReleases() {
  return releasedFirst().slice(0, SHOWN)
}

/**
 * A stream's latest release — `{version, tag}`, as both stream modules return it — or null
 * when that stream has none: the newest notes file in its directory at or below the version
 * its package manifest carries, which is the rule `src/lib/console-release.mjs` and
 * `src/lib/mobile-release.mjs` apply (see CONSOLE and MOBILE above).
 */
function newestNotes(stream) {
  if (!existsSync(stream.notesDir) || !existsSync(stream.packageFile)) return null
  const packaged = versionOf(`v${JSON.parse(readFileSync(stream.packageFile, 'utf8')).version}`)
  const released = readdirSync(stream.notesDir)
    .filter((name) => /^v\d+\.\d+\.\d+\.md$/.test(name))
    .map(tagOf)
    .filter((tag) => {
      const file = versionOf(tag)
      for (let i = 0; i < 3; i++) {
        if (file[i] !== packaged[i]) return file[i] < packaged[i]
      }
      return true
    })
    .sort((a, b) => {
      const va = versionOf(a)
      const vb = versionOf(b)
      for (let i = 0; i < 3; i++) {
        if (va[i] !== vb[i]) return vb[i] - va[i]
      }
      return 0
    })
  if (released.length === 0) return null
  const version = released[0].slice(1)
  return { version, tag: `${stream.tagPrefix}v${version}` }
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

// The phone app's summaries are keyed by its own tag, `mobile-vX.Y.Z` (that file's header,
// `docs/releases/mobile/README.md`), and the same one-line shape holds. **Zero entries is the
// file's state today and not a failure**: no mobile release exists, so the page renders no
// mobile line to carry one, and the file is read here for the day one does.
const MOBILE_SUMMARY_ENTRY = /^ {2}(mobile-v\d+\.\d+\.\d+): (.+)$/

/** Every entry in `website/data/mobile-release-summaries.yaml`, by tag: today, none. */
function mobileSummaryEntries() {
  const lines = readFileSync(MOBILE_SUMMARIES_FILE, 'utf8').split('\n')
  const start = lines.indexOf('summaries:')
  assert.notEqual(start, -1, `${path.relative(REPO, MOBILE_SUMMARIES_FILE)} carries no \`summaries:\` line at the start of a line: the file states its entries under it`)

  const entries = new Map()
  for (let i = start + 1; i < lines.length; i++) {
    const line = lines[i]
    if (line.trim() === '' || line.trimStart().startsWith('#')) continue
    if (!line.startsWith(' ')) break // the next top-level key ends the map.
    const match = MOBILE_SUMMARY_ENTRY.exec(line)
    assert.ok(
      match,
      `${path.relative(REPO, MOBILE_SUMMARIES_FILE)}:${i + 1}: ${JSON.stringify(line)} is not \`  mobile-vX.Y.Z: one line of text\`: an entry is the app's tag and its summary on one line, and a value written another way is refused rather than read approximately`,
    )
    const [, tag, summary] = match
    assert.ok(summary.trim().length > 0, `${path.relative(REPO, MOBILE_SUMMARIES_FILE)}:${i + 1}: ${tag} carries an empty summary, and the changelog renders it under the app's line`)
    assert.equal(entries.has(tag), false, `${path.relative(REPO, MOBILE_SUMMARIES_FILE)}:${i + 1}: ${tag} is written twice, and the file's own reader keeps one entry per tag`)
    entries.set(tag, summary)
  }
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

/**
 * The stream lines the page renders under the entries, in the order it renders them: the words
 * of each line, the URL it links (or null for a line that links nothing, which is the mobile
 * app's — it creates no GitHub release), and the line's own attributes, which is where the
 * mobile line carries the tag it is about.
 */
function streamLines(html) {
  return [...html.matchAll(/<p class="changelog-stream"([^>]*)>([\s\S]*?)<\/p>/g)].map((match) => {
    const anchor = /<a\b[^>]*href="([^"]*)"/.exec(match[2])
    return {
      text: elementText(match[2]),
      href: anchor === null ? null : anchor[1],
      attrs: match[1],
    }
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
 * heading as the heading element it is. The summary is a data file's line rather than the
 * release file's — the case below holds it there — so it is left out here, as it is left out
 * of the gate above.
 */
function entryElements(html, tag) {
  const at = html.indexOf(`<h2 id="${tag}"`)
  assert.notEqual(at, -1, `the page carries no entry for ${tag}`)
  const article = html.slice(html.lastIndexOf('<article', at), html.indexOf('</article>', at))

  const out = []
  for (const match of article.matchAll(/<(h3|p|li)\b([^>]*)>([\s\S]*?)<\/\1>/g)) {
    if (match[2].includes('release-summary')) continue
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

test('the changelog page lists exactly the three newest CLI releases', () => {
  // The build's HTML directly rather than `builtPage()`: with no page there are no tags on
  // it, and the failure a reader needs is the one below — which release is missing, or which
  // one the page should not show — not "the page does not exist".
  const onPage = tagsOnPage(build().html ?? '')
  const files = releaseFiles().map(tagOf)
  const expected = shownReleases()

  // The cut is a cut: the directory holds more files than the page renders, so a page that
  // once again listed all of them fails here rather than passing by having nothing to drop
  // (D1640, D1645 — the three newest release files, a draft of the next release not counted).
  assert.ok(
    files.length > SHOWN,
    `docs/releases/ holds ${files.length} release files, and this case is about the page rendering the ${SHOWN} newest of them: with no older file there is no cut to read`,
  )

  // Newest first, so the release a reader would miss first is the one named: the newest
  // file is the one committed to main before its tag exists (D100).
  for (const tag of expected) {
    assert.ok(onPage.includes(tag), `page omits ${tag}, and it is one of the ${SHOWN} newest release files`)
  }
  for (const tag of onPage) {
    assert.ok(files.includes(tag), `page lists ${tag}, docs/releases/ holds no such file`)
  }
  assert.deepEqual(
    onPage,
    expected,
    `page lists ${JSON.stringify(onPage)}, and the ${SHOWN} newest release files are ${JSON.stringify(expected)}: the page renders the newest releases and links the rest to GitHub (D1640)`,
  )
})

test('the changelog page links the full list on GitHub', () => {
  const html = builtPage()
  const repository = /^repository:\s*(\S+)$/m.exec(readFileSync(path.join(SITE, 'data', 'site.yaml'), 'utf8'))[1]

  // The link the spec asks for, under the entries: one anchor to the repository's releases
  // page, which carries the CLI's and the console's releases (D1648). It is the one sentence
  // of the page's own that the owner allowed (the reconciliation's section 4 item 3), which is
  // why the "adds no sentence of its own" case names it in its allow-list.
  const links = [...html.matchAll(/<a\b[^>]*href="([^"]*)"[^>]*>([\s\S]*?)<\/a>/g)].filter(
    (match) => elementText(match[2]) === MORE_LABEL,
  )
  assert.equal(links.length, 1, `the page carries ${links.length} ${JSON.stringify(MORE_LABEL)} link(s), and the reader who wants the full history is given one`)
  assert.equal(
    links[0][1],
    `${repository}/releases`,
    `the full list is linked at ${links[0][1]}, and the repository's releases page is ${repository}/releases — a URL built from site.yaml's own fact and never typed into the page`,
  )
})

test('the dropped entries’ anchors and release links are gone', () => {
  const html = builtPage()
  const repository = /^repository:\s*(\S+)$/m.exec(readFileSync(path.join(SITE, 'data', 'site.yaml'), 'utf8'))[1]

  // The entries below the three are gone rather than merely hidden: an anchor that resolves
  // to nothing is worse than no anchor, and nothing in this repository links one (D1646). The
  // GitHub release URL every entry used stays valid for the dropped ones, and the link under
  // the entries is where a reader finds them now.
  const dropped = releasedFirst().slice(SHOWN)
  assert.ok(dropped.length > 0, 'docs/releases/ holds no release below the three newest, so this case proves nothing')
  for (const tag of dropped) {
    assert.ok(!html.includes(`id="${tag}"`), `the page still carries an element with id="${tag}", and the release it anchors is not rendered (D1646)`)
    assert.ok(
      !html.includes(`href="${repository}/releases/tag/${tag}"`),
      `the page still links ${tag}'s release directly, and the entry was dropped: the link to the full list is where a reader finds it now`,
    )
  }
  for (const tag of shownReleases()) {
    assert.ok(html.includes(`id="${tag}"`), `the page carries no id="${tag}", and the release it anchors is one of the three it renders`)
  }
})

test('the changelog page names the latest console release, linking its GitHub release', () => {
  const html = builtPage()
  const release = newestNotes(CONSOLE)
  const repository = /^repository:\s*(\S+)$/m.exec(readFileSync(path.join(SITE, 'data', 'site.yaml'), 'utf8'))[1]

  assert.ok(release, `docs/releases/console/ names no release at or below console/package.json's version, so there is nothing for this case to hold the page to`)
  const lines = streamLines(html)
  assert.deepEqual(
    lines.filter((line) => line.text.startsWith(CONSOLE_LABEL)).map((line) => line.text),
    [`${CONSOLE_LABEL} v${release.version}`],
    `the page's console line(s) are ${JSON.stringify(lines.map((line) => line.text))}, and ${CONSOLE.name}'s latest release is v${release.version}: the line names the newest notes file at or below the version console/package.json carries (D1641, D1644)`,
  )
  const console = lines.find((line) => line.text.startsWith(CONSOLE_LABEL))
  assert.equal(
    console.href,
    `${repository}/releases/tag/${release.tag}`,
    `the console line links ${console.href}, and its release is at ${repository}/releases/tag/${release.tag}`,
  )
})

test('the changelog page names no mobile line while no mobile release exists', () => {
  const html = builtPage()

  // The premise, asserted rather than assumed: the day the app's first release lands, this
  // case stops describing the tree and has to be replaced by one that reads its line (D1644).
  assert.equal(
    newestNotes(MOBILE),
    null,
    `docs/releases/mobile/ names a release at or below mobile/package.json's version, and the page renders a line for it: this case describes the tree before the app's first release and no longer applies`,
  )
  assert.deepEqual(
    streamLines(html).filter((line) => line.attrs.includes('data-mobile-release')),
    [],
    'the page renders a mobile line, and no mobile release exists: the line is the app\'s own release\'s sentence, so there is nothing it could say (D1644)',
  )
})

test('the build produces the changelog page', () => {
  const { status, output } = build()
  assert.equal(status, 0, `the site's build failed:\n${output}`)
  assert.ok(build().html, 'the build produced no website/dist/changelog/index.html')
})

test('the changelog page lists the newest release first', () => {
  const onPage = tagsOnPage(builtPage())
  const expected = shownReleases()

  // The newest entry is the newest file, and the order of the three is the files' own order:
  // the newest release is the one a reader sees first, and it is the file the version in the
  // filename says it is (D100 — no tag is read for this, and none is needed). "Newest" is the
  // version triple, never the file's time or its name's order — v0.9.7 comes after v0.10.0 by
  // name and before it by version.
  assert.equal(onPage[0], expected[0], `the first entry is ${onPage[0]}, and the newest release file is ${expected[0]}`)
  assert.deepEqual(onPage, expected, 'the entries are not the newest release files, newest first')
})

test('the changelog page adds no sentence of its own', () => {
  const html = builtPage()
  const files = releasesText()
  // Both summaries files, because both kinds of line are the site's data rather than the
  // page's prose: the CLI's, keyed by the release's tag, and the app's, keyed by the app's
  // (the file its own header explains). The app's has no entry today, which is its state.
  const summaries = [...summaryEntries().values(), ...mobileSummaryEntries().values()].map(plain).join('\n')
  const consoleRelease = newestNotes(CONSOLE)
  const mobileRelease = newestNotes(MOBILE)
  // The page's own words, and the whole of them: its title, the notes block's label — the
  // one word of an entry that names what the block is rather than saying anything about a
  // release — the link to the full list, the label of each stream's line (the two the owner
  // allowed in the reconciliation's section 4 item 3), and the tags, which come from the
  // filenames. Every
  // other word the page renders comes from a release file, from the release's own summary in
  // website/data/release-summaries.yaml, or from the app's line in the summaries file beside
  // it. Each label below is named by its words and the version the tree's own notes give, so
  // a line that named another version, or said anything else, fails here.
  const own = new Set([
    'Changelog',
    NOTES_LABEL,
    MORE_LABEL,
    ...(consoleRelease === null ? [] : [`${CONSOLE_LABEL} v${consoleRelease.version}`]),
    ...(mobileRelease === null ? [] : [`${MOBILE_LABEL} v${mobileRelease.version}`]),
    ...releaseFiles().map(tagOf),
  ])

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
  const releases = shownReleases()

  // One summary per rendered release, in the order the entries are, and none anywhere else: a
  // release whose summary is missing is an entry with nothing under its heading, and a
  // summary for a release the page does not render is a line nothing renders.
  assert.deepEqual(
    onPage.map((item) => item.tag),
    releases,
    `the page does not render one summary per rendered release, in the newest-first order of the entries: it renders ${JSON.stringify(onPage.map((item) => item.tag))}, and the ${SHOWN} newest are ${JSON.stringify(releases)}`,
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

  // And the other direction: every entry in the data file is a release file, so an entry
  // outliving its release is refused rather than left behind. The page renders the three
  // newest of them, so the entry a release below the three carries is not rendered and still
  // has to be there (D1647): the page validates every release file's summary before it slices
  // (the fixture case near the foot of this file).
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

test('no entry carries a state marker: the page renders releases, and nothing else', () => {
  const html = builtPage()

  // A release file written ahead of its release is not an entry at all (`src/lib/changelog.mjs`
  // leaves it out), so the page has no "not yet released" state to render and no entry may
  // carry one: the marker this page used to print for the newest file — above the plugin's
  // version — could never be set, because the release process records the CLI stream's current
  // version and bumps the plugin in the same release commit. An entry here is a release that
  // happened, and a page that marked one differs from the three release files it renders.
  assert.ok(
    !html.includes('not-yet-released'),
    'the page renders a "not yet released" marker, and every entry it renders is a release: a file ahead of the CLI stream’s current version is not rendered at all (src/lib/changelog.mjs)',
  )
})

test('the changelog page renders each release file, line for line and in the file’s order', () => {
  const html = builtPage()

  // One comparison per rendered release, of the whole entry against the whole file: every
  // content line is rendered, none is added, and the order is the file's — the Highlights,
  // then each section heading as its own `<h3>` with its lines under it, in the order the
  // file writes the two sections. A `### Known limits` line that leaks into the page as text,
  // a section rendered before the one the file puts first, or a line left out all differ from
  // the file here, in the element that differs. The files below the three are not rendered,
  // so the comparison is the three the page shows (D1640).
  for (const tag of shownReleases()) {
    assert.deepEqual(
      entryElements(html, tag),
      fileElements(`${tag}.md`),
      `the entry for ${tag} is not docs/releases/${tag}.md rendered, line for line and in the file’s order`,
    )
  }

  // And the section is on the page as a heading rather than only as text somewhere: a reader
  // reads a release's known limits under that heading — which is what the release rule asks
  // the page for. The files read are the three the page renders, because those are the
  // entries whose headings are on it.
  const withLimits = shownReleases().filter((tag) => contentLines(`${tag}.md`).includes(sectionHeading('Known limits')))
  assert.ok(
    withLimits.length > 0,
    `none of the ${SHOWN} rendered release files carries a Known limits section, so this case proves nothing about the headings the page renders`,
  )
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
  // (the owner's brief of 2026-10-19) and the app's line is its file's, so they are the other
  // half of what the page says besides the files — the sentences held to "no date" are the
  // ones that are neither.
  const files = releasesText()
  const summaries = [...summaryEntries().values(), ...mobileSummaryEntries().values()].map(plain)
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

  for (const tag of shownReleases()) {
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
// The rules, on directories this file controls (D1640, D1644, F15, D1647).
//
// The cases above read the corpus on `main`, which sorts the same way by name and by version
// and holds more than three release files. These build a copy whose `docs/releases/` holds
// files of this file's choosing, so the rules the page is built on are the subject rather than
// the corpus: "newest" is the version triple and not the filename's order, the summary check
// covers every release file rather than the three the page renders, and the app's first
// release has a sentence to say before the page renders a line for it.
// ---------------------------------------------------------------------------------------

test('the page’s releases are the newest by version, not by the filename’s order', (t) => {
  // Three files whose names sort one way and whose versions sort another: `v0.10.0` is the
  // first by name and the middle one by version, and `v0.20.3` is the last by name and the
  // newest of the three. A rule that cut the list by the filenames' order — or that compared
  // two filenames as strings — would drop the newest release of the three off the page here.
  const files = [
    ['v0.9.7', 'the middle file by name, and the oldest by version'],
    ['v0.10.0', 'the first file by name, and the middle one by version'],
    ['v0.20.3', 'the last file by name, and the newest by version'],
  ]
  const summary = (tag) => `the release filed as ${tag}`
  const built = buildCopy(
    t,
    ({ root, site }) => {
      const releasesDir = path.join(root, 'docs', 'releases')
      for (const name of readdirSync(releasesDir)) {
        if (name.endsWith('.md') && name !== NON_RELEASE) rmSync(path.join(releasesDir, name))
      }
      for (const [tag, line] of files) writeFileSync(path.join(releasesDir, `${tag}.md`), `- ${line}\n`)
      writeFileSync(
        path.join(site, 'data', 'release-summaries.yaml'),
        ['version: 1', 'summaries:', ...files.map(([tag]) => `  ${tag}: ${summary(tag)}`)].join('\n') + '\n',
      )
    },
    'fugaro-changelog-order-',
  )

  assert.equal(built.status, 0, `the site's build failed on a directory of three release files:\n${built.output}`)
  const html = readFileSync(path.join(built.dist, 'changelog', 'index.html'), 'utf8')
  assert.deepEqual(
    tagsOnPage(html),
    ['v0.20.3', 'v0.10.0', 'v0.9.7'],
    'the page does not list the three releases newest first by version: "newest" is the version triple (`src/lib/changelog.mjs`), and an order read off the filenames would put v0.10.0 first and v0.20.3 — the newest of the three — last, where the three-entry cut drops it',
  )
})

test('the build stops for an older release with no summary line', (t) => {
  // The oldest release file is one the page does not render, so a check that ran over the
  // three rendered entries would never look at it (D1647, the plan's F15). The fixture takes
  // that one line out of the copy's summaries file, and the build has to stop.
  const dropped = newestFirst().at(-1)
  const built = buildCopy(
    t,
    ({ site }) => {
      const file = path.join(site, 'data', 'release-summaries.yaml')
      const before = readFileSync(file, 'utf8')
      const after = before.replace(new RegExp(`^ {2}${dropped.replace(/\./g, '\\.')}: .*$`, 'm'), '')
      assert.notEqual(
        after,
        before,
        `website/data/release-summaries.yaml carries no \`  ${dropped}: \` line, and this case takes one away: a corpus where ${dropped} is already missing would pass this case for the wrong reason`,
      )
      writeFileSync(file, after)
    },
    'fugaro-changelog-summary-',
  )

  assert.notEqual(
    built.status,
    0,
    `the build succeeded with ${dropped}'s summary line removed: the page renders the three newest releases, and ${dropped} is not one of them, so the check has to run over every release file before the cut (F15, D1647)`,
  )
  assert.match(
    built.output,
    /website\/data\/release-summaries\.yaml/,
    `the failure does not name website/data/release-summaries.yaml, so it does not say which file the release has no line in:\n${built.output}`,
  )
  assert.ok(
    built.output.includes(dropped),
    `the failure does not name ${dropped}, so it does not say which release has no summary:\n${built.output}`,
  )
})

test('a release file written ahead of the CLI’s current release is not rendered, and needs no summary line', (t) => {
  // The release process writes the next release's highlights into the pull request that makes
  // the change (docs/releases/README.md), so docs/releases/ holds a file for a version the CLI
  // stream has not reached — v0.22.0 sat there while the stream was at v0.21.0, and the build
  // stopped on it for a summary line it does not have to carry. The fixture is that tree: the
  // draft, with no line in the summaries file, and the page renders the three newest **releases**
  // without it.
  const draft = 'v0.99.0'
  const built = buildCopy(
    t,
    ({ root }) => {
      writeFileSync(path.join(root, 'docs', 'releases', `${draft}.md`), '- the next release’s highlights, written ahead of its release\n')
      assert.ok(
        !summaryEntries().has(draft),
        `website/data/release-summaries.yaml carries a ${draft} line, and this case is about the draft the page must not ask one of`,
      )
    },
    'fugaro-changelog-draft-',
  )

  assert.equal(
    built.status,
    0,
    `the site's build failed on a tree carrying ${draft}.md, a release file above the CLI’s ${cliCurrent()} with no summary line: a draft of the next release is neither rendered nor asked for one\n${built.output}`,
  )
  const html = readFileSync(path.join(built.dist, 'changelog', 'index.html'), 'utf8')

  // The cut counts releases only: the draft would otherwise be the newest file of all, take the
  // first slot and push the newest release off the page.
  assert.deepEqual(
    tagsOnPage(html),
    shownReleases(),
    `the page renders ${JSON.stringify(tagsOnPage(html))}, and the ${SHOWN} newest releases are ${JSON.stringify(shownReleases())}: ${draft} is above the CLI’s ${cliCurrent()} and is not one of them`,
  )
  assert.ok(!html.includes(`id="${draft}"`), `the page carries an element with id="${draft}", and the release it anchors is a draft of the next release (src/lib/changelog.mjs)`)
  assert.ok(!html.includes(`/releases/tag/${draft}`), `the page links ${draft} at its GitHub release, and no such release exists yet`)
})

test('the build stops for the release at the CLI’s current version with no summary line', (t) => {
  // The same draft file, with the CLI stream raised to its version: it is the release the stream
  // is at, so it is an entry like any other and the page stops the build for the summary line it
  // has not got. The boundary is "at or below the current version is a release", and a page that
  // skipped everything it had no summary for fails here.
  const release = 'v0.99.0'
  const built = buildCopy(
    t,
    ({ root }) => {
      writeFileSync(path.join(root, 'docs', 'releases', `${release}.md`), '- a release with no summary line\n')
      const streams = path.join(root, 'docs', 'release-streams.yaml')
      const before = readFileSync(streams, 'utf8')
      const after = before.replace(`current: "${cliCurrent().slice(1)}"`, `current: "${release.slice(1)}"`)
      assert.notEqual(
        after,
        before,
        `docs/release-streams.yaml no longer carries \`current: "${cliCurrent().slice(1)}"\` under the cli stream, and this case raises it to make ${release} a release`,
      )
      writeFileSync(streams, after)
    },
    'fugaro-changelog-at-current-',
  )

  assert.notEqual(
    built.status,
    0,
    `the build succeeded with ${release} at the CLI’s current version and no summary line: a release file at or below that version is a release, and every release file needs its line in website/data/release-summaries.yaml (F15, D1647)\n${built.output}`,
  )
  assert.match(
    built.output,
    /website\/data\/release-summaries\.yaml/,
    `the failure does not name website/data/release-summaries.yaml, so it does not say which file the release has no line in:\n${built.output}`,
  )
  assert.ok(
    built.output.includes(release),
    `the failure does not name ${release}, so it does not say which release has no summary:\n${built.output}`,
  )
})

test('the build stops for a mobile release with no line in the app’s summaries file', (t) => {
  // The app's stream reaches this page the day the app has a release, and that release's
  // sentence is its `mobile-vX.Y.Z` line in website/data/mobile-release-summaries.yaml (D1644).
  // The file holds no entry today, so the fixture puts the app's first release on the tree and
  // changes nothing else: a line with nothing to say is what the build has to refuse.
  const first = 'v0.0.1'
  const built = buildCopy(
    t,
    ({ root }) => {
      const manifestFile = path.join(root, 'mobile', 'package.json')
      const manifest = JSON.parse(readFileSync(manifestFile, 'utf8'))
      writeFileSync(manifestFile, `${JSON.stringify({ ...manifest, version: first.slice(1) }, null, 2)}\n`)
      writeFileSync(path.join(root, 'docs', 'releases', 'mobile', `${first}.md`), "- the app's first release\n")
      assert.equal(
        mobileSummaryEntries().size,
        0,
        `website/data/mobile-release-summaries.yaml carries a mobile entry, and this case is about the build refusing a release that has none: it describes the file before the app's first release`,
      )
    },
    'fugaro-changelog-mobile-',
  )

  assert.notEqual(
    built.status,
    0,
    `the build succeeded with the app's ${first} release on the tree and no \`mobile-${first}\` line in website/data/mobile-release-summaries.yaml: the page renders the app's line with that release's own sentence, so a release with no line is a line the page cannot write (D1644)`,
  )
  assert.match(
    built.output,
    /mobile-release-summaries\.yaml/,
    `the failure does not name website/data/mobile-release-summaries.yaml:\n${built.output}`,
  )
  assert.ok(
    built.output.includes(`mobile-${first}`),
    `the failure does not name the app's release, \`mobile-${first}\`, so it does not say which line is missing:\n${built.output}`,
  )
})

test('latestMobileRelease names the newest app note at or below the app’s version', () => {
  // The rule the page reads the app's release by, on its own: the newest notes file at or
  // below the version `mobile/package.json` carries, which is `latestConsoleRelease`'s rule
  // (D1644). Nothing in this repository names an app release today, so the fixtures are the
  // versions themselves rather than the tree's.
  const dir = mkdtempSync(path.join(tmpdir(), 'fugaro-mobile-release-'))
  try {
    const notesDir = path.join(dir, 'notes')
    const packageFile = path.join(dir, 'package.json')
    mkdirSync(notesDir)
    // Three notes of the fixture's own, and the file the directory skips by name.
    for (const version of ['0.9.7', '0.10.0', '0.20.3']) writeFileSync(path.join(notesDir, `v${version}.md`), `- the app's ${version} release\n`)
    writeFileSync(path.join(notesDir, NON_RELEASE), '# the app\n')

    /** What the rule names for a package manifest carrying `version`. */
    const named = (version) => {
      writeFileSync(packageFile, `${JSON.stringify({ name: 'fugaro-mobile', version })}\n`)
      return latestMobileRelease({ notesDir, packageFile })
    }

    // The note at the version line is the newest published release, and it is named.
    assert.deepEqual(
      named('0.20.3'),
      { version: '0.20.3', tag: 'mobile-v0.20.3' },
      'a note at the version the package carries is not named, and it is the app\'s latest published release',
    )
    // A note written ahead of the version line is not published: the newest note at or below
    // it is named instead, which is why `v0.20.3.md` is not the answer here.
    assert.deepEqual(
      named('0.19.0'),
      { version: '0.10.0', tag: 'mobile-v0.10.0' },
      'a note above the version the package carries is named, and no release is out for it: the rule names the newest note at or below the version',
    )
    // Every note above it: nothing is published, and the page renders no line.
    assert.equal(named('0.1.0'), null, 'no note is at or below the version the package carries, and a release was named')
    // No note at all, which is the state `docs/releases/mobile/` is in today.
    for (const name of readdirSync(notesDir)) {
      if (name !== NON_RELEASE) rmSync(path.join(notesDir, name))
    }
    assert.equal(named('0.20.3'), null, 'the app has no release note, and one was named')
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})

test('latestMobileRelease names nothing when the app’s notes directory is not there', () => {
  // A stream whose notes are not on the tree has no release, and the page renders no line:
  // the module answers null rather than throwing, so a site built before the app's notes
  // directory exists is a site with no app line rather than a failed build.
  const dir = mkdtempSync(path.join(tmpdir(), 'fugaro-mobile-absent-'))
  try {
    const packageFile = path.join(dir, 'package.json')
    writeFileSync(packageFile, `${JSON.stringify({ name: 'fugaro-mobile', version: '0.0.0' })}\n`)
    assert.equal(
      latestMobileRelease({ notesDir: path.join(dir, 'notes'), packageFile }),
      null,
      'the notes directory is not there, and a release was named',
    )
  } finally {
    rmSync(dir, { recursive: true, force: true })
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

  // One card per rendered release, drawn on `base.css`'s own shape: the panel a page is drawn
  // on, one step above the ground with a hairline and the larger radius. The page used to be a
  // list of entries separated by a hairline; the bold look makes each release a thing of its
  // own, and a class of this page's own would be a second card shape for the same site.
  const articles = [...html.matchAll(/<article\b([^>]*)>/g)]
  assert.equal(
    articles.length,
    SHOWN,
    `the page renders ${articles.length} entries, and it renders the ${SHOWN} newest release files — one card each (D1640)`,
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

// The notes' reveal (the owner's request of 2026-10-24): an entrance on `[open]` in every
// browser, gated on the reader's no-preference, and nothing hidden outside the animation.
test('the release notes reveal on open, only where motion is welcome, and are never hidden at rest', () => {
  const html = readFileSync(path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'dist', 'changelog', 'index.html'), 'utf8')
  assert.match(html, /<details class="release-notes"[^>]*>\s*<summary[^>]*>Highlights<\/summary>\s*<div class="release-notes-body"/, 'the notes carry no body wrapper for the reveal to run on')
  const css = [...html.matchAll(/<style\b[^>]*>([\s\S]*?)<\/style>/g)].map((m) => m[1]).join('\n')
  const reveal = cssRules(css).filter((rule) => /release-notes-body/.test(rule.selector || '') && cssDeclarations(rule.body).some(([p]) => p === 'animation' || p === 'animation-name'))
  assert.ok(reveal.length > 0, 'the notes carry no reveal')
  for (const rule of reveal) {
    assert.match(rule.selector, /\[open\]/, `${rule.selector} animates the notes without the reader opening them`)
    assert.ok(rule.conditions.some((c) => /prefers-reduced-motion\s*:\s*no-preference/.test(c)), `${rule.selector} animates for a reader who asked for less motion`)
  }
  const hiding = cssRules(css).filter((rule) => /release-notes-body/.test(rule.selector || '') && cssDeclarations(rule.body).some(([p, v]) => (p === 'opacity' && v.trim() === '0') || p === 'visibility'))
  assert.deepEqual(hiding.map((rule) => rule.selector), [], 'a rule hides the notes outside the animation, so a browser that runs none never shows them')
})
