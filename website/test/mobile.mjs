// website/test/mobile.mjs — the phone app's page (the zoom-diagrams plan
// `docs/website/zoom-diagrams/implementation-plan.md` §3, **Part 3**; the owner's answers of
// 2026-10-21 under D239, D240, D241 and D242).
//
// `/mobile/` is a "coming soon" page, and the whole of this file is the three conditions D240
// put on one, plus the two answers that decide what it may say:
//
//   1. **The badge** (D240's third condition). The page carries a preview badge, it says
//      "Coming soon", and it sits in the block that holds the headline and the lead — a
//      labelled preview is not a claim about today, and an unlabelled page would make the
//      site's own FAQ answer about what exists false.
//   2. **No date** (D240's first condition). A four-digit year and every month name are
//      refused in the page's own text, in its `<title>` and in its description: the phone
//      app's schedule is the owner's, and a date here is a claim this repository cannot back.
//      The plants below are the two the plan names — `in 2027` and `March` — because a
//      refusal nobody has watched fire is a refusal nobody knows the shape of.
//   3. **Every claim marked** (D240's second condition, D241's answer). The claims are
//      `website/data/mobile-claims.yaml`'s rows and this file reads them **literally**, one
//      line at a time, the way `test/caps-and-costs.mjs` reads `typical-costs.yaml`: every row
//      renders, once, inside the list its own `state` names, with the state marked on the card
//      too. The plants move a planned row into the shipped list and a shipped row into the
//      planned one, which is the defect the condition is about — a page whose bullets are all
//      present tense is a page that states four falsehoods the repository documents against.
//   4. **The roster is planned while the phone cannot read it** (ZF23, D241). This is the one
//      reading in this file that pins a *product* fact to a repository file rather than to a
//      document, and it is deliberate: `mobile/src/feed/paths.js` denies `agents/**` to the
//      phone outright (`DENIED_FAMILIES`), so a page that printed the agent roster among the
//      shipped claims would be saying something the phone's own read grant contradicts. The
//      reading is derived from that file — `agents` in `DENIED_FAMILIES` is what turns it on,
//      and a grant that one day names the family turns it off — and its failure names the file.
//   5. **The voice row is on-device and names no speech service** (D242). The wake word's row
//      says where the listening and the recognition happen and nothing about where a transcript
//      would be sent, because the local/remote evaluation switch is design only and not built
//      (ZF28). The service names below are the ones a marketing page reaches for; a page that
//      named one would be previewing a switch the owner has not adopted.
//   6. **No waitlist** (D239, answered 2026-10-21: "no waitlist"). Nothing on the page asks a
//      reader for anything: no form, no control, no `mailto:` and no third party. The privacy
//      page, `linkcheck.mjs`'s `mailto:` refusal, the CSP's `form-action 'self'` and Check
//      69b's "nothing phones home" are all untouched by this part, and the reading below is
//      what holds the page to that rather than to a promise.
//
// **The screenshots are the owner's and the file ships empty** (the plan's Part 3, item 4;
// D251). The page renders no slider at all today — no section, no heading, no frame and no
// picture-shaped hole, the console's own empty-list case — and the fixture build below proves
// the other end of it: a row in `website/data/mobile-screens.yaml` renders the slider, with the
// mobile page's own slot and ids. The fixture names a picture this repository already carries,
// because no agent commits a screenshot (D190) and a fixture that named a file of its own would
// be one.
//
// **The word ceiling, the lead ceiling and the links are not read here**: `test/budget.mjs`
// measures every built page's words, `test/linkcheck.mjs` resolves every link and every anchor
// of every built page, and `test/a11y.mjs` holds the landmarks and the headings of the page
// like any other. A second reading of them here would be a second rule.
//
// How it builds: `test/lib/build.mjs`'s `buildCopy` stages a copy of the repository in a temp
// directory and builds it — the shape `console.mjs`, `console-screens.mjs` and `layout.mjs`
// use. Every build is network-free: the only executable started is the site's own installed
// Astro.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { buildCopy } from './lib/build.mjs'
import { DIST, REPO, find, parse, textOf, walk } from './lib/html.mjs'

/** The claims, as every message below names them. */
const CLAIMS_YAML = 'website/data/mobile-claims.yaml'
/** The screenshots, which ship with no row. */
const SCREENS_YAML = 'website/data/mobile-screens.yaml'
/** The page itself, named by the refusals that are about its own markup. */
const PAGE = 'website/src/pages/mobile.astro'
/** The hook the shared slider marks its section with, handed this page's own slot. */
const SLOT = 'mobile-screens'
/** The phone's own read gate: the file this file pins the roster to (ZF23). */
const PATHS_JS = 'mobile/src/feed/paths.js'
/** The two states a claim may carry, in the order the page renders them. */
const STATES = ['shipped', 'planned']

/** A file of this checkout, as text, by its path from the repository root. */
function repoFile(rel) {
  return readFileSync(path.join(REPO, rel), 'utf8')
}

/** The built `/mobile/` page of a build that is expected to have succeeded. */
function mobilePageOf(built) {
  assert.equal(built.status, 0, `the build failed:\n${built.output}`)
  return readFileSync(path.join(built.dist, 'mobile', 'index.html'), 'utf8')
}

