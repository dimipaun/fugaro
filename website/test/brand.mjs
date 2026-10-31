// website/test/brand.mjs — the logo, the icons, the manifest and the hosting config.
//
// Every icon the site serves is derived from `brand/fugaro-logo.png` by
// `scripts/gen-brand-icons.py` and committed under `public/`. What is checked here:
//
//   1. **The files exist and are what their names say**: each PNG's own header gives
//      the size its name and its `<link sizes>` promise, and the home-screen icons are
//      opaque (iOS composites a transparent apple-touch-icon onto black, and a maskable
//      icon with holes shows the launcher's colour through them).
//   2. **The head points at them, and only at files the build ships** (Global
//      Constraint 9), and no icon, manifest or logo comes from another origin (D106).
//   3. **The manifest is valid**: JSON, named Fugaro, icon paths relative and present,
//      one maskable entry, and its colours are the `--ground` of the palette in force,
//      so the splash screen matches the page.
//   4. **The header logo is decoration beside the name**: empty alt, inside the home
//      link, its drawn size half the file's, and the file within its byte budget.
//   5. **firebase.json deploys the build and names no project**, and no `.firebaserc`
//      is committed: the project is passed on the command line (scripts/deploy-website.sh).
//
// It reads the artifact `npm run build` writes, which `npm run check` builds first.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { siteFacts } from './lib/html.mjs'
import { loadLanding } from '../src/lib/landing.mjs'

/** The analytics origin, the same one value `origins.mjs` and the banner hold (W3.2). */
const ANALYTICS_ORIGIN = 'https://www.googletagmanager.com'

const SITE = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const REPO = path.dirname(SITE)
const PUBLIC = path.join(SITE, 'public')
const DIST = path.join(SITE, 'dist')

/** The header logo's byte budget: it is on every page. */
const LOGO_BUDGET = 40 * 1024

/** name -> [size, opaque] for every PNG icon the generator writes. */
const ICONS = {
  'favicon-16x16.png': [16, false],
  'favicon-32x32.png': [32, false],
  'apple-touch-icon.png': [180, true],
  'android-chrome-192x192.png': [192, true],
  'android-chrome-512x512.png': [512, true],
  'android-chrome-maskable-512x512.png': [512, true],
}

/** A PNG's width, height and colour type, read from its IHDR chunk. */
function png(file) {
  const b = readFileSync(file)
  assert.equal(b.subarray(0, 8).toString('hex'), '89504e470d0a1a0a', `${file} is not a PNG`)
  assert.equal(b.subarray(12, 16).toString('latin1'), 'IHDR', `${file} has no IHDR chunk first`)
  return { width: b.readUInt32BE(16), height: b.readUInt32BE(20), colourType: b[25], bytes: b.length }
}

function builtPage(rel) {
  const file = path.join(DIST, rel)
  if (!existsSync(file)) {
    throw new Error(`${rel} is not in dist/: \`npm run check\` builds before it runs these checks. Run \`npm run check\` from website/.`)
  }
  return readFileSync(file, 'utf8')
}

function htmlPages(dir = DIST) {
  return readdirSync(dir).flatMap((name) => {
    const abs = path.join(dir, name)
    if (statSync(abs).isDirectory()) return htmlPages(abs)
    return name.endsWith('.html') ? [abs] : []
  })
}

