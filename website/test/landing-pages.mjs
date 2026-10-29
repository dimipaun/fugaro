// website/test/landing-pages.mjs — the four campaign pages one template builds (go-to-market
// plan, Task G2.2; reconciliation F6, F11, F12, F14, F17, F18, F20, F22).
//
// G2.1 is the data and the loader; this file is the gate over what a browser is served. The
// four paths the spec fixes (`/save`, `/control`, `/scale`, `/crew`) are one Astro route and
// one campaign mode of the base layout, and the rules below are the ones a template gets
// wrong quietly:
//
//   1. **The page is a campaign page and nothing else.** It is `noindex` with a canonical to
//      the home page, it carries no section menu, and no sitemap lists it: a page built for
//      paid traffic that a crawler indexes is a page competing with the site's own home page
//      for the same queries (spec §6, F14).
//   2. **The copy is the data file's, not the template's.** The `h1` is the variant's own
//      headline and the line under it is its supporting line, both read from
//      `website/data/landing.yaml` through `loadLanding()`. Nothing here types a variant's
//      promise into a component (G2.1, D102).
//   3. **The proof band publishes no figure nothing backs.** While a variant carries the
//      sentinel `unmeasured`, the band prints no digit at all — the band's whole job is to be
//      the one place a measurement appears, and a figure typed into it would be the claim F6
//      refuses (G2.1's guard reads the file; this reads the built page).
//   4. **A visitor who declines marketing consent still has a working Book a demo link.** The
//      booking call to action is a plain `<a href>` to the file's Calendly link: no embed, no
//      iframe, no vendor script, and no WhatsApp link — G2.3 renders the embed behind the
//      campaign consent and G2.4 owns the `wa.me` link and its events, and both of them need
//      the plain link to already be here (spec §6, F17, F22, D401).
//   5. **The page is complete without the media the owner has not produced yet.** No campaign
//      video exists and no partner logo exists, so no `<video>` is rendered, the social-proof
//      section is not rendered at all while `partner_logos` is empty, and every file the page
//      does fetch is one the build produced (F11, F14, D405).
//   6. **The campaign mode is responsive, statically.** The gates have no browser: the
//      rendered mobile and desktop reading is owner-run (G2.5). What can be held here is that
//      the layout's campaign rules and the sections' own rules sit inside breakpoints, so a
//      phone gets a layout of its own rather than the wide one squeezed.
//
// **Red step** (the plan's own, recorded here as the plan requires — the line this file printed
// before the template existed): the first case below failed with
// `the build produced no page at save/index.html, control/index.html, scale/index.html,
// crew/index.html`, because `src/pages/[variant].astro` did not exist yet. The cases were
// written against the plan's Files line, before the route was written.
//
// It reads `dist/`, which `npm run check` builds first (`FUGARO_GATES_DIST` points it at a
// copy, which is how each refusal was mutation-proved). Standard library only; no network.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'

import { loadLanding } from '../src/lib/landing.mjs'
import { DIST, SITE, builtPages, cssRules, filesUnder, findAll, parse, servedFile, siteFacts, textOf, walk } from './lib/html.mjs'

const LANDING = loadLanding()
const FACTS = siteFacts()

/** The home page, as the canonical URL is composed from site.yaml's origin (`Base.astro`). */
const HOME = new URL('/', FACTS.canonicalURL).href

/** The repository, from `website/data/site.yaml`: the secondary call to action links it. */
const REPOSITORY = /^repository:\s*(\S+)\s*$/m.exec(readFileSync(path.join(SITE, 'data', 'site.yaml'), 'utf8'))?.[1]
assert.ok(REPOSITORY, 'website/data/site.yaml carries no `repository:` line, and the campaign pages\' install link is that fact')

// The sections the spec writes, top to bottom, and the marker the template names each one with.
// The order is the spec's own list (spec §6): hero, proof number, how it works, social proof,
// calls to action.
const SPEC_SECTIONS = ['hero', 'proof', 'how-it-works', 'social-proof', 'calls-to-action']

/** The four pages this file is about, as the variant and the file the build serves it from. */
const PAGES = LANDING.variants.map((variant) => ({ variant, rel: `${variant.name}/index.html` }))

