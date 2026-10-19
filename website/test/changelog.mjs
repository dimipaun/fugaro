// website/test/changelog.mjs — the build-time check for the changelog generator (W0.6).
//
// The source of truth of every assertion here is `docs/releases/` — the directory of
// Highlights files, read directly by this file — never the generator's own output and
// never a git tag or a commit date (D100): a release file is committed to `main` before
// its tag exists, and a shallow checkout has no tags to read at all.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { buildChangelog } from '../src/lib/changelog.mjs'

const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..')
const RELEASES_DIR = path.join(REPO_ROOT, 'docs', 'releases')

// `docs/releases/README.md` explains the format; it is skipped by name, not by pattern.
const NON_RELEASE = 'README.md'

// The headings `docs/releases/README.md` names as the sections a release file may carry
// after its Highlights, in full: a `###` heading, its words a title and its block of lines
// the section's own. There are exactly two, both optional, and the file may hold them in
// either order — the entry carries the file's order, not this list's.
//
// They are `###` and not `##` on purpose: GoReleaser puts the file under its own
// `## Highlights` in the release body, so a `##` inside the file would be a second
// top-level heading beside it. The generator recognises these two lines and no other
// heading: anything else the file writes is its own text, and it is rendered as written.
const SECTION_TITLES = ['For operators', 'Known limits']

/** The file line that opens a section, from its title: `For operators` -> `### For operators`. */
function headingLine(title) {
  return `### ${title}`
}

/**
 * A file's lines, split the way `docs/releases/README.md` describes: the Highlights bullets
 * first, then each section heading with the lines under it, in the order the file writes
 * them. Every assertion about the generator's split is made against this and never against
 * the generator's own rule, so a change in the format fails here rather than being followed
 * silently.
 */
function splitSections(lines) {
  const highlights = []
  const sections = []
  for (const line of lines) {
    const title = SECTION_TITLES.find((candidate) => line === headingLine(candidate))
    if (title !== undefined) {
      sections.push({ title, lines: [] })
      continue
    }
    const section = sections[sections.length - 1]
    if (section === undefined) highlights.push(line)
    else section.lines.push(line)
  }
  return { highlights, sections }
}

// The release files' names, read here rather than asked of the generator: the point of
// these cases is to compare the generator's answer against the directory itself.
//
// The enumeration is deliberately the *loosest* one that is still correct — every `*.md`
// in the directory except `README.md`, which is skipped by name — and not the generator's
// `vX.Y.Z.md` pattern: a file the generator does not recognise has to fail this case
// loudly, which it cannot do if the expectation is filtered by the same rule the generator
// filters with (D100 and F44 call this coverage check the whole of the drift protection).
function releaseFiles() {
  return readdirSync(RELEASES_DIR).filter((name) => name.endsWith('.md') && name !== NON_RELEASE)
}

// The tag a file's entry must carry, taken from the filename alone.
function tagOf(fileName) {
  return fileName.slice(0, -'.md'.length)
}

function versionOf(fileName) {
  const m = /^v(\d+)\.(\d+)\.(\d+)\.md$/.exec(fileName)
  return [Number(m[1]), Number(m[2]), Number(m[3])]
}

function newestFirst(a, b) {
  const va = versionOf(a)
  const vb = versionOf(b)
  for (let i = 0; i < 3; i++) {
    if (va[i] !== vb[i]) return vb[i] - va[i]
  }
  return 0
}

// The lines a reader is meant to see: the file's own lines, with the authoring notes an
// HTML comment wraps (v0.7.5 carries one) left out.
function contentLines(fileName) {
  const text = readFileSync(path.join(RELEASES_DIR, fileName), 'utf8').replace(/<!--[\s\S]*?-->/g, '')
  return text.split('\n').map((line) => line.trim()).filter((line) => line.length > 0)
}