/** The `--ground` literal of the palette tokens.css imports. */
function groundColour() {
  const styles = path.join(SITE, 'src', 'styles')
  const palette = /@import\s+"\.\/(palette-[a-z-]+\.css)"/.exec(readFileSync(path.join(styles, 'tokens.css'), 'utf8'))
  assert.ok(palette, 'tokens.css imports no palette')
  const ground = /--ground:\s*(#[0-9A-Fa-f]{6})\s*;/.exec(readFileSync(path.join(styles, palette[1]), 'utf8'))
  assert.ok(ground, `${palette[1]} has no --ground literal`)
  return ground[1].toUpperCase()
}

test('the source logo is in the tree', () => {
  const logo = png(path.join(REPO, 'brand', 'fugaro-logo.png'))
  assert.equal(logo.colourType, 6, 'brand/fugaro-logo.png is the transparent RGBA original')
  for (const name of ['app-icon-1024.png', 'app-icon-android-foreground-1024.png', 'README.md']) {
    assert.ok(existsSync(path.join(REPO, 'brand', name)), `brand/${name} is missing`)
  }
  assert.equal(png(path.join(REPO, 'brand', 'app-icon-1024.png')).colourType, 2, 'the iOS app-icon master must be opaque RGB')
})

test('every icon is the size its name says, and the home-screen icons are opaque', () => {
  for (const [name, [size, opaque]] of Object.entries(ICONS)) {
    const icon = png(path.join(PUBLIC, name))
    assert.equal(icon.width, size, `${name} width`)
    assert.equal(icon.height, size, `${name} height`)
    // Colour type 2 is RGB, 6 is RGBA.
    assert.equal(icon.colourType, opaque ? 2 : 6, `${name} must be ${opaque ? 'opaque RGB' : 'transparent RGBA'}`)
  }
  const ico = readFileSync(path.join(PUBLIC, 'favicon.ico'))
  assert.equal(ico.readUInt16LE(2), 1, 'favicon.ico is not an icon file')
  const sizes = Array.from({ length: ico.readUInt16LE(4) }, (_, i) => ico[6 + i * 16] || 256)
  assert.deepEqual(sizes.sort((a, b) => a - b), [16, 32, 48], 'favicon.ico carries 16, 32 and 48 px images')
})

test('the head links every icon and the manifest, and each resolves to a built file', () => {
  const page = builtPage('index.html')
  const head = page.slice(0, page.indexOf('</head>'))
  const expected = [
    ['icon', '/favicon.ico'],
    ['icon', '/favicon-32x32.png'],
    ['icon', '/favicon-16x16.png'],
    ['apple-touch-icon', '/apple-touch-icon.png'],
    ['manifest', '/site.webmanifest'],
  ]
  for (const [rel, href] of expected) {
    const tag = [...head.matchAll(/<link\b[^>]*>/g)].map((m) => m[0]).find((t) => t.includes(`rel="${rel}"`) && t.includes(`href="${href}"`))
    assert.ok(tag, `the head has no <link rel="${rel}" href="${href}">`)
    assert.ok(existsSync(path.join(DIST, href)), `${href} is linked but not in dist/`)
  }
  assert.match(head, /rel="icon" type="image\/png" sizes="32x32"/)
  assert.match(head, /rel="apple-touch-icon" sizes="180x180"/)
})

test('no page loads an icon, a manifest or an image from another origin', () => {
  const pages = htmlPages()
  assert.ok(pages.length > 0, 'dist/ holds no page')
  for (const file of pages) {
    const html = readFileSync(file, 'utf8')
    for (const m of html.matchAll(/<link\b[^>]*\brel="(?:icon|apple-touch-icon|manifest)"[^>]*>|<img\b[^>]*>/g)) {
      const url = /\b(?:href|src)="([^"]*)"/.exec(m[0])
      assert.ok(url, `${path.relative(DIST, file)}: ${m[0]} names no URL`)
      assert.ok(url[1].startsWith('/') && !url[1].startsWith('//'), `${path.relative(DIST, file)}: ${m[0]} is not first-party (D106)`)
    }
  }
})

test('the manifest is valid and matches the palette', () => {
  const manifest = JSON.parse(readFileSync(path.join(DIST, 'site.webmanifest'), 'utf8'))
  assert.equal(manifest.name, 'Fugaro')
  assert.equal(manifest.short_name, 'Fugaro')
  const ground = groundColour()
  assert.equal(manifest.theme_color.toUpperCase(), ground, 'theme_color is the palette ground')
  assert.equal(manifest.background_color.toUpperCase(), ground, 'background_color is the palette ground')
  assert.ok(Array.isArray(manifest.icons) && manifest.icons.length >= 3, 'the manifest lists its icons')
  for (const icon of manifest.icons) {
    assert.ok(!/^([a-z]+:|\/)/i.test(icon.src), `${icon.src} is not a relative path`)
    const file = path.join(DIST, icon.src)
    assert.ok(existsSync(file), `${icon.src} is in the manifest but not in dist/`)
    const { width, height } = png(file)
    assert.equal(icon.sizes, `${width}x${height}`, `${icon.src} sizes`)
    assert.equal(icon.type, 'image/png')
  }
  assert.equal(manifest.icons.filter((i) => i.purpose === 'maskable').length, 1, 'exactly one maskable icon')
})