/** One built page, by its path relative to `dist/`. */
function pageOf(rel) {
  const page = builtPages(DIST).find((entry) => entry.rel === rel)
  assert.ok(page, `${rel} was not built`)
  return page
}

/** The sections one built page marks, in the order a reader meets them. */
function sectionsOf(html) {
  return findAll(parse(html), (el) => el.attrs.has('data-section')).map((el) => el.attrs.get('data-section'))
}

/** The layout's own source, which the static case below reads. */
function layoutSource() {
  const file = path.join(SITE, 'src', 'layouts', 'Base.astro')
  try {
    return readFileSync(file, 'utf8')
  } catch {
    assert.fail('website/src/layouts/Base.astro is missing: the campaign mode the four pages render through lives there (G2.2)')
  }
}

// ---- 1. the four paths, and what a crawler sees ------------------------------------------

test('the four campaign paths build, one page each', () => {
  const built = builtPages(DIST).map((entry) => entry.rel)
  const missing = PAGES.filter((page) => !built.includes(page.rel)).map((page) => page.rel)
  assert.deepEqual(
    missing,
    [],
    `the build produced no page at ${missing.join(', ')}: the four campaigns of the spec are four pages of one template, and a campaign whose page is missing sends its traffic to nothing (spec §6, G2.2)`,
  )
  assert.equal(new Set(PAGES.map((page) => page.rel)).size, 4, 'the four variants are not four distinct paths')
})

test('each campaign page is noindex, canonical to the home page, and absent from every sitemap', () => {
  for (const { rel } of PAGES) {
    const { html } = pageOf(rel)
    const doc = parse(html)

    const robots = findAll(doc, (el) => el.name === 'meta' && el.attrs.get('name') === 'robots').map((el) => el.attrs.get('content') ?? '')
    assert.ok(
      robots.some((content) => /\bnoindex\b/.test(content)),
      `${rel} carries no <meta name="robots" content="noindex">: a campaign page is built for paid traffic and is not a page the site wants in a search result (spec §6, F14)`,
    )

    const canonical = findAll(doc, (el) => el.name === 'link' && el.attrs.get('rel') === 'canonical')
    assert.equal(canonical.length, 1, `${rel} carries ${canonical.length} canonical links; a page carries one`)
    assert.equal(
      canonical[0].attrs.get('href'),
      HOME,
      `${rel} is canonical to ${JSON.stringify(canonical[0].attrs.get('href'))}: the four pages are variants of the home page's promise and point at it, so the four do not compete with each other or with the site's own page for one query (spec §6, F14)`,
    )
  }

  // The campaign pages a sitemap body lists, as the route each `<loc>` resolves to: the four
  // routes are served at `/save/` and the file names them `/save`, so the trailing slash is
  // what the comparison is made without.
  const listedCampaigns = (body) =>
    [...body.matchAll(/<loc>\s*([^<\s]+)\s*<\/loc>/g)]
      .map((match) => new URL(match[1], HOME).pathname.replace(/\/$/, ''))
      .filter((route) => PAGES.some(({ variant }) => variant.path.replace(/\/$/, '') === route))

  // The build produces no sitemap today (astro.config.mjs carries no sitemap integration,
  // `loadSitemapHref` returns null), so this loop runs over nothing — the planted body below is
  // what proves the reading, and it goes through the same call the real files would. The pages
  // are held to it either way, by carrying no sitemap hint of their own.
  for (const rel of filesUnder(DIST).filter((name) => /(^|\/)sitemap[^/]*\.xml$/.test(name))) {
    const listed = listedCampaigns(readFileSync(path.join(DIST, rel), 'utf8'))
    assert.deepEqual(
      listed,
      [],
      `${rel} lists ${listed.join(', ')}, which ${listed.length === 1 ? 'is a noindex campaign page' : 'are noindex campaign pages'}: a sitemap is the list of the pages a site offers a crawler, and offering one it refuses to index is a contradiction (spec §6, F14)`,
    )
  }
  const planted = `<?xml version="1.0" encoding="UTF-8"?><urlset><url><loc>${HOME}</loc></url><url><loc>${HOME}save/</loc></url></urlset>`
  assert.deepEqual(
    listedCampaigns(planted),
    ['/save'],
    'the sitemap reading does not see a campaign page a sitemap lists, which is the check this case is: the four pages are not offered to a crawler (spec §6, F14)',
  )
  assert.deepEqual(
    listedCampaigns(`<urlset><url><loc>${HOME}</loc></url></urlset>`),
    [],
    `the sitemap reading counts ${HOME} as a campaign page, and the home page is the page the four campaign pages are canonical to (spec §6, F14)`,
  )
  for (const { rel } of PAGES) {
    assert.ok(!/<link[^>]*rel="sitemap"/.test(pageOf(rel).html), `${rel} carries a sitemap hint, and the four campaign pages are not in any sitemap`)
  }
})