test('changelog builds from docs/releases', () => {
  const entries = buildChangelog()

  assert.ok(Array.isArray(entries), 'the generator returns an array')
  assert.ok(entries.length > 0, 'the generator returns at least one entry')

  for (const entry of entries) {
    // The shape is the whole of the contract W1.9's page renders, and a `date` field is
    // deliberately absent: the files carry no date, the build is network-free, and the
    // newest file has no tag yet (D100).
    assert.deepEqual(
      Object.keys(entry).sort(),
      ['highlights', 'released', 'sections', 'tag', 'version'],
      `${entry.tag}: the entry carries exactly the five designed fields`,
    )
    assert.equal('date' in entry, false, `${entry.tag}: no date field`)
    assert.match(entry.tag, /^v\d+\.\d+\.\d+$/, `${entry.tag}: the tag is vX.Y.Z`)
    assert.equal(entry.tag.slice(1), entry.version, `${entry.tag}: version is the tag without the v`)
    assert.equal(typeof entry.released, 'boolean', `${entry.tag}: released is a boolean`)
    assert.ok(Array.isArray(entry.highlights) && entry.highlights.length > 0, `${entry.tag}: highlights is a non-empty array`)
    assert.ok(Array.isArray(entry.sections), `${entry.tag}: sections is an array`)
    for (const section of entry.sections) {
      // Both headings are the same kind of thing, so they are the same shape: a title the
      // file writes and the lines under it. The title is one of the two the format names —
      // never a heading the generator invented, and never a third section.
      assert.deepEqual(Object.keys(section).sort(), ['lines', 'title'], `${entry.tag}: a section carries a title and its lines`)
      assert.ok(SECTION_TITLES.includes(section.title), `${entry.tag}: ${section.title} is not a section docs/releases/README.md names`)
      assert.ok(Array.isArray(section.lines), `${entry.tag}: a section's lines are an array`)
    }
    for (const line of [...entry.highlights, ...entry.sections.flatMap((section) => section.lines)]) {
      assert.equal(typeof line, 'string', `${entry.tag}: every line is a string`)
      assert.ok(line.trim().length > 0, `${entry.tag}: no blank line is rendered`)
    }
  }

  // Newest first, by the version parsed from the filename.
  const expected = entries.map((entry) => entry.tag).slice().sort((a, b) => newestFirst(`${a}.md`, `${b}.md`))
  assert.deepEqual(
    entries.map((entry) => entry.tag),
    expected,
    'entries are ordered newest first by version',
  )
})

test('changelog covers every release file', () => {
  const files = releaseFiles()
  const entries = buildChangelog()

  // `docs/releases/README.md` is the one file that is not a release; the count below is
  // every other `*.md` in the directory, so a release file the generator does not
  // recognise shrinks the answer and fails here.
  assert.ok(existsSync(path.join(RELEASES_DIR, NON_RELEASE)), 'docs/releases/README.md is the file skipped by name')
  assert.equal(
    entries.length,
    files.length,
    `changelog lists ${entries.length} releases, docs/releases/ holds ${files.length}`,
  )
  assert.deepEqual(
    new Set(entries.map((entry) => entry.tag)),
    new Set(files.map(tagOf)),
    'every *.md in docs/releases/ other than README.md is an entry, and no entry is outside the directory',
  )
})

test('changelog lists no release that is not a file', () => {
  for (const entry of buildChangelog()) {
    const fileName = `${entry.tag}.md`
    assert.ok(
      existsSync(path.join(RELEASES_DIR, fileName)),
      `${entry.tag}: docs/releases/${fileName} is the file the entry names`,
    )
    assert.equal(entry.version, entry.tag.slice(1), `${entry.tag}: the version comes from the filename`)
  }
})

test('changelog adds no sentence of its own', () => {
  for (const entry of buildChangelog()) {
    const fileName = `${entry.tag}.md`
    const raw = readFileSync(path.join(RELEASES_DIR, fileName), 'utf8')
    const written = [...entry.highlights, ...entry.sections.flatMap((section) => section.lines)]

    // Every rendered line is the file's own text, verbatim (the same no-invention rule
    // W0.5's page holds to).
    for (const line of written) {
      assert.ok(raw.includes(line), `${entry.tag}: the file does not carry this line: ${line}`)
    }

    // And nothing the file says is dropped, so a change in the files' shape fails here
    // rather than silently shrinking the page: each section's lines are the file's, and
    // its title is the file's own heading word for word.
    const fromFile = splitSections(contentLines(fileName))
    assert.deepEqual(entry.highlights, fromFile.highlights, `${entry.tag}: every highlight line is rendered`)
    assert.deepEqual(entry.sections, fromFile.sections, `${entry.tag}: every section, and every line in it, is rendered`)

    // The section titles are read off the file rather than typed here: a title the file
    // does not carry would be a heading the generator invented.
    for (const section of entry.sections) {
      assert.ok(
        raw.split('\n').some((line) => line.trim() === headingLine(section.title)),
        `${entry.tag}: the file carries no ${headingLine(section.title)} heading for the section ${section.title}`,
      )
    }
  }
})

