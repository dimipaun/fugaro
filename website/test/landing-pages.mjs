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
//   3. **The proof band publishes a backed figure, or it publishes nothing at all.** While a
//      variant carries the sentinel `unmeasured` there is no band on the page: no placeholder in
//      the big type, no sentence explaining the absence, and no empty box where the band would
//      be — the band's whole job is to be the one place a measurement appears, and a figure
//      typed into it would be the claim F6 refuses (G2.1's guard reads the file; this reads the
//      built page). The other half is a case below: a variant whose figure the measurement backs
//      does render the band, with the figure and the entry it was drawn from. That the section
//      is absent rather than empty is the owner's answer of 2026-10-30, recorded as D409, and
//      with it the four pages omit spec §6's section 2 on purpose.
//   4. **A visitor who declines marketing consent still has a working way to book and a way to
//      chat.** The booking call to action is a plain `<a href>` to the file's Calendly link and
//      the chat one is the `wa.me` link the file's WhatsApp number and the variant's own
//      pre-fill make: no embed, no iframe and no vendor script behind either. G2.3 renders the
//      embed behind the campaign consent and G2.4 owns the key events the three calls fire, and
//      both of them need the plain links to already be here (spec §6, F17, F22, D401).
//   5. **The page is complete without the media the owner has not produced yet.** No campaign
//      video exists and no partner logo exists, so no `<video>` is rendered, the social-proof
//      section is not rendered at all while `partner_logos` is empty, and every file the page
//      does fetch is one the build produced (F11, F14, D405).
//   6. **The campaign mode is responsive, statically.** The gates have no browser: the
//      rendered mobile and desktop reading is owner-run (G2.5). What can be held here is that
//      the layout's campaign rules and the sections' own rules sit inside breakpoints, so a
//      phone gets a layout of its own rather than the wide one squeezed.
//   7. **The hero's own picture is the console's own screen.** Each variant names a `screenshot`
//      — a file of `website/public/screenshots/` that has to be a row of
//      `website/data/console-screens.yaml` — and the hero renders that capture large, with the
//      row's own alt text, caption and pixel size: one picture per hero, fetched with the page,
//      from an origin the build already serves (F14, D190). The campaign's own drawing is the
//      fallback: it is what a hero shows while a variant names no screen, it is inline SVG, so
//      nothing is fetched and no raster file is added, and it carries the variant's tone — its own
//      accent and its own motif, keyed off `data-tone` — a name for a reader who is not shown it,
//      and no text inside the drawing, because a picture made of words is a second paragraph the
//      page's word ceiling would have to count. A variant that names a video or an image takes
//      the media slot at the top of the page instead, and renders no picture in the hero at all
//      (the owner's feedback of 2026-10-30 asked for the picture as well as the band's absence:
//      D409's row records the band, D411's the picture; F14, G2.5).
//   8. **The page's two fields of colour are the site's own shape, and each closing action
//      carries its own mark.** The steps' panel and the closing band are cut at `--radius-xl`,
//      the site's largest corner step, and each of the three calls to action has an inline
//      `aria-hidden` mark beside its label: the owner's feedback of 2026-10-30 (D412's findings 1
//      and 2) asked for a friendlier band than three right angles and three lines of type, and the
//      mark is decoration whose meaning is the link's own words, so no reader is owed it (F14).
//   9. **Every campaign page shows the console's screens, in the homepage's own slider.** The
//      four pages that argue for the product now show it: below the hero and above the closing
//      call to action, the same `ScreenshotSlider` over the same rows
//      (`website/data/home-screens.yaml`, drawn from this site's own `public/screenshots/`), with
//      the focusable track, the dots and the `hidden` control bar the homepage's panel has, and
//      every slide in the document so a reader with no script gets the pictures stacked (the
//      owner's feedback of 2026-10-30, D412's finding 3; D246, F14).
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
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'

import { loadLanding } from '../src/lib/landing.mjs'
import { DIST, SITE, builtPages, cssDeclarations, cssRules, filesUnder, findAll, parse, servedFile, siteFacts, textOf, tokenize, walk } from './lib/html.mjs'
import { buildCopy, dropCampaignScreenshots } from './lib/build.mjs'
import { readScreenRows } from './lib/screens.mjs'

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

/** The sentinel `website/data/landing.yaml` writes in place of a figure the owner has not
 * published: a variant carrying it renders no proof band at all (F6). */
const SENTINEL = 'unmeasured'

/**
 * The sections one variant's page renders, in the spec's order: the spec's five, less the two
 * that render only while a data file backs them — the proof band while the variant's figure is
 * the sentinel, and the social-proof section while `partner_logos` names no partner (F6, F11).
 * The pair is what makes the reading below an equality: a page that rendered a fifth section,
 * dropped a required one or drew them out of order is refused, and so is a page whose band came
 * back while the file still carries no figure.
 */
function sectionsFor(variant) {
  return SPEC_SECTIONS.filter((name) => {
    if (name === 'proof') return variant.proofNumber.value !== SENTINEL
    if (name === 'social-proof') return LANDING.partnerLogos.length > 0
    return true
  })
}

/** The four pages this file is about, as the variant and the file the build serves it from. */
const PAGES = LANDING.variants.map((variant) => ({ variant, rel: `${variant.name}/index.html` }))

/** One built page, by its path relative to `dist/`. A case that reads a copy passes the copy's
 * own index; the shipped build is the default. */
function pageOf(rel, dist = DIST) {
  const page = builtPages(dist).find((entry) => entry.rel === rel)
  assert.ok(page, `${rel} was not built`)
  return page
}

/** The sections one built page marks, in the order a reader meets them. */
function sectionsOf(html) {
  return findAll(parse(html), (el) => el.attrs.has('data-section')).map((el) => el.attrs.get('data-section'))
}

/**
 * **A picture is something a reader is meant to look at, and a decorative mark is not one.** The
 * hero's readings count the elements that draw a picture — the console screen (`<img>`), the
 * campaign's own drawing (`<svg>`) and the media a variant names — and since the owner's
 * feedback of 2026-10-30 (D412's finding 1) each call to action carries an `<svg>` mark beside its
 * label. That mark is `aria-hidden` decoration whose meaning is the link's own words, so it is
 * not a picture of anything and no reading of "how many pictures has this hero" may count it:
 * a hero with a booking button would otherwise carry two pictures and a page whose variant names
 * a video would carry one. The attribute rather than the class, because `aria-hidden` is what
 * the rule is — a drawing a screen reader is told nothing about is a drawing the page is not
 * showing anyone (`CampaignIcon.astro`).
 */