// ---- 2. the sections, in the spec's order ------------------------------------------------

test('the sections are the spec\'s, in the spec\'s order', () => {
  for (const { rel } of PAGES) {
    const found = sectionsOf(pageOf(rel).html)
    for (const name of found) {
      assert.ok(SPEC_SECTIONS.includes(name), `${rel} marks a section ${JSON.stringify(name)}, which is not one of the spec's: ${SPEC_SECTIONS.join(', ')} (spec §6)`)
    }
    const present = found.filter((name) => name !== 'social-proof')
    assert.deepEqual(
      present,
      SPEC_SECTIONS.filter((name) => name !== 'social-proof'),
      `${rel} carries the sections ${JSON.stringify(found)}: the spec fixes the order — hero, the proof number, how it works, social proof, the calls to action — and the page renders them in it (spec §6)`,
    )
    const at = found.map((name) => SPEC_SECTIONS.indexOf(name))
    assert.deepEqual(at, [...at].sort((a, b) => a - b), `${rel} draws the spec's sections out of order: ${JSON.stringify(found)} (spec §6)`)
  }
})

test('the hero is the variant\'s own copy, read from the data file', () => {
  for (const { variant, rel } of PAGES) {
    const doc = parse(pageOf(rel).html)
    const h1s = findAll(doc, 'h1')
    assert.equal(h1s.length, 1, `${rel} carries ${h1s.length} h1 elements; it has exactly one`)
    assert.equal(
      textOf(h1s[0]).trim(),
      variant.headline,
      `${rel}'s h1 is ${JSON.stringify(textOf(h1s[0]).trim())}, and the variant's own headline is ${JSON.stringify(variant.headline)}: the headline is written once, in website/data/landing.yaml, and the page reads it (G2.1, D102)`,
    )
    assert.ok(
      textOf(doc).includes(variant.supportingLine),
      `${rel} does not carry the variant's supporting line ${JSON.stringify(variant.supportingLine)}: both lines are the file's, and the page types neither (G2.1)`,
    )
  }
})

// ---- 3. the proof band publishes no unbacked figure --------------------------------------

test('the proof band prints no figure while the variant carries the sentinel', () => {
  for (const { variant, rel } of PAGES) {
    const band = findAll(parse(pageOf(rel).html), (el) => el.attrs.get('data-section') === 'proof')
    assert.equal(band.length, 1, `${rel} marks ${band.length} proof sections; the band the spec asks for is one`)
    const text = textOf(band[0])
    if (variant.proofNumber.value === 'unmeasured') {
      assert.ok(
        !/\d/.test(text),
        `${rel}'s proof band carries a digit — ${JSON.stringify(text.trim())} — and its variant's figure is the sentinel: a published figure is never typed into a page, and this band prints one only when website/data/landing.yaml names the measurement that backs it (F6, G2.1)`,
      )
    } else {
      assert.ok(text.includes(variant.proofNumber.value), `${rel}'s proof band does not print the figure its variant names (${JSON.stringify(variant.proofNumber.value)})`)
    }
  }
})

// ---- 4. the calls to action ---------------------------------------------------------------

