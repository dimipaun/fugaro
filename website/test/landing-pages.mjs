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
//      does render the band, with the figure and the entry it was drawn from.
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
//   7. **The hero's own picture is drawn in the markup.** Each hero carries the campaign's
//      illustration: inline SVG, so nothing is fetched and no raster file is added (F14), with
//      the variant's tone on it — its own accent and its own motif, keyed off `data-tone` — a
//      name for a reader who is not shown it, and no text inside the drawing, because a picture
//      made of words is a second paragraph the page's word ceiling would have to count. The
//      drawing is the page's own and not a variant's media: a variant that names a video or an
//      image still takes the media slot at the top of the page instead (the owner's feedback of
//      2026-10-30; F14, G2.5).
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
import { DIST, SITE, builtPages, cssRules, filesUnder, findAll, parse, servedFile, siteFacts, textOf, tokenize, walk } from './lib/html.mjs'
import { buildCopy } from './lib/build.mjs'

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
    // The block is written once per variant and `tone: scale` names the one being changed, so
    // the figure lands on /scale and on no other page.
    const after = before.replace(
      '    tone: scale\n    proof_number:\n      value: unmeasured\n      source: unmeasured\n',
      `    tone: scale\n    proof_number:\n      value: "${FIGURE}"\n      source: ${SOURCE}\n`,
    )
    assert.notEqual(after, before, 'the fixture did not give /scale a figure in the copy, so no case below could read the band')
    writeFileSync(file, after)
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
  assert.equal(findAll(parse(crew.html), (el) => el.name === 'a' && textOf(el).trim() === 'Book a demo').length, 1, '/crew lost its booking link along with the missing video')
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
    for (const selector of ['.campaign-steps', '.campaign-calls', '.campaign-hero-art']) {
      assert.ok(
        rules.some((rule) => (rule.conditions ?? []).length > 0 && rule.selector.includes(selector)),
        `${rel} declares ${selector} at one width only: the sections stack on a phone and sit in a row on a wide viewport, and the gates have no browser — this is the static half, and the rendered reading is G2.5's (spec §6)`,
      )
    }
  }
})

// ---- 7. the hero's own picture (the owner's feedback of 2026-10-30; F14, G2.5) -------------

/** The hero band of one built page, and the illustration it draws. */
function artOf(rel) {
  const { html } = pageOf(rel)
  const hero = findAll(parse(html), (el) => el.attrs.get('data-section') === 'hero')
  assert.equal(hero.length, 1, `${rel} marks ${hero.length} hero sections; the page has one`)
  const drawings = findAll(hero[0], (el) => el.name === 'svg' && (el.attrs.get('class') ?? '').split(/\s+/).includes('campaign-hero-art'))
  assert.equal(
    drawings.length,
    1,
    `${rel} draws ${drawings.length} hero illustrations: every campaign page carries the campaign's one picture, and it is the hero's own (the owner's feedback of 2026-10-30)`,
  )
  return { html, hero: hero[0], svg: drawings[0] }
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

test('the hero draws the campaign\'s picture inline, named, and with no request of its own', () => {
  const drawings = new Set()
  for (const { variant, rel } of PAGES) {
    const { html, svg } = artOf(rel)

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

test('the hero\'s picture sits under the words on a phone, and every tone paints its own accent', () => {
  const TONES = LANDING.variants.map((variant) => variant.tone)
  for (const { rel } of PAGES) {
    const { html } = artOf(rel)
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
    for (const rule of rules.filter((r) => r.selector.includes('.campaign-hero') && /grid-template-columns/.test(r.body))) {
      assert.ok(
        (rule.conditions ?? []).length > 0,
        `${rel} places the picture beside the copy at every width: the hero is one column until a breakpoint says otherwise, so a phone is never asked to read a headline and a drawing side by side (the owner's feedback of 2026-10-30)`,
      )
    }

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
