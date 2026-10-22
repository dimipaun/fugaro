// website/src/lib/changelog.mjs — the changelog, built from docs/releases/ at site build time.
//
// Source of truth: docs/releases/vX.Y.Z.md, the hand-written Highlights of release vX.Y.Z
// (docs/releases/README.md). This module reads those files and returns them; the site's
// changelog page (W1.9) renders the result during `npm run build`.
//
// Three things are deliberate:
//
//  * **Nothing is generated into the tree.** The output is never written to disk, so there
//    is no committed copy to go stale when a Highlights file lands on `main` before
//    `scripts/release.sh` runs — which is the order the process requires.
//  * **No date.** The files carry no title and no date, the site's build is network-free,
//    and the newest file is published last, so a date taken from a release's own history does
//    not exist for the entry a reader sees first. The page prints the tag and links the entry
//    to its GitHub release, where the date is GitHub's.
//  * **Standard library only** (node:fs, node:path), so it runs under `node --test` before
//    the site's toolchain exists, and no npm dependency is added.
//
// The one field that reads a file outside `docs/releases/` is `released`, and it reads the
// **plugin's own version** (`plugin/.claude-plugin/plugin.json`): an entry is released when
// its version is at or below that one. The release process merges the plugin's version bump to
// `main` (`scripts/release.sh`'s "release: vX.Y.Z" PR) and only then pushes the tag, so on
// `main` the version equals the latest published release — and a release file written ahead of
// its bump is correctly not yet released. The number is a file in this repository rather than a
// tag, which is deliberate: the deploy build checks out without tags, and a changelog that read
// `git tag --list` printed "not yet released" for every release on the deployed site.

import { readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

// website/src/lib/changelog.mjs -> website/src/lib -> website/src -> website -> the root.
const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..', '..')

export const RELEASES_DIR = path.join(REPO_ROOT, 'docs', 'releases')

// The CLI plugin's own manifest: the one fact that says which release is the newest one
// published. `scripts/release.sh` bumps it in the commit that cuts a release, which lands on
// `main` before the tag exists — so the version here IS the latest released version, and a
// release file for a higher version is one written ahead of its release.
export const PLUGIN_FILE = path.join(REPO_ROOT, 'plugin', '.claude-plugin', 'plugin.json')

// `docs/releases/README.md` describes the format; it is the one file in the directory that
// is not a release, and it is skipped by name rather than by the pattern below. Every other
// file must be a release file: one that is not named `vX.Y.Z.md` is a mistake in the tree
// (a release notes draft, a stray note), and this module throws rather than dropping it
// silently — a changelog that leaves a release out without saying so is the failure D100
// and F44 exist to prevent.
const NON_RELEASE = 'README.md'

// The `###` headings a release file may carry inside its body, in full: the operator steps,
// and the release's known limits (`docs/releases/README.md`). Both are optional, either may
// appear without the other, and the file may write them in either order.
//
// They are `###` and not `##` because GoReleaser puts the file into the release body under
// its own `## Highlights` (`.goreleaser.yaml`, `release.header`): a `##` inside the file
// would be a second top-level heading beside it. So the level is the format's, and these
// two lines are the whole of what this module recognises as a heading: every other line the
// file writes is its own text and is rendered as written, heading or not.
const SECTION_HEADINGS = ['### For operators', '### Known limits']

// The title of a section, from the heading line the file writes: the words a reader reads,
// with the `###` that made the line a heading left behind (it is markup, like a bullet's
// marker, and the page renders the section as a heading element of its own).
const HEADING_MARKER = /^#{1,6}\s+/

const RELEASE_FILE = /^v(\d+)\.(\d+)\.(\d+)\.md$/

// The version from the filename, as a comparable triple, or null for a name that is not a
// release file's.
function versionOf(fileName) {
  const m = RELEASE_FILE.exec(fileName)
  if (!m) return null
  return {
    tag: fileName.slice(0, -'.md'.length),
    version: `${m[1]}.${m[2]}.${m[3]}`,
    parts: [Number(m[1]), Number(m[2]), Number(m[3])],
  }
}

function newestFirst(a, b) {
  for (let i = 0; i < 3; i++) {
    if (a.parts[i] !== b.parts[i]) return b.parts[i] - a.parts[i]
  }
  return 0
}

/** The version triple of `x.y.z`, or null for anything else. */
function partsOf(version) {
  const m = /^(\d+)\.(\d+)\.(\d+)$/.exec(version)
  return m === null ? null : [Number(m[1]), Number(m[2]), Number(m[3])]
}

/** True when `parts` is at or below `other`, version for version. */
function atOrBelow(parts, other) {
  for (let i = 0; i < 3; i++) {
    if (parts[i] !== other[i]) return parts[i] < other[i]
  }
  return true
}

// The plugin's version, read from its manifest. This is the number every entry's `released`
// is compared with, so a manifest that is missing, is not JSON or carries no `x.y.z` version
// stops the build naming the file: an unreadable version is not "nothing is released", and
// guessing one would put a wrong "not yet released" on the page.
function releasedVersion(pluginFile) {
  let manifest
  try {
    manifest = JSON.parse(readFileSync(pluginFile, 'utf8'))
  } catch (cause) {
    throw new Error(
      `${pluginFile} cannot be read as JSON: the changelog reads the plugin's own version ` +
        `to decide which releases are published, and a version it cannot read is not a ` +
        `changelog with nothing released (${cause.message}).`,
    )
  }
  const parts = partsOf(manifest.version)
  if (parts === null) {
    throw new Error(
      `${pluginFile} carries the version ${JSON.stringify(manifest.version)}: the changelog ` +
        `compares each release's version with this one, and it is x.y.z.`,
    )
  }
  return parts
}

// The lines a reader is meant to see: the file's own lines, with the authoring notes an
// HTML comment wraps left out (v0.7.5's trailing `<!-- ... -->` block is notes for the
// next release, not text of this one, and rendering it would put them in the page's HTML).
const HTML_COMMENT = /<!--[\s\S]*?-->/g

function contentLines(text) {
  return text
    .replace(HTML_COMMENT, '')
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
}

// A file's content lines, split the way `docs/releases/README.md` describes the file: the
// Highlights bullets first, then each `###` section heading with the lines under it, in the
// order the file writes them. A section is `{title, lines}` and the two headings are the
// same kind of thing, so they take the same path here — a second one is a second entry in
// the list, not a second rule. A heading the module does not know is not a heading at all:
// its line stays the text it is, which is what keeps a typo'd heading visible to a reader
// and to the site's page gate rather than silently swallowed.
function splitSections(lines) {
  const highlights = []
  const sections = []
  for (const line of lines) {
    if (SECTION_HEADINGS.includes(line)) {
      sections.push({ title: line.replace(HEADING_MARKER, ''), lines: [] })
      continue
    }
    const section = sections[sections.length - 1]
    if (section === undefined) highlights.push(line)
    else section.lines.push(line)
  }
  return { highlights, sections }
}

// The entries, newest first by the version in the filename. Each entry is the file's own
// text and nothing else: `highlights` is what the file says before its first section
// heading (the 3 to 6 user-facing bullets), and `sections` is every section it writes after
// that — its title, and its lines verbatim, in the order the file writes them — so no
// sentence here is one the file does not carry.
//
// `released` is the one field that is not the file's text, and it is the whole of what a
// reader needs to tell a published release from a file written ahead of its release: the
// entry's version against the plugin's own (see the header). Nothing here reads the checkout
// the build runs in.
export function buildChangelog({ releasesDir = RELEASES_DIR, pluginFile = PLUGIN_FILE } = {}) {
  const published = releasedVersion(pluginFile)

  const files = readdirSync(releasesDir)
    .filter((fileName) => fileName !== NON_RELEASE)
    .map((fileName) => {
      const version = versionOf(fileName)
      if (version === null) {
        throw new Error(
          `docs/releases/${fileName} is not a release file: every file there is vX.Y.Z.md, ` +
            `and README.md (which describes the format) is the only file that is not one`,
        )
      }
      return { fileName, ...version }
    })

  return files
    .sort((a, b) => newestFirst(a, b))
    .map((file) => {
      const lines = contentLines(readFileSync(path.join(releasesDir, file.fileName), 'utf8'))
      const { highlights, sections } = splitSections(lines)

      return {
        tag: file.tag,
        version: file.version,
        released: atOrBelow(file.parts, published),
        highlights,
        sections,
      }
    })
}
