// website/test/os-cards.mjs — one card per system, each with a picture of its own
// (the owner's draft of 2026-10-19, "Getting started, draft 2 (OS icons)").
//
// Getting Started's install section opens with a card per operating system, and the card is
// what the owner's draft is: the system's own glyph, the system's name, the path
// `website/data/install.yaml` offers that system first, and — where the console ships for it —
// the console's own block. Two things about that shape no other gate can see, and this file is
// the gate for both:
//
//   1. **No card names a system the file does not carry.** A card is an offer, and an offer
//      under a system `install.yaml` does not describe — or under one it describes as
//      `untested`, which is the Windows entry D94 settled and this page must never turn into a
//      card — is the page promising something the repository's own file does not. The card's
//      `data-platform` is held to the file's platform list, its name to the file's own name for
//      that system, and its path to the path the file offers that system *first*: the file's
//      paths are in the order a reader runs them, so the first one a system is offered is the
//      one its card carries, and a card that picked a later path would be the page choosing a
//      route the file did not choose.
//   2. **Every card is drawn with exactly one icon, and it draws something.** The owner's
//      draft draws each card with a glyph; so a card with no `<svg>`, with two, with one a
//      screen reader is handed as content beside the name that already says the system, with
//      one that fetches a file or an origin (an icon font, an `<image>`, a `<use href>`, an
//      inline style), or — the defect no other check in this repository can see — with a
//      `<svg>` that holds no shape at all and so renders as an empty box, is refused here. The
//      drawings are also held to being as many as the cards: two systems drawn with one picture
//      is one icon used twice, and the picture then says nothing about the system under it.
//
// **The file the cards are held to is the site's own reader of it** — `content.ts`'s
// `loadInstallFacts()`, the same one the page renders from — rather than a second regex over
// the YAML: a gate with its own parser would be the second copy of the install facts this
// whole pipeline exists to avoid, and it would keep passing while the page and the file
// disagreed in a way only the real reader refuses (Global Constraint 6).
//
// **No trademark is drawn, and that is a rule about the drawings rather than a style.** The
// glyphs are this page's own simple shapes — a laptop for macOS, a terminal for Linux — and
// the gate reads the *drawing* of each icon (its shapes and their attributes) rather than the
// component's source: a card whose icon is another document's mark is a mark this repository
// may not copy, and the shapes are what would have to change to put one there. The picture is
// the owner's check; that every card has one, and that it is the page's own inline drawing, is
// this gate's.
//
// The page is read from `dist/`, which `npm run check` builds before it runs this file, through
// the same HTML reader the other gates use (`lib/html.mjs`). No browser, no network.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { DIST, REPO, find, parse, textOf, walk } from './lib/html.mjs'
import { buildCopy } from './lib/build.mjs'
import { loadInstallFacts } from '../src/content.ts'

/** The file every message below names, as the page's own header names it. */
const INSTALL_FILE = 'website/data/install.yaml'

/** The heading of the console's block, which is a card's own section and not a card. */
const CONSOLE_HEADING = 'Desktop console'

/**
 * The shapes an `<svg>` can be drawn with. Everything else inside one — a `<title>`, a
 * `<defs>`, a `<desc>` — is not a picture: a glyph whose whole content is a name for itself
 * draws nothing, which is the empty-box refusal below.
 */
const SHAPE_ELEMENTS = new Set(['path', 'circle', 'ellipse', 'rect', 'line', 'polyline', 'polygon'])

/**
 * The ways an `<svg>` could pull something in from outside itself, which is what an inline
 * icon of this site may never do: an attribute naming another document, or an element whose
 * whole job is to reference one. `style` is on the list for the same reason and not a
 * different one: a glyph painted by an inline style is a colour the palette does not carry and
 * the deployed policy's `style-src` would have to admit.
 */
const REFERRING_ATTRS = new Set(['href', 'xlink:href', 'src', 'srcset', 'style', 'poster'])
const REFERRING_ELEMENTS = new Set(['image', 'use', 'script', 'foreignobject', 'iframe', 'object', 'embed'])

/** An element's attributes, without Astro's scoping attribute, as one comparable string. */
function attrsOf(el) {
  return [...el.attrs.entries()]
    .filter(([name]) => !name.startsWith('data-astro-cid-'))
    .map(([name, value]) => `${name}=${JSON.stringify(value)}`)
    .sort()
    .join(' ')
}