const PICTURE_ELEMENTS = new Set(['img', 'picture', 'source', 'svg', 'video'])
function picturesIn(node) {
  return findAll(node, (el) => PICTURE_ELEMENTS.has(el.name) && el.attrs.get('aria-hidden') !== 'true')
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

test('the sections are the spec\'s, in the spec\'s order, and only the ones the data backs', () => {
  for (const { variant, rel } of PAGES) {
    const found = sectionsOf(pageOf(rel).html)
    for (const name of found) {
      assert.ok(SPEC_SECTIONS.includes(name), `${rel} marks a section ${JSON.stringify(name)}, which is not one of the spec's: ${SPEC_SECTIONS.join(', ')} (spec §6)`)
    }
    assert.deepEqual(
      found,
      sectionsFor(variant),
      `${rel} carries the sections ${JSON.stringify(found)}, and the ones its data files back are ${JSON.stringify(sectionsFor(variant))}: the spec fixes the order — hero, the proof number, how it works, social proof, the calls to action — and the page renders the spec's sections in it, without the two a data file has nothing for (spec §6, F6, F11)`,
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

// The band's two states, read off the built page. **The sentinel renders nothing**: not a
// placeholder in the big type, not a sentence saying the figure is not measured yet, not an
// empty band holding the space for one — the page is complete without it, and a notice that a
// number is missing is a page apologising for something the reader did not ask for (the owner's
// feedback of 2026-10-30; F6, G2.1).
test('a variant with no published figure renders no proof band, and no page says one is unmeasured', () => {
  for (const { variant, rel } of PAGES) {
    const { html } = pageOf(rel)
    const band = findAll(parse(html), (el) => el.attrs.get('data-section') === 'proof')
    if (variant.proofNumber.value === SENTINEL) {
      assert.equal(
        band.length,
        0,
        `${rel} renders ${band.length} proof section(s) while its variant's figure is the sentinel: a variant with no published figure renders nothing for it — no placeholder, no apology and no empty box where the band would be (F6)`,
      )
    } else {
      assert.equal(band.length, 1, `${rel} marks ${band.length} proof sections; the band the spec asks for is one`)
      assert.ok(textOf(band[0]).includes(variant.proofNumber.value), `${rel}'s proof band does not print the figure its variant names (${JSON.stringify(variant.proofNumber.value)})`)
    }
    for (const word of ['unmeasured', 'not measured']) {
      assert.ok(
        !new RegExp(word, 'i').test(html),
        `${rel} carries the text ${JSON.stringify(word)}: a variant whose figure is not published renders no notice about it, and the page a visitor is served says nothing about a figure that is not there (the owner's feedback of 2026-10-30; F6)`,
      )
    }
  }
})

// The other half of that rule, and the reason it is a rule about the band rather than about a
// page that lost it: a variant whose figure `typical-costs.yaml` backs DOES render the band.
// The shipped file carries the sentinel on all four variants, so the only way to read that
// render is a copy that names a figure — this case builds one, with `/scale` carrying the `task`
// row's figure (396 runs, above site.yaml's minimum of 20), and reads the band on it beside the
// three pages that stayed sentinel.
test('a variant whose figure is published renders the band, with the entry it was drawn from', async (t) => {
  const FIGURE = '0.36'
  const SOURCE = 'task'
  const built = buildCopy(t, ({ site }) => {
    const file = path.join(site, 'data', 'landing.yaml')
    const before = readFileSync(file, 'utf8')
    // The variants are told apart by their own `path:` lines, so the block the figure is written
    // into is cut out by that marker rather than by the keys around it: the figure lands on
    // /scale and on no other page, whatever else the variant carries.
    const at = before.indexOf('  - path: /scale\n')
    assert.notEqual(at, -1, 'the fixture found no /scale variant in the copy, so no case below could read the band')
    const next = before.indexOf('\n  - path: ', at + 1)
    const end = next === -1 ? before.length : next
    const block = before.slice(at, end)
    const patched = block.replace(
      '      value: unmeasured\n      source: unmeasured\n',
      `      value: "${FIGURE}"\n      source: ${SOURCE}\n`,
    )
    assert.notEqual(patched, block, 'the fixture did not give /scale a figure in the copy, so no case below could read the band')
    writeFileSync(file, before.slice(0, at) + patched + before.slice(end))
  })
  assert.equal(built.status, 0, `the campaign build failed with a figure named:\n${built.output}`)

  const copyPage = (rel) => {
    const page = builtPages(built.dist).find((entry) => entry.rel === rel)
    assert.ok(page, `the copy built no ${rel}`)
    return parse(page.html)
  }

  const band = findAll(copyPage('scale/index.html'), (el) => el.attrs.get('data-section') === 'proof')
  assert.equal(band.length, 1, `/scale renders ${band.length} proof sections while its variant names the measured figure ${FIGURE}: the band is where a backed figure is published, and a variant that has one renders it (F6)`)
  const heading = findAll(band[0], 'h2')
  assert.equal(heading.length, 1, `/scale's proof band carries ${heading.length} headings; the figure is the band's one heading`)
  assert.equal(textOf(heading[0]).trim(), FIGURE, `/scale's proof band is headed ${JSON.stringify(textOf(heading[0]).trim())}, and the figure website/data/landing.yaml names is ${JSON.stringify(FIGURE)}: the band prints the file's figure and types none of its own (F6, G2.1)`)
  assert.ok(
    textOf(band[0]).includes(SOURCE),
    `/scale's proof band does not name ${JSON.stringify(SOURCE)}, the entry of website/data/typical-costs.yaml the figure was drawn from: the band names its source rather than restating the measurement (F6, D96)`,
  )

  for (const rel of ['save/index.html', 'control/index.html', 'crew/index.html']) {
    assert.equal(
      findAll(copyPage(rel), (el) => el.attrs.get('data-section') === 'proof').length,
      0,
      `${rel} renders a proof band while its variant still carries the sentinel: a variant with no figure renders nothing for it, whatever another variant's page does (F6)`,
    )
  }
})

// ---- 4. the calls to action ---------------------------------------------------------------

test('there is no email form, and the calls to action are a plain booking link, the repository and the chat', () => {
  for (const { variant, rel } of PAGES) {
    const { html } = pageOf(rel)
    const doc = parse(html)

    assert.equal(findAll(doc, 'form').length, 0, `${rel} carries a form: the spec's calls to action are the booking link and the repository, and there is no waitlist and no email field anywhere on the page (spec §6)`)
    assert.equal(findAll(doc, (el) => el.name === 'input' || el.name === 'textarea' || el.name === 'select').length, 0, `${rel} carries an input: the page asks a visitor for nothing (spec §6)`)
    assert.ok(!/mailto:/i.test(html), `${rel} carries a mailto: link, and the page asks for no address (spec §6)`)

    // The embed is G2.3's, behind the campaign consent; the link is this page's, and it is the
    // one a visitor who declines that consent keeps.
    assert.equal(findAll(doc, 'iframe').length, 0, `${rel} carries an iframe: the Calendly embed is G2.3's and loads only after the marketing consent (D401)`)
    assert.ok(!/calendly\.com\/(assets|widgets?|embed)/i.test(html), `${rel} names a Calendly embed asset: the booking call to action here is a plain link (G2.2, G2.3)`)

    // The booking link is on the page twice since the owner's feedback of 2026-10-30 (finding
    // 1): once in the hero, so the first screen has an action, and once in this closing band,
    // which is where the three calls to action belong. Both are the file's one link.
    const demo = findAll(doc, (el) => el.name === 'a' && textOf(el).trim() === 'Book a demo')
    assert.equal(
      demo.length,
      2,
      `${rel} carries ${demo.length} "Book a demo" links; the page offers the booking in the hero and in the closing calls to action, and both are the campaign's one booking link`,
    )
    for (const link of demo) {
      assert.equal(
        link.attrs.get('href'),
        LANDING.calendlyURL,
        `${rel}'s booking link is ${JSON.stringify(link.attrs.get('href'))}, and website/data/landing.yaml writes ${LANDING.calendlyURL}: the link is the file's, with no query of its own (the UTMs G2.4 passes through are added at the link, F22)`,
      )
    }

    const install = findAll(doc, (el) => el.name === 'a' && textOf(el).trim() === 'Install free')
    assert.equal(install.length, 1, `${rel} carries ${install.length} "Install free" links; the secondary call to action is the repository (spec §6)`)
    assert.equal(install[0].attrs.get('href'), REPOSITORY, `${rel}'s install link is ${JSON.stringify(install[0].attrs.get('href'))}, and site.yaml names the repository (${REPOSITORY})`)

    // The third call to action: the chat the spec's §6 asks for, and the one link a visitor can
    // use without leaving a booking. Its address is the loader's — the file's number and this
    // variant's own pre-fill — so a page that typed one would be a second copy of the file.
    const chat = findAll(doc, (el) => el.name === 'a' && textOf(el).trim() === 'Chat with the founder')
    assert.equal(chat.length, 1, `${rel} carries ${chat.length} "Chat with the founder" links; the third call to action is the WhatsApp chat (spec §6)`)
    assert.equal(
      chat[0].attrs.get('href'),
      variant.whatsappURL,
      `${rel}'s chat link is ${JSON.stringify(chat[0].attrs.get('href'))}, and landing.yaml's WhatsApp number with this variant's own pre-fill makes ${variant.whatsappURL}: the link is the file's, and nothing here adds a query to it`,
    )

    // The two ids are how `site.js` finds the two secondary calls to fire their key events
    // (G2.4): the markup is where the hook lives, so it is pinned here rather than only in the
    // case that presses them.
    for (const [id, link] of [['campaign-install', install[0]], ['campaign-whatsapp', chat[0]]]) {
      assert.equal(link.attrs.get('id'), id, `${rel}'s ${textOf(link).trim()} link carries no id="${id}", which is the one thing site.js reads it by (G2.4)`)
    }
  }
})

// **Each action carries its own small mark** (the owner's feedback of 2026-10-30, D412's finding 1).
// The finding was that the three closing actions read as three lines of a paragraph rather than
// as three things a reader can do, and the marks are the half of the answer a browser gets with
// no script at all: the booking has a calendar beside it, the install a download and the chat a
// bubble, each an inline `<svg>` in `CampaignIcon.astro` rather than an icon font or a fetched
// file, in the control's own colour (`stroke="currentColor"`) so the band paints no second one.
// **The mark is `aria-hidden`, and that is the rule rather than a detail**: the link's own words
// are the meaning, so a reader who is not shown the drawing is owed nothing about it — and the
// link is still an `<a>` to the file's own address with its label unchanged, which is what the
// case above holds from the other side. What is *not* read here is the movement: the 2 px lift
// on each of the four plates and the 2 px nudge on each of the three marks are facts about the
// cascade, and `website/test/base-styles.mjs`'s campaign-motion case reads all seven — where
// they are written, inside `prefers-reduced-motion: no-preference` and inside the site's
// pointer query, so a reader who asked their system for less motion is served the band still.
const CAMPAIGN_ICON = (el) => el.name === 'svg' && (el.attrs.get('class') ?? '').split(/\s+/).includes('campaign-icon')

test('each call to action carries its own aria-hidden mark, and stays a link', () => {
  // The three actions, in the band's own order, with the drawing each one is: a calendar for
  // the booking, a download for the install and a bubble for the chat.
  const marks = [
    ['Book a demo', 'calendar'],
    ['Install free', 'download'],
    ['Chat with the founder', 'chat'],
  ]

  for (const { rel } of PAGES) {
    const doc = parse(pageOf(rel).html)
    const calls = findAll(doc, (el) => el.attrs.get('data-section') === 'calls-to-action')
    assert.equal(calls.length, 1, `${rel} marks ${calls.length} calls-to-action sections, so the band this case reads is not the page's own`)

    // Three links, and no fourth: the booking widget's frame is a `div`, the band's heading and
    // line are type, and an anchor here that is not one of the three actions is a third thing to
    // press on a field that offers three.
    const links = findAll(calls[0], (el) => el.name === 'a')
    assert.equal(
      links.length,
      3,
      `${rel}'s closing band carries ${links.length} link(s) (${links.map((el) => JSON.stringify(textOf(el).trim())).join(', ')}): the three calls to action are the booking, the install and the chat (spec §6)`,
    )

    for (const [label, icon] of marks) {
      const link = links.find((el) => textOf(el).trim() === label)
      assert.ok(
        link,
        `${rel}'s closing band carries no ${JSON.stringify(label)} link: the spec's three calls to action are a real link each, so a reader who declines the marketing consent still has a way to book, to install and to chat (spec §6, F17, D401)`,
      )
      assert.equal(link.name, 'a', `${rel}'s ${JSON.stringify(label)} action is a <${link.name}>: it is a link, not a control a script has to wire up`)
      assert.ok((link.attrs.get('href') ?? '').trim() !== '', `${rel}'s ${JSON.stringify(label)} action carries no href, so it is not a link anywhere`)

      const found = findAll(link, CAMPAIGN_ICON)
      assert.equal(
        found.length,
        1,
        `${rel}'s ${JSON.stringify(label)} action carries ${found.length} mark(s): the owner asked each of the three closing actions for one small icon beside its label (the owner's feedback of 2026-10-30, D412's finding 1)`,
      )
      const [mark] = found
      assert.equal(
        mark.attrs.get('aria-hidden'),
        'true',
        `${rel}'s ${JSON.stringify(label)} mark is ${JSON.stringify(mark.attrs.get('aria-hidden'))} to a screen reader: the link's own words are the meaning, and a mark announced beside them is the same fact read twice (the owner's feedback of 2026-10-30, D412's finding 1)`,
      )
      assert.equal(mark.attrs.get('focusable'), 'false', `${rel}'s ${JSON.stringify(label)} mark is focusable, and a decorative drawing is not something a keyboard should land on`)
      assert.equal(
        mark.attrs.get('data-icon'),
        icon,
        `${rel}'s ${JSON.stringify(label)} action carries the ${JSON.stringify(mark.attrs.get('data-icon'))} mark: the owner named the three — a calendar for the booking, a download for the install and a bubble for the chat (the owner's feedback of 2026-10-30, D412's finding 1)`,
      )

      // Inline, in the control's own colour: no icon font, no fetched file, and no host. The
      // drawing is shapes in the page (`<path>`, `<rect>`, `<circle>`), so an element that
      // references something — `<use href>`, `<image>`, a font — would be the second origin the
      // four pages may not have (F14).
      assert.equal(mark.attrs.get('stroke'), 'currentColor', `${rel}'s ${JSON.stringify(label)} mark is stroked in ${JSON.stringify(mark.attrs.get('stroke'))}: the band paints one colour, so the mark is drawn in the control's own`)
      assert.equal(mark.attrs.get('fill'), 'none', `${rel}'s ${JSON.stringify(label)} mark is filled with ${JSON.stringify(mark.attrs.get('fill'))}: the site's marks are line drawings (the owner's feedback of 2026-10-30, D412's finding 1)`)
      assert.deepEqual(
        findAll(mark, (el) => ['use', 'image', 'foreignObject'].includes(el.name)).map((el) => el.name),
        [],
        `${rel}'s ${JSON.stringify(label)} mark references something rather than drawing it: the marks are inline shapes, so nothing is fetched and no origin is added (F14)`,
      )

      // The mark leads the label: the link is one control whose first thing is the drawing, so a
      // reader meets the two in the order the design puts them.
      const [first] = walk(link)
      assert.equal(first?.name, 'svg', `${rel}'s ${JSON.stringify(label)} action draws <${first?.name}> before its mark, and the mark is the first thing in the control`)
    }

    // The hero's booking is the same link and may share the same style (the owner's own
    // allowance), so it carries the same calendar mark: the two are one action offered twice,
    // and a reader who meets the first screen gets the mark there too.
    const hero = findAll(doc, (el) => el.attrs.get('data-section') === 'hero')[0]
    const booking = findAll(hero, (el) => el.name === 'a').find((el) => textOf(el).trim() === 'Book a demo')
    assert.ok(booking, `${rel}'s hero carries no booking link, so nothing here reads the mark it shares with the closing band`)
    assert.deepEqual(
      findAll(booking, CAMPAIGN_ICON).map((el) => [el.attrs.get('data-icon'), el.attrs.get('aria-hidden')]),
      [['calendar', 'true']],
      `${rel}'s hero booking carries ${JSON.stringify(findAll(booking, CAMPAIGN_ICON).map((el) => el.attrs.get('data-icon')))}: the two booking links are one action, and the hero's shares the closing band's mark (the owner's feedback of 2026-10-30, D412's finding 1)`,
    )
  }
})

// **The first screen has an action** (the owner's feedback of 2026-10-30, finding 1). The
// finding was read off the live preview: the hero promised something, drew a small box beside
// the promise and put the only way to act at the bottom of the page. The booking link is now in
// the hero as well, under the supporting line and above the picture, so a visitor who reads the
// promise at 1440x900 or at 390x844 — the consent bar counted, it sits above the header — has
// something to press without scrolling. The closing band keeps its own, because that is where
// the three calls to action belong and a reader who reaches the end of the page should not have
// to scroll back up.
test('the hero carries the booking action, above the picture and before every other section', () => {
  for (const { rel } of PAGES) {
    const doc = parse(pageOf(rel).html)
    const sections = findAll(doc, (el) => el.attrs.has('data-section'))
    assert.equal(sections[0].attrs.get('data-section'), 'hero', `${rel} opens with ${JSON.stringify(sections[0].attrs.get('data-section'))} rather than the hero, so the action below is not on the first screen`)

    const hero = sections[0]
    const anchors = findAll(hero, (el) => el.name === 'a')
    assert.equal(anchors.length, 1, `${rel}'s hero carries ${anchors.length} links; the first screen's one action is the booking`)
    assert.equal(
      textOf(anchors[0]).trim(),
      'Book a demo',
      `${rel}'s hero action is ${JSON.stringify(textOf(anchors[0]).trim())}: the first screen's one action is the booking, and it is a real link the markup holds rather than a script's`,
    )

    // The order a phone shows and a screen reader hears: the promise, then the action, then the
    // picture. The picture is the biggest thing in the hero and it may not be the reason the
    // booking link is below the fold.
    // The icon the action carries is part of the action — it is inside the `<a>` — so it is read
    // as one element with it rather than as a fourth thing the hero draws (the owner's feedback
    // of 2026-10-30, D412's finding 1).
    const order = [...walk(hero)]
      .filter((el) => ['h1', 'a', 'img', 'svg'].includes(el.name))
      .map((el) => el.name)
    assert.deepEqual(
      order,
      ['h1', 'a', 'svg', 'img'],
      `${rel}'s hero draws ${JSON.stringify(order)}: the headline first, then the booking action — its icon inside it — and then the picture, so the action sits directly under the supporting line and above the picture at every width (the owner's feedback of 2026-10-30)`,
    )

    const calls = findAll(doc, (el) => el.attrs.get('data-section') === 'calls-to-action')
    assert.equal(calls.length, 1, `${rel} marks ${calls.length} calls-to-action sections`)
    assert.equal(
      findAll(calls[0], (el) => el.name === 'a' && textOf(el).trim() === 'Book a demo').length,
      1,
      `${rel}'s closing band carries no "Book a demo" link of its own: the three calls to action are that band's, and the hero's copy is the one for the first screen`,
    )
  }
})

// **No icon on top of a step** (the owner's feedback of 2026-10-30, finding 4). Each step was a
// small column with a 28 px line icon above its heading, three of them in a row — a mark for a
// heading that already says the same thing, and the reason the section read as three identical
// cards rather than as one composition across the page. The headings and the lines are the
// section now, and the layout is `base-styles.mjs`'s reading: the steps are as wide as the page.
test('no picture sits on top of a step, and every step keeps its heading and its line', () => {
  for (const { rel } of PAGES) {
    const doc = parse(pageOf(rel).html)
    const steps = findAll(doc, (el) => el.attrs.get('data-section') === 'how-it-works')
    assert.equal(steps.length, 1, `${rel} marks ${steps.length} how-it-works sections; the spec's section 3 is one`)
    assert.equal(
      findAll(steps[0], 'svg').length,
      0,
      `${rel}'s how-it-works section draws ${findAll(steps[0], 'svg').length} picture(s): the step's own heading says what the step is, and a mark on top of it is a second thing to read for one fact (the owner's feedback of 2026-10-30)`,
    )
    assert.equal(findAll(steps[0], 'img').length, 0, `${rel}'s how-it-works section carries an image, and the step is words`)

    const items = findAll(steps[0], (el) => el.name === 'li')
    assert.equal(items.length, 3, `${rel}'s how-it-works section lists ${items.length} steps; the spec's three are three`)
    for (const item of items) {
      assert.equal(findAll(item, (el) => el.name === 'h3').length, 1, `${rel} carries a step with ${findAll(item, (el) => el.name === 'h3').length} headings: each step is named once, and the name is what a screen reader lists the steps by`)
      assert.equal(findAll(item, (el) => el.name === 'p').length, 1, `${rel} carries a step with ${findAll(item, (el) => el.name === 'p').length} lines: each step is one heading and one line`)
    }
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
    // Every picture on the page is one the data file names: the site's own wordmark, the
    // variant's console screen (named in `landing.yaml`, described in `console-screens.yaml`) and
    // — when the file carries them — a variant's media and the partners' logos. A picture the file
    // does not name is a logo or a photograph nobody gave permission for, which is what F11 and
    // F12 refuse and what the empty `partner_logos` above is the state of today.
    const named = new Set([
      '/logo.png',
      ...PAGES.flatMap(({ variant }) => [variant.image, variant.screenshot?.src]),
      ...LANDING.partnerLogos.map((logo) => logo.image),
    ].filter(Boolean))
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

// The other half of the media rule (G2.2's review finding, G2.3): a variant's `video` is a
// fact the loader validated and the template never rendered, which is a promise nothing keeps.
// The repository carries no video, so the only way to see the render is a copy that names one:
// this case builds two — a variant whose file is under `website/public/` and one whose file is
// not — and reads both pages, because the rule is "when it is set" AND "complete when the file
// is missing", and a case that only built the first would pass on a page that carried a player
// with nothing behind it.
test('a variant that names a video renders it in the hero, and one whose file is missing renders without it', async (t) => {
  const VIDEO = '/videos/save.mp4'
  const built = buildCopy(t, ({ site }) => {
    const file = path.join(site, 'data', 'landing.yaml')
    const before = readFileSync(file, 'utf8')
    const after = before
      .replace('  - path: /save\n', `  - path: /save\n    video: ${VIDEO}\n`)
      .replace('  - path: /crew\n', '  - path: /crew\n    video: /videos/crew.mp4\n')
    assert.notEqual(after, before, 'the fixture did not name a video in the copy, so no case below could read one')
    writeFileSync(file, after)
    mkdirSync(path.join(site, 'public', 'videos'), { recursive: true })
    writeFileSync(path.join(site, 'public', 'videos', 'save.mp4'), 'not really a video, and the build goes on')
  })
  assert.equal(built.status, 0, `the campaign build failed with a video named:\n${built.output}`)

  const save = builtPages(built.dist).find((entry) => entry.rel === 'save/index.html')
  assert.ok(save, 'the copy built no /save page')
  const videos = findAll(parse(save.html), 'video')
  assert.equal(videos.length, 1, `/save renders ${videos.length} <video> elements although its variant names one video: the variant's media is rendered, once`)
  assert.equal(
    videos[0].attrs.get('src'),
    VIDEO,
    `/save's video is ${JSON.stringify(videos[0].attrs.get('src'))}, and the variant names ${VIDEO}: the file's path is what the page fetches, and nothing rewrites it`,
  )
  assert.ok(servedFile(built.dist, VIDEO) !== null, `/save names ${VIDEO} and the copy does not serve it, so the page carries a request for nothing`)
  // The hero: the media slot is inside `main` and ahead of the words, so a reader meets the
  // campaign's own file first and the headline follows it.
  const order = [...save.html.matchAll(/<(main|video|h1)\b/g)].map((m) => m[1])
  assert.deepEqual(
    order.slice(0, 3),
    ['main', 'video', 'h1'],
    `the /save page draws ${JSON.stringify(order.slice(0, 3))}; the video is the campaign's own and sits at the top of the hero, above the headline (F14)`,
  )
  // The hero's own picture stands down for the variant that brought its own media: the owner's
  // file is what the top of that page is for, and two pictures of the same campaign would be one
  // too many (D409's and D411's rows, F14). Both halves of the picture are read — the console screen the
  // variant names and the drawing a variant with no screen would fall back to — because `art` is
  // the one behaviour no shipped page can read: every variant in website/data/landing.yaml names
  // neither a video nor an image, so only this copy, where one does, reaches the prop's false half.
  const saveHero = findAll(parse(save.html), (el) => el.attrs.get('data-section') === 'hero')
  assert.equal(saveHero.length, 1, 'the copy built no /save hero to read the picture off')
  const savePicture = picturesIn(saveHero[0])
  assert.equal(
    savePicture.length,
    0,
    `/save renders ${savePicture.length} hero picture(s) although its variant names a video: a variant with media of its own takes the media slot and the hero draws nothing beside it (F14, G2.5)`,
  )

  // The variant whose file the owner has not produced: no player, and the page is otherwise
  // whole — its own headline, its own calls to action.
  const crew = builtPages(built.dist).find((entry) => entry.rel === 'crew/index.html')
  assert.ok(crew, 'the copy built no /crew page')
  assert.equal(
    findAll(parse(crew.html), 'video').length,
    0,
    '/crew names a video the repository does not carry and renders a <video> anyway: a player with nothing behind it is the broken element a visitor reached from an ad sees (F14, G2.5)',
  )
  const headline = findAll(parse(crew.html), 'h1').map((el) => textOf(el).trim())
  assert.deepEqual(headline, [LANDING.variants.find((one) => one.name === 'crew').headline], '/crew lost its headline along with the missing video, so the page is not complete without the file')
  // Two, since the owner's feedback of 2026-10-30 (finding 1): the hero's own booking action and
  // the closing band's. The video this variant names is not served, so both halves of the page
  // are what it has to keep.
  assert.equal(
    findAll(parse(crew.html), (el) => el.name === 'a' && textOf(el).trim() === 'Book a demo').length,
    2,
    '/crew lost a booking link along with the missing video: the page keeps the hero action and the closing band without the file it names (F14, G2.5)',
  )
  // And the picture is there: `/crew` names a video the repository does not carry, so the variant
  // has no media the loader can report and the hero draws the console screen the variant names —
  // the same picture the four shipped pages take today, reached here by a variant that did name a
  // file (F14, G2.5).
  const crewHero = findAll(parse(crew.html), (el) => el.attrs.get('data-section') === 'hero')
  assert.equal(crewHero.length, 1, 'the copy built no /crew hero to read the picture off')
  // Read inside the hero: the screen this variant names is one of the console's, and the slider
  // below the hero shows it too (the owner's feedback of 2026-10-30, D412's finding 3), so the page's
  // own count of that file is two and the hero's is one.
  const crewPicture = picturesIn(crewHero[0]).filter((el) => el.attrs.get('src') === LANDING.variants.find((one) => one.name === 'crew').screenshot.src)
  assert.equal(
    crewPicture.length,
    1,
    '/crew\'s hero draws no picture although the video it names is not served: a variant whose media the build does not hold renders the page complete, with the console screen its claim is proved by in the hero rather than a hole (F14, G2.5)',
  )
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
    for (const selector of ['.campaign-steps', '.campaign-calls', '.campaign-hero-shot']) {
      assert.ok(
        rules.some((rule) => (rule.conditions ?? []).length > 0 && rule.selector.includes(selector)),
        `${rel} declares ${selector} at one width only: the sections stack on a phone and sit in a row on a wide viewport, and the gates have no browser — this is the static half, and the rendered reading is G2.5's (spec §6)`,
      )
    }
  }
})

// ---- 7. the hero's own picture (the owner's instruction of 2026-10-30; F14, D190, G2.5) -----

/** The hero band of one built page. */
function heroOf(rel, dist = DIST) {
  const { html } = pageOf(rel, dist)
  const hero = findAll(parse(html), (el) => el.attrs.get('data-section') === 'hero')
  assert.equal(hero.length, 1, `${rel} marks ${hero.length} hero sections; the page has one`)
  return { html, hero: hero[0] }
}

/**
 * A build of the repository with the four variants' screenshots left out of the data file: on
 * that copy the campaign's own drawing is the hero's picture, which is the state the three cases
 * below read. The shipped file names a screen on every variant, so the fallback is reached by no
 * page the repository builds — and a fallback nothing reads is a fallback nothing keeps.
 */
function withoutScreenshots(t) {
  const built = buildCopy(t, ({ site }) => {
    assert.ok(dropCampaignScreenshots(site), 'the fixture found no screenshots to take out of the copy\'s website/data/landing.yaml, so the state below is not the one it means to build')
  })
  assert.equal(built.status, 0, `the campaign build failed with no screenshot named:\n${built.output}`)
  return built.dist
}

test('the hero shows the console screen its variant names, once, with the row\'s own words and size', () => {
  // The console page's own slider, as the set of screens the site serves: the campaign heroes
  // draw from the same four files and add none of their own (the four are `console-screens.yaml`'s
  // rows, and the hero's `src` is the row's).
  const consolePage = pageOf('console/index.html')
  const served = new Set(findAll(parse(consolePage.html), (el) => el.name === 'img').map((el) => el.attrs.get('src')))

  for (const { variant, rel } of PAGES) {
    const { hero } = heroOf(rel)
    const screen = variant.screenshot
    assert.ok(
      screen,
      `${rel}'s variant names no screenshot: the hero's picture is a real screen of the console, and every variant in website/data/landing.yaml names the one its claim is proved by (the owner's instruction of 2026-10-30)`,
    )

    // One picture, and it is the screen: the drawing is the fallback for a variant that names
    // none, so the two never share a hero.
    const pictures = picturesIn(hero)
    assert.equal(
      pictures.length,
      1,
      `${rel}'s hero carries ${pictures.length} picture(s): the hero's picture is one, and it is the console screen the variant names — the drawing stands down for it rather than sitting beside it (the owner's instruction of 2026-10-30)`,
    )
    assert.equal(pictures[0].name, 'img', `${rel}'s hero's one picture is a <${pictures[0].name}>: the variant names a screenshot, so the picture is that file and not the campaign's drawing (the owner's instruction of 2026-10-30)`)
    const img = pictures[0]

    // The bytes are the site's own: a path the build already served for the console page, on no
    // other origin, and no second copy of the file for the campaign (F14, D190).
    assert.equal(
      img.attrs.get('src'),
      screen.src,
      `${rel}'s hero shows ${JSON.stringify(img.attrs.get('src'))}, and the screen the variant names is served at ${JSON.stringify(screen.src)}: the hero fetches the row's own file and adds none`,
    )
    assert.ok(!/^[a-z][a-z0-9+.-]*:|^\/\//i.test(img.attrs.get('src')), `${rel}'s hero asks for its picture at ${JSON.stringify(img.attrs.get('src'))}, which is another origin: the campaign pages fetch the screens the site already serves and admit no new host (F14, F20)`)
    assert.ok(servedFile(DIST, screen.src) !== null, `${rel}'s hero shows ${screen.src}, which the build does not serve, so the page carries a request for nothing (F14)`)
    assert.ok(
      served.has(screen.src),
      `${rel}'s hero shows ${screen.src}, and the console page's own slider does not: the campaigns' screens are the console's four, by their existing names, rather than pictures the campaign pages bring with them (D190)`,
    )

    // What describes the picture is the data file's, once: the alt text a reader who is not shown
    // the picture gets, the caption under it, and the pixels that reserve its box before its bytes
    // land — the same row the console page renders, so the two pages cannot describe one file
    // differently (D190).
    assert.equal(img.attrs.get('alt'), screen.alt, `${rel}'s hero picture is described as ${JSON.stringify(img.attrs.get('alt'))}, and website/data/console-screens.yaml describes ${screen.file} as ${JSON.stringify(screen.alt)}: one picture has one alt text, and the campaign page carries no second copy of the sentence (D190)`)
    assert.equal(img.attrs.get('width'), String(screen.width), `${rel}'s hero picture declares no width of the row's own (${screen.width}): the two attributes are what reserve the picture's box before its bytes land, and the page shifts as it arrives without them`)
    assert.equal(img.attrs.get('height'), String(screen.height), `${rel}'s hero picture declares no height of the row's own (${screen.height}): the two attributes are what reserve the picture's box before its bytes land, and the page shifts as it arrives without them`)

    // The picture is the hero's, so it is fetched with the page rather than when it comes near the
    // viewport, and it is what a reader on paid traffic came for.
    assert.equal(img.attrs.get('loading'), 'eager', `${rel}'s hero picture is not fetched with the page (loading=${JSON.stringify(img.attrs.get('loading'))}): the hero's picture is the first screen's, so it is not deferred until it scrolls near (the owner's instruction of 2026-10-30)`)
    assert.equal(img.attrs.get('fetchpriority'), 'high', `${rel}'s hero picture carries no fetchpriority="high": the campaign pages are built for paid traffic, and the picture the page is about is the one to ask for first (the owner's instruction of 2026-10-30)`)

    // The figure is the site's hero-picture slot, which is what takes a hero's picture out of the
    // page's opening prose: the screen's alt text is the console's own and is written for a reader
    // who is not shown the picture, and `budget.mjs`'s lead reader and `fold.mjs`'s rule 1 both
    // read this class to leave it out of the lead (`CampaignHero.astro`).
    const figures = findAll(hero, (el) => el.name === 'figure')
    assert.equal(figures.length, 1, `${rel}'s hero carries ${figures.length} figures; the picture and its caption are one`)
    assert.deepEqual(
      (figures[0].attrs.get('class') ?? '').split(/\s+/),
      ['campaign-hero-shot', 'hero-shot'],
      `${rel}'s hero figure is classed ${JSON.stringify(figures[0].attrs.get('class'))}: the campaign's own class and the site's \`hero-shot\`, which is the one the lead readers take a hero's picture out of a page's opening by — without it the screen's alt text would be counted as the campaign's lead (D188)`,
    )
    const captions = findAll(figures[0], (el) => el.name === 'figcaption')
    assert.equal(captions.length, 1, `${rel}'s hero picture carries ${captions.length} captions; the screen's own line is one`)
    assert.equal(textOf(captions[0]).trim(), screen.caption, `${rel}'s hero picture is captioned ${JSON.stringify(textOf(captions[0]).trim())}, and the row for ${screen.file} carries ${JSON.stringify(screen.caption)}: the caption is the data file's, and the page types none of its own (D190)`)

    assert.equal(
      findAll(hero, (el) => el.name === 'svg' && (el.attrs.get('class') ?? '').split(/\s+/).includes('campaign-hero-art')).length,
      0,
      `${rel}'s hero draws the campaign's own illustration as well as the console screen: the screen replaces the drawing, and a hero with two pictures has one too many (the owner's instruction of 2026-10-30)`,
    )
  }
})

/** The hero band of one built page, and the illustration it draws (a page built without a
 * screenshot named, `withoutScreenshots()` above). */
function artOf(rel, dist = DIST) {
  const { html, hero } = heroOf(rel, dist)
  const drawings = findAll(hero, (el) => el.name === 'svg' && (el.attrs.get('class') ?? '').split(/\s+/).includes('campaign-hero-art'))
  assert.equal(
    drawings.length,
    1,
    `${rel} draws ${drawings.length} hero illustrations although its variant names no screenshot: the campaign's own drawing is the hero's picture for a variant that names none, and it is the hero's own (the owner's feedback of 2026-10-30, and the fallback the owner's instruction of 2026-10-30 keeps)`,
  )
  return { html, hero, svg: drawings[0] }
}

/**
 * The drawing's own markup, as the page wrote it: from the `<svg>` the hero carries to its
 * closing tag, so the rules below read the picture and not the page around it.
 */
function artMarkup(html) {
  const tokens = tokenize(html)
  const at = tokens.findIndex(
    (t) => t.kind === 'start' && t.name === 'svg' && (t.attrs.get('class') ?? '').split(/\s+/).includes('campaign-hero-art'),
  )
  assert.notEqual(at, -1, 'the page carries no `<svg class="campaign-hero-art">`, so there is no drawing to read')
  let depth = 0
  for (let i = at; i < tokens.length; i += 1) {
    const t = tokens[i]
    if (t.kind === 'start' && t.name === 'svg' && !t.selfClosing) depth += 1
    else if (t.kind === 'end' && t.name === 'svg') {
      depth -= 1
      if (depth === 0) return html.slice(tokens[at].at, t.at + '</svg>'.length)
    }
  }
  return assert.fail('the hero illustration is never closed, so its markup cannot be read')
}

test('the hero draws the campaign\'s picture inline, named, and with no request of its own', async (t) => {
  const dist = withoutScreenshots(t)
  const drawings = new Set()
  for (const { variant, rel } of PAGES) {
    const { html, svg } = artOf(rel, dist)

    // Inline SVG, so nothing is fetched: no picture file, no icon font, no raster asset. The
    // markup is read whole because a drawing can make a request through an element this file
    // does not name — an `<image>`, a `<use>` pointing elsewhere, a CSS `url()` in a filter.
    const markup = artMarkup(html)
    for (const guilty of [/<image\b/i, /<use\b/i, /feImage/i, /\bhref\s*=/i, /\bsrc\s*=/i, /url\(/i, /https?:/i]) {
      assert.ok(
        !guilty.test(markup),
        `${rel}'s hero illustration carries ${JSON.stringify(guilty.source)}: the picture is drawn in the markup and fetches nothing — no image file, no external host and no icon set (F14, the owner's feedback of 2026-10-30)`,
      )
    }

    // A reader who is not shown the picture is told what it is, once: the site's own rule for a
    // drawing the page means something by (`a11y.mjs`).
    const label = (svg.attrs.get('aria-label') ?? '').trim()
    assert.equal(svg.attrs.get('role'), 'img', `${rel}'s hero illustration has no role="img": the picture says what the page is about, so it is announced as one image rather than read out as a shape (a11y.mjs)`)
    assert.ok(label !== '', `${rel}'s hero illustration carries no aria-label: a drawing with role="img" and no name is a picture a screen reader can announce nothing about (a11y.mjs)`)
    assert.ok(
      !/\bworkers?\b/i.test(label),
      `${rel}'s hero illustration is named ${JSON.stringify(label)}: "worker" is retired from the site's copy — a run's unit of work is a job (wording.mjs, changes spec item 2)`,
    )

    // No text inside the drawing. A picture made of words is prose the page's word ceiling would
    // have to count, and every word on a campaign page comes from a data file (G2.1, D188).
    assert.equal(
      textOf(svg).trim(),
      '',
      `${rel}'s hero illustration carries text — ${JSON.stringify(textOf(svg).trim())} — and the picture is drawn rather than set in type: the page's words come from website/data/landing.yaml, and a label inside the drawing would be a second copy of one of them (G2.1, D188)`,
    )
    assert.equal(findAll(svg, 'text').length, 0, `${rel}'s hero illustration carries ${findAll(svg, 'text').length} <text> elements, and the drawing is made of shapes rather than of type (D188)`)

    // The variant's own tone, on the picture: the accent and the motif are keyed off it, so the
    // four pages are one drawing with four registers (spec §6).
    assert.equal(svg.attrs.get('data-tone'), variant.tone, `${rel}'s hero illustration carries data-tone=${JSON.stringify(svg.attrs.get('data-tone'))}, and the variant's tone is ${JSON.stringify(variant.tone)}: the drawing is the campaign's, and its accent is the variant's own (the owner's feedback of 2026-10-30)`)
    assert.equal(findAll(svg, (el) => el.attrs.has('data-motif')).map((el) => el.attrs.get('data-motif'))[0], variant.tone, `${rel}'s hero illustration carries no motif of the variant's tone (${JSON.stringify(variant.tone)}): the four campaigns are one picture with a motif each (spec §6)`)

    drawings.add(markup)
  }
  assert.equal(drawings.size, PAGES.length, `the four campaign pages draw ${drawings.size} distinct picture(s): a picture that is the same markup on every page is a drawing with no tone, whatever the attribute above says (the owner's feedback of 2026-10-30)`)
})

test('the hero\'s picture sits under the words on a phone, and every tone paints its own accent', async (t) => {
  const dist = withoutScreenshots(t)
  const TONES = LANDING.variants.map((variant) => variant.tone)
  for (const { rel } of PAGES) {
    const { html } = artOf(rel, dist)
    const doc = parse(html)
    const order = [...walk(doc)].filter((el) => el.name === 'h1' || (el.name === 'svg' && (el.attrs.get('class') ?? '').split(/\s+/).includes('campaign-hero-art')))
    assert.deepEqual(
      order.map((el) => el.name),
      ['h1', 'svg'],
      `${rel} draws the illustration ${JSON.stringify(order.map((el) => el.name))}: the headline comes first and the picture follows it, so a phone — where the hero is one column — reads the promise before anything is drawn under it (the owner's feedback of 2026-10-30)`,
    )

    const rules = cssRules(findAll(doc, (el) => el.name === 'style').map((el) => el.raw ?? '').join('\n'))
    const conditions = (selector) => rules.filter((rule) => rule.selector.includes(selector)).map((rule) => (rule.conditions ?? []).join(' and '))
    // Both spellings of a width: the source writes `min-width: 48rem` and the build's minifier
    // writes the range form, `(width>=48rem)`, so a reader that knows only the first would call
    // a breakpoint that is there absent (the same pair `console.mjs` reads).
    const WIDTHS = {
      'a floor under the width': /min-width\s*:|width\s*>=/,
      'a ceiling on the width': /max-width\s*:|width\s*<=/,
    }
    for (const [what, spelling] of Object.entries(WIDTHS)) {
      assert.ok(
        conditions('.campaign-hero-art').some((condition) => spelling.test(condition)),
        `${rel} declares .campaign-hero-art with ${what} written nowhere: the drawing is read on a phone and on a desktop, and each width's rule is written down rather than inherited (the owner's feedback of 2026-10-30)`,
      )
    }
    // The width rules each say which side of the breakpoint they are on, and at least one of
    // them lays the hero out in more than one column: the filter above is vacuous on a hero that
    // never declares one, so the drawing would be asserted beside the copy by nothing at all.
    // The properties a two-column hero could be written with are all admitted — the point is
    // that the hero becomes two columns somewhere, not that it does it with a grid.
    const TWO_COLUMNS = /grid-template-columns\s*:\s*[^;]*\S|flex-direction\s*:\s*row|columns\s*:\s*[2-9]/
    for (const rule of rules.filter((r) => r.selector.includes('.campaign-hero') && /grid-template-columns/.test(r.body))) {
      assert.ok(
        (rule.conditions ?? []).length > 0,
        `${rel} places the picture beside the copy at every width: the hero is one column until a breakpoint says otherwise, so a phone is never asked to read a headline and a drawing side by side (the owner's feedback of 2026-10-30)`,
      )
    }
    assert.ok(
      rules.some(
        (rule) =>
          rule.selector.includes('.campaign-hero') &&
          (rule.conditions ?? []).some((condition) => /min-width\s*:|width\s*>=/.test(condition)) &&
          TWO_COLUMNS.test(rule.body),
      ),
      `${rel} never lays its hero out in more than one column: the drawing sits beside the copy on a wide viewport rather than under it at every width, and the rule that does it is the wide viewport's own (the owner's feedback of 2026-10-30)`,
    )

    // One accent per tone, and the four are not one colour under four names.
    const accents = TONES.map((tone) => {
      const rule = rules.find((r) => new RegExp(`\\.campaign-hero-art(?:\\[[^\\]]*\\])*\\[data-tone=["']?${tone}["']?\\]`).test(r.selector))
      assert.ok(rule, `${rel}'s stylesheet carries no .campaign-hero-art rule for the ${tone} tone: each variant's picture is painted in the variant's own accent (the owner's feedback of 2026-10-30)`)
      const accent = /--art-accent:\s*([^;]+)/.exec(rule.body)
      assert.ok(accent, `the ${tone} tone's rule sets no --art-accent, so the accent it names is never painted`)
      return accent[1].trim()
    })
    assert.ok(
      accents.every((accent) => /^var\(--[a-z-]+\)$/.test(accent)),
      `${rel} paints the hero's accent with ${JSON.stringify(accents)}: an accent is a palette token, never a colour literal (W1.2)`,
    )
    assert.ok(new Set(accents).size > 1, `${rel} paints all four tones with ${JSON.stringify(accents)}: a tone the picture does not answer is a data attribute nothing reads (the owner's feedback of 2026-10-30)`)
  }
})

// **The picture is flat, and it is framed by nothing** (the owner's feedback of 2026-10-30,
// finding 2). What the first screen showed was a small box: a 1 px border around a panel, thin
// line art inside it and a fraction of the screen spent on the one thing the page is about. The
// drawing is now filled shapes in the palette's own tones — no gradient, no filter, no glow, no
// shadow, and no shape hugging the edge of the viewBox, which is the frame the old drawing drew
// for itself — and the rules below are what keeps the next redraw flat.
//
// The second half is the origin. The drawing is inline SVG precisely so that the picture costs
// no request (the case above), and the styles that dress it may not add one either: a
// `url(…)` — a background image, an SVG filter, a font — is a second origin on four pages built
// for paid traffic, and `origins.mjs` and `privacy.mjs` are the site-wide halves of the same
// rule. Nothing here is a host; what this reads is the local shape of it.
test('the hero\'s picture is drawn flat and framed by nothing, and the campaign styles fetch nothing', async (t) => {
  const dist = withoutScreenshots(t)
  for (const { rel } of PAGES) {
    const { html } = artOf(rel, dist)
    const markup = artMarkup(html)
    for (const guilty of [
      /<linearGradient/i,
      /<radialGradient/i,
      /<filter\b/i,
      /<mask\b/i,
      /feDropShadow/i,
      /drop-shadow/i,
      /filter\s*:/i,
      /box-shadow/i,
    ]) {
      assert.ok(
        !guilty.test(markup),
        `${rel}'s hero illustration carries ${JSON.stringify(guilty.source)}: the drawing is flat filled shapes in the palette's tones — no gradient, no glow and no shadow at rest (the owner's feedback of 2026-10-30)`,
      )
    }

    // Every rectangle is inset from the viewBox, so none of them is the panel the drawing used to
    // sit on: a rect that spans the box is a border around the picture, which is the timid shape
    // the finding names. Four units is the room a rounded corner needs to be seen as one.
    const doc = parse(html)
    const svg = findAll(doc, (el) => el.name === 'svg' && (el.attrs.get('class') ?? '').split(/\s+/).includes('campaign-hero-art'))[0]
    // (`viewBox` is written `viewbox` by the reader, as `caps-figures.mjs` records.)
    const declared = svg.attrs.get('viewbox') ?? ''
    const box = declared.trim().split(/\s+/).map(Number)
    assert.equal(box.length, 4, `${rel}'s hero illustration declares no four-number viewBox (${JSON.stringify(declared)})`)
    const [, , boxW, boxH] = box
    for (const rect of findAll(svg, 'rect')) {
      const [x, y, w, h] = ['x', 'y', 'width', 'height'].map((name) => Number(rect.attrs.get(name) ?? 0))
      assert.ok(
        x >= 4 && y >= 4 && x + w <= boxW - 4 && y + h <= boxH - 4,
        `${rel}'s hero illustration draws a rect at ${JSON.stringify([x, y, w, h])} in a ${boxW}x${boxH} viewBox: the picture has no frame around it, so no shape hugs the edge of the box it is drawn in (the owner's feedback of 2026-10-30)`,
      )
    }

    const css = findAll(parse(html), (el) => el.name === 'style').map((el) => el.raw ?? '').join('\n')
    const campaignRules = cssRules(css).filter((rule) => /\.campaign-[a-z-]/.test(rule.selector ?? ''))
    assert.ok(campaignRules.length > 0, `${rel} carries no campaign rule at all, so nothing below this line is a reading of it`)
    for (const rule of campaignRules) {
      for (const guilty of [/url\(/i, /@import/i]) {
        assert.ok(
          !guilty.test(rule.body),
          `${rel}: the campaign rule "${rule.selector}" carries ${JSON.stringify(guilty.source)}: the campaign pages' own styles fetch nothing — no image, no filter, no font and no second origin (F14, the owner's feedback of 2026-10-30)`,
        )
      }
    }
    assert.equal(
      cssRules(css).filter((rule) => rule.importURL !== undefined).length,
      0,
      `${rel}'s stylesheets carry an @import: every stylesheet on a campaign page is one the build produced and inlined, so nothing is fetched to read the page`,
    )
  }
})

// ---- 8. the two fields of colour, and the corners they are cut at --------------------------

// **The band behind the closing actions was too square** (the owner's feedback of 2026-10-30,
// D412's finding 2). A page whose hero, steps and closing call to action all end in right angles reads
// as a wireframe rather than as the site's own design, and the two fields of colour a campaign
// page paints — the steps' panel and the closing band — were the flat rectangles the finding
// names. Both are now cut at `--radius-xl`, the site's largest corner step
// (`src/styles/tokens.css` draws four radius steps and says in as many words that there is
// deliberately no step above this one), so the page's two fields are one shape and the corner is
// a step every page already carries rather than a number this band invented. A campaign page
// draws exactly these two fields of colour: the hero, the proof band and the slider's own section
// are on the ground, and a third field here would make the page three blocks of tone with
// sections between them (`CampaignScreens.astro` records the same ruling from the other side).
//
// **What the radius may not cost** is the second half, and it is what the finding says: flat
// colour, no gradient and no shadow at rest. A field that answered "too square" with a gradient
// and a drop shadow would be the *other* thing the owner refused on these pages, so neither
// field gains either — the radius is the whole of the change to the box.

/** The page's own rules for one class as the built stylesheet writes it: the class with its
 *  `data-astro-cid` attribute and nothing after it, whether alone or in a selector list. A rule
 *  for a class whose name merely begins with this one (`.campaign-calls-title`,
 *  `.campaign-calls-primary`) is not a rule for the class itself. */
function rulesFor(css, className) {
  const wanted = new RegExp(`(^|,)\\s*${className.replace('.', '\\.')}(\\[data-astro-cid-[^\\]]+\\])?\\s*(,|$)`)
  return cssRules(css).filter((rule) => wanted.test(rule.selector ?? ''))
}

test('the two fields of colour are cut at the site\'s largest radius, flat and unframed', () => {
  for (const { rel } of PAGES) {
    const css = findAll(parse(pageOf(rel).html), (el) => el.name === 'style')
      .map((el) => el.raw ?? '')
      .join('\n')

    for (const [field, className] of [
      ['the closing band', '.campaign-calls'],
      ['the steps\' panel', '.campaign-steps'],
    ]) {
      const own = rulesFor(css, className)
      assert.ok(own.length > 0, `${rel} carries no rule for ${className} at all, so nothing below this line is a reading of ${field}`)

      const radii = own.flatMap((rule) => cssDeclarations(rule.body)).filter(([property]) => property === 'border-radius').map(([, value]) => value)
      assert.deepEqual(
        radii,
        ['var(--radius-xl)'],
        `${rel}'s ${field} is cut at ${JSON.stringify(radii)}: the owner asked the band behind the closing actions for generous corners and for any other panel with the same hard-cornered look to be rounded with it, and the radius is the site's own largest step rather than a number the component invented (the owner's feedback of 2026-10-30, D412's finding 2)`,
      )

      for (const rule of own) {
        for (const refused of [/gradient/i, /box-shadow/i, /drop-shadow/i, /(^|[\s;{])border\s*:/i, /(^|[\s;{])filter\s*:/i]) {
          assert.ok(
            !refused.test(rule.body),
            `${rel}: "${rule.selector}" carries ${JSON.stringify(refused.source)}: the rounded field is the owner's whole change to its box — the two fields stay flat colour, with no gradient, no shadow, no filter and no hairline drawn around them (the owner's feedback of 2026-10-30, D412's finding 2)`,
          )
        }
      }
    }

    // The token rather than the number: the radius the two fields share is the site's own step,
    // so a change to the scale moves both fields and neither has a corner of its own.
    const declared = cssRules(css).flatMap((rule) => cssDeclarations(rule.body)).filter(([property]) => property === '--radius-xl')
    assert.deepEqual(
      declared.map(([, value]) => value),
      ['20px'],
      `${rel} declares --radius-xl as ${JSON.stringify(declared.map(([, value]) => value))}: the two fields are cut at a token of the site's own scale (src/styles/tokens.css), and a page that carried the number instead would be a corner nothing else on the site moves with`,
    )
  }
})

// ---- 9. the console's screens on every campaign page (D412's finding 3, 2026-10-30) --------

// **The finding was "the campaign pages show one screenshot and no way to see the rest".** The
// homepage has had a slider of all of the console's screens since the brief of 2026-10-21, and
// the owner asked for it on all four campaign pages: below the hero and before the closing call
// to action, in a section with its own heading, showing every screen that panel shows. What the
// four pages render is `ScreenshotSlider.astro` — the homepage's own component, and the console
// page's before that — over `website/data/home-screens.yaml` through
// `loadHomeSliderScreens()`, which is the reader the homepage's panel uses. **So there is no
// second slider and no second copy of a picture**: the campaign page's slides are the homepage's
// slides, file for file, `alt` for `alt`, caption for caption and in the file's own order, which
// is what the first case below holds by comparing the two built pages rather than by reading the
// data file twice.
//
// The other three readings are the shape the owner asked for and the three ways a slider fails a
// reader: **a thumb** (a control at a target size, and one slide per width so a swipe moves one
// picture), **a keyboard** (the track is a focusable scroll container with a name, and the dots
// are anchors into their own slides, so nothing about the slider needs a pointer), and **no
// script at all** (every slide is in the document as a list item, so a reader whose browser runs
// no script gets the pictures stacked, and the bar of arrows is emitted `hidden` rather than
// drawn dead). `public/scripts/site.js` drives all of it and needed no change for these pages:
// it finds a slider by `[data-slider]`, so the CSP is untouched and no page loads a second file.

const HOME_SCREENS_YAML = 'website/data/home-screens.yaml'
/** The campaign page's slider, as a `data-slot` a gate can find: the homepage's is `home-screens`. */
const CAMPAIGN_SLIDER_SLOT = 'campaign-screens'

/**
 * The console rows of `website/data/home-screens.yaml`, in the file's order. The reader is
 * `test/lib/screens.mjs`'s, which is the same one `website/test/home.mjs` holds the homepage's
 * panel to; the `fugaro watch` capture — the file's last row, under `public/images/`, and the
 * homepage's hero picture rather than a slide of either slider — is filtered out here because
 * the four campaign pages show the console's screens and that row is not one of them.
 */
function consoleRows() {
  return readScreenRows(HOME_SCREENS_YAML).filter((entry) => entry.dir === 'screenshots')
}

/** The pictures one built slider shows, in the order a reader meets them: the row's file at the
 *  site path the page serves it from, and the words and the size that go with it. */
function slidesOf(html, slot) {
  const doc = parse(html)
  const slider = findAll(doc, (el) => el.attrs.get('data-slot') === slot)[0]
  assert.ok(
    slider,
    `a built page carries no [data-slot="${slot}"]: the slider is what shows these pages the console's screens, and a page without one is a page whose pictures nobody sees (the owner's feedback of 2026-10-30, D412's finding 3)`,
  )
  return findAll(slider, (el) => el.name === 'li' && el.attrs.has('data-slider-slide')).map((slide) => {
    const [img] = findAll(slide, 'img')
    assert.ok(img, `a ${slot} slide carries no <img>, so nothing here is a reading of the picture the slide is for`)
    return {
      src: img.attrs.get('src'),
      alt: img.attrs.get('alt'),
      width: img.attrs.get('width'),
      height: img.attrs.get('height'),
      loading: img.attrs.get('loading'),
      caption: findAll(slide, (el) => el.name === 'figcaption').map(textOf).join(' ').trim(),
    }
  })
}

test('every campaign page shows the console\'s screens in the homepage\'s own slider, in the file\'s order', () => {
  const rows = consoleRows()
  assert.ok(rows.length > 1, `${HOME_SCREENS_YAML} carries ${rows.length} console row(s): a slider of one picture is not a slider, and the owner asked for every screen the homepage shows`)

  // The homepage's own slider, which `website/test/home.mjs` holds to this same file row for
  // row. Reading it here is what makes "the same screens" a comparison between two built pages
  // rather than a promise about two components.
  const homeSlides = slidesOf(pageOf('index.html').html, 'home-screens')
  assert.deepEqual(
    homeSlides.map((slide) => slide.src),
    rows.map((row) => `/${row.dir}/${row.file}`),
    `the homepage's slider shows ${JSON.stringify(homeSlides.map((slide) => slide.src))} and ${HOME_SCREENS_YAML} lists ${JSON.stringify(rows.map((row) => row.file))}: the campaign pages are held to the homepage's slides below, so the homepage's slider has to be the file's rows for that reading to mean anything about the file (the brief of 2026-10-21)`,
  )

  for (const { rel } of PAGES) {
    const { html } = pageOf(rel)
    const doc = parse(html)

    // One slider, and it is a section of its own with a heading of its own: a reader meets it as
    // a named block rather than as pictures that arrived under the section before it.
    const sliders = findAll(doc, (el) => el.attrs.get('data-slot') === CAMPAIGN_SLIDER_SLOT)
    assert.equal(
      sliders.length,
      1,
      `${rel} carries ${sliders.length} [data-slot="${CAMPAIGN_SLIDER_SLOT}"]: the owner asked every landing page for one slideshow of the console's screens (the owner's feedback of 2026-10-30, D412's finding 3)`,
    )
    const [slider] = sliders
    assert.equal(slider.name, 'section', `the campaign slideshow is a <${slider.name}>: it is a section of the page with its own heading, not a div the heading above it happens to cover`)
    assert.ok(
      slider.attrs.has('data-slider'),
      `${rel}'s slideshow carries no \`data-slider\`, and that attribute is how the site's one script finds the block it drives: without it the arrows, the count and the mark on the current dot are bound to nothing`,
    )

    const headings = findAll(slider, (el) => el.name === 'h2')
    assert.equal(headings.length, 1, `${rel}'s slideshow carries ${headings.length} headings: the owner asked for a section with its own heading, and one heading names it`)
    const headingId = headings[0].attrs.get('id')
    assert.ok((headingId ?? '').trim() !== '', `${rel}'s slideshow heading carries no id, so nothing can point at it`)
    assert.equal(
      slider.attrs.get('aria-labelledby'),
      headingId,
      `${rel}'s slideshow is labelled by ${JSON.stringify(slider.attrs.get('aria-labelledby'))} and its heading carries the id ${JSON.stringify(headingId)}: a focusable track inside a section nobody named is a group a screen reader cannot say the name of`,
    )
    assert.ok(textOf(headings[0]).trim().length > 0, `${rel}'s slideshow heading is empty, so the section is a heading that says nothing`)

    // The slides themselves: the homepage's, in the homepage's order, with the row's own words
    // and the row's own pixel size — which is what reserves each slide's box before its bytes
    // land, so the page does not jump as the pictures arrive.
    const slides = slidesOf(html, CAMPAIGN_SLIDER_SLOT)
    assert.deepEqual(
      slides,
      homeSlides,
      `${rel}'s slideshow shows ${JSON.stringify(slides.map((slide) => slide.src))} with the words ${JSON.stringify(slides.map((slide) => slide.caption))}, and the homepage's panel shows ${JSON.stringify(homeSlides.map((slide) => slide.src))} with ${JSON.stringify(homeSlides.map((slide) => slide.caption))}: the four pages show the screens the homepage shows, in the file's own order, with each row's own alt text, caption and size (the owner's feedback of 2026-10-30, D412's finding 3; D246)`,
    )
    for (const [index, slide] of slides.entries()) {
      assert.ok((slide.alt ?? '').trim() !== '', `${rel}'s slide ${index + 1} (${slide.src}) carries no alt text: a reader who cannot see the picture is owed what it shows, and the words are ${HOME_SCREENS_YAML}'s own`)
      for (const side of ['width', 'height']) {
        assert.ok(
          Number(slide[side]) > 0,
          `${rel}'s slide ${index + 1} declares ${side}=${JSON.stringify(slide[side])}: the row's own pixel size is what reserves the slide's box before its bytes land (${HOME_SCREENS_YAML})`,
        )
      }
      // Same origin, and no file the site does not already serve: the pictures are the console's
      // own captures under `public/screenshots/`, named by the data file and added by nobody —
      // the owner will replace them later, so nothing here copies or re-encodes one (F14, D190).
      assert.match(
        slide.src ?? '',
        /^\/screenshots\/[A-Za-z0-9._-]+$/,
        `${rel}'s slide ${index + 1} is served from ${JSON.stringify(slide.src)}: the console's screens are this site's own files under /screenshots/, so no campaign page adds a picture and no page names a second origin (F14, D190)`,
      )
    }

    // One eager picture, which is the first slide — the one a reader is shown without scrolling
    // — and every other slide lazy, so the four pages that argue for the product do not fetch
    // four full-size captures before the argument starts (the owner's own bound).
    assert.equal(slides[0]?.loading, 'eager', `${rel}'s first slide loads ${JSON.stringify(slides[0]?.loading)}: the picture the section opens with is the one the page fetches with it`)
    assert.deepEqual(
      slides.slice(1).map((slide) => slide.loading),
      slides.slice(1).map(() => 'lazy'),
      `${rel}'s slides after the first load ${JSON.stringify(slides.slice(1).map((slide) => slide.loading))}: every screen below the first is fetched when a reader reaches it, and the hero's own picture stays the page's one eager one (the owner's feedback of 2026-10-30, D412's finding 3)`,
    )
  }
})

test('the campaign slideshow is thumb-friendly, keyboard-operable and complete with no script', () => {
  for (const { rel } of PAGES) {
    const { html } = pageOf(rel)
    const doc = parse(html)
    const slider = findAll(doc, (el) => el.attrs.get('data-slot') === CAMPAIGN_SLIDER_SLOT)[0]
    const count = slidesOf(html, CAMPAIGN_SLIDER_SLOT).length

    // The no-script half: every slide is in the document as a list item of the track, so a
    // browser that runs no script draws the pictures stacked and in order. Nothing about the
    // slider is a script's, and the CSS is what makes the row scroll.
    const track = findAll(slider, (el) => el.attrs.has('data-slider-track'))
    assert.equal(track.length, 1, `${rel}'s slideshow carries ${track.length} tracks: the slides are one scrollable row, and a page that drew them any other way could not be swiped`)
    assert.equal(track[0].name, 'ul', `the campaign slideshow's track is a <${track[0].name}>: one list item per screen, in the file's order`)
    assert.equal(
      findAll(track[0], (el) => el.name === 'li' && el.attrs.has('data-slider-slide')).length,
      count,
      `${rel}'s track carries a different number of slides than the slideshow has pictures, so some pictures are not in the document a reader with no script receives`,
    )

    // The keyboard half: a scroll container that cannot be focused cannot be scrolled without a
    // pointer, so the track takes focus and is named — a focusable region is announced as an
    // unnamed group without a label.
    assert.equal(track[0].attrs.get('tabindex'), '0', `${rel}'s track carries tabindex=${JSON.stringify(track[0].attrs.get('tabindex'))}: the slides off the right-hand edge are reachable by keyboard only if the track can take focus`)
    assert.match(
      track[0].attrs.get('aria-label') ?? '',
      /scroll sideways/i,
      `${rel}'s track is named ${JSON.stringify(track[0].attrs.get('aria-label'))}: a reader who lands on it is told what the box is and what to do with it`,
    )

    // The thumb half: one dot per slide, each an anchor into its own slide and named for a screen
    // reader, which is the whole control with no script — the browser's own jump, at a target a
    // thumb can hit (`ScreenshotSlider.astro`'s dot rules).
    const dots = findAll(slider, (el) => el.name === 'a' && el.attrs.has('data-slider-dot'))
    assert.equal(dots.length, count, `${rel}'s slideshow carries ${dots.length} dot(s) for ${count} slide(s): the dots are the slides' order read twice`)
    for (const [index, dot] of dots.entries()) {
      assert.equal(dot.attrs.get('href'), `#campaign-screen-${index + 1}`, `${rel}'s dot ${index + 1} points at ${JSON.stringify(dot.attrs.get('href'))}: each dot is an anchor into its own slide, so the control works with the browser's own jump rather than a script's`)
      assert.equal(
        findAll(slider, (el) => el.attrs.get('id') === dot.attrs.get('href')?.slice(1)).length,
        1,
        `${rel}'s dot ${index + 1} points at ${JSON.stringify(dot.attrs.get('href'))} and no slide carries that id, so the dot is a link to nowhere`,
      )
      assert.ok((dot.attrs.get('aria-label') ?? '').trim() !== '', `${rel}'s dot ${index + 1} carries no aria-label: a dot drawn by the stylesheet has no text for a screen reader to announce`)
    }

    // The bar the script drives is emitted `hidden`, which is the fallback's other half: with no
    // script it is not drawn at all rather than drawn as two buttons that do nothing. Its two
    // arrows carry their names in `aria-label` for the same reason the dots do.
    const bar = findAll(slider, (el) => el.attrs.has('data-slider-bar'))
    assert.equal(bar.length, 1, `${rel}'s slideshow carries ${bar.length} control bars: the arrows and the count are one block, and it is the block the script reveals`)
    assert.ok(
      bar[0].attrs.has('hidden'),
      `${rel}'s control bar is not emitted hidden: with no script it would stand as buttons that do nothing, and the owner asked for a plain no-script fallback`,
    )
    assert.deepEqual(
      findAll(bar[0], (el) => el.attrs.has('data-slider-prev') || el.attrs.has('data-slider-next')).map((el) => el.attrs.get('aria-label')),
      ['Previous screenshot', 'Next screenshot'],
      `${rel}'s control bar carries ${JSON.stringify(findAll(bar[0], 'button').map((el) => el.attrs.get('aria-label')))}: the two arrows are named, because a chevron drawn by the stylesheet has no text to announce`,
    )
    const counter = findAll(bar[0], (el) => el.attrs.has('data-slider-counter'))
    assert.equal(counter.length, 1, `${rel}'s control bar carries ${counter.length} counters, and the count is one live region the script keeps in step`)
    assert.equal(
      counter[0].attrs.get('role'),
      'status',
      `${rel}'s counter is a <${counter[0].name}> with role=${JSON.stringify(counter[0].attrs.get('role'))}: the count changes as a reader moves, and a live region is how that reaches a reader who cannot see the track scroll`,
    )
    assert.equal(textOf(counter[0]).trim(), '', `${rel}'s counter carries text in the markup (${JSON.stringify(textOf(counter[0]).trim())}): the script writes it, and a number written by the build is a number nothing keeps in step`)
  }
})