/** The built `/mobile/` page of this checkout, which `npm run check` built before this file. */
function page() {
  return readFileSync(path.join(DIST, 'mobile', 'index.html'), 'utf8')
}

/** A quoted scalar's contents, with the escapes this file's own rows use undone. */
function scalar(raw) {
  const text = raw.trim()
  if (!text.startsWith('"')) return text
  assert.ok(
    text.length >= 2 && text.endsWith('"'),
    `${CLAIMS_YAML}: ${JSON.stringify(raw)} opens a quoted scalar that does not close on the same line, so this reader cannot read the claim — write the claim on one line, as every row of the file does`,
  )
  return text.slice(1, -1).replace(/\\(.)/g, '$1')
}

/**
 * The claims the data file writes, read literally: one row per claim, in the file's order,
 * with the `state` and the `text` each row carries. The file is read rather than a copy of its
 * text typed here, which would be the copy that drifts first — and the rows are what the page
 * is held to, so a claim reworded in the file is a claim the page has to render again.
 */
export function claimRows(source = repoFile(CLAIMS_YAML)) {
  const rows = []
  for (const line of source.split('\n')) {
    const state = /^\s*-\s+state:\s*(\S+)\s*$/.exec(line)
    if (state) {
      rows.push({ state: state[1], text: null })
      continue
    }
    const text = /^\s*text:\s*(\S.*)$/.exec(line)
    if (text && rows.length > 0 && rows[rows.length - 1].text === null) {
      rows[rows.length - 1].text = scalar(text[1])
    }
  }
  assert.ok(
    rows.length > 0,
    `${CLAIMS_YAML} writes no row this reader can find, and the page is those rows: a file this reader cannot read is a file whose claims nothing below holds`,
  )
  for (const row of rows) {
    assert.ok(
      row.text !== null && row.text !== '',
      `${CLAIMS_YAML} writes a row with state ${JSON.stringify(row.state)} and no text this reader can read: every row of the file is a claim, and the page renders the claim's own words`,
    )
  }
  return rows
}

/**
 * The families `mobile/src/feed/paths.js` refuses the phone, read out of the file itself.
 *
 * This is the pin ZF23 is about: the phone's read grant is four families and `agents` is not
 * one of them, so the roster is a family the app cannot read today. The reader asserts it found
 * the list rather than returning an empty one — a renamed constant would otherwise turn the
 * reading below off silently, which is the one way a pin like this rots.
 */
export function deniedFamilies(source = repoFile(PATHS_JS)) {
  const list = /DENIED_FAMILIES\s*=\s*Object\.freeze\(\[([^\]]*)\]\)/.exec(source)
  assert.ok(
    list,
    `${PATHS_JS} carries no \`DENIED_FAMILIES = Object.freeze([...])\` this reader can find: the reading below holds the page's roster claim to the families the phone's own gate refuses, and a renamed list is a reading that would go quiet rather than fail`,
  )
  return [...list[1].matchAll(/'([^']*)'|"([^"]*)"/g)].map((m) => m[1] ?? m[2])
}

/** The element a node sits inside that carries `data-claims`, or null. */
function claimListOf(el) {
  for (let node = el; node; node = node.parent) {
    if (node.attrs && node.attrs.has('data-claims')) return node
  }
  return null
}

/**
 * Every way a built `/mobile/` page breaks the claim rule D240's second condition and D241's
 * answer state: the page renders the rows the data file writes, each one inside the list its
 * own `state` names and with its state marked on the card.
 *
 * The two lists are read by their `data-claims` hook, and the hook is what "the element the
 * page reserves for planned claims" means: a page that renamed its lists is a page this reader
 * refuses rather than one it reads the wrong half of.
 */
export function claimFailures(html, { claims = claimRows() } = {}) {
  const out = []
  const all = [...walk(parse(html))]

  const lists = all.filter((el) => el.attrs.has('data-claims'))
  const drawn = lists.map((el) => el.attrs.get('data-claims'))
  if (drawn.join(' ') !== STATES.join(' ')) {
    out.push(
      `${PAGE} renders ${lists.length} claim list(s) (${JSON.stringify(drawn)}), and the page reserves one per state in the order ${JSON.stringify(STATES)}: the two states are the whole of the marking, so a page that lost one, renamed one or drew them the other way round has a reader guessing which list they are reading (D240, D241)`,
    )
    return out
  }
  for (const list of lists) {
    if (list.name !== 'ul') {
      out.push(`${PAGE} renders the ${JSON.stringify(list.attrs.get('data-claims'))} claims as a <${list.name}>, and the claims are a list of bullets: they are marked one by one, so they are a \`ul\` (D241)`)
    }
  }

  const cards = all.filter((el) => el.name === 'li' && el.attrs.has('data-state'))
  if (cards.length !== claims.length) {
    out.push(
      `${PAGE} renders ${cards.length} claim card(s) and ${CLAIMS_YAML} writes ${claims.length} row(s): every row of the file is a claim the page prints and the page prints no claim the file does not write, so a row added to the file and not to the page (or the other way round) is a bullet the reader does not get (D241)`,
    )
  }

  for (const row of claims) {
    const mine = cards.filter((card) => textOf(card).includes(row.text))
    if (mine.length !== 1) {
      out.push(
        `${PAGE} renders ${mine.length} card(s) carrying the claim ${JSON.stringify(row.text)}: every row of ${CLAIMS_YAML} is a bullet on the page, and a claim that is not there — or that is there twice — is the page and its own data file disagreeing about what the app does (D241)`,
      )
      continue
    }
    const card = mine[0]
    const list = claimListOf(card)
    const where = list ? list.attrs.get('data-claims') : null
    if (where !== row.state) {
      out.push(
        `${PAGE} renders the claim ${JSON.stringify(row.text)} inside the ${JSON.stringify(where)} list, and ${CLAIMS_YAML} gives it state ${JSON.stringify(row.state)}: a bullet is marked shipped or planned by the list it is in and by the mark on its card, so a row rendered in the other list tells a reader the app does something it does not do today — which is the false present tense D240's second condition exists to refuse (D241)`,
      )
    }
    if (card.attrs.get('data-state') !== row.state) {
      out.push(
        `${PAGE} marks the claim ${JSON.stringify(row.text)} as ${JSON.stringify(card.attrs.get('data-state'))} on its own card, and ${CLAIMS_YAML} gives it state ${JSON.stringify(row.state)}: the mark on the card and the list the card is in are the same fact twice, so a card that carries the other state says the opposite of the list it stands in (D241)`,
      )
    }
  }
  return out
}