/** The drawing inside one `<svg>`: its shapes and their own attributes, and nothing above it. */
function drawingOf(svg) {
  return [...walk(svg)]
    .filter((el) => SHAPE_ELEMENTS.has(el.name))
    .map((el) => `${el.name} ${attrsOf(el)}`.trim())
    .join(';')
}

/** What one `<svg>` points at, or '' when it points at nothing (the good case). */
function referenceOf(svg) {
  for (const el of [svg, ...walk(svg)]) {
    for (const [name, value] of el.attrs ?? []) {
      if (REFERRING_ATTRS.has(name)) return `<${el.name} ${name}=${JSON.stringify(value)}>`
    }
    if (REFERRING_ELEMENTS.has(el.name)) return `<${el.name}>`
  }
  return ''
}

/**
 * The icons a card renders, as the facts the refusals are about: one entry per `<svg>` in the
 * card. Every `<svg>` under the card counts — a reader does not go looking for a class name, so
 * a second picture added without one is still a second icon.
 */
function iconsOf(card) {
  const out = []
  // The copy button's own glyphs (a clipboard and a checkmark, the owner's feedback of
  // 2026-10-24) are the control's, not the system's, and are not counted.
  const inCopyButton = new Set()
  for (const el of walk(card)) if (el.attrs?.has?.('data-copy-button')) for (const inner of walk(el)) inCopyButton.add(inner)
  for (const el of walk(card)) {
    if (el.name !== 'svg' || inCopyButton.has(el)) continue
    out.push({
      drawing: drawingOf(el),
      hidden: el.attrs.get('aria-hidden') === 'true',
      focusable: el.attrs.get('focusable') === 'false',
      reference: referenceOf(el),
    })
  }
  return out
}

/** True when `el` is `block` or sits inside it. */
function within(el, block) {
  for (let node = el; node !== null; node = node.parent) if (node === block) return true
  return false
}

/**
 * Getting Started's cards, as the facts this gate reads: for each `<article class="install-card">`
 * on the page, the platform it names, the path under it, the system's name, its icons and how
 * many commands it prints. `hasInstall` is whether the page carries the `#install` section at
 * all, and `inInstall` per card whether it sits inside it: the cards are the section a reader
 * installs from, and a card somewhere else on the page is a card the section does not have.
 *
 * `null` when the page has no `main`, which is the failure the case below reports rather than an
 * error here.
 */
function cardsOf(html) {
  const main = find(parse(html), 'main')
  if (main === null) return null
  const install = [...walk(main)].find((el) => el.attrs.get('id') === 'install')
  const cards = []
  for (const el of walk(main)) {
    if (el.name !== 'article') continue
    const classes = (el.attrs.get('class') ?? '').split(/\s+/)
    if (!classes.includes('install-card')) continue
    const heading = [...walk(el)].find((node) => node.name === 'h3')
    cards.push({
      platform: el.attrs.get('data-platform') ?? '',
      path: el.attrs.get('data-path') ?? '',
      name: heading === undefined ? '' : textOf(heading).trim(),
      icons: iconsOf(el),
      inInstall: install !== undefined && within(el, install),
    })
  }
  return { hasInstall: install !== undefined, cards }
}

/**
 * Every way the cards break the two rules above, as the sentences a reader of this file sees.
 * Written as a function over the page's own facts and `install.yaml`'s so a fixture can prove
 * it refuses: the artifact case at the bottom holds the built page to the file, and the cases
 * beside it plant one defect each against a fixture that keeps every rule.
 *
 * @param {{hasInstall: boolean, cards: object[]}|null} page `cardsOf`'s reading of the page
 * @param {{platforms: object[], paths: object[]}} install `loadInstallFacts()`, or the fixture
 */
