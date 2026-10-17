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
//    and the newest file has no tag yet, so a date taken from a tag does not exist for the
//    entry a reader sees first. The page prints the tag and links the entry to its GitHub
//    release, where the date is GitHub's.
//  * **Standard library only** (node:fs, node:path, node:child_process), so it runs under
//    `node --test` before the site's toolchain exists, and no npm dependency is added.
//
// The one field that reads the checkout is `released`: true only where `git tag --list`
// finds the tag in the checkout the build ran in. A shallow checkout has no tags at all, so
// every entry there is `false` — the page's "not yet released" state, which is the honest
// rendering of a file committed to `main` before its tag exists. No test pins it.

import { execFileSync } from 'node:child_process'
import { readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

// website/src/lib/changelog.mjs -> website/src/lib -> website/src -> website -> the root.
const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..', '..')

export const RELEASES_DIR = path.join(REPO_ROOT, 'docs', 'releases')

// `docs/releases/README.md` describes the format; it is the one file in the directory that
// is not a release, and it is skipped by name rather than by the pattern below. Every other
// file must be a release file: one that is not named `vX.Y.Z.md` is a mistake in the tree
// (a release notes draft, a stray note), and this module throws rather than dropping it
// silently — a changelog that leaves a release out without saying so is the failure D100
// and F44 exist to prevent.
const NON_RELEASE = 'README.md'

const OPERATORS_HEADING = '### For operators'

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

// The checkout's tags, read once. `git` absent, a shallow checkout or a non-repository
// directory are all "no tags": every entry is then not yet released.
function releasedTags(cwd) {
  try {
    const out = execFileSync('git', ['tag', '--list'], { cwd, encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] })
    return new Set(out.split('\n').map((line) => line.trim()).filter((line) => line.length > 0))
  } catch {
    return new Set()
  }
}

// The entries, newest first by the version in the filename. Each entry is the file's own
// text and nothing else: `highlights` is what the file says before its `### For operators`
// heading (the 3 to 6 user-facing bullets) and `for_operators` is what it says after it,
// verbatim, so no sentence here is one the file does not carry.
export function buildChangelog({ releasesDir = RELEASES_DIR, cwd = REPO_ROOT } = {}) {
  const tags = releasedTags(cwd)

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
      const heading = lines.findIndex((line) => line === OPERATORS_HEADING)

      return {
        tag: file.tag,
        version: file.version,
        released: tags.has(file.tag),
        highlights: heading === -1 ? lines : lines.slice(0, heading),
        for_operators: heading === -1 ? [] : lines.slice(heading + 1),
      }
    })
}
