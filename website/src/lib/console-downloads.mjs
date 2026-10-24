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
// `Fugaro Console-X.Y.Z-universal-mac.zip` for macOS and the unsigned
// `Fugaro.Console-X.Y.Z-linux-x86_64.AppImage` and `…-linux-amd64.deb` for Linux
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
// **The checksums are held to the release's own sums file.** A checksum is a fact about bytes,
// and no build can compute it: the one thing a reader of `downloads.json` cannot check is
// whether a well-formed value is the value the release published. So the release's own
// `console-sha256sums.txt` — the file the console's workflow uploads beside the artifacts,
// whose bytes are copied verbatim into `data/console-sha256sums.txt` when the console's release
// is turned on — is read here, and every entry's `sha256` has to be the value that file records
// for the entry's published name. That is what makes a wrong checksum a build error rather than
// a page that prints a number nothing can match: the cross-check is the release's, and a
// value that disagrees with it is refused by name.
//
// **And the name a reader's link carries is not the name the release's own files print.** GitHub
// rewrites a space in an uploaded asset's name to a dot: the build writes
// `Fugaro Console-X.Y.Z-universal.dmg` on the runner, the release's sums file prints that
// name, and the release page serves the file as `Fugaro.Console-X.Y.Z-universal.dmg`. So the data file carries the *release's* name — the one
// its own sums file lists, which is what the checksum above is keyed by — and the URL's last
// segment is derived from it by `publishedName` below. This is the one fact about the console's
// release that nothing in this tree can record, because the rename happens after the artifacts
// leave the runner; it is stated once, there, and confirmed against the published release by the
// owner's live check (`docs/gcp-live-checklist.md`, check 69a). Getting it wrong is not a
// cosmetic bug: a link spelled with the build's own name is a 404 for every reader.
//
// **And the tag's prefix is the workflow's, not a literal this module or the data file chose.**
// The URL's release segment is `<prefix><version>`, and the prefix is what the release workflow
// itself writes when it composes the tag it publishes under (`CONSOLE_TAG="<prefix>$VERSION"` in
// `.github/workflows/console-release.yml`). A prefix in `downloads.json` that the workflow does
// not compose names a tag no run ever creates, and every button 404s — so the build reads that
// line and refuses the disagreement, the same way the Go boundary test does over the tree
// (`internal/cli/docs_website_boundary_test.go`, `TestWebsiteConsoleReleaseFactsAreComplete`).
// Holding it here, in the build, is what makes the data file's own reader the refusal a page
// build meets rather than a gate a later test run meets.
//
// **And a platform whose own facts differ is a block the file may hold, or not.** The console
// ships for macOS, and since the owner's ruling of 2026-10-20 it is built for Linux too —
// unsigned, an AppImage and a `.deb` (D10) — while the sentence both pages render about the
// console is the macOS one, because signing and notarisation are macOS's facts and Linux has
// neither. So the data file may carry an optional `linux` block: the sentence a Linux reader
// needs (what the build is, that it is unsigned, what proves the bytes), the steps that
// sentence points at, and the ids of the assets it links. **It renders only when it is there**,
// which is the component's own `if` and not a promise about its copy, and while it is not there
// the page renders nothing about Linux at all — no heading, no sentence, no link — which is what
// keeps the two pages saying macOS only without the markup saying so.
//
// **And its links are held to the file's own assets.** Every id the block names has to be an id
// `downloads` lists, and every entry there has already been held to the release's published name
// and to the checksum the release's own sums file records for it. So a Linux link can appear
// only once the asset's name, its checksum and the release's own line for it are all in the
// data — the gate the follow-up that publishes has to pass, rather than a sentence it has to
// remember to write.
//
// Standard library only, and no network: this module is imported by the build, and it
// fetches nothing (Global Constraints 4 and 8).

import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'

/** The file these facts live in, as every message below names it. */
export const DOWNLOADS_FILE = 'website/content/downloads.json'

/**
 * The release's own sums file, as every message about a checksum names it: the console
 * workflow uploads it beside the artifacts (`gh release create … console-sha256sums.txt`),
 * and the copy under `website/data/` is byte-for-byte the published one.
 */
