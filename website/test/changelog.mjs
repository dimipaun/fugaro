// website/test/changelog.mjs — the build-time check for the changelog generator (W0.6).
//
// The source of truth of every assertion here is `docs/releases/` — the directory of
// Highlights files, read directly by this file — never the generator's own output and
// never a git tag or a commit date (D100): a release file is committed to `main` before
// its tag exists, and a shallow checkout has no tags to read at all. Which releases count
// as published is the plugin's own version (`plugin/.claude-plugin/plugin.json`), a file in
// this repository rather than a tag of the checkout the build runs in.
//
// Two of the files' version lines are read here, both from this repository and both compared
// against the directory rather than against the generator's own answer: the CLI stream's
// `current` version (`docs/release-streams.yaml`) decides which files are releases at all — a
// file above it is the next release's notes, written ahead of their release, and is left out —
// and the plugin's version decides which of those releases are published.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { buildChangelog } from '../src/lib/changelog.mjs'

const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..')
const RELEASES_DIR = path.join(REPO_ROOT, 'docs', 'releases')

// The plugin's manifest, whose version decides which releases are published (the owner's
// brief of 2026-10-19). Read here rather than asked of the generator: the point of the cases
// below is to compare the generator's answer against the file the rule names.
const PLUGIN_FILE = path.join(REPO_ROOT, 'plugin', '.claude-plugin', 'plugin.json')

// The release streams catalogue, whose CLI stream's `current` version decides which release
// files are releases at all: a file for a version above it is the next release's notes, written
// ahead of their release (docs/releases/README.md asks for them in the pull request that makes
// the change), and the generator leaves it out.
const STREAMS_FILE = path.join(REPO_ROOT, 'docs', 'release-streams.yaml')

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

/**
 * The CLI stream's current release, as the tag of the file it names, read off
 * `docs/release-streams.yaml` by this file — the file the rule names rather than the
 * generator's reader of it (the same reason the release files are read here rather than asked
 * of the generator).
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

/** True when the file's version is at or below the version of `other`: a release, not a draft. */
function atOrBelow(fileName, other) {
  const file = versionOf(fileName)
  const limit = versionOf(other)
  for (let i = 0; i < 3; i++) {
    if (file[i] !== limit[i]) return file[i] < limit[i]
  }
  return true
}

/** The files that are releases: the ones at or below the CLI stream's current version. */
function releasedFiles() {
  return releaseFiles().filter((name) => atOrBelow(name, `${cliCurrent()}.md`))
}

/**
 * A release streams catalogue for a fixture, in the shape `docs/release-streams.yaml` carries,
 * cut down to the one stream the changelog reads: the CLI's current version. A fixture names
 * its own `current` so its cases depend on no file outside its directory — and the version
 * below is above every fixture file's, unless the case is about the boundary itself.
 */
function writeCatalogue(dir, current) {
  const file = path.join(dir, 'release-streams.yaml')
  writeFileSync(
    file,
    [
      '# a release streams catalogue, cut down to the CLI’s own version line',
      'streams:',
      '  - name: cli',
      '    theme: animals',
      '    names: [Badger, Camel]',
      `    current: "${current}"`,
      '    next: Dolphin',
      '    tag_prefix: v',
      '',
    ].join('\n'),
  )
  return file
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
    // deliberately absent: the files carry no date and the build is network-free, so the
    // date a reader wants is the one the GitHub release states (D100).
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
  const files = releasedFiles()
  const entries = buildChangelog()

  // `docs/releases/README.md` is the one file that is not a release; the count below is
  // every other `*.md` in the directory at or below the CLI stream's current version, so a
  // release file the generator does not recognise shrinks the answer and fails here — and a
  // file above that version (the next release's notes, written ahead of its release) is
  // deliberately not an entry, which the case after this one holds.
  assert.ok(existsSync(path.join(RELEASES_DIR, NON_RELEASE)), 'docs/releases/README.md is the file skipped by name')
  assert.equal(
    entries.length,
    files.length,
    `changelog lists ${entries.length} releases, docs/releases/ holds ${files.length} release files at or below ${cliCurrent()}`,
  )
  assert.deepEqual(
    new Set(entries.map((entry) => entry.tag)),
    new Set(files.map(tagOf)),
    'every *.md in docs/releases/ other than README.md and the drafts above the CLI’s current version is an entry, and no entry is outside the directory',
  )
})

