// website/test/lib/console-downloads.mjs — the console's data file, read the way a test has
// to read it (the owner's changes spec of 2026-10-18, item 6).
//
// Two pages offer the console — the console page's Downloads section and Getting Started's
// console option — and two test files check what they render (`console.mjs` and
// `console-option.mjs`). Both need the same things: the data file parsed, the string a
// download URL of this repository contains, the release's tag, and what the release publishes.
//
// **What this file must not become is a second copy of the build's arithmetic.** The site
// derives every URL at build time from `site.yaml`'s repository, the tag `console-vX.Y.Z` and
// the asset's published name; a test that derived the same URL the same way would agree with
// the build even when both were wrong, which is the one failure the released-state cases exist
// to catch. So the expectation is anchored to a *third* thing: the release's own account of
// what it ships (`console/README.md`) and the workflow that cuts its tag
// (`.github/workflows/console-release.yml`). `publishedDownloads` below carries no URL at all —
// `assertReleaseURL` holds the href the page rendered to those facts one part at a time.
//
// It lives under `test/lib/` rather than beside the gates because `npm run check` runs
// `node --test test/*.mjs`: a helper at the top level would be run as a test file of its own.
// Standard library only, and no network (Global Constraint 4).

import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { REPO } from './html.mjs'

export const DOWNLOADS_JSON = 'website/content/downloads.json'
export const SITE_YAML = 'website/data/site.yaml'
/** The release's own documentation of what it publishes, and of the tag it is cut as. */
export const CONSOLE_README = 'console/README.md'
export const RELEASE_WORKFLOW = '.github/workflows/console-release.yml'

/** The bytes of a file in the checkout, for a test to hold the build's output against. */
export function repoFile(relative) {
  return readFileSync(path.join(REPO, relative), 'utf8')
}

/** The console's data file, parsed: the facts the pages render and the tests check. */
export function downloadsData() {
  return JSON.parse(repoFile(DOWNLOADS_JSON))
}

/** `site.yaml`'s `repository`: where the console's release, and every download, lives. */
export function repository() {
  const m = /^repository:\s*(\S+)$/m.exec(repoFile(SITE_YAML))
  if (m === null) throw new Error(`${SITE_YAML} states no repository, and every download URL is composed from it`)
  return m[1]
}

/**
 * The string a console download URL of this repository contains, built from `site.yaml`'s own
 * repository rather than typed here: while `features.console_downloads` is false the built
 * site may carry none of it, and the same shape is what the Go boundary test scans the whole
 * site for (Global Constraint 9). Assembled at run time so that this file is not itself a
 * second spelling of a URL the site must not carry.
 */
export function downloadNeedle() {
  const base = new URL(repository())
  return `${base.host}${base.pathname}/releases/download`
}

/** The release's tag: the console's own, `console-vX.Y.Z`, cut from the CLI's tag. */
export function consoleTag(data = downloadsData()) {
  return `${data.release.tag_prefix}${data.release.version}`
}

/**
 * The prefix the release workflow itself cuts the console's tag with: the
 * `CONSOLE_TAG="<prefix>$VERSION"` line of `.github/workflows/console-release.yml`. Read from
 * that file, so the data file's `release.tag_prefix` is held to the line that composes the tag
 * rather than to a remembered spelling of it.
 */
export function workflowTagPrefix() {
  const m = /CONSOLE_TAG="([^"$]+)\$VERSION"/.exec(repoFile(RELEASE_WORKFLOW))
  if (m === null) {
    throw new Error(
      `${RELEASE_WORKFLOW} no longer composes the console's tag as CONSOLE_TAG="<prefix>$VERSION", and ${DOWNLOADS_JSON}'s URLs are built from that prefix`,
    )
  }
  return m[1]
}

/**
 * The tag's own spelling as the release workflow prints it: the literal `console-vX.Y.Z` in its
 * header. A second, independent reading of the same fact `workflowTagPrefix` takes from the
 * line that cuts the tag — this one is prose the workflow writes about itself, so a data file
 * whose prefix drifts has to disagree with both to pass.
 */
export function workflowTagShape() {
  const m = /`(console-[a-z-]+)X\.Y\.Z`/.exec(repoFile(RELEASE_WORKFLOW))
  if (m === null) {
    throw new Error(
      `${RELEASE_WORKFLOW} no longer prints the console's tag as \`console-vX.Y.Z\`, and ${DOWNLOADS_JSON}'s URLs are built from that spelling`,
    )
  }
  return `${m[1]}X.Y.Z`
}