/**
 * Every way a built `/mobile/` page promises a date. The page's own text, its `<title>` and its
 * description are read — the three places the page itself writes words; the header, the footer
 * and the consent banner are the layout's and shared with every other page, so a date in them
 * is not this page's and is not this reading's.
 */
export function dateFailures(html) {
  const out = []
  const doc = parse(html)
  const title = find(doc, 'title')
  const description = [...walk(doc)].find((el) => el.name === 'meta' && el.attrs.get('name') === 'description')
  const main = find(doc, 'main')
  assert.ok(main, `the built /mobile/ page has no \`main\`, so the text this reading is about cannot be read at all`)
  const places = [
    ["the page's <title>", title ? textOf(title) : ''],
    ["the page's description", description ? description.attrs.get('content') ?? '' : ''],
    ["the page's text", textOf(main)],
  ]
  for (const [what, text] of places) {
    const year = /\b(?:19|20)\d{2}\b/.exec(text)
    if (year) {
      out.push(
        `${what} carries the four-digit year ${JSON.stringify(year[0])}: the phone app's schedule is the owner's, so no date is promised on this page at all — the badge says the app is coming and the claims say what is built, and a year is a promise neither of them makes (D240's first condition)`,
      )
    }
    for (const month of MONTHS) {
      const found = new RegExp(`\\b${month}\\b`, 'i').exec(text)
      if (found) {
        out.push(
          `${what} names the month ${JSON.stringify(found[0])}: a month is a date with the year left out, and D240's first condition is that this page promises no date — not a year, not a month and not a season (D240)`,
        )
      }
    }
  }
  return out
}

/**
 * The month names a date would be written with: the twelve, and the three-letter forms the
 * site's own prose would use. A list rather than a parser, because the rule is a refusal and
 * not a reading of dates.
 */
const MONTHS = [
  'January', 'February', 'March', 'April', 'May', 'June',
  'July', 'August', 'September', 'October', 'November', 'December',
  'Jan', 'Feb', 'Mar', 'Apr', 'Jun', 'Jul', 'Aug', 'Sep', 'Sept', 'Oct', 'Nov', 'Dec',
]

/**
 * Every way a built `/mobile/` page breaks the badge rule: exactly one element marked as the
 * preview badge, saying "Coming soon", in the block that holds the headline — the page's lead
 * block, which is where a reader looks for what a page is before reading what it says.
 */
export function badgeFailures(html) {
  const out = []
  const doc = parse(html)
  const all = [...walk(doc)]
  const badges = all.filter((el) => el.attrs.has('data-preview-badge'))
  if (badges.length !== 1) {
    return [
      `the built /mobile/ page carries ${badges.length} element(s) marked \`data-preview-badge\`, and it carries exactly one: D240's third condition is that a "coming soon" page shows a badge saying it is a preview, and a preview a reader cannot see is a page that reads as a claim about today`,
    ]
  }
  const badge = badges[0]
  const text = textOf(badge).trim()
  if (text !== 'Coming soon') {
    out.push(
      `the preview badge reads ${JSON.stringify(text)}, and D240's third condition asks for a badge that says the page is a preview — the words the page's own copy uses are "Coming soon"`,
    )
  }
  const h1 = find(doc, 'h1')
  if (!h1) {
    out.push(`the built /mobile/ page has no \`h1\`, so the block the badge belongs to cannot be read: the badge sits with the headline and the lead (D240)`)
    return out
  }
  // The block the headline is in: the nearest ancestor that holds both. The badge belongs in
  // it, and before the headline — the console page's own pill is drawn the same way, and a
  // badge below the fold is a badge a reader meets after the claims it is meant to qualify.
  let block = h1
  for (let node = badge; node; node = node.parent) {
    if ([...walk(node)].includes(h1)) {
      block = node
      break
    }
  }
  if (![...walk(block)].includes(badge)) {
    out.push(
      `the preview badge is drawn outside the block that holds the page's headline: the badge belongs with the headline and the lead, which is the page's lead block (D240's third condition; ${PAGE})`,
    )
    return out
  }
  const order = [...walk(block)]
  if (order.indexOf(badge) > order.indexOf(h1)) {
    out.push(
      `the preview badge is drawn after the page's headline: a preview label under the claim it qualifies is a reader meeting the claims first, and the badge belongs above the headline — the shape the console page's own pill is drawn in (D240; ${PAGE})`,
    )
  }
  return out
}