export function osCardFailures(page, install) {
  if (page === null) {
    return ['the built /getting-started/ page carries no `main`, so the cards it opens with cannot be read at all']
  }
  const out = []
  if (!page.hasInstall) {
    return ['the built /getting-started/ page carries no `#install` section, and the cards are the section a reader installs from (the owner\'s draft of 2026-10-19)']
  }

  const supported = install.platforms.filter((platform) => platform.status === 'supported')

  for (const [index, card] of page.cards.entries()) {
    const named = card.platform === '' ? 'no `data-platform`' : JSON.stringify(card.platform)
    if (!card.inInstall) {
      out.push(
        `card ${index + 1} of the page (${named}) is not inside \`#install\`: the cards are the section a reader installs from, and a card outside it is an offer the install section does not make (the owner's draft of 2026-10-19)`,
      )
    }
  }

  if (page.cards.length !== supported.length) {
    out.push(
      `the page draws ${page.cards.length} install card(s) and ${INSTALL_FILE} marks ${supported.length} platform(s) supported (${supported.map((platform) => platform.name).join(', ')}): the section draws one card per system the file supports, and a card the file does not support is an offer this repository cannot keep (D94)`,
    )
  }

  for (const [index, card] of page.cards.entries()) {
    const where = `install card ${index + 1}`
    const platform = install.platforms.find((entry) => entry.id === card.platform)

    if (platform === undefined) {
      out.push(
        `${where} names the platform ${card.platform === '' ? '(none)' : JSON.stringify(card.platform)}, which ${INSTALL_FILE} carries no platform by: the cards are the systems the file describes, and a card under a system it does not carry is the page naming an operating system this repository's own install facts have never heard of (the owner's draft of 2026-10-19; Global Constraint 6)`,
      )
      continue
    }
    if (platform.status !== 'supported') {
      out.push(
        `${where} is drawn for ${platform.name}, which ${INSTALL_FILE} marks ${JSON.stringify(platform.status)}: a card is an offer, and a system the file does not mark supported — Windows, whose own note says this repository builds no binary for it — is a note in the platform list and never a card a reader can install from (D94, finding 20)`,
      )
    }
    if (card.name !== platform.name) {
      out.push(
        `${where} is headed ${JSON.stringify(card.name)}, and ${INSTALL_FILE} names ${JSON.stringify(platform.id)} ${JSON.stringify(platform.name)}: the card's heading is the file's own name for the system, so a rename there moves the heading here (Global Constraint 6)`,
      )
    }
    if (supported[index] !== undefined && card.platform !== supported[index].id) {
      out.push(
        `${where} is drawn for ${JSON.stringify(card.platform)} where ${INSTALL_FILE}'s supported platforms put ${JSON.stringify(supported[index].id)} at that position: the cards are the supported systems in the file's own order, which is the order a reader meets them (the owner's draft of 2026-10-19)`,
      )
    }

    // The path under the card: one the file carries, offered on that system, and the first one
    // it offers that system.
    const chosen = install.paths.find((entry) => entry.id === card.path)
    if (chosen === undefined) {
      out.push(
        `${where} carries the path ${card.path === '' ? '(none)' : JSON.stringify(card.path)}, which ${INSTALL_FILE} carries no path by: a card's command is the file's own, and a path the file does not have is a command nothing pins (Global Constraint 6)`,
      )
    } else {
      if (!chosen.platforms.includes(platform.goos)) {
        out.push(
          `${where} offers the path ${JSON.stringify(chosen.id)} under ${platform.name}, and ${INSTALL_FILE} offers that path on ${chosen.platforms.join(', ')}: a card carries a path the file offers that system, and a path offered on another one is an install route the file does not claim works here (Global Constraint 6)`,
        )
      }
      const first = install.paths.find((entry) => entry.platforms.includes(platform.goos))
      if (first !== undefined && first.id !== chosen.id) {
        out.push(
          `${where} carries ${JSON.stringify(chosen.id)} where ${INSTALL_FILE} offers ${platform.name} ${JSON.stringify(first.id)} first: the file's paths are in the order a reader runs them, so the card carries the first one the system is offered and a later one would be the page choosing a route the file did not choose (the owner's draft of 2026-10-19)`,
        )
      }
    }

    // The icon: exactly one, a picture rather than content, and nothing fetched.
    const icons = card.icons
    if (icons.length !== 1) {
      out.push(
        `${where} carries ${icons.length} icon(s) and every card carries one: the owner's draft of 2026-10-19 draws each system's card with a glyph, and a card with none or with several is a system the picture does not mark (the owner's draft of 2026-10-19)`,
      )
      continue
    }
    const [icon] = icons
    if (icon.drawing === '') {
      out.push(
        `${where}: its icon draws nothing (an <svg> with no shape in it), and the glyph is what marks the card — a card whose icon renders as an empty box is a card with no picture at all, which is the defect no other gate in this repository can see: the element is present, named, decorated and clean (the owner's draft of 2026-10-19)`,
      )
    }
    if (!icon.hidden) {
      out.push(
        `${where}: its icon is not marked as decoration (aria-hidden="true"), and the picture is the card's — a screen reader handed the glyph as well reads the system's name beside it twice (the owner's draft of 2026-10-19)`,
      )
    }
    if (!icon.focusable) {
      out.push(
        `${where}: its icon is not marked out of the tab order (focusable="false"), which is the other half of the decoration contract this site already writes on a drawing (\`a11y.mjs\` holds the flow's arrows and the benefits' icons to the same pair) — an icon is decoration on both (the owner's draft of 2026-10-19)`,
      )
    }
    if (icon.reference !== '') {
      out.push(
        `${where}: its icon references something outside itself (${icon.reference}), and an icon of this site is inline SVG drawn with the page's own tokens — an icon font, an image file, a <use href> or an inline style is another thing a reader's browser has to fetch, or a colour the palette does not carry (Global Constraints 4 and 8)`,
      )
    }
  }

  // As many pictures as cards: a glyph repeated under two systems is one icon used twice, and
  // the drawing then says nothing about the system under it.
  const drawings = page.cards.flatMap((card) => card.icons.map((icon) => icon.drawing))
  if (new Set(drawings).size !== drawings.length) {
    const repeated = drawings.filter((drawing, i) => drawings.indexOf(drawing) !== i)
    out.push(
      `the cards draw ${drawings.length} icon(s) with ${new Set(drawings).size} distinct picture(s), so ${repeated.length} of them repeat another card's glyph (${JSON.stringify(repeated[0])}): each system's card carries a picture of its own (the owner's draft of 2026-10-19)`,
    )
  }

  return out
}

