// website/src/lib/console-downloads.mjs — the console's download facts, read and derived
// once (website plan, Task W1.6; the owner's changes spec of 2026-10-18, item 6).
//
// Two pages offer the console — the console page's own Downloads section and the console
// option of Getting Started — and they offer the same artifact. So the data file is read
// here, once, and both pages render what this module hands them: a second reader would be a
// second set of refusals to keep in step, and the rule below ("no link while the console is
// unreleased") is the one rule a reader may not get wrong.
//
// **The URLs are derived, and nothing under `website/` writes one.** The release workflow
// publishes the console as its own GitHub release, `console-vX.Y.Z` — X.Y.Z is the CLI tag
// it was triggered by — carrying `Fugaro Console-X.Y.Z-universal.dmg` and
// `Fugaro Console-X.Y.Z-universal-mac.zip` for macOS and nothing else
// (`.github/workflows/console-release.yml`, `console/README.md`, `docs/release.md`). So
// `content/downloads.json` carries the released *names* — the tag's prefix, the version,
// and each asset's file name with `{version}` where the version goes — and this module
// composes the URL from them and from `site.yaml`'s own `repository`. The owner's change
// when the console is published is therefore a flag (`site.yaml`'s
// `features.console_downloads`), `available` below it, and a version bump: no URL is typed
// anywhere, and the day the version moves, every button moves with it.
//
// **That is also why the file may carry the names while the console is unreleased.** The
// rule the Go boundary test enforces over the tree is that no file under `website/` carries
// this repository's release-asset download URL while `features.console_downloads` is false
// (`internal/cli/docs_website_boundary_test.go`, Global Constraint 9, finding 25) — and the
// URL is composed at build time, in this module, out of a file that carries none. What the
// two pages render stays structural: the download list is rendered from `available`'s
// branch, so an unreleased console has no link to follow even though its asset names are
// already in the data file.
//
// Standard library only, and no network: this module is imported by the build, and it
// fetches nothing (Global Constraints 4 and 8).

import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'

/** The file these facts live in, as every message below names it. */
export const DOWNLOADS_FILE = 'website/content/downloads.json'

/** The placeholder an asset's `file` carries where the release's version goes. */
const VERSION_PLACEHOLDER = '{version}'

/**
 * A build error naming the data file, the way `content.ts` names the file it refused: the
 * reader of the message is whoever just edited `downloads.json`.
 */
function refuse(message) {
  throw new Error(`${DOWNLOADS_FILE}: ${message} (W1.6 steps 1 and 2).`)
}

function asMap(value, what) {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    refuse(`${what} is not a JSON object, and the console's facts are read from keys in one`)
  }
  return value
}

/** A string a page renders, and never the empty one: a blank claim is a hole in the page. */
function asText(value, what) {
  if (typeof value !== 'string' || value.trim() === '') {
    refuse(
      `${what} is ${value === undefined ? 'not written in the file' : JSON.stringify(value)}, and every fact below is a written sentence`,
    )
  }
  return value
}

/** A claim: a sentence and the document it came from, and nothing else. */
function asClaim(value, what) {
  const map = asMap(value, what)
  for (const key of Object.keys(map)) {
    if (key !== 'text' && key !== 'source') {
      refuse(
        `${what} carries the key "${key}", and a claim is a "text" and the "source" it came from — a sentence without a source is a sentence nobody can check (W1.6 step 3)`,
      )
    }
  }
  return { text: asText(map.text, `${what}.text`), source: asText(map.source, `${what}.source`) }
}

/**
 * The published name of one asset, with the release's version substituted, and the URL a
 * reader downloads it from — composed from the site's own repository, the release's tag and
 * the published name, and never written down (see the header).
 */
function downloadURL(repository, tag, file) {
  const base = repository.replace(/\/+$/, '')
  let composed
  try {
    composed = new URL(`${base}/releases/download/${tag}/${encodeURIComponent(file)}`)
  } catch {
    refuse(
      `cannot compose a download URL: website/data/site.yaml's repository is ${JSON.stringify(repository)}, which is not an absolute URL, and every console download is built from it`,
    )
  }
  if (composed.protocol !== 'https:') {
    refuse(`builds a ${composed.protocol} download URL from website/data/site.yaml's repository, and a reader downloads over https`)
  }
  return composed.href
}