/**
 * Every way a built `/mobile/` page breaks the voice rule: the wake word's row says where the
 * listening and the recognition happen, and the page names no speech service at all (D242).
 *
 * The two halves are one rule seen from both ends: the first says the page says the right
 * thing, and the second says it does not say the other thing — a page that described the wake
 * word and then named the engine it might use would be previewing the local/remote evaluation
 * switch the owner has not adopted (ZF28).
 */
export function voiceFailures(html, { claims = claimRows() } = {}) {
  const out = []
  const text = textOf(parse(html))
  for (const service of SPEECH_SERVICES) {
    if (new RegExp(`\\b${service}\\b`, 'i').test(text)) {
      out.push(
        `the built /mobile/ page names the speech service ${JSON.stringify(service)}: the voice row is on-device only (D242), the local/remote evaluation switch is design only and not built, and a page that named an engine would be previewing a switch the owner has not adopted (ZF28)`,
      )
    }
  }
  const wake = claims.filter((row) => /\bwake word\b/i.test(row.text))
  if (wake.length !== 1) {
    out.push(
      `${CLAIMS_YAML} writes ${wake.length} row(s) about the wake word, and the page carries one: D241's answer names the wake word as one of the planned bullets, and a row that was dropped or duplicated is a bullet the reader does not get (or gets twice)`,
    )
    return out
  }
  if (!/on the phone|on-device|on the device/i.test(wake[0].text)) {
    out.push(
      `the wake word's row (${JSON.stringify(wake[0].text)}) does not say where the listening and the recognition happen: the voice bullet is on-device only, so the row says so in its own words — the page names no speech service and no cloud, because the local/remote switch is not built (D242, ZF28)`,
    )
  }
  return out
}

/**
 * The speech services a "coming soon" page would reach for, and the reason the list is here
 * rather than in the page: naming one is the claim D242 refuses. The words are the products'
 * own names, matched as words so that a longer word containing one is not a hit.
 */
const SPEECH_SERVICES = [
  'Speech-to-Text',
  'Google Speech',
  'Cloud Speech',
  'Whisper',
  'Deepgram',
  'Azure Speech',
  'Amazon Transcribe',
  'ElevenLabs',
  'AssemblyAI',
  'Speechmatics',
  'Rev AI',
]

/**
 * Every way a built `/mobile/` page claims the roster while the phone cannot read it (ZF23).
 *
 * **This is the one reading in this file that pins a product fact to a repository file rather
 * than to a document, and it is deliberate** (the plan's Part 3, Tests). The phone's read grant
 * is four families and `agents/**` is not one of them, so a page that printed the agent roster
 * among the claims it marks *shipped* would be describing a family the app's own gate refuses
 * to ask for. The reading is derived from both ends: `agents` in `DENIED_FAMILIES` is what
 * turns it on, and the words the page prints in its shipped list are what it refuses — so the
 * day the grant names the family, this reading stops applying rather than going stale.
 */
export function rosterFailures(html, { denied = deniedFamilies() } = {}) {
  const out = []
  if (!denied.includes('agents')) return out
  const all = [...walk(parse(html))]
  const shipped = all.find((el) => el.attrs.get('data-claims') === 'shipped')
  if (!shipped) return out
  const text = textOf(shipped)
  const roster = /\broster\b/i.exec(text)
  if (roster) {
    out.push(
      `the built /mobile/ page prints ${JSON.stringify(text.slice(Math.max(0, roster.index - 20), roster.index + 40).trim())} among the claims it marks shipped, and ${PATHS_JS} refuses the phone the \`agents\` family (DENIED_FAMILIES carries "agents"): the roster is what that family would carry, so a page that says the app has it today says something the app's own read gate contradicts — mark the roster planned, or grant the family in ${PATHS_JS} and mark it shipped there (ZF23, D241)`,
    )
  }
  return out
}

/**
 * Every way a built `/mobile/` page asks a reader for something: D239's answer of 2026-10-21
 * is "no waitlist", so the page carries the badge and the copy and nothing else — no form, no
 * control, no `mailto:` and no collector of any kind. The reading is over the page's `main`
 * (the layout's own chrome, which carries the consent banner's buttons, is not this page's).
 */