export const SUMS_FILE = 'website/data/console-sha256sums.txt'

/**
 * The workflow that cuts the console's release tag, as every message about the tag's prefix
 * names it: it is where `<prefix>$VERSION` is composed, and the data file's `release.tag_prefix`
 * is held to that line rather than to a remembered spelling of it.
 */
export const RELEASE_WORKFLOW = '.github/workflows/console-release.yml'

/** The placeholder an asset's `file` carries where the release's version goes. */
const VERSION_PLACEHOLDER = '{version}'

/**
 * The name the release serves an asset under, which is the name the URL's last segment has to
 * carry: GitHub rewrites a space in an uploaded asset's name to a dot, so the file the build
 * writes as `Fugaro Console-X.Y.Z-universal.dmg` — the name the release's own
 * `console-sha256sums.txt` prints — is served, and linked to, as
 * `Fugaro.Console-X.Y.Z-universal.dmg`. A name with no space in it — Linux's pair, which the
 * packer writes with a dot already — is served as it is written, and the rule is applied to
 * every asset rather than to the ones it changes.
 *
 * This is the whole of the rule, and it is stated here rather than read from anywhere because
 * nothing in this tree can carry it: the rename happens on GitHub, after the artifacts leave the
 * runner, so no document, workflow or checksum list in the repository records it. It was
 * confirmed against a published release's own asset list (2026-10-19 for `console-v0.9.0`: the
 * dotted names are the ones served, the dotted URL answers 200 and the `%20` one 404), and the
 * owner's live check re-confirms it — `docs/gcp-live-checklist.md` check 69a follows every URL
 * the pages carry. A link that skipped this step is a 404 for every reader, which is what
 * `website/test/console.mjs` refuses.
 */
function publishedName(file) {
  return file.split(' ').join('.')
}

/**
 * The prefix the release workflow composes the console's tag with: the `<prefix>` of
 * `CONSOLE_TAG="<prefix>$VERSION"`, which is the line `publish` builds the release's tag from
 * and therefore the only spelling of the prefix a run ever creates a tag under.
 *
 * Read from the workflow rather than taken from the data file, so this is a check and not a
 * second copy: the file lives beside the site in this repository, and the build of a copy of
 * the repository reads the copy's own workflow (`website/test/lib/build.mjs` carries it).
 *
 * A workflow that no longer composes its tag that way, or that is not there at all, is a
 * refusal rather than a skipped check: a prefix this cannot read is a prefix nothing holds the
 * data file's URLs to, and the URLs are the whole of what the check protects.
 */
function workflowTagPrefix(siteRoot) {
  const file = path.join(path.dirname(siteRoot), ...RELEASE_WORKFLOW.split('/'))
  if (!existsSync(file)) {
    refuse(
      `cannot be read: ${RELEASE_WORKFLOW} is the workflow that composes the console's release tag, and it is where the release.tag_prefix below is checked rather than assumed. Every download URL is \`<repository>/releases/download/<prefix><version>/<name>\`, so a prefix nothing composes names a tag no run creates (D11)`,
      'W3.1',
    )
  }
  const match = /CONSOLE_TAG="([^"$]+)\$VERSION"/.exec(readFileSync(file, 'utf8'))
  if (!match) {
    refuse(
      `${RELEASE_WORKFLOW} no longer composes the console's tag as \`CONSOLE_TAG="<prefix>$VERSION"\`, and release.tag_prefix below is the prefix of the tag every download URL names: a prefix this cannot read is a prefix nothing holds the URLs to (D11)`,
      'W3.1',
    )
  }
  return match[1]
}

/**
 * A build error naming the data file, the way `content.ts` names the file it refused: the
 * reader of the message is whoever just edited `downloads.json`. The task the rule comes from
 * is W1.6, which wrote the file and its first refusals, unless the caller names another — the
 * checksum cross-check below is W3.1's, and a message carrying both tags would name the wrong
 * one twice.
 */