test('there is no email form, and the calls to action are a plain booking link and the repository', () => {
  for (const { rel } of PAGES) {
    const { html } = pageOf(rel)
    const doc = parse(html)

    assert.equal(findAll(doc, 'form').length, 0, `${rel} carries a form: the spec's calls to action are the booking link and the repository, and there is no waitlist and no email field anywhere on the page (spec §6)`)
    assert.equal(findAll(doc, (el) => el.name === 'input' || el.name === 'textarea' || el.name === 'select').length, 0, `${rel} carries an input: the page asks a visitor for nothing (spec §6)`)
    assert.ok(!/mailto:/i.test(html), `${rel} carries a mailto: link, and the page asks for no address (spec §6)`)

    // The embed is G2.3's, behind the campaign consent; the link is this page's, and it is the
    // one a visitor who declines that consent keeps.
    assert.equal(findAll(doc, 'iframe').length, 0, `${rel} carries an iframe: the Calendly embed is G2.3's and loads only after the marketing consent (D401)`)
    assert.ok(!/calendly\.com\/(assets|widgets?|embed)/i.test(html), `${rel} names a Calendly embed asset: the booking call to action here is a plain link (G2.2, G2.3)`)

    const demo = findAll(doc, (el) => el.name === 'a' && textOf(el).trim() === 'Book a demo')
    assert.equal(demo.length, 1, `${rel} carries ${demo.length} "Book a demo" links; the campaign has one primary call to action`)
    assert.equal(
      demo[0].attrs.get('href'),
      LANDING.calendlyURL,
      `${rel}'s booking link is ${JSON.stringify(demo[0].attrs.get('href'))}, and website/data/landing.yaml writes ${LANDING.calendlyURL}: the link is the file's, with no query of its own (the UTMs G2.4 passes through are added at the link, F22)`,
    )

    const install = findAll(doc, (el) => el.name === 'a' && textOf(el).trim() === 'Install free')
    assert.equal(install.length, 1, `${rel} carries ${install.length} "Install free" links; the secondary call to action is the repository (spec §6)`)
    assert.equal(install[0].attrs.get('href'), REPOSITORY, `${rel}'s install link is ${JSON.stringify(install[0].attrs.get('href'))}, and site.yaml names the repository (${REPOSITORY})`)
  }
})

// ---- 5. no media, no logo, and nothing the build did not produce ---------------------------

test('no partner logo is rendered, and the social-proof section is hidden while the file names none', () => {
  assert.deepEqual(
    LANDING.partnerLogos,
    [],
    'website/data/landing.yaml names a partner logo, and this case reads the shipped file: a partner is named only with that partner\'s written permission, which is the owner\'s (F11, F12, D405)',
  )
  for (const { rel } of PAGES) {
    const { html } = pageOf(rel)
    const doc = parse(html)
    assert.ok(
      !sectionsOf(html).includes('social-proof'),
      `${rel} renders the social-proof section while website/data/landing.yaml names no partner: the section is hidden until one exists (F11, D405)`,
    )
    // Every picture on the page is one the data file names: the site's own wordmark, and — when
    // the file carries them — a variant's media and the partners' logos. A picture the file does
    // not name is a logo or a photograph nobody gave permission for, which is what F11 and F12
    // refuse and what the empty `partner_logos` above is the state of today.
    const named = new Set(['/logo.png', ...PAGES.map(({ variant }) => variant.image), ...LANDING.partnerLogos.map((logo) => logo.image)].filter(Boolean))
    for (const src of findAll(doc, (el) => el.name === 'img').map((el) => el.attrs.get('src'))) {
      assert.ok(
        named.has(src),
        `${rel} renders the picture ${JSON.stringify(src)}, which website/data/landing.yaml does not name: every picture on a campaign page is the file's, and a partner's is shown only with that partner's written permission (F11, F12, D405)`,
      )
    }
  }
})

