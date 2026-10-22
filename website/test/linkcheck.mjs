// website/test/linkcheck.mjs — every link goes somewhere (website plan, W1.11 step 1;
// reconciliation finding 59, decision D108's second gate).
//
// Over the BUILT artifact (`dist/`), because a link is broken or not in the HTML a reader
// gets, whatever the source looked like:
//
//   1. Every internal URL — an `a`/`area` href, a `link` href, a `script`/`img`/`source`
//      `src` — resolves to a file the build produced: a page (`/docs/` is served by
//      `docs/index.html`) or an asset. Resolution is the browser's: a relative href is
//      resolved against the page's own URL, which is how `docs/x.md` on `/security/` becomes
//      `/security/docs/x.md`.
//   2. Every `#fragment` resolves to an `id` (or an `a[name]`) in the target page, and a bare
//      `#` — a link to nowhere — is refused.
//   3. Every external URL is `https:` and its host is on site.yaml's `allowed_hosts`; the
//      canonical host is this site, so a link to it is checked as an internal one. A link
//      into this repository on GitHub (`blob`/`tree` on `main` or `HEAD`, and
//      raw.githubusercontent.com) must name a path that exists in the repository, and a
//      fragment into a markdown file must be one of its headings' GitHub anchors — the
//      file-level half W1.7's own test checks for the docs hub, applied here to every page.
//      Release tags are not checked: a hermetic test has no tags to read (D100).
//   4. `javascript:`, `data:`, protocol-relative `//host` and every other scheme are refused.
//
// **The one recorded exception this file used to carry is gone, and this is the change that
// removed it** (the owner's brief of 2026-10-19). The security page rendered `SECURITY.md`
// byte for byte (D99), and SECURITY.md links its neighbours relatively
// (`docs/design/v1.md#6-security-model`), which on the site resolved under `/security/` and
// were not served — a defect W1.11 was not allowed to fix in W1.8's file. The page now
// renders the document's own reporting section (which carries no link) instead of the whole
// document, so the repository-relative links are no longer on any page and every one of them
// fails here again, as it should.
//
// It reads `dist/`, which `npm run check` builds first (`FUGARO_GATES_DIST` points it at a
// copy, which is how each refusal was mutation-proved). Standard library only; no network.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { githubSlug, headingText } from '../src/lib/github-slug.mjs'
import { DIST, HTMLScanError, REPO, SITE, builtPages, parse, servedFile, siteFacts, walk } from './lib/html.mjs'

// The repository every GitHub link is built from, as site.yaml states it.
function repositoryPath(siteYAML = readFileSync(path.join(SITE, 'data', 'site.yaml'), 'utf8')) {
  const m = /^repository:\s*"?https:\/\/github\.com\/([^"\s#]+?)\/?"?\s*(#.*)?$/m.exec(siteYAML)
  if (!m) throw new Error('site.yaml has no repository: https://github.com/<owner>/<name> line')
  return m[1]
}

// ---- the anchors of a markdown file, as GitHub renders them ----------------------------