/**
 * The console's facts: `downloads.json`, validated, held to `site.yaml`'s feature flag, and
 * with every download URL derived from the release's own published names.
 *
 * Every rule below is a state the page must not render rather than a style: a missing file,
 * a flag that disagrees with the site's, a release block that names no version, an asset
 * file that is a URL or that carries no version placeholder, and a section that says the
 * console is available with nothing to download are all build errors, each naming the file
 * and the key it refused.
 *
 * @param {string} siteRoot the site's root, as `content.ts`'s `SITE_ROOT` reports it
 * @param {boolean} flagFromSiteYAML `site.yaml`'s `features.console_downloads`
 * @param {string} repository `site.yaml`'s `repository`: where the release is published
 */
export function loadConsoleDownloads(siteRoot, flagFromSiteYAML, repository) {
  const file = path.join(siteRoot, 'content', 'downloads.json')
  if (!existsSync(file)) {
    refuse(
      'is missing, and the pages that offer the console render what the console is, that it is not released and what it will be from it — the build stops rather than rendering an empty section',
    )
  }
  const raw = readFileSync(file, 'utf8')
  if (raw.trim() === '') refuse('is empty, and an empty file is an empty section')

  let parsed
  try {
    parsed = JSON.parse(raw)
  } catch (err) {
    refuse(`is not JSON: ${err.message}`)
  }

  const doc = asMap(parsed, 'the document')
  const allowed = ['version', 'available', 'name', 'what', 'not_released', 'properties', 'release', 'downloads']
  for (const key of Object.keys(doc)) {
    if (!allowed.includes(key)) {
      refuse(
        `carries the key "${key}", which no part of the pages reads — a fact nobody reads is a fact nothing pins, so a new key arrives with the element that renders it`,
      )
    }
  }

  if (doc.version !== 1) refuse(`version is ${JSON.stringify(doc.version)}, and this reader knows version 1`)

  const available = doc.available
  if (typeof available !== 'boolean') {
    refuse(`available is ${JSON.stringify(available)}, and the console's downloads are rendered from a true or false`)
  }

  // The two flags are one fact. A disagreement is not a page that looks odd: it is the site
  // saying the console has shipped while the repository's own rule says it has not, or the
  // reverse.
  if (available !== flagFromSiteYAML) {
    refuse(
      `available is ${available} and website/data/site.yaml's features.console_downloads is ${flagFromSiteYAML}. The console's release flips both in one commit, and the site cannot render a console that has shipped while the repository's own rule still refuses its download URL (W3.1, Global Constraint 9)`,
    )
  }

  const properties = (() => {
    if (!Array.isArray(doc.properties) || doc.properties.length === 0) {
      refuse(
        "properties is not a non-empty list, and the console page states the console's own read-only, no-telemetry and release properties from it (W1.6 step 3)",
      )
    }
    return doc.properties.map((entry, index) => {
      const map = asMap(entry, `properties[${index}]`)
      for (const key of Object.keys(map)) {
        if (!['id', 'title', 'text', 'source'].includes(key)) {
          refuse(`properties[${index}] carries the key "${key}", and a property is an id, a title, a text and the decision it came from`)
        }
      }
      const claim = asClaim({ text: map.text, source: map.source }, `properties[${index}]`)
      return {
        id: asText(map.id, `properties[${index}].id`),
        title: asText(map.title, `properties[${index}].title`),
        text: claim.text,
        source: claim.source,
      }
    })
  })()

  // The release the download URLs are built from: its tag's prefix, the CLI version it was
  // cut from, and the document that decides both. A URL with no version in it is the state
  // this block exists to make impossible.
  const release = (() => {
    const map = asMap(doc.release, 'release')
    for (const key of Object.keys(map)) {
      if (!['tag_prefix', 'version', 'source'].includes(key)) {
        refuse(`release carries the key "${key}", and a release is a tag_prefix, the version it was cut from and the "source" that decides both`)
      }
    }
    const tagPrefix = asText(map.tag_prefix, 'release.tag_prefix')
    const version = asText(map.version, 'release.version')
    // X.Y.Z without the tag's `v`, because that is the version the published names carry:
    // the workflow cuts `console-v$VERSION` from `VERSION="${TAG#v}"` and refuses an
    // artifact not named for it, so `Fugaro Console-X.Y.Z-universal.dmg` has no `v` in it
    // (`.github/workflows/console-release.yml`, `console/README.md`).
    if (!/^\d+\.\d+\.\d+$/.test(version)) {
      refuse(`release.version is ${JSON.stringify(version)}, which is not an X.Y.Z version: the console's tag is console-vX.Y.Z and its published names carry X.Y.Z, both cut from the CLI's own vX.Y.Z tag (D11, docs/release.md)`)
    }
    if (!/^[a-z][a-z0-9-]*-v$/.test(tagPrefix)) {
      refuse(`release.tag_prefix is ${JSON.stringify(tagPrefix)}, and the console's tag is that prefix and the version — console-v and vX.Y.Z, as docs/release.md writes it`)
    }
    return { tagPrefix, version, tag: `${tagPrefix}${version}`, source: asText(map.source, 'release.source') }
  })()

  const notReleased = (() => {
    const map = asMap(doc.not_released, 'not_released')
    for (const key of Object.keys(map)) {
      if (!['text', 'short', 'source'].includes(key)) {
        refuse(`not_released carries the key "${key}", and the console's unreleased state is the sentence a page renders, its one-line form and the source of both`)
      }
    }
    return {
      ...asClaim({ text: map.text, source: map.source }, 'not_released'),
      short: asText(map.short, 'not_released.short'),
    }
  })()

  const downloads = (() => {
    if (!Array.isArray(doc.downloads)) {
      refuse('downloads is not a list, and each page renders one link per entry when the console has shipped')
    }
    return doc.downloads.map((entry, index) => {
      const map = asMap(entry, `downloads[${index}]`)
      for (const key of Object.keys(map)) {
        // `url` is refused by name rather than ignored: a URL in this file is the defect the
        // Go boundary test scans the whole site for, and it would be a URL nothing keeps in
        // step with the release the names below are cut from.
        if (!['id', 'platform', 'file'].includes(key)) {
          refuse(
            `downloads[${index}] carries the key "${key}", and a download is an id, the platform it is for and the published name of the asset it is: the URL is derived from those and never written down (Global Constraint 9)`,
          )
        }
      }
      const platform = asText(map.platform, `downloads[${index}].platform`)
      const asset = asText(map.file, `downloads[${index}].file`)
      if (/^[a-z][a-z0-9+.-]*:/i.test(asset) || asset.startsWith('//') || asset.includes('/')) {
        refuse(
          `downloads[${index}].file is ${JSON.stringify(asset)}, which is a URL or a path rather than the name the release publishes: the file is the asset's own name, and the URL is derived from the release`,
        )
      }
      if (!asset.includes(VERSION_PLACEHOLDER)) {
        refuse(
          `downloads[${index}].file is ${JSON.stringify(asset)} and carries no ${VERSION_PLACEHOLDER}: the published name carries the tag's version, and a name with no version in it is one the next release does not publish`,
        )
      }
      const published = asset.split(VERSION_PLACEHOLDER).join(release.version)
      return {
        id: asText(map.id, `downloads[${index}].id`),
        platform,
        // What a reader sees on the button: the platform, and the asset's own extension, so
        // two assets of one platform — the disk image and the archive — are told apart
        // without a second fact to keep in step with the file name.
        label: `${platform} ${path.extname(published)}`,
        file: published,
        url: downloadURL(repository, release.tag, published),
      }
    })
  })()

  if (available && downloads.length === 0) {
    refuse('available is true and downloads is empty: a section that says the console has shipped with nothing to download is the broken state the flag exists to prevent')
  }

  return {
    available,
    name: asText(doc.name, 'name'),
    what: asClaim(doc.what, 'what'),
    notReleased,
    properties,
    release,
    downloads,
  }
}