test('the page is complete without the video file, and fetches nothing the build did not produce', () => {
  for (const { variant, rel } of PAGES) {
    const { html } = pageOf(rel)
    const doc = parse(html)

    if (variant.video === null) {
      assert.equal(
        findAll(doc, 'video').length,
        0,
        `${rel} renders a <video> and its variant names no video in website/data/landing.yaml: the owner produces the campaign media, and the page builds without it (F14, G2.5)`,
      )
    }
    for (const el of walk(doc)) {
      if (el.name === 'a' || el.name === 'link') continue
      for (const attr of ['src', 'poster', 'srcset']) {
        const value = el.attrs.get(attr)
        if (typeof value !== 'string' || value === '' || /^[a-z]+:|^\/\//i.test(value)) continue
        assert.ok(
          servedFile(DIST, new URL(value, 'https://x/').pathname) !== null,
          `${rel}: <${el.name} ${attr}="${value}"> is not a file the build produced, so the page carries a request for nothing (F14)`,
        )
      }
    }
  }
})

// ---- 6. the campaign chrome: no menu, a minimal footer, and a responsive mode -------------

test('the campaign pages carry no section menu, and a footer of the repository, the docs and the statement', () => {
  for (const { rel } of PAGES) {
    const { html } = pageOf(rel)
    const doc = parse(html)

    assert.equal(
      findAll(doc, (el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('site-nav')).length,
      0,
      `${rel} carries the site's section menu: the campaign pages are stripped down — no navigation menu, a minimal footer (spec §6, G2.2)`,
    )
    const navs = findAll(doc, 'nav')
    assert.equal(navs.length, 1, `${rel} carries ${navs.length} nav landmarks; the page has one, in its footer`)

    const footers = findAll(doc, 'footer')
    assert.equal(footers.length, 1, `${rel} carries ${footers.length} footers; the campaign mode has one`)
    const hrefs = [...walk(footers[0])].filter((el) => el.name === 'a').map((el) => el.attrs.get('href') ?? '')
    for (const want of [REPOSITORY, '/docs/', '/privacy/']) {
      assert.ok(
        hrefs.includes(want),
        `${rel}'s footer links ${JSON.stringify(hrefs)}: the minimal footer carries the repository, the docs and the privacy statement — the statement is where consent is withdrawn, and the banner on the page links it (W1.12, G2.2)`,
      )
    }
    assert.ok(!/\bpricing\b/i.test(html), `${rel} names a pricing page: no pricing page exists and pricing is outside this repository (G2.2)`)
  }
})

test('the layout\'s campaign mode is responsive, and declares the two slots the campaign uses', () => {
  const source = layoutSource()

  const style = /<style is:global>([\s\S]*?)<\/style>/.exec(source)
  assert.ok(style, 'Base.astro carries no `<style is:global>` block, and the campaign mode\'s own rules live there')

  const campaign = /\.campaign-[a-z-]+/.exec(style[1])
  assert.ok(
    campaign,
    'Base.astro styles no `.campaign-…` class: the campaign mode is a mode of this layout, and its rules are written where the site chrome\'s are',
  )

  const rules = cssRules(style[1])
  const inBreakpoint = (selector) =>
    rules.some((rule) => (rule.conditions ?? []).length > 0 && rule.selector.split(',').some((part) => part.includes(selector)))
  for (const selector of ['.campaign-header', '.campaign-footer']) {
    assert.ok(
      inBreakpoint(selector),
      `Base.astro declares ${selector} at one width only: the campaign pages are read on a phone and on a desktop, and the layout's own rules for them sit inside a breakpoint (spec §6, G2.2)`,
    )
  }
  assert.ok(
    rules.some((rule) => (rule.conditions ?? []).some((condition) => /max-width/.test(condition))),
    'Base.astro\'s campaign rules carry no max-width breakpoint: the phone layout is the one a campaign page is read at most, and it is written down rather than inherited (G2.2)',
  )

  // The two slots the plan names: the campaign banner G2.3 wires, and the variant's own image.
  for (const name of ['campaign-banner', 'media']) {
    assert.ok(
      source.includes(`<slot name="${name}"`),
      `Base.astro declares no \`<slot name="${name}">\`: the campaign mode carries the slot where the campaign banner is wired (G2.3) and the one the variant's own image renders in (G2.2)`,
    )
  }
})

test('the campaign sections are laid out at more than one width', () => {
  for (const { rel } of PAGES) {
    const { html } = pageOf(rel)
    const styles = findAll(parse(html), (el) => el.name === 'style').map((el) => el.raw ?? '').join('\n')
    const rules = cssRules(styles)
    for (const selector of ['.campaign-steps', '.campaign-calls']) {
      assert.ok(
        rules.some((rule) => (rule.conditions ?? []).length > 0 && rule.selector.includes(selector)),
        `${rel} declares ${selector} at one width only: the sections stack on a phone and sit in a row on a wide viewport, and the gates have no browser — this is the static half, and the rendered reading is G2.5's (spec §6)`,
      )
    }
  }
})