/** The built /getting-started/ page, as this gate reads it. */
function builtPage(dist = DIST) {
  return readFileSync(path.join(dist, 'getting-started', 'index.html'), 'utf8')
}

// ---- the built site ---------------------------------------------------------------------

test('every install card is a system install.yaml carries, drawn with an icon of its own', () => {
  // The whole of the gate, over the page `npm run check` built and the file the page renders
  // from: one card per supported system, in the file's order, each with the path the file
  // offers that system first and one picture of its own. The cards are read off the built
  // artifact rather than the component's source, because what a reader gets is the artifact.
  const failures = osCardFailures(cardsOf(builtPage()), loadInstallFacts())
  assert.deepEqual(
    failures,
    [],
    `the install cards are not the systems ${INSTALL_FILE} carries:\n  ${failures.join('\n  ')}`,
  )
})

test("the console's block is a section of a card, not a card of its own", () => {
  // The console is offered in the card of the system it ships for, and its block is inside that
  // card rather than beside it: a page that drew the console as a third card would be offering
  // a system that is not a system, and the count above would be counting it as one. The
  // console's own rules — the heading, the downloads, the unreleased state — are
  // `console-option.mjs`'s; what this case holds is that the block sits inside a card, which is
  // what makes the card count a count of systems.
  // The page is parsed once, and the heading and the card are read from that one tree: two
  // parses of the same bytes are two sets of nodes, and a card found in the second is not the
  // card the heading sits in, however alike the two pages are.
  const doc = parse(builtPage())
  const headings = [...walk(doc)].filter((el) => el.name === 'h4' && textOf(el).trim() === CONSOLE_HEADING)
  assert.equal(
    headings.length,
    1,
    `the built page carries ${headings.length} "${CONSOLE_HEADING}" heading(s), and the console is offered once, in the card of the system it ships for (the owner's draft of 2026-10-19)`,
  )
  const card = [...walk(doc)].find(
    (el) => el.name === 'article' && (el.attrs.get('class') ?? '').split(/\s+/).includes('install-card') && within(headings[0], el),
  )
  assert.ok(card !== undefined, `the built page draws the console's "${CONSOLE_HEADING}" block outside every install card, and the block belongs in the card of the system the console ships for (the owner's draft of 2026-10-19)`)
})