test('the header logo is decoration inside the home link, within its budget', () => {
  const page = builtPage('index.html')
  const link = /<a\b[^>]*\bhref="\/"[^>]*\brel="home"[^>]*>([\s\S]*?)<\/a>/.exec(page)
  assert.ok(link, 'the header has no home link')
  const img = /<img\b[^>]*>/.exec(link[1])
  assert.ok(img, 'the home link carries no logo')
  assert.match(img[0], /\balt=""/, 'the logo sits beside the name, so its alt is empty')
  assert.match(link[1].replace(/<[^>]*>/g, '').trim(), /^Fugaro$/, 'the home link names the site in text')
  const logo = png(path.join(PUBLIC, 'logo.png'))
  assert.ok(logo.bytes <= LOGO_BUDGET, `logo.png is ${logo.bytes} bytes, over its ${LOGO_BUDGET}-byte budget`)
  assert.match(img[0], new RegExp(`width="${logo.width / 2}" height="${logo.height / 2}"`), 'the logo is drawn at half its pixel size (2x)')
})

test('firebase.json deploys the build and names no project, and no .firebaserc is committed', () => {
  const config = JSON.parse(readFileSync(path.join(REPO, 'firebase.json'), 'utf8'))
  assert.equal(config.hosting.public, 'website/dist')
  assert.equal(config.hosting.cleanUrls, true)
  assert.ok(!('site' in config.hosting) && !('target' in config.hosting), 'firebase.json names a site: the project is passed at deploy time')
  assert.ok(!existsSync(path.join(REPO, '.firebaserc')), '.firebaserc is committed: the project is passed at deploy time')

  const all = Object.fromEntries(config.hosting.headers.find((h) => h.source === '**').headers.map((h) => [h.key, h.value]))
  const csp = all['Content-Security-Policy']
  assert.ok(csp, 'no Content-Security-Policy')
  // The site loads from no origin but its own, with the one analytics exception W3.2 pins
  // (D93, D106) and the campaign's three (G2.3, D401) — and each exception is read here as an
  // exact origin, not as "a remote origin is fine" — with one wildcard, `https://*.licdn.com`,
  // which the owner ruled into `img-src` alone (2026-10-31) and which `script-src` here still
  // refuses. The three sets are the ones the two data files write, so a CSP that dropped one,
  // or grew a fourth, is this case failing rather than a literal below going stale;
  // `website/test/origins.mjs` holds the whole policy, directive by directive, to Google's and
  // the three vendors' documented minimum, and this is the coarser check that those exceptions
  // are the only ones.
  const campaign = siteFacts().marketing
  assert.deepEqual(
    [...new Set([...csp.matchAll(/https?:\/\/[^\s;]*/g)].map((m) => m[0]))].sort(),
    [
      'https://www.googletagmanager.com',
      'https://*.google-analytics.com',
      'https://*.google.com',
      campaign.metaOrigin,
      campaign.linkedinOrigin,
      campaign.calendlyOrigin,
      'https://www.facebook.com',
      'https://px.ads.linkedin.com',
      'https://*.licdn.com',
      new URL(loadLanding().calendlyURL).origin,
    ].sort(),
    `the CSP admits an origin that is not one of the ones the site's two data files, W3.2 and the owner's ruling of 2026-10-31 (LinkedIn's img-src wildcard) pin: ${csp}`,
  )
  assert.ok(!/script-src[^;]*\*/.test(csp), 'script-src admits a wildcard origin')
  assert.match(csp, /default-src 'self'/)
  assert.match(
    csp,
    new RegExp(`script-src 'self' ${ANALYTICS_ORIGIN.replace(/\./g, '\\.')}( https://\\S+)*;`),
    'scripts are this site, Google Analytics and the three vendors of the campaign gate, and nothing else (W3.2, G2.3)',
  )
  assert.equal(all['X-Content-Type-Options'], 'nosniff')
  assert.ok(all['Referrer-Policy'], 'no Referrer-Policy')

  // Hashed assets are cached for a year; the catch-all (HTML) is cached briefly.
  const hashed = config.hosting.headers.find((h) => h.source === '/_astro/**')
  assert.ok(hashed && /immutable/.test(hashed.headers[0].value), 'hashed assets are not cached as immutable')
  assert.match(all['Cache-Control'], /max-age=(\d{1,3})\b/, 'HTML is cached for minutes, not days')
})