test('the generator splits a file at both section headings, in the file’s own order', () => {
  // The corpus on `main` gives the generator one file with a `### Known limits` section
  // (v0.8.5) and twelve with none, which is enough to catch a heading that leaks into the
  // page and not enough to hold the split itself. These files do: every shape the format
  // allows — both sections, each one alone, neither — and one that writes the two in the
  // reverse order, which is what makes "the entry carries the file's order" an assertion
  // rather than a property of the one file that has both.
  const dir = mkdtempSync(path.join(tmpdir(), 'fugaro-changelog-releases-'))
  try {
    const files = {
      'v1.2.0.md': ['- a release with no section at all', '- and its second bullet'].join('\n'),
      'v1.2.1.md': [
        '- a release with the operators block only',
        '',
        headingLine('For operators'),
        '',
        '1. upgrade the CLI',
        '2. rebuild the images',
      ].join('\n'),
      'v1.2.2.md': [
        '- a release with the known limits only',
        '',
        headingLine('Known limits'),
        '',
        '- a limit with `code` in it',
        '- a second limit',
      ].join('\n'),
      'v1.2.3.md': [
        '- a release with both sections',
        '- and a second Highlights bullet',
        '',
        headingLine('For operators'),
        '',
        '1. upgrade the CLI',
        '',
        headingLine('Known limits'),
        '',
        '- a limit with `code` in it',
        '- a second limit',
      ].join('\n'),
      'v1.2.4.md': [
        '- a release whose sections are the other way round',
        '',
        headingLine('Known limits'),
        '',
        '- a limit first',
        '',
        headingLine('For operators'),
        '',
        '1. upgrade the CLI',
      ].join('\n'),
    }
    for (const [fileName, text] of Object.entries(files)) writeFileSync(path.join(dir, fileName), `${text}\n`)

    const entries = buildChangelog({ releasesDir: dir, cwd: dir })

    // Newest first, so the fixture's version tells the case which file it is looking at.
    assert.deepEqual(
      entries.map((entry) => entry.tag),
      ['v1.2.4', 'v1.2.3', 'v1.2.2', 'v1.2.1', 'v1.2.0'],
      'the entries are the fixture files, newest first',
    )

    // The fixture's own lines, read the way `contentLines` reads the real files: trimmed,
    // blank lines dropped. The file the entry names is the fixture's, never the corpus on
    // `main` — the tags here are versions no release file carries on purpose.
    const linesOf = (text) => text.split('\n').map((line) => line.trim()).filter((line) => line.length > 0)
    for (const entry of entries) {
      const fromFile = splitSections(linesOf(files[`${entry.tag}.md`]))
      assert.deepEqual(entry.highlights, fromFile.highlights, `${entry.tag}: the Highlights are the lines before the first section heading`)
      assert.deepEqual(entry.sections, fromFile.sections, `${entry.tag}: the sections are the file's own, in the file's own order`)
    }

    // The two named shapes, said out loud, so a generator that recognised only one heading
    // — or only the order the one file on `main` writes them in — fails on a line that
    // names what it got wrong.
    const both = entries.find((entry) => entry.tag === 'v1.2.3')
    assert.deepEqual(
      both.sections.map((section) => section.title),
      ['For operators', 'Known limits'],
      'a file with both sections is split at both',
    )
    const reversed = entries.find((entry) => entry.tag === 'v1.2.4')
    assert.deepEqual(
      reversed.sections.map((section) => section.title),
      ['Known limits', 'For operators'],
      'a file that writes the two sections in the other order keeps the file’s order',
    )
    assert.deepEqual(
      entries.find((entry) => entry.tag === 'v1.2.0').sections,
      [],
      'a file with no section heading has no sections',
    )
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})

test('README carries no changelog section', () => {
  const readme = readFileSync(path.join(REPO_ROOT, 'README.md'), 'utf8')

  // F44: the site's page is the first reader-facing index of docs/releases/, and it stays
  // that way only while the README keeps no changelog section of its own.
  for (const line of readme.split('\n')) {
    if (/^#{1,6}\s/.test(line)) {
      assert.doesNotMatch(line, /changelog/i, `README gained a changelog heading: ${line}`)
    }
  }
  // The README points at the Releases page instead, which is what a reader sees today.
  assert.match(readme, /github\.com\/dimipaun\/fugaro\/releases/, 'README links the Releases page')
})