export function waitlistFailures(html) {
  const out = []
  const main = find(parse(html), 'main')
  assert.ok(main, `the built /mobile/ page has no \`main\`, so the controls this reading is about cannot be read at all`)
  const inside = [...walk(main)]
  const controls = inside.filter((el) => ['form', 'input', 'textarea', 'select', 'button'].includes(el.name))
  if (controls.length > 0) {
    out.push(
      `the built /mobile/ page carries ${controls.length} form control(s) (${controls.map((el) => `<${el.name}>`).join(', ')}): D239's answer of 2026-10-21 is "no waitlist" — the page carries the badge and the copy and nothing else, so a form here is a reader asked for an address by a page that has nowhere to keep it`,
    )
  }
  for (const link of inside.filter((el) => el.name === 'a')) {
    const href = link.attrs.get('href') ?? ''
    if (href.startsWith('mailto:')) {
      out.push(
        `the built /mobile/ page links ${JSON.stringify(href)}, and D239's answer refuses the \`mailto:\` door with the rest of them: a waitlist by mail is a collector the privacy page would have to describe, and the page carries none (D239, ZF32)`,
      )
    }
    if (/^https?:\/\//i.test(href)) {
      out.push(
        `the built /mobile/ page links the external ${JSON.stringify(href)}: a "coming soon" page asks nothing of a reader and sends them nowhere, and a third party on it would be a collector with a CSP and privacy duty the owner did not take (D239, ZU5)`,
      )
    }
  }
  return out
}

/** Every failure of every rule above, as one list: the shipped page is held to all of them. */
export function mobileFailures(html, options = {}) {
  return [
    ...claimFailures(html, options),
    ...dateFailures(html),
    ...badgeFailures(html),
    ...voiceFailures(html, options),
    ...rosterFailures(html, options),
    ...waitlistFailures(html),
  ]
}

// ---------------------------------------------------------------------------------------
// The built page, held to every rule above.
// ---------------------------------------------------------------------------------------

test('the phone app\'s page renders its claims, its badge and no date', () => {
  const failures = mobileFailures(page())
  assert.deepEqual(
    failures,
    [],
    `the built /mobile/ page breaks the rules its own part is written to (the zoom-diagrams plan §3, Part 3; D239–D242):\n  ${failures.join('\n  ')}`,
  )
})

test('the page is the data file\'s rows, and the two states are the two the page draws', () => {
  // The reading above is only worth running if it is reading something: the claims the file
  // writes are the ones the page prints, the file carries both states (a page with one of them
  // is a page whose marking means nothing), and the two hooks the reading resolves are the two
  // the page renders. This is the control the plants below rest on.
  const claims = claimRows()
  assert.deepEqual(
    [...new Set(claims.map((row) => row.state))].sort(),
    [...STATES].sort(),
    `${CLAIMS_YAML} writes the states ${JSON.stringify([...new Set(claims.map((row) => row.state))])}, and the page renders ${JSON.stringify(STATES)}: a file with one state is a file whose marking cannot be read, and a state the page does not draw is a bullet nothing marks (D240, D241)`,
  )
  assert.ok(
    claims.some((row) => row.state === 'shipped') && claims.some((row) => row.state === 'planned'),
    `${CLAIMS_YAML} has no ${claims.some((row) => row.state === 'shipped') ? 'planned' : 'shipped'} row: D241's answer splits the bullets, and the split is what a reader reads to know what the app does today`,
  )
})

// ---------------------------------------------------------------------------------------
// The plants: each refusal fires on the defect it names, on the page that shipped.
// ---------------------------------------------------------------------------------------

test('the claim reading refuses a row rendered in the other state\'s list, and only those', () => {
  const html = page()
  assert.deepEqual(claimFailures(html), [], 'the shipped page does not pass its own reading, so the plants below would prove nothing')

  // A planned row moved into the shipped list: the defect D240's second condition is about —
  // the page then says the app has the roster today.
  const plannedCard = /<li class="card mobile-claim" data-state="planned"[\s\S]*?<\/li>/.exec(html)
  assert.ok(plannedCard, 'the built page carries no planned claim card for this plant to move, so the reading below would be proved against nothing')
  const shippedList = /<ul class="mobile-claims" data-claims="shipped"[^>]*>/.exec(html)
  assert.ok(shippedList, 'the built page carries no shipped claims list for this plant to move a row into')
  const movedIn = html
    .replace(plannedCard[0], '')
    .replace(shippedList[0], shippedList[0] + plannedCard[0])
  const inFailures = claimFailures(movedIn)
  assert.ok(
    inFailures.some((one) => /inside the "shipped" list/.test(one) && /gives it state "planned"/.test(one)),
    `a planned row moved into the shipped list was not refused, and that is the false present tense D240's second condition exists to refuse:\n  ${inFailures.join('\n  ')}`,
  )

  // And the other way round: the one shipped row rendered among the planned ones, which is the
  // page understating what the app does — the same defect seen from the other side, and the
  // reason the reading is a comparison rather than a rule about one list.
  const shippedCard = /<li class="card mobile-claim" data-state="shipped"[\s\S]*?<\/li>/.exec(html)
  assert.ok(shippedCard, 'the built page carries no shipped claim card for this plant to move')
  const plannedList = /<ul class="mobile-claims" data-claims="planned"[^>]*>/.exec(html)
  assert.ok(plannedList, 'the built page carries no planned claims list for this plant to move a row into')
  const movedOut = html
    .replace(shippedCard[0], '')
    .replace(plannedList[0], plannedList[0] + shippedCard[0])
  const outFailures = claimFailures(movedOut)
  assert.ok(
    outFailures.some((one) => /inside the "planned" list/.test(one) && /gives it state "shipped"/.test(one)),
    `a shipped row moved into the planned list was not refused:\n  ${outFailures.join('\n  ')}`,
  )
})