// ---- the refusals, kept -----------------------------------------------------------------

/**
 * The fixture's own install facts: two supported systems, three paths in the order a reader
 * runs them, and one untested system — the Windows shape D94 settles. Small enough to hold in
 * the head, and shaped like the real file: `paths` in order, `platforms` with the `goos` each
 * path names, so that macOS's first path is Homebrew, Linux's first is the archive, and there
 * is a third path both systems are offered *after* their first one. That third one is what
 * makes the "first path" refusal reachable: without it, every card a fixture can plant carries
 * either the right path or one the file does not carry at all, and the rule the whole card
 * shape rests on would be code no case fires.
 */
const FIXTURE_INSTALL = {
  platforms: [
    { id: 'macos', name: 'macOS', goos: 'darwin', status: 'supported' },
    { id: 'linux', name: 'Linux', goos: 'linux', status: 'supported' },
    { id: 'windows', name: 'Windows', goos: 'windows', status: 'untested' },
  ],
  paths: [
    { id: 'homebrew', platforms: ['darwin'] },
    { id: 'archive', platforms: ['darwin', 'linux'] },
    { id: 'cosign', platforms: ['darwin', 'linux'] },
  ],
}

/** Two distinct drawings, one per card, so the fixture page is the page the gate passes. */
const FIXTURE_GLYPHS = ['M1 1h2v2', 'M3 3h2v2']

/**
 * One card, in the shape the page writes one: an `<article class="install-card">` carrying its
 * platform and its path, its icon, its system's name and its command. `icon` and `marks` are
 * the two things a fixture plants — the picture, and the attributes beyond the drawing — so a
 * case can leave `aria-hidden` off or hang a reference on the `<svg>`.
 */
function cardMarkup({ platform, path: installPath, name, icon, marks = 'aria-hidden="true" focusable="false"' }) {
  // `icon: null` is a card with no `<svg>` at all, which is the one defect a body inside an
  // `<svg>` cannot express: an empty drawing and a missing icon are different failures, and
  // the gate has a refusal for each.
  const picture = icon === null ? '' : `<svg class="os-icon" viewBox="0 0 24 24" ${marks}>${icon}</svg>`
  return `<article class="install-card" data-platform="${platform}" data-path="${installPath}">${picture}<h3>${name}</h3><pre>brew install x</pre></article>`
}

/** A page carrying the two fixture cards and nothing else, for the refusals below. */
function cardsPage(cards, { section = true } = {}) {
  const body = cards
    .map((card, index) =>
      cardMarkup({
        ...card,
        // The fixture's own glyph unless the case planted one — including a planted `null`,
        // which is the card with no `<svg>` at all.
        icon: card.icon !== undefined ? card.icon : `<path d="${FIXTURE_GLYPHS[index % FIXTURE_GLYPHS.length]}"></path>`,
      }),
    )
    .join('')
  const install = section ? `<section id="install"><h2>Install</h2><div class="install-cards">${body}</div></section>` : body
  return `<!DOCTYPE html><html lang="en"><head><title>t</title></head><body><main><h1>t</h1>${install}</main></body></html>`
}

/** The fixture's two good cards. */
const GOOD_CARDS = [
  { platform: 'macos', path: 'homebrew', name: 'macOS' },
  { platform: 'linux', path: 'archive', name: 'Linux' },
]

test('the fixture page keeps every rule, so the refusals below are about what they plant', () => {
  // The cases below all plant one defect beside this page: if the fixture itself were refused,
  // every one of them would pass for the wrong reason.
  assert.deepEqual(
    osCardFailures(cardsOf(cardsPage(GOOD_CARDS)), FIXTURE_INSTALL),
    [],
    'the fixture page is not the page the gate passes, so the refusals below prove nothing',
  )
})

