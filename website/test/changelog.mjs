// website/test/changelog.mjs — the build-time check for the changelog generator (W0.6).
//
// The source of truth of every assertion here is `docs/releases/` — the directory of
// Highlights files, read directly by this file — never the generator's own output and
// never a git tag or a commit date (D100): a release file is committed to `main` before
// its tag exists, and a shallow checkout has no tags to read at all.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { buildChangelog } from '../src/lib/changelog.mjs'

const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..')
const RELEASES_DIR = path.join(REPO_ROOT, 'docs', 'releases')

// `docs/releases/README.md` explains the format; it is skipped by name, not by pattern.
const NON_RELEASE = 'README.md'

const OPERATORS_HEADING = '### For operators'

// The release files' names, read here rather than asked of the generator: the point of
// these cases is to compare the generator's answer against the directory itself.
function releaseFiles() {
  return readdirSync(RELEASES_DIR)
    .filter((name) => name !== NON_RELEASE)
    .filter((name) => /^v\d+\.\d+\.\d+\.md$/.test(name))
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
      ['for_operators', 'highlights', 'released', 'tag', 'version'],
      `${entry.tag}: the entry carries exactly the five designed fields`,
    )
    assert.equal('date' in entry, false, `${entry.tag}: no date field`)
    assert.match(entry.tag, /^v\d+\.\d+\.\d+$/, `${entry.tag}: the tag is vX.Y.Z`)
    assert.equal(entry.tag.slice(1), entry.version, `${entry.tag}: version is the tag without the v`)
    assert.equal(typeof entry.released, 'boolean', `${entry.tag}: released is a boolean`)
    assert.ok(Array.isArray(entry.highlights) && entry.highlights.length > 0, `${entry.tag}: highlights is a non-empty array`)
    assert.ok(Array.isArray(entry.for_operators), `${entry.tag}: for_operators is an array`)
    for (const line of [...entry.highlights, ...entry.for_operators]) {
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

  assert.equal(
    entries.length,
    files.length,
    `changelog lists ${entries.length} releases, docs/releases/ holds ${files.length}`,
  )
  assert.deepEqual(
    new Set(entries.map((entry) => entry.tag)),
    new Set(files.map((fileName) => fileName.slice(0, -'.md'.length))),
    'every release file is an entry, and no entry is outside the directory',
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
    const written = [...entry.highlights, ...entry.for_operators]

    // Every rendered line is the file's own text, verbatim (the same no-invention rule
    // W0.5's page holds to).
    for (const line of written) {
      assert.ok(raw.includes(line), `${entry.tag}: the file does not carry this line: ${line}`)
    }

    // And nothing the file says is dropped, so a change in the files' shape fails here
    // rather than silently shrinking the page.
    const fromFile = contentLines(fileName)
    const heading = fromFile.findIndex((line) => line === OPERATORS_HEADING)
    const highlights = heading === -1 ? fromFile : fromFile.slice(0, heading)
    const forOperators = heading === -1 ? [] : fromFile.slice(heading + 1)
    assert.deepEqual(entry.highlights, highlights, `${entry.tag}: every highlight line is rendered`)
    assert.deepEqual(entry.for_operators, forOperators, `${entry.tag}: every operator line is rendered`)
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