test('the date reading refuses the year and the month the plan names, and only where it looks', () => {
  const html = page()
  assert.deepEqual(dateFailures(html), [], 'the shipped page carries no date, so the plants below would prove nothing')

  // The two plants the plan names: `in 2027` and `March`. Both are written into the page's
  // `main` and nowhere else — the lead is also the page's description, so a plant that took the
  // first match would be testing the description twice and the page's own text not at all.
  const lead = 'The app is in progress:'
  const mainAt = html.indexOf('<main')
  assert.ok(mainAt !== -1 && html.slice(mainAt).includes(lead), 'the built page no longer carries, in its `main`, the lead these plants write into, so the plants below would prove nothing')
  const inMain = (written) => html.slice(0, mainAt) + html.slice(mainAt).replace(lead, written)
  const year = dateFailures(inMain('The app is in progress in 2027:'))
  assert.ok(
    year.some((one) => /^the page's text carries the four-digit year "2027"/.test(one)),
    `a year written into the page's own text was not refused:\n  ${year.join('\n  ')}`,
  )
  const month = dateFailures(inMain('The app is in progress: March brings'))
  assert.ok(
    month.some((one) => /^the page's text names the month "March"/.test(one)),
    `a month name written into the page's own text was not refused:\n  ${month.join('\n  ')}`,
  )

  // And the two places a date would hide that are not the page's prose: its title and its
  // description. Both are the page's own words, and both reach a reader who never opens it.
  const title = dateFailures(html.replace('<title>Mobile · Fugaro</title>', '<title>Mobile · Fugaro · 2027</title>'))
  assert.ok(
    title.some((one) => /^the page's <title> carries the four-digit year/.test(one)),
    `a year in the page's own <title> was not refused:\n  ${title.join('\n  ')}`,
  )
  const description = dateFailures(
    html.replace('name="description" content="Your runs\'', 'name="description" content="Coming in 2027: your runs\''),
  )
  assert.ok(
    description.some((one) => /^the page's description carries the four-digit year/.test(one)),
    `a year in the page's own description was not refused:\n  ${description.join('\n  ')}`,
  )
})

test('the badge reading refuses a page with no badge, with the wrong words, or with it below the headline', () => {
  const html = page()
  assert.deepEqual(badgeFailures(html), [], 'the shipped page carries the badge D240 asks for, so the plants below would prove nothing')

  const badge = /<p class="pill mobile-badge"[^>]*>[\s\S]*?<\/p>/.exec(html)
  assert.ok(badge, 'the built page carries no preview badge for these plants to move or reword')

  const gone = badgeFailures(html.replace(badge[0], ''))
  assert.ok(
    gone.some((one) => /carries 0 element\(s\) marked/.test(one)),
    `a page with no preview badge at all was not refused:\n  ${gone.join('\n  ')}`,
  )

  const reworded = badgeFailures(html.replace(badge[0], badge[0].replace('Coming soon', 'Preview')))
  assert.ok(
    reworded.some((one) => /the preview badge reads "Preview"/.test(one)),
    `a badge that says something other than "Coming soon" was not refused:\n  ${reworded.join('\n  ')}`,
  )

  // The badge below the headline: still on the page, still the right words, and in the wrong
  // place — the defect a reading that only looked for the words would pass.
  const h1End = html.indexOf('</h1>') + '</h1>'.length
  const movedBelow = html.replace(badge[0], '').slice(0, h1End) + badge[0] + html.replace(badge[0], '').slice(h1End)
  const below = badgeFailures(movedBelow)
  assert.ok(
    below.some((one) => /drawn after the page's headline/.test(one)),
    `a badge moved below the headline was not refused:\n  ${below.join('\n  ')}`,
  )
})

test('the voice reading refuses a named speech service and a row that does not say where it happens', () => {
  const html = page()
  assert.deepEqual(voiceFailures(html), [], 'the shipped page names no speech service and says where the voice runs, so the plants below would prove nothing')

  const service = voiceFailures(html.replace('the recognition happen on the phone', 'the recognition happen in the cloud (Speech-to-Text)'))
  assert.ok(
    service.some((one) => /names the speech service "Speech-to-Text"/.test(one)),
    `a page that named a speech service was not refused, and D242 is the answer that keeps one off the page:\n  ${service.join('\n  ')}`,
  )

  const claims = claimRows()
  const wake = claims.find((row) => /\bwake word\b/i.test(row.text))
  assert.ok(wake, `${CLAIMS_YAML} writes no row about the wake word, so this plant has nothing to reword`)
  const silent = voiceFailures(html, {
    claims: claims.map((row) => (row === wake ? { ...row, text: 'A wake word that answers to your voice.' } : row)),
  })
  assert.ok(
    silent.some((one) => /does not say where the listening and the recognition happen/.test(one)),
    `a wake-word row that stopped saying where the recognition happens was not refused:\n  ${silent.join('\n  ')}`,
  )
})

test('the waitlist reading refuses a form and a mailto link, and only those', () => {
  const html = page()
  assert.deepEqual(waitlistFailures(html), [], 'the shipped page asks a reader for nothing, so the plants below would prove nothing')

  const form = waitlistFailures(html.replace('</article>', '<form action="/waitlist"><input type="email" name="email"></form></article>'))
  assert.ok(
    form.some((one) => /carries 2 form control\(s\)/.test(one)),
    `a form on the page was not refused, and D239's answer is "no waitlist":\n  ${form.join('\n  ')}`,
  )

  const mail = waitlistFailures(html.replace('</article>', '<a href="mailto:hi@example.com">Tell me when it ships</a></article>'))
  assert.ok(
    mail.some((one) => /links "mailto:hi@example\.com"/.test(one)),
    `a \`mailto:\` link on the page was not refused:\n  ${mail.join('\n  ')}`,
  )

  const external = waitlistFailures(html.replace('</article>', '<a href="https://example.com/waitlist">Join the list</a></article>'))
  assert.ok(
    external.some((one) => /links the external "https:\/\/example\.com\/waitlist"/.test(one)),
    `a third-party link on the page was not refused:\n  ${external.join('\n  ')}`,
  )
})

// ---------------------------------------------------------------------------------------
// The roster, pinned to the phone's own read gate (ZF23): the one build this file makes.
// ---------------------------------------------------------------------------------------

test('the roster reading refuses a roster marked shipped, and names the phone\'s own gate', (t) => {
  // The plant the plan names: a row inserted into the **data file** with the roster marked
  // shipped. It is a build rather than a string edit because the whole path is the point —
  // the file, the reader that validates it, the page that renders it, and this reading — and
  // because the failure has to name `mobile/src/feed/paths.js` for a reader to know which of
  // the two ends moved.
  const built = buildCopy(t, ({ site }) => {
    writeFileSync(
      path.join(site, 'data', 'mobile-claims.yaml'),
      [
        'version: 1',
        'claims:',
        '  - state: shipped',
        '    text: "Status and spend for every run, read straight from your own Firebase project."',
        '  - state: shipped',
        '    text: "A live agent roster: which agent is on which run."',
        '  - state: planned',
        '    text: "Pairing with a QR scan instead of a typed project id."',
        '',
      ].join('\n'),
    )
  })
  const failures = rosterFailures(mobilePageOf(built))
  assert.ok(
    failures.some((one) => one.includes(PATHS_JS)),
    `a data file that marks the roster shipped built a page this reading did not refuse, or refused it without naming ${PATHS_JS} — the file the claim contradicts:\n  ${failures.join('\n  ')}`,
  )

  // The control: the same reading passes on the page that shipped, which is what makes the
  // refusal above about the planted row rather than about the reading being stuck on.
  assert.deepEqual(rosterFailures(page()), [], 'the shipped page marks the roster planned, and this reading refuses it anyway')
})

test('the roster reading is off when the phone\'s grant names the family', () => {
  // The pin is derived from `mobile/src/feed/paths.js` at both ends, so the day the grant
  // changes the reading stops applying rather than going stale. This case rewrites the file's
  // own list, feeds the reading the roster page the case above refuses, and watches it go
  // quiet: a reading that refused the words whatever the grant said would be a reading that
  // never learns the app grew the family, which is the way a pin like this one rots.
  const source = repoFile(PATHS_JS)
  const granted = source.replace("Object.freeze(['agents', 'outcomes', 'runs', 'fugaro'])", "Object.freeze(['outcomes', 'runs', 'fugaro'])")
  assert.notEqual(granted, source, `this case did not change the denied families, so it would prove nothing: ${PATHS_JS}'s list is no longer the one it was written with — read the file and fix this case rather than deleting it`)
  assert.ok(!deniedFamilies(granted).includes('agents'), `${PATHS_JS}'s rewritten list still denies \`agents\``)

  const shippedList = /<ul class="mobile-claims" data-claims="shipped"[^>]*>/.exec(page())
  assert.ok(shippedList, 'the built page carries no shipped claims list for this case to plant the roster in')
  const roster = page().replace(
    shippedList[0],
    `${shippedList[0]}<li class="card mobile-claim" data-state="shipped"><p class="mobile-claim-text">A live agent roster: which agent is on which run.</p></li>`,
  )
  assert.ok(
    rosterFailures(roster).some((one) => one.includes(PATHS_JS)),
    'the plant this case rests on — a roster in the shipped list — is not refused with the grant as it stands, so the case below would prove nothing',
  )
  assert.deepEqual(
    rosterFailures(roster, { denied: deniedFamilies(granted) }),
    [],
    `the reading still refuses a roster when ${PATHS_JS} no longer denies \`agents\`: the pin reads the grant, not the words (ZF23)`,
  )
})

// ---------------------------------------------------------------------------------------
// The screenshots: no row renders no slider, and a row renders the mobile page's own.
// ---------------------------------------------------------------------------------------

test('the screenshots ship with no row, and the page renders no slider at all', () => {
  const screens = repoFile(SCREENS_YAML)
  assert.match(
    screens,
    /^screens: \[\]$/m,
    `${SCREENS_YAML} carries a row: the pictures are the owner's and arrive as files (D251), and a row here without the picture beside it in website/public/screenshots/ is a slider drawn over a file that is not there — if the owner's captures have landed, this case is the one to update, in the commit that adds them`,
  )

  const all = [...walk(parse(page()))]
  const slider = all.filter((el) => el.attrs.has('data-slider') || el.attrs.get('data-slot') === SLOT)
  assert.deepEqual(
    slider.map((el) => `<${el.name} data-slot="${el.attrs.get('data-slot') ?? ''}">`),
    [],
    `the built /mobile/ page draws slider markup with no row in ${SCREENS_YAML}: with nothing to show, the slider is not there at all — no section, no heading and no frame, because a reader who met an empty box could not tell it from a picture that failed to arrive (the console's own empty-list case)`,
  )
})

test('a row in the screenshots file renders the slider, with this page\'s own slot and ids', (t) => {
  // The other end of the branch above, and the whole of what item 4 of the part asks for: the
  // page is written to render whatever arrives, so adding a slide is a row here and the file it
  // names under `public/screenshots/`, and nothing in the page or the component moves. The
  // picture is one this repository already carries — no agent commits a screenshot (D190), so a
  // fixture that named a file of its own would be the one thing this fixture may not do.
  const built = buildCopy(t, ({ site }) => {
    writeFileSync(
      path.join(site, 'data', 'mobile-screens.yaml'),
      [
        'version: 1',
        'screens:',
        '  - file: console-live.webp',
        '    alt: "The console\'s Live screen, drawn in this fixture as the phone page\'s first slide."',
        '    caption: Live',
        '    width: 1920',
        '    height: 1200',
        '',
      ].join('\n'),
    )
  })
  const html = mobilePageOf(built)
  const slider = [...walk(parse(html))].find((el) => el.attrs.get('data-slot') === SLOT)
  assert.ok(
    slider,
    `the /mobile/ page renders no element marked data-slot="${SLOT}" with one row in ${SCREENS_YAML}: the page is written to render whatever arrives in the file, and the slot it hands the component is this page's own`,
  )
  const images = [...walk(slider)].filter((el) => el.name === 'img')
  assert.equal(images.length, 1, `the slider renders ${images.length} <img> for one row in ${SCREENS_YAML}`)
  assert.equal(images[0].attrs.get('src'), '/screenshots/console-live.webp', 'the one slide\'s src is not the row\'s own file under /screenshots/')
  assert.equal(images[0].attrs.get('alt'), "The console's Live screen, drawn in this fixture as the phone page's first slide.", 'the one slide does not carry the row\'s alt text')
  const captions = [...walk(slider)].filter((el) => el.name === 'figcaption')
  assert.equal(captions.length, 1, `the slider renders ${captions.length} caption(s) for one slide`)
  assert.equal(textOf(captions[0]).trim(), 'Live', 'the one slide\'s caption is not the row\'s caption')
  // The ids the dots point at are the mobile page's, not the console's: the two pages carry the
  // same component, and a shared id prefix would put the console's anchors on this page.
  assert.ok(
    [...walk(slider)].some((el) => (el.attrs.get('id') ?? '').startsWith('mobile-screen-')),
    `the slider's slides carry no \`mobile-screen-\` id: the component is handed this page's own prefix, so a deep link into a slide names a slide of this page`,
  )
  assert.ok(
    !html.includes('id="console-screen-'),
    `the /mobile/ page carries a \`console-screen-\` id, which is the console page's: the two pages render the same component and each hands it its own prefix`,
  )
})

// ---------------------------------------------------------------------------------------
// The data file's own refusals, and the page's guard over an empty state.
// ---------------------------------------------------------------------------------------

test('a claim whose state is neither of the two stops the build, naming the file and both states', (t) => {
  const built = buildCopy(t, ({ site }) => {
    writeFileSync(
      path.join(site, 'data', 'mobile-claims.yaml'),
      [
        'version: 1',
        'claims:',
        '  - state: shipped',
        '    text: "Status and spend for every run."',
        '  - state: maybe',
        '    text: "A wake word."',
        '',
      ].join('\n'),
    )
  })
  assert.notEqual(built.status, 0, `a claim whose state is "maybe" built a site: ${CLAIMS_YAML} is the page's marking, and a state the page does not know is a bullet nothing can mark (D240)`)
  assert.match(built.output, /mobile-claims\.yaml/, `the build failed without naming ${CLAIMS_YAML}, so the message does not say which file to fix:\n${built.output}`)
  assert.match(built.output, /`shipped` or `planned`/, `the build failed without naming the two states a claim may carry:\n${built.output}`)
})

test('a file with no planned row stops the build, because the page says what is still to come', (t) => {
  // The page's own guard, and the reason it is a guard rather than an empty list: a page whose
  // planned list is empty is a heading over nothing, and it reads as a page that says the app
  // is finished. The file's reader refuses an empty file for the same reason, one level up.
  const built = buildCopy(t, ({ site }) => {
    writeFileSync(
      path.join(site, 'data', 'mobile-claims.yaml'),
      [
        'version: 1',
        'claims:',
        '  - state: shipped',
        '    text: "Status and spend for every run."',
        '',
      ].join('\n'),
    )
  })
  assert.notEqual(built.status, 0, `a file with no planned row built a page: the page draws a heading over an empty list, which reads as an app with nothing left to build`)
  assert.match(built.output, /"planned" has no row/, `the build failed without naming the state that is missing its row:\n${built.output}`)
  assert.match(built.output, /mobile-claims\.yaml/, `the build failed without naming ${CLAIMS_YAML}:\n${built.output}`)
})