test('a card under a system install.yaml does not carry is refused, and so is one it marks untested', () => {
  // The rule the owner's brief names: no card names a platform `install.yaml` does not carry.
  // Planted three ways — a system the file has never heard of, the untested Windows entry, and
  // a card the file carries but under a status that is not `supported` — beside the two cards
  // that keep the rule.
  const unknown = [...GOOD_CARDS.slice(0, 1), { platform: 'freebsd', path: 'archive', name: 'FreeBSD' }]
  assert.match(
    osCardFailures(cardsOf(cardsPage(unknown)), FIXTURE_INSTALL).join('\n'),
    /names the platform "freebsd", which website\/data\/install\.yaml carries no platform by/,
    'a card under a system the file does not carry is not refused',
  )

  const windows = [...GOOD_CARDS.slice(0, 1), { platform: 'windows', path: 'archive', name: 'Windows' }]
  assert.match(
    osCardFailures(cardsOf(cardsPage(windows)), FIXTURE_INSTALL).join('\n'),
    /is drawn for Windows, which website\/data\/install\.yaml marks "untested"/,
    'the untested system as a card is not refused, and a card is an offer the file does not make for Windows (D94)',
  )

  const named = [GOOD_CARDS[0], { ...GOOD_CARDS[1], name: 'GNU/Linux' }]
  assert.match(
    osCardFailures(cardsOf(cardsPage(named)), FIXTURE_INSTALL).join('\n'),
    /is headed "GNU\/Linux", and website\/data\/install\.yaml names "linux" "Linux"/,
    'a card headed with the page\'s own name for a system is not refused',
  )

  const none = [GOOD_CARDS[0]]
  assert.match(
    osCardFailures(cardsOf(cardsPage(none)), FIXTURE_INSTALL).join('\n'),
    /the page draws 1 install card\(s\) and website\/data\/install\.yaml marks 2 platform\(s\) supported/,
    'a dropped card is not refused',
  )

  const swapped = [GOOD_CARDS[1], GOOD_CARDS[0]]
  assert.match(
    osCardFailures(cardsOf(cardsPage(swapped)), FIXTURE_INSTALL).join('\n'),
    /is drawn for "linux" where website\/data\/install\.yaml's supported platforms put "macos" at that position/,
    'the cards in another order than the file\'s are not refused',
  )
})

test('a card carrying a path the file does not offer that system is refused', () => {
  // The path under a card is the file's, and it is the FIRST one the file offers that system:
  // a card that picked a later path, or a path offered on another system, is the page choosing
  // an install route the file did not choose for the system under it.
  const other = [GOOD_CARDS[0], { platform: 'linux', path: 'homebrew', name: 'Linux' }]
  assert.match(
    osCardFailures(cardsOf(cardsPage(other)), FIXTURE_INSTALL).join('\n'),
    /offers the path "homebrew" under Linux, and website\/data\/install\.yaml offers that path on darwin/,
    'a card carrying a path the file does not offer that system is not refused',
  )

  const absent = [GOOD_CARDS[0], { platform: 'linux', path: 'snap', name: 'Linux' }]
  assert.match(
    osCardFailures(cardsOf(cardsPage(absent)), FIXTURE_INSTALL).join('\n'),
    /carries the path "snap", which website\/data\/install\.yaml carries no path by/,
    'a card carrying a path the file does not carry is not refused',
  )

  // The headline rule: the path is the FIRST one the file offers that system. The fixture's
  // `archive` is offered on macOS but comes after Homebrew, so a macOS card carrying it is a
  // path the membership check above waves through and only this refusal catches.
  const later = [{ platform: 'macos', path: 'archive', name: 'macOS' }, GOOD_CARDS[1]]
  assert.match(
    osCardFailures(cardsOf(cardsPage(later)), FIXTURE_INSTALL).join('\n'),
    /carries "archive" where website\/data\/install\.yaml offers macOS "homebrew" first/,
    'a card carrying a path the file offers that system later is not refused',
  )
})