/**
 * The naming rule the console's release job enforces on the assets it uploads, read out of that
 * job rather than remembered: `dmgs=(./*.dmg)` and `zips=(./*.zip)` are the shapes it takes, and
 * `case "$f" in *"-$VERSION-"*)` refuses any asset not named for the version. The data file's
 * names are held to both (`console.mjs`), so the day the release's own rule changes, the site
 * fails there rather than publishing buttons that 404.
 */
export function workflowAssetRule() {
  const workflow = repoFile(RELEASE_WORKFLOW)
  const extensions = [...new Set([...workflow.matchAll(/\*\.(dmg|zip)\b/g)].map((match) => match[1]))].sort()
  if (extensions.length === 0) {
    throw new Error(
      `${RELEASE_WORKFLOW} no longer takes the console's assets by extension (\`dmgs=(./*.dmg)\`, \`zips=(./*.zip)\`), and ${DOWNLOADS_JSON}'s assets are those shapes`,
    )
  }
  return { extensions, versioned: /\*"-\$VERSION-"\*/.test(workflow) }
}

/**
 * The names the console's release publishes, as the release's own documentation prints them:
 * `console/README.md`'s `release/Fugaro Console-<version>-universal.dmg` and
 * `…-universal-mac.zip`, with the version put in for the placeholder. Read from that document
 * rather than substituted out of `downloads.json`, so the data file and the built page are both
 * held to the release's own account of what it ships.
 */
export function publishedAssets(data = downloadsData()) {
  const documented = [
    ...repoFile(CONSOLE_README).matchAll(/Fugaro Console-<version>-universal(?:-mac)?\.(?:dmg|zip)/g),
  ].map((match) => match[0].replace('<version>', data.release.version))
  const names = [...new Set(documented)]
  if (names.length === 0) {
    throw new Error(
      `${CONSOLE_README} no longer prints the names the console's release publishes, and ${DOWNLOADS_JSON}'s assets are those names`,
    )
  }
  return names
}

/**
 * What a page should render when the console has shipped: one entry per asset in the data file,
 * paired in order with the release's documented names. The label is the platform the data file
 * states and the extension of the **documented** name, so a label that disagrees with the
 * release disagrees with the test rather than with a copy of the build.
 *
 * There is deliberately no `url` here: `assertReleaseURL` below holds the href the page
 * rendered to the release's facts one part at a time, which is the only way a test can see a bug
 * that a URL composed here, the same way the build composes it, would share with the build.
 */
export function publishedDownloads(data = downloadsData()) {
  const names = publishedAssets(data)
  if (names.length !== data.downloads.length) {
    throw new Error(
      `${DOWNLOADS_JSON} lists ${data.downloads.length} asset(s) and ${CONSOLE_README} documents ${names.length}: the data file's assets are the release's own`,
    )
  }
  return data.downloads.map((entry, index) => {
    const name = names[index]
    return {
      id: entry.id,
      platform: entry.platform,
      name,
      label: `${entry.platform} ${name.slice(name.lastIndexOf('.'))}`,
    }
  })
}

/**
 * Hold a rendered href to the release it must come from, part by part: the origin of the
 * repository `site.yaml` links, the release-download path under it, the console's tag, and the
 * asset's documented name — percent-encoded, because the href a reader clicks is a URL.
 *
 * Each part is asserted against the fact it comes from (`site.yaml`'s `repository`, the tag the
 * release is cut as, `console/README.md`'s name) rather than against a URL composed here, and
 * the encoding is read off the **raw** string: `new URL()` normalizes what it is handed — a
 * space in a path becomes `%20` once parsed — so only the href as written says whether the build
 * composed a URL or pasted a name into one.
 */
export function assertReleaseURL(actual, name, data = downloadsData()) {
  let url
  try {
    url = new URL(actual)
  } catch {
    assert.fail(`${JSON.stringify(actual)} is not an absolute URL, and a download a reader clicks is one`)
  }
  const base = new URL(repository())
  assert.equal(url.origin, base.origin, `${actual} is not on ${base.origin}, the repository ${SITE_YAML} links`)

  const dir = `${base.pathname.replace(/\/+$/, '')}/releases/download/${consoleTag(data)}/`
  assert.ok(
    url.pathname.startsWith(dir),
    `${actual} is not an asset of the console's own release: its path does not start with ${dir}, so it names the wrong repository path, the wrong release or the wrong tag`,
  )
  const segment = url.pathname.slice(dir.length)
  assert.ok(
    segment !== '' && !segment.includes('/'),
    `${actual} names no single asset of the release`,
  )
  assert.equal(
    decodeURIComponent(segment),
    name,
    `${actual} does not name ${JSON.stringify(name)}, the asset the release publishes`,
  )
  assert.ok(
    actual.endsWith(`/${encodeURIComponent(name)}`),
    `${actual} does not carry the asset's published name percent-encoded, and a URL a reader clicks is a URL`,
  )
}
