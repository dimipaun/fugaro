// website/src/lib/console-release.mjs — the desktop console's latest release, read at build time.
//
// The console releases on its own stream (`console-vX.Y.Z`, docs/release.md "Console releases"):
// its notes are `docs/releases/console/vX.Y.Z.md` and its version line is `console/package.json`.
// Both are written on `main` before the tag, so neither alone proves a release is out. The rule
// is the CLI changelog's (`src/lib/changelog.mjs`, which holds release files to the plugin's
// version): the latest console release is the newest notes file at or below the package's
// version. A notes file written ahead of the version bump is never named, and a version bump with
// no notes file names nothing new. No version is typed anywhere on the site.
//
// Standard library only; nothing fetched.

import { existsSync, readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'

function parts(version) {
  const m = /^(\d+)\.(\d+)\.(\d+)$/.exec(version)
  return m === null ? null : [Number(m[1]), Number(m[2]), Number(m[3])]
}

function compare(a, b) {
  for (let i = 0; i < 3; i++) if (a[i] !== b[i]) return a[i] - b[i]
  return 0
}

/** `{ version, tag }` of the newest released console, or null when none is. */
export function latestConsoleRelease({ notesDir, packageFile }) {
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
  return { version: released[0].version, tag: `console-v${released[0].version}` }
}