test('the icon refusals fire on a missing icon, a second one, a hidden one and a repeated one', () => {
  // The owner's draft draws each card with a glyph. Five defects, planted one at a time beside
  // the fixture that keeps the rule: no icon, two, one a screen reader is handed as content,
  // one out of the decoration contract's other half, and two systems drawn with one picture.
  const plant = (index, extra) =>
    GOOD_CARDS.map((card, i) => (i === index ? { ...card, ...extra } : card))
  const refusal = (cards) => osCardFailures(cardsOf(cardsPage(cards)), FIXTURE_INSTALL).join('\n')

  assert.match(
    refusal(plant(0, { icon: null })),
    /install card 1 carries 0 icon\(s\) and every card carries one/,
    'a card drawn with no icon at all is not refused',
  )

  assert.match(
    refusal(plant(0, { icon: '<path d="M1 1h2v2"></path></svg><svg class="os-icon" aria-hidden="true" focusable="false"><path d="M9 9h2v2"></path>' })),
    /install card 1 carries 2 icon\(s\)/,
    'a card drawn with two icons is not refused',
  )

  assert.match(
    refusal(plant(0, { marks: 'focusable="false"' })),
    /install card 1: its icon is not marked as decoration/,
    'an icon a screen reader is handed as content beside the system\'s name is not refused',
  )

  assert.match(
    refusal(plant(0, { marks: 'aria-hidden="true"' })),
    /install card 1: its icon is not marked out of the tab order/,
    'an icon that is nothing of the decoration contract but its aria-hidden is not refused',
  )

  // The empty icon: an `<svg>` that renders as an empty box — a picture a reader never sees,
  // and the one defect of this set that leaves the element present, named and clean.
  for (const [what, icon] of [
    ['no children at all', ''],
    ['a title and nothing else', '<title>laptop</title>'],
    ['an empty group', '<g></g>'],
    ['definitions with no shape', '<defs><clipPath id="c"></clipPath></defs>'],
  ]) {
    assert.match(
      refusal(plant(0, { icon })),
      /install card 1: its icon draws nothing/,
      `an icon of ${what} is not refused: a glyph that renders as an empty box is a card with no picture`,
    )
  }

  // A reference from inside the `<svg>` and one on the `<svg>` itself: an icon font or an image
  // file arrives through the first, and the second is the shape that carries a colour the
  // palette does not have.
  assert.match(
    refusal(plant(0, { icon: '<use href="/icons.svg#laptop"></use>' })),
    /references something outside itself \(<use href="\/icons\.svg#laptop">\)/,
    'an icon that pulls a glyph in from another file is not refused',
  )
  assert.match(
    refusal(plant(0, { icon: '<path d="M1 1h2v2"></path>', marks: 'aria-hidden="true" focusable="false" style="stroke:red"' })),
    /references something outside itself \(<svg style="stroke:red">\)/,
    'an icon painted by an inline style is not refused',
  )

  assert.match(
    refusal(GOOD_CARDS.map((card) => ({ ...card, icon: '<path d="M1 1h2v2"></path>' }))),
    /draw 2 icon\(s\) with 1 distinct picture\(s\)/,
    'two systems drawn with one glyph between them are not refused',
  )
})

test('a page whose cards are not in the install section, or which has none, is refused', () => {
  // The section is where the cards are: the count and the icons above are about the section a
  // reader installs from, so a card outside it is a card the section does not have, and a page
  // with no section is a page this gate cannot read.
  const outside = osCardFailures(cardsOf(cardsPage(GOOD_CARDS, { section: false })), FIXTURE_INSTALL).join('\n')
  assert.match(outside, /carries no `#install` section/, 'a page with no install section is not refused')

  // The same two cards on a page whose install section is there and empty: the cards are on the
  // page, the section is on the page, and the cards are not in it.
  const nested = cardsPage(GOOD_CARDS, { section: false }).replace(
    '<h1>t</h1>',
    '<h1>t</h1><section id="install"><h2>Install</h2></section>',
  )
  assert.match(
    osCardFailures(cardsOf(nested), FIXTURE_INSTALL).join('\n'),
    /card 1 of the page \("macos"\) is not inside `#install`/,
    'a card outside the install section is not refused',
  )

  assert.match(
    osCardFailures(null, FIXTURE_INSTALL).join('\n'),
    /carries no `main`/,
    'a page with no main is not refused',
  )
})

// ---- the build's own refusals ------------------------------------------------------------