const anchorCache = new Map()
export function markdownAnchors(file) {
  if (anchorCache.has(file)) return anchorCache.get(file)
  const ids = new Set()
  const seen = new Map()
  let fence = null
  for (const line of readFileSync(file, 'utf8').split('\n')) {
    const f = /^\s{0,3}(`{3,}|~{3,})/.exec(line)
    if (f) {
      if (fence === null) fence = f[1][0]
      else if (f[1][0] === fence) fence = null
      continue
    }
    if (fence !== null) continue
    const h = /^\s{0,3}#{1,6}\s+(.*?)\s*#*\s*$/.exec(line)
    if (h) {
      const base = githubSlug(headingText(h[1]))
      const n = seen.get(base) ?? 0
      seen.set(base, n + 1)
      ids.add(n === 0 ? base : `${base}-${n}`)
    }
    for (const m of line.matchAll(/<a\s+(?:id|name)="([^"]+)"/g)) ids.add(m[1])
  }
  anchorCache.set(file, ids)
  return ids
}

/** Why a fragment into a repository file does not resolve, or null. */
function repoFragmentFailure(file, fragment) {
  if (!fragment) return null
  if (!file.endsWith('.md')) return `#${fragment} points into a file that is not markdown, whose anchors this check cannot know`
  return markdownAnchors(file).has(fragment) ? null : `#${fragment} is not a heading of ${path.relative(REPO, file)}`
}

/** Why a path in the repository does not exist as the kind a GitHub URL names, or null. */
function repoPathFailure(rel, kind) {
  const file = path.join(REPO, rel)
  if (!path.resolve(file).startsWith(REPO + path.sep) && path.resolve(file) !== REPO) return `${rel} leaves the repository`
  if (!existsSync(file)) return `${rel} does not exist in the repository`
  const isDir = statSync(file).isDirectory()
  if (kind === 'blob' && isDir) return `${rel} is a directory, linked as a file (blob)`
  if (kind === 'tree' && !isDir) return `${rel} is a file, linked as a directory (tree)`
  return null
}

// ---- one artifact ----------------------------------------------------------------------

/** The ids a built page declares. */
function idsOf(doc) {
  const ids = new Set()
  for (const el of walk(doc)) {
    if (el.attrs.has('id')) ids.add(el.attrs.get('id'))
    if (el.name === 'a' && el.attrs.has('name')) ids.add(el.attrs.get('name'))
  }
  return ids
}

/** The URL-bearing attributes the link check follows, per element. */
function urlsOf(el) {
  const out = []
  const take = (attr) => el.attrs.has(attr) && out.push([attr, el.attrs.get(attr)])
  if (el.name === 'a' || el.name === 'area' || el.name === 'link') take('href')
  if (['script', 'img', 'source', 'iframe', 'video', 'audio', 'track', 'embed'].includes(el.name)) take('src')
  if (el.name === 'form') take('action')
  return out
}

/**
 * Every link failure of a built artifact: the links that go nowhere and the links that go
 * somewhere they may not. It returns them, so a caller can assert the list is empty and a
 * fixture can watch one arrive.
 */
export function linkFailures(dist, facts, repository) {
  const pages = builtPages(dist)
  const docs = new Map()
  const failures = []
  for (const page of pages) {
    try {
      docs.set(page.rel, parse(page.html))
    } catch (err) {
      if (!(err instanceof HTMLScanError)) throw err
      failures.push(`${page.rel}: ${err.message}`)
    }
  }
  const idsByFile = new Map([...docs].map(([rel, doc]) => [rel, idsOf(doc)]))
  const base = `https://${facts.canonicalHost}`

  for (const page of pages) {
    const doc = docs.get(page.rel)
    if (!doc) continue
    const pageURL = new URL(page.url, base)
    for (const el of walk(doc)) {
      for (const [attr, raw] of urlsOf(el)) {
        const where = `${page.rel}: <${el.name} ${attr}="${raw}">`
        const value = raw.trim()
        if (value === '' || value === '#') {
          failures.push(`${where} links nowhere`)
          continue
        }
        if (value.startsWith('//')) {
          failures.push(`${where} is protocol-relative`)
          continue
        }
        const scheme = /^([a-zA-Z][a-zA-Z0-9+.-]*):/.exec(value)
        if (scheme && scheme[1].toLowerCase() !== 'https') {
          failures.push(`${where} uses ${scheme[1].toLowerCase()}: — the site links over https only, and never to a javascript: or data: URL`)
          continue
        }
        let url
        try {
          url = new URL(value, pageURL)
        } catch {
          failures.push(`${where} is not a URL`)
          continue
        }
        const fragment = url.hash ? decodeURIComponent(url.hash.slice(1)) : null
        // `new URL('/a/#').hash` is '', not '#', so an empty fragment is read off the text.
        if (value.endsWith('#')) {
          failures.push(`${where} has an empty fragment`)
          continue
        }

        if (url.host !== facts.canonicalHost) {
          if (!facts.allowedHosts.has(url.host)) {
            failures.push(`${where}: ${url.host} is not on the allowed-host list (site.yaml allowed_hosts)`)
            continue
          }
          const gh = new RegExp(`^/${repository}/(blob|tree)/(?:main|HEAD)/(.+)$`).exec(url.pathname)
          const raw = url.host === 'raw.githubusercontent.com' ? new RegExp(`^/${repository}/(?:main|HEAD)/(.+)$`).exec(url.pathname) : null
          if (url.host === 'github.com' && gh) {
            const rel = decodeURIComponent(gh[2]).replace(/\/$/, '')
            const why = repoPathFailure(rel, gh[1]) ?? (gh[1] === 'blob' ? repoFragmentFailure(path.join(REPO, rel), fragment) : null)
            if (why) failures.push(`${where}: ${why}`)
          } else if (raw) {
            const why = repoPathFailure(decodeURIComponent(raw[1]), 'blob')
            if (why) failures.push(`${where}: ${why}`)
          }
          continue
        }

        // This site.
        const served = servedFile(dist, url.pathname)
        if (served === null) {
          failures.push(`${where}: resolves to ${url.pathname}, which the build did not produce`)
          continue
        }
        if (fragment !== null) {
          const ids = idsByFile.get(served)
          if (!ids) {
            failures.push(`${where}: #${fragment} points into ${served}, which is not a page`)
          } else if (!ids.has(fragment)) {
            failures.push(`${where}: #${fragment} is not an id in ${served}`)
          }
        }
      }
    }
  }
  return { failures }
}

// ---- the gate --------------------------------------------------------------------------

const FACTS = siteFacts()
const REPOSITORY = repositoryPath()

test('every link on the built site resolves: pages, assets, anchors, and allowed hosts only', () => {
  const { failures } = linkFailures(DIST, FACTS, REPOSITORY)
  assert.deepEqual(failures, [], `the built site carries a link that goes nowhere or somewhere it may not:\n  ${failures.join('\n  ')}`)
})

// ---- the refusals, kept ----------------------------------------------------------------

const FIXTURE_FACTS = siteFacts(`canonical_url: https://site.example
analytics:
  enabled: false
  measurement_id: ""
allowed_hosts:
  - github.com
  - site.example
`)

/** A temp dist of pages { rel: bodyHTML }, and the link check's verdict on it. */
function verdict(t, pages) {
  const dist = mkdtempSync(path.join(tmpdir(), 'fugaro-linkcheck-'))
  t.after(() => rmSync(dist, { recursive: true, force: true }))
  for (const [rel, body] of Object.entries(pages)) {
    mkdirSync(path.dirname(path.join(dist, rel)), { recursive: true })
    writeFileSync(
      path.join(dist, rel),
      rel.endsWith('.html') ? `<!DOCTYPE html><html lang="en"><head><title>t</title></head><body><main id="main">${body}</main></body></html>` : body,
    )
  }
  return linkFailures(dist, FIXTURE_FACTS, REPOSITORY).failures
}

test('a link to a page the build did not produce is refused', (t) => {
  const f = verdict(t, { 'index.html': '<a href="/nope/">x</a><a href="/a/">a</a>', 'a/index.html': '' })
  assert.equal(f.length, 1, f.join('\n'))
  assert.match(f[0], /resolves to \/nope\/, which the build did not produce/)
})

test('a missing anchor, on this page or another, is refused', (t) => {
  const f = verdict(t, {
    'index.html': '<h2 id="here">h</h2><a href="#here">ok</a><a href="#gone">x</a><a href="/a/#there">ok</a><a href="/a/#nowhere">x</a><a href="#">x</a><a href="/a/#">x</a>',
    'a/index.html': '<p id="there">t</p>',
  })
  assert.equal(f.length, 4, f.join('\n'))
  assert.match(f[0], /#gone is not an id in index\.html/)
  assert.match(f[1], /#nowhere is not an id in a\/index\.html/)
  assert.match(f[2], /links nowhere/)
  assert.match(f[3], /<a href="\/a\/#">.*has an empty fragment/)
})

test('a relative link resolves the way a browser resolves it', (t) => {
  const f = verdict(t, { 'a/index.html': '<a href="docs/x.md">x</a><a href="../b/">ok</a>', 'b/index.html': '' })
  assert.equal(f.length, 1, f.join('\n'))
  assert.match(f[0], /resolves to \/a\/docs\/x\.md/)
})

test('an external host off the list, and every unsafe scheme, are refused', (t) => {
  const f = verdict(t, {
    'index.html': [
      '<a href="https://evil.example.net/">x</a>',
      '<a href="http://github.com/">x</a>',
      '<a href="javascript:void(0)">x</a>',
      '<a href="data:text/html,hi">x</a>',
      '<a href="//github.com/">x</a>',
      '<a href="mailto:someone@site.example">x</a>',
      '<a href="https://site.example/nope/">x</a>',
    ].join(''),
  })
  const expected = [/evil\.example\.net is not on the allowed-host list/, /uses http:/, /uses javascript:/, /uses data:/, /protocol-relative/, /uses mailto:/, /\/nope\/, which the build did not produce/]
  assert.equal(f.length, expected.length, f.join('\n'))
  expected.forEach((re, i) => assert.match(f[i], re))
})

test('a link into this repository on GitHub must name a path and a heading that exist', (t) => {
  const repo = `https://github.com/${REPOSITORY}`
  const f = verdict(t, {
    'index.html': [
      `<a href="${repo}/blob/main/SECURITY.md">ok</a>`,
      `<a href="${repo}/blob/main/no-such-file.md">x</a>`,
      `<a href="${repo}/tree/main/docs">ok</a>`,
      `<a href="${repo}/blob/main/README.md#no-such-heading">x</a>`,
    ].join(''),
  })
  assert.equal(f.length, 2, f.join('\n'))
  assert.match(f[0], /no-such-file\.md does not exist in the repository/)
  assert.match(f[1], /#no-such-heading is not a heading of README\.md/)
})

test('a broken asset reference is refused', (t) => {
  const f = verdict(t, { 'index.html': '<link rel="stylesheet" href="/_astro/a.css"><img src="/missing.svg" alt="">', '_astro/a.css': 'a{}' })
  assert.equal(f.length, 1, f.join('\n'))
  assert.match(f[0], /\/missing\.svg, which the build did not produce/)
})
