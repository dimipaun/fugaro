// website/test/lib/console-downloads.mjs — the console's data file, read the way a test has
// to read it (the owner's changes spec of 2026-10-18, item 6).
//
// Two pages offer the console — the console page's Downloads section and Getting Started's
// console option — and two test files check what they render (`console.mjs` and
// `console-option.mjs`). Both need the same three things: the data file parsed, the string a
// download URL of this repository contains, and what the release's published names compose to
// once the version is substituted. That last one is arithmetic a test and the build have to
// agree on — the site derives every URL at build time from `site.yaml`'s repository, the tag
// `console-vX.Y.Z` and the asset's published name — so it is written here once, and a test
// that disagrees with the build fails in one place rather than in two spellings.
//
// It lives under `test/lib/` rather than beside the gates because `npm run check` runs
// `node --test test/*.mjs`: a helper at the top level would be run as a test file of its own.
// Standard library only, and no network (Global Constraint 4).

import { readFileSync } from 'node:fs'
import path from 'node:path'
import { REPO } from './html.mjs'

export const DOWNLOADS_JSON = 'website/content/downloads.json'
export const SITE_YAML = 'website/data/site.yaml'

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
 * What the pages should render when the console has shipped: one link per entry in the data
 * file, with the version substituted into the published name and the URL composed from it —
 * the same derivation `src/lib/console-downloads.mjs` does at build time.
 */
export function publishedDownloads(data = downloadsData()) {
  const base = repository().replace(/\/+$/, '')
  const tag = consoleTag(data)
  return data.downloads.map((entry) => {
    const file = entry.file.split('{version}').join(data.release.version)
    return {
      id: entry.id,
      platform: entry.platform,
      file,
      label: `${entry.platform} ${path.extname(file)}`,
      url: `${base}/releases/download/${tag}/${encodeURIComponent(file)}`,
    }
  })
}