test('a supported system the page cannot draw a card for stops the build, naming the file', (t) => {
  // The page's frontmatter refuses two states rather than rendering a card with nothing in it:
  // a system the file marks supported with no path under it, and one this page carries no glyph
  // for. Both are read here off a build of a copy of the repository, because both are build
  // errors rather than pages: the failure has to name the file, and the page has to not exist.
  const noPath = buildCopy(t, ({ site }) => {
    const file = path.join(site, 'data', 'install.yaml')
    const before = readFileSync(file, 'utf8')
    const after = before.replace(
      '  - id: windows\n',
      '  - id: bsd\n    name: BSD\n    goos: freebsd\n    status: supported\n  - id: windows\n',
    )
    assert.notEqual(after, before, 'the fixture did not add a supported platform, so this case would pass on anything')
    writeFileSync(file, after)
  })
  assert.notEqual(noPath.status, 0, `the build rendered a card for a supported system with no path under it:\n${noPath.output}`)
  assert.match(noPath.output, /BSD \(freebsd\) supported and carries no install path/, 'the failure does not say which system has no path')
  assert.match(noPath.output, /install\.yaml/, 'the failure does not name the file the system came from')

  const noGlyph = buildCopy(t, ({ site }) => {
    const file = path.join(site, 'data', 'install.yaml')
    const before = readFileSync(file, 'utf8')
    const after = before
      .replace(
        '  - id: windows\n',
        '  - id: bsd\n    name: BSD\n    goos: freebsd\n    status: supported\n  - id: windows\n',
      )
      .replace(
        '  - id: plugin\n',
        '  - id: bsd-archive\n    name: BSD archive\n    source: README.md\n    platforms: [freebsd]\n    note: A tarball for BSD.\n    commands:\n      - |-\n        pkg install fugaro\n\n  - id: plugin\n',
      )
    assert.notEqual(after, before, 'the fixture did not add a supported platform and a path for it, so this case would pass on anything')
    writeFileSync(file, after)
  })
  assert.notEqual(noGlyph.status, 0, `the build rendered a card for a system it has no glyph for:\n${noGlyph.output}`)
  assert.match(noGlyph.output, /the card for BSD \(bsd, which website\/data\/install\.yaml marks supported\) is drawn with no glyph/, 'the failure does not say which card has no glyph')
})

test('a console published for a system this page draws no card for stops the build', (t) => {
  // The console's block sits in the card of the system its own data file names, and the two
  // files are one fact: a console published for a system this page draws no card for has no
  // card to sit in, and rendering the download under another system would offer a reader a
  // build for a machine they are not on. The failure names both files.
  const built = buildCopy(t, ({ site }) => {
    const file = path.join(site, 'content', 'downloads.json')
    const data = JSON.parse(readFileSync(file, 'utf8'))
    for (const download of data.downloads) download.platform = 'Windows'
    writeFileSync(file, `${JSON.stringify(data, null, 2)}\n`)
  })
  assert.notEqual(built.status, 0, `the build offered a console download under a system this page draws no card for:\n${built.output}`)
  assert.match(built.output, /downloads\.json publishes the console for Windows/, 'the failure does not name the platform the console is published for')
  assert.match(built.output, /install\.yaml/, 'the failure does not name the install facts it looked the platform up in')
})

test('the site\'s own reader is what the cards are held to', () => {
  // The gate reads `install.yaml` through `content.ts`'s `loadInstallFacts()` rather than
  // through a regex of its own, so the file it holds the page to is the file the page renders
  // from — and a reader that stopped refusing an unknown key would stop the page build, not
  // this gate. What this case holds is the reading itself: the file the repository ships
  // carries supported systems with a path each, which is what the cases above are about.
  const install = loadInstallFacts()
  const supported = install.platforms.filter((platform) => platform.status === 'supported')
  assert.ok(supported.length >= 2, `${INSTALL_FILE} carries ${supported.length} supported platform(s), and the cards this gate is about are one per supported system`)
  for (const platform of supported) {
    assert.ok(
      install.paths.some((entry) => entry.platforms.includes(platform.goos)),
      `${INSTALL_FILE} marks ${platform.name} supported and offers it no path, and a card is the path the file offers that system first`,
    )
  }
  assert.ok(
    install.platforms.some((platform) => platform.status !== 'supported'),
    `${INSTALL_FILE} marks every platform supported, so the untested case the cards must never draw (D94) is not in the file this gate reads`,
  )
  // And the file is the repository's, not the fixture's: the gate above reads the fixture's
  // facts, and this is the one place that says the real file is the one the page is held to.
  assert.ok(
    readFileSync(path.join(REPO, INSTALL_FILE), 'utf8').includes('status: supported'),
    `${INSTALL_FILE} carries no supported platform at all, and the page opens with a card per supported system`,
  )
})