function refuse(message, task = 'W1.6 steps 1 and 2') {
  throw new Error(`${DOWNLOADS_FILE}: ${message} (${task}).`)
}

/** A build error about the release's sums file, named the way the file is. */
function refuseSums(message) {
  throw new Error(`${SUMS_FILE}: ${message} (W3.1).`)
}

/**
 * The release's published checksums, read from the sums file the release carries: one
 * `<64 hex>  <published name>` line per asset, which is the shape `sha256sum` writes and
 * `shasum -a 256 -c` reads. A line of any other shape is refused rather than skipped — a
 * skipped line is an asset the cross-check below would silently stop covering — and the file
 * is required to be non-empty, because a sums file with nothing in it would make every
 * check below vacuous.
 */
function releaseSums(siteRoot) {
  const file = path.join(siteRoot, 'data', 'console-sha256sums.txt')
  if (!existsSync(file)) {
    refuseSums(
      'is missing: it is the release\'s own checksum list, uploaded beside the artifacts, and the copy here is what the checksums in the data file are held to. A console release is turned on with it (W3.1)',
    )
  }
  const raw = readFileSync(file, 'utf8')
  const sums = new Map()
  for (const [index, line] of raw.split('\n').entries()) {
    if (line === '') continue
    const match = /^([0-9a-f]{64}) {2}(.+)$/.exec(line)
    if (!match) {
      refuseSums(
        `line ${index + 1} is ${JSON.stringify(line)}, and the file the release publishes is one "<64 hex>  <name>" line per asset — the shape sha256sum writes and shasum -a 256 -c reads. A line this file cannot read is an asset nothing below covers`,
      )
    }
    const [, sha256, name] = match
    if (sums.has(name)) {
      refuseSums(`names ${JSON.stringify(name)} twice (line ${index + 1}), and one published name has one checksum`)
    }
    sums.set(name, sha256)
  }
  if (sums.size === 0) {
    refuseSums('carries no checksum line, so every asset below would be checked against nothing')
  }
  return sums
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
 * The URL a reader downloads one asset from — composed from the site's own repository, the
 * release's tag and the name the release SERVES the asset under, and never written down (see
 * the header and `publishedName`).
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
  const allowed = ['version', 'available', 'name', 'what', 'released', 'not_released', 'properties', 'release', 'downloads', 'linux']
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
      `available is ${available} and website/data/site.yaml's features.console_downloads is ${flagFromSiteYAML}. The console's release flips both in one commit, and the site cannot render a console that has shipped while the repository's own rule still refuses its download URL`,
      'W3.1, Global Constraint 9',
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
    // And the prefix is the workflow's own, not merely one of the right shape: a prefix that
    // passes the shape above and that no run composes is a tag no release carries, and the URLs
    // below name it. The check reads `.github/workflows/console-release.yml`'s own
    // `CONSOLE_TAG="<prefix>$VERSION"` line — the same line the Go boundary test reads — so the
    // data file cannot drift from the release's tag without the page build refusing it.
    const composedPrefix = workflowTagPrefix(siteRoot)
    if (tagPrefix !== composedPrefix) {
      refuse(
        `release.tag_prefix is ${JSON.stringify(tagPrefix)} and ${RELEASE_WORKFLOW} composes the console's release tag with ${JSON.stringify(composedPrefix)} (\`CONSOLE_TAG="${composedPrefix}$VERSION"\`): every console download URL is \`<repository>/releases/download/<prefix><version>/<name>\`, so a prefix the workflow does not compose names a tag no run creates — and every button on the page is then a 404 (D11)`,
        'W3.1',
      )
    }
    return { tagPrefix, version, tag: `${tagPrefix}${version}`, source: asText(map.source, 'release.source') }
  })()

  // What the section says once the console has shipped: the platform, the signing and
  // notarisation, and the CLI-first rule. The checksums themselves are per asset (below),
  // because a checksum is a fact about one file rather than about the release — and **this
  // block carries no verification command**: the owner read the page on 2026-10-21 and asked
  // for `shasum -a 256 Fugaro.Console-*` to come out from under the macOS cards, whose readers
  // have Apple's signature to check, so the released block is one sentence and its source, and a
  // file that grew a key back is refused below. The commands that do exist belong to the
  // unsigned platform's own block, where a signature is exactly what is missing.
  const released = (() => {
    const map = asMap(doc.released, 'released')
    for (const key of Object.keys(map)) {
      if (!['text', 'source'].includes(key)) {
        refuse(`released carries the key "${key}", and the console's released state is the sentence a page renders and the source of it`)
      }
    }
    return asClaim({ text: map.text, source: map.source }, 'released')
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

  // The release's own published checksums, read before the entries below are: each entry's
  // `sha256` is held to the value this file records for its published name, which is the one
  // check that can tell a wrong checksum from a right one without the network.
  const sums = releaseSums(siteRoot)

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
        if (!['id', 'platform', 'file', 'sha256'].includes(key)) {
          refuse(
            `downloads[${index}] carries the key "${key}", and a download is an id, the platform it is for, the published name of the asset it is and the SHA-256 of the bytes the release published: the URL is derived from those and never written down (Global Constraint 9)`,
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
      // The checksum of the bytes the release published, as `console-sha256sums.txt`
      // records them. It is written as 64 lowercase hex digits and nothing else,
      // because it is printed beside the link for a reader to check a download with:
      // a truncated, uppercased or non-hex value is a checksum `shasum -a 256 -c`
      // can never match, and a page that prints one is worse than a page that prints
      // none. The shape is not the whole check — a well-formed value can still be the
      // wrong one — and the release's own sums file is what settles that, below.
      const sha256 = asText(map.sha256, `downloads[${index}].sha256`)
      if (!/^[0-9a-f]{64}$/.test(sha256)) {
        refuse(
          `downloads[${index}].sha256 is ${JSON.stringify(sha256)}, which is not 64 lowercase hex digits: the checksum is what a reader compares a download against with shasum -a 256, so a value of any other shape is one nothing can ever match`,
        )
      }
      const published = asset.split(VERSION_PLACEHOLDER).join(release.version)
      // The release's own checksum for the published name, which is what makes the value
      // above checkable: a well-formed checksum is one a reader can compare a download
      // against, and the release's sums file is the only thing that says WHICH value is the
      // right one. A name the release does not publish is refused here too — it is the same
      // mistake, an entry naming an asset the release does not carry.
      const recorded = sums.get(published)
      if (recorded === undefined) {
        refuse(
          `downloads[${index}].file is ${JSON.stringify(asset)}, which the release's own checksum list (${SUMS_FILE}) does not name: the file is the published name of an asset of ${release.tag}, and a name the release does not publish is a download that 404s (D11)`,
          'W3.1',
        )
      }
      if (recorded !== sha256) {
        refuse(
          `downloads[${index}].sha256 is ${JSON.stringify(sha256)} and ${SUMS_FILE} records ${JSON.stringify(recorded)} for ${JSON.stringify(published)}: the release's own file is what the checksum is checked against, and a value that disagrees with it is a checksum that fails for every reader who checks their download`,
          'W3.1',
        )
      }
      // The release's own name is the sums file's key; the name a reader's link carries is the
      // one GitHub serves, and the one step between them is `publishedName` (its comment has
      // the rule and the evidence for it).
      const served = publishedName(published)
      return {
        id: asText(map.id, `downloads[${index}].id`),
        platform,
        sha256,
        // What a reader sees on the button: the platform, and the asset's own extension, so
        // two assets of one platform — the disk image and the archive — are told apart
        // without a second fact to keep in step with the file name.
        label: `${platform} ${path.extname(published)}`,
        // The name the release SERVES, which is the URL's last segment and the name a reader's
        // browser saves the file as: the data file carries the release's own name, and
        // `publishedName` is the one step between the two.
        file: served,
        url: downloadURL(repository, release.tag, served),
      }
    })
  })()

  if (available && downloads.length === 0) {
    refuse('available is true and downloads is empty: a section that says the console has shipped with nothing to download is the broken state the flag exists to prevent')
  }

  // The optional block for a platform whose own facts differ from the released sentence's —
  // Linux, whose build is unsigned and whose files are not disk images the macOS sentence
  // describes (the owner's ruling of 2026-10-20; D10, and the header above). Absent is the state
  // the pages render nothing for: the block's links are its assets, so the sentences are not
  // there either, and no part of a page has to say a platform is missing.
  const linux = (() => {
    if (doc.linux === undefined) return null
    const map = asMap(doc.linux, 'linux')
    for (const key of Object.keys(map)) {
      if (!['text', 'assets', 'verify', 'source'].includes(key)) {
        refuse(
          `linux carries the key "${key}", and this block is the sentence a page renders, the ids of the assets it links, the steps that prove those bytes and the "source" that sentence came from: a fact nothing renders is a fact nothing pins`,
          'D10',
        )
      }
    }
    const claim = asClaim({ text: map.text, source: map.source }, 'linux')
    if (!Array.isArray(map.assets) || map.assets.length === 0) {
      refuse(
        "linux.assets is not a non-empty list, and this block exists for the assets it links: a paragraph about a platform's downloads with no download in it is the one thing it must not be",
        'D10',
      )
    }
    const named = map.assets.map((value, index) => {
      const id = asText(value, `linux.assets[${index}]`)
      const entry = downloads.find((download) => download.id === id)
      if (entry === undefined) {
        // The gate, in the one place a link can be introduced: the block links assets this file
        // lists, and every one of those has already been held to the release's published name
        // and to the checksum the release's own sums file records for it. So a link to a name
        // that is not in the data — the Linux artifact before anyone has published it — cannot
        // be rendered at all.
        refuse(
          `linux.assets[${index}] is ${JSON.stringify(id)} and downloads lists no entry with that id (${downloads.map((download) => download.id).join(', ')}): the block's links are the assets this file lists, each with the release's own published name and the checksum its own sums file records — so an id nothing backs is a download link no fact in this file supports (D10)`,
          'D10',
        )
      }
      return entry
    })
    if (new Set(named.map((entry) => entry.id)).size !== named.length) {
      refuse('linux.assets names one asset twice, and one asset of a release is one link', 'D10')
    }
    // The steps that stand behind the links, and they are required rather than optional: an
    // unsigned artifact is the one download on this page with nothing to check it for, so the
    // commands that prove its bytes are the whole of what the block is for beside the sentence
    // that says it is unsigned. They are commands and not prose — a reader runs them — so each
    // is a non-empty string, and each carries the version placeholder wherever the release's
    // version goes, substituted here the same way an asset's `file` name is: the version is
    // written in one place, and a version bump moves the commands with it.
    if (!Array.isArray(map.verify) || map.verify.length === 0) {
      refuse(
        "linux.verify is not a non-empty list, and an unsigned build is the one download here with no signature to check: the steps that prove these bytes are what a Linux reader does instead (docs/release.md, \"Verifying the console's release\")",
        'D10',
      )
    }
    const verify = map.verify.map((value, index) => {
      const command = asText(value, `linux.verify[${index}]`)
      if (!command.includes(VERSION_PLACEHOLDER)) {
        refuse(
          `linux.verify[${index}] is ${JSON.stringify(command)} and carries no ${VERSION_PLACEHOLDER}: these steps name the release's own tag, and a command with the version typed into it is one the next release leaves naming the last one`,
          'D10',
        )
      }
      return command.split(VERSION_PLACEHOLDER).join(release.version)
    })
    // Whether the block is drawn is `available`'s business and not this reader's: the section
    // renders the block — as it renders the assets — only while the console has shipped, so an
    // unpublished console with a block in the file draws nothing of it. That is the same `if`
    // the download list above it lives under, and a second refusal here would only make the
    // flag-off fixture the tests build (W3.1's unreleased state) carry one more edit.
    return { ...claim, downloads: named, verify }
  })()

  return {
    available,
    name: asText(doc.name, 'name'),
    what: asClaim(doc.what, 'what'),
    released,
    notReleased,
    properties,
    release,
    downloads,
    linux,
  }
}
