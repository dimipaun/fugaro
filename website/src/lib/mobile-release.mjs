// website/src/lib/mobile-release.mjs — the mobile app's latest release, read at build time.
//
// The app releases on its own stream (`mobile-vX.Y.Z`, docs/release.md "Mobile releases"): its
// notes are `docs/releases/mobile/vX.Y.Z.md` and its version line is `mobile/package.json`.
// The rule is the console's (`src/lib/console-release.mjs`, which is this module line for line
// with the other package manifest): the latest release is the newest notes file at or below the
// version the package carries, so a notes file written ahead of the version bump is never named
// and a version bump with no notes file names nothing new. No version is typed anywhere on the
// site.
//
// **The app's stream has one difference the console's does not, and it is the page's rather
// than this module's**: the app publishes no GitHub release, so the page renders its line with
// the release's own sentence from `website/data/mobile-release-summaries.yaml` and no link
// (`src/pages/changelog.astro`, D1644).
//
// Standard library only; nothing fetched.

import { existsSync, readFileSync, readdirSync } from 'node:fs'

function parts(version) {
  const m = /^(\d+)\.(\d+)\.(\d+)$/.exec(version)
  return m === null ? null : [Number(m[1]), Number(m[2]), Number(m[3])]
}

function compare(a, b) {
  for (let i = 0; i < 3; i++) if (a[i] !== b[i]) return a[i] - b[i]
  return 0
}

/** `{ version, tag }` of the newest released app build, or null when none is. */
export function latestMobileRelease({ notesDir, packageFile }) {
  if (!existsSync(notesDir) || !existsSync(packageFile)) return null
  const packaged = parts(String(JSON.parse(readFileSync(packageFile, 'utf8')).version ?? ''))
  if (packaged === null) return null
  const released = readdirSync(notesDir)
    .map((name) => /^v(\d+\.\d+\.\d+)\.md$/.exec(name))
    .filter((m) => m !== null)
    .map((m) => ({ version: m[1], parts: parts(m[1]) }))
    .filter((one) => compare(one.parts, packaged) <= 0)
    .sort((a, b) => compare(b.parts, a.parts))
  if (released.length === 0) return null
  return { version: released[0].version, tag: `mobile-v${released[0].version}` }
}