test('a release file for a version above the CLI’s current one is left out, and needs no summary line', () => {
  // The release process writes the next release's highlights into the pull request that makes
  // the change (docs/releases/README.md), so `docs/releases/` holds files for versions the CLI
  // stream has not reached: v0.22.0 sat there while `current` was 0.21.0. Such a file is a
  // draft of the next release, not a release, so the changelog leaves it out — it is not
  // rendered, and it is not held to the summary line every release file needs. The fixture is
  // the shape of that tree: three released files, one draft above them with no summary at all,
  // and a catalogue naming the version the stream is at.
  const dir = mkdtempSync(path.join(tmpdir(), 'fugaro-changelog-draft-'))
  const releasesDir = path.join(dir, 'releases')
  try {
    mkdirSync(releasesDir)
    writeFileSync(path.join(releasesDir, 'v1.2.0.md'), '- a released file with one bullet\n')
    writeFileSync(path.join(releasesDir, 'v1.2.1.md'), '- a released file with one bullet\n')
    writeFileSync(path.join(releasesDir, 'v1.2.2.md'), '- a released file with one bullet\n')
    // The draft: a Highlights file with no summary line anywhere, which is the file the site's
    // page stops the build for when it is a release.
    writeFileSync(path.join(releasesDir, 'v1.3.0.md'), '- the next release’s highlights, written ahead of it\n')

    // The catalogue, written in the shape docs/release-streams.yaml carries.
    const streamsFile = path.join(dir, 'release-streams.yaml')
    writeFileSync(
      streamsFile,
      [
        '# the release streams catalogue, cut down to the CLI’s own version line',
        'streams:',
        '  - name: cli',
        '    theme: animals',
        '    names: [Badger, Camel]',
        '    current: "1.2.2"',
        '    next: Dolphin',
        '    tag_prefix: v',
        '',
        '  - name: console',
        '    theme: fruits',
        '    names: [Cherry, Mango]',
        '    current: "0.110.0"',
        '    next: Melon',
        '    tag_prefix: console-v',
        '',
      ].join('\n'),
    )

    const pluginFile = path.join(dir, 'plugin.json')
    const withVersion = (version) => {
      writeFileSync(pluginFile, `${JSON.stringify({ name: 'fugaro', version })}\n`)
      return buildChangelog({ releasesDir, pluginFile, streamsFile }).map((entry) => entry.tag)
    }

    assert.deepEqual(
      withVersion('1.2.2'),
      ['v1.2.2', 'v1.2.1', 'v1.2.0'],
      'the file above the CLI’s current version is in the changelog: the release files are the ones at or below it, and a draft of the next release is neither an entry nor a file the summary check has to cover',
    )
    // The version the plugin carries does not make a draft a release: v1.3.0 is the release the
    // stream has not reached, and a manifest from a later minor than it is not a release file.
    assert.deepEqual(
      withVersion('9.9.9'),
      ['v1.2.2', 'v1.2.1', 'v1.2.0'],
      'a plugin from a later minor put the draft in the changelog, and the CLI stream’s current version is what says which files are releases',
    )

    // And the boundary is "at or below": the version the stream is at is a release, not a draft.
    writeFileSync(
      streamsFile,
      readFileSync(streamsFile, 'utf8').replace('current: "1.2.2"', 'current: "1.3.0"'),
    )
    assert.deepEqual(
      withVersion('9.9.9'),
      ['v1.3.0', 'v1.2.2', 'v1.2.1', 'v1.2.0'],
      'the file at the version the CLI stream is at is not an entry, and it is the release the stream has reached',
    )

    // A catalogue with no CLI stream, and one whose current version is not x.y.z, stop the
    // build rather than reading as "every release file is a draft".
    const withoutCurrent = path.join(dir, 'no-cli.yaml')
    writeFileSync(withoutCurrent, 'streams:\n  - name: console\n    current: "0.110.0"\n')
    assert.throws(
      () => buildChangelog({ releasesDir, pluginFile, streamsFile: withoutCurrent }),
      new RegExp(`^Error: ${withoutCurrent.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}: no \`cli\` stream`),
      'a catalogue with no CLI stream left every release file out instead of stopping the build',
    )
    const badCurrent = path.join(dir, 'bad-current.yaml')
    writeFileSync(badCurrent, 'streams:\n  - name: cli\n    current: 1.3\n')
    assert.throws(
      () => buildChangelog({ releasesDir, pluginFile, streamsFile: badCurrent }),
      /version is 1\.3/,
      'a current version that is not x.y.z was compared with the release files rather than refused',
    )
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
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
  const releasesDir = path.join(dir, 'releases')
  try {
    mkdirSync(releasesDir)
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
    for (const [fileName, text] of Object.entries(files)) writeFileSync(path.join(releasesDir, fileName), `${text}\n`)
    // The published version the fixture's entries are judged against: one of its own, so the
    // shape cases below depend on no file outside this directory. It is written beside the
    // release directory rather than inside it, because everything in the release directory
    // but README.md has to be a release file.
    const pluginFile = path.join(dir, 'plugin.json')
    writeFileSync(pluginFile, `${JSON.stringify({ name: 'fugaro', version: '1.2.4' })}\n`)
    // The catalogue too, at a version above the fixture's files: the case is about the split,
    // and a fixture whose files read as drafts of the next release would have no entries.
    const streamsFile = writeCatalogue(dir, '9.9.9')

    const entries = buildChangelog({ releasesDir, pluginFile, streamsFile })

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

test('a release is published when its version is at or below the plugin’s, whatever the checkout carries', () => {
  // The owner's brief of 2026-10-19: the deploy build checks out without tags, so a rule read
  // from `git tag --list` printed "not yet released" for every release on the deployed site.
  // The rule is now the plugin's own version against the release's, and the fixture below is
  // held to it in a directory that is not a git repository at all: a tag cannot be the reason
  // any of these answers is what it is.
  const dir = mkdtempSync(path.join(tmpdir(), 'fugaro-changelog-published-'))
  const releasesDir = path.join(dir, 'releases')
  try {
    mkdirSync(releasesDir)
    for (const fileName of ['v1.2.0.md', 'v1.2.1.md', 'v1.2.2.md']) {
      writeFileSync(path.join(releasesDir, fileName), '- a release file with one bullet\n')
    }
    const pluginFile = path.join(dir, 'plugin.json')
    // The catalogue's own current version, above the fixture's files: this case is about which
    // of them the plugin's version has published, and none of them is a draft.
    const streamsFile = writeCatalogue(dir, '9.9.9')
    const published = (version) => {
      writeFileSync(pluginFile, `${JSON.stringify({ name: 'fugaro', version }, null, 2)}\n`)
      return Object.fromEntries(buildChangelog({ releasesDir, pluginFile, streamsFile }).map((entry) => [entry.tag, entry.released]))
    }

    assert.ok(!existsSync(path.join(dir, '.git')), 'the fixture directory must not be a git repository, or a tag could be the reason for an answer below')

    assert.deepEqual(
      published('1.2.1'),
      { 'v1.2.2': false, 'v1.2.1': true, 'v1.2.0': true },
      'a release file above the plugin’s version is not yet released, and one at or below it is',
    )
    assert.deepEqual(
      published('1.2.2'),
      { 'v1.2.2': true, 'v1.2.1': true, 'v1.2.0': true },
      'the version the plugin carries is published: the bump lands before the tag, so an equal version is a released one',
    )
    assert.deepEqual(
      published('9.9.9'),
      { 'v1.2.2': true, 'v1.2.1': true, 'v1.2.0': true },
      'a plugin from a later minor releases everything below it',
    )
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})

test('the plugin’s own version names a release file, and that release is published', () => {
  const { version } = JSON.parse(readFileSync(PLUGIN_FILE, 'utf8'))
  const entry = buildChangelog().find((candidate) => candidate.version === version)

  // The version in the manifest is the newest released one, and the changelog's whole reason
  // for reading it is to say which entries those are: a manifest naming a release with no
  // file, or a file whose release reads as unpublished, is the drift this case refuses.
  assert.ok(
    entry !== undefined,
    `plugin/.claude-plugin/plugin.json names ${version} and docs/releases/ has no v${version}.md: the manifest's version is the newest released one, and the file it names is the changelog's own entry for it`,
  )
  assert.equal(entry.released, true, `docs/releases/v${version}.md reads as not yet released: the plugin's version is ${version}, and the release it names is published`)
})

test('a plugin manifest the changelog cannot read stops the build, naming the file', () => {
  const dir = mkdtempSync(path.join(tmpdir(), 'fugaro-changelog-plugin-'))
  const releasesDir = path.join(dir, 'releases')
  try {
    mkdirSync(releasesDir)
    writeFileSync(path.join(releasesDir, 'v1.2.3.md'), '- a release file with one bullet\n')
    const pluginFile = path.join(dir, 'plugin.json')
    const streamsFile = writeCatalogue(dir, '9.9.9')

    writeFileSync(pluginFile, '{ not json\n')
    assert.throws(
      () => buildChangelog({ releasesDir, pluginFile, streamsFile }),
      new RegExp(`^Error: ${pluginFile.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')} cannot be read as JSON`),
      'a manifest that is not JSON is not a changelog with nothing released',
    )

    writeFileSync(pluginFile, `${JSON.stringify({ name: 'fugaro', version: '0.9' })}\n`)
    assert.throws(
      () => buildChangelog({ releasesDir, pluginFile, streamsFile }),
      /carries the version "0\.9"/,
      'a version that is not x.y.z is refused rather than compared',
    )
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})

test('the generator steps over the mobile app’s notes directory, and nothing else', () => {
  // `docs/releases/mobile/` is the app's own stream's notes (the app releases on
  // `mobile-vX.Y.Z`, docs/release.md's "Mobile releases", and its highlights live in that
  // directory). It is a directory rather than a release file, and the generator skips it
  // by name: the rule below still refuses every other name this changelog does not
  // recognise, one level down as well as here, so this case holds both halves — the
  // directory an app note lives in, and the throw a stray file still gets.
  const dir = mkdtempSync(path.join(tmpdir(), 'fugaro-changelog-mobile-'))
  const releasesDir = path.join(dir, 'releases')
  try {
    mkdirSync(releasesDir)
    mkdirSync(path.join(releasesDir, 'mobile'))
    writeFileSync(path.join(releasesDir, 'v1.2.0.md'), '- a release file with one bullet\n')
    writeFileSync(
      path.join(releasesDir, 'mobile', 'v0.200.0.md'),
      '- the app’s first release, Falcon\n',
    )
    writeFileSync(path.join(releasesDir, 'mobile', 'README.md'), 'the app’s notes format\n')
    const pluginFile = path.join(dir, 'plugin.json')
    writeFileSync(pluginFile, `${JSON.stringify({ name: 'fugaro', version: '1.2.0' })}\n`)
    const streamsFile = writeCatalogue(dir, '9.9.9')

    const entries = buildChangelog({ releasesDir, pluginFile, streamsFile })
    assert.deepEqual(
      entries.map((entry) => entry.tag),
      ['v1.2.0'],
      'an app note under docs/releases/mobile/ is not an entry of this changelog, and the CLI’s own file beside it still is',
    )

    // A stray *file* is still refused, which is what makes skipping the directory by name a
    // rule rather than a hole: the app's notes are in their own directory, and a new file
    // at the top level is a mistake this build stops on, naming it.
    writeFileSync(path.join(releasesDir, 'draft.md'), '- a stray note\n')
    assert.throws(
      () => buildChangelog({ releasesDir, pluginFile, streamsFile }),
      /docs\/releases\/draft\.md is not a release file/,
      'a stray file in docs/releases/ is still refused by name, so the skipped directory did not widen into a general “ignore what does not parse”',
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
