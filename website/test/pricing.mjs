// website/test/pricing.mjs — the pricing page (the subscriptions plan, Task S7.2; the owner's
// rulings D403, D430 and T22).
//
// **The page prints the plan's tier table, so the rule here is that it prints it.** The three
// tiers and the twelve feature rows come from `docs/subscriptions/spec.md`'s "Tiers and
// features" section — the owner's plan, kept in this repository as the excerpt D403 leaves of
// it — and this file reads that section and holds the built page to it, row for row: the same
// names, the same order and the same answer in each of the three columns. The page writes its
// own copy of the table (it is a page's data, and the repository's own price scan reads a page
// and not the markdown it was read from), so the two can drift only past this case, which names
// the row that moved.
//
//   1. **The page is the route the plan names.** `/pricing/` is built, with the page's own title
//      and one `h1`, and it carries the page's own marker — the artifact a reader receives, not
//      the source that produced it.
//   2. **The three tiers are the spec's three, in the spec's order, with the spec's own words
//      for how each one is bought** ("Community (free forever), Team (self-serve, per seat) and
//      Enterprise (annual, invoiced)"). A tier added to the plan, a reordered page and a rewording
//      are three different failures and each one arrives here.
//   3. **The Team price is the placeholder, and the page prints no figure at all.** The figure is
//      published at the owner's publication (S7.4) and never enters this repository (D430, T22),
//      so what the price line carries is the sentence the page states, and a currency amount or a
//      percentage anywhere on the page is refused — read the way the repository's own scan reads
//      one (`internal/cli/docs_subscriptions_test.go`, S0.1: the page's text with its stylesheet
//      and its script dropped), because this page is the one a figure would land on. **The
//      publication is the commit that moves both**: it replaces the sentence, drops this case's
//      pin and teaches S0.1's exact allow-list the figure, which is why neither of them is
//      written to let a figure through quietly.
//   4. **The Enterprise call to action is the file's booking link.** T22 answers the tier with
//      "contact us", the site's contact route is the link `website/data/landing.yaml` holds, and
//      the page reads it through the same loader the campaign pages do — so this holds the built
//      page's href to the file's own value, and refuses a second link or a second route in the
//      card. No e-mail address is published on this site (the owner's ruling of 2026-10-19, held
//      by `test/privacy.mjs`), so the link is the whole of what "contact us" may be.
//   5. **The page's own links are the four the page promises.** The Enterprise card's contact
//      link and the three lines under the table — the features page, the caps and costs page and
//      the Subscription Terms — and nothing else inside the page's own article. The layout's
//      chrome is not this page's, and the page-wide gates (`a11y.mjs`, `budget.mjs`, `copy.mjs`,
//      `fold.mjs`, `linkcheck.mjs`, `meta.mjs`, `origins.mjs`, `spacing.mjs`, `wording.mjs`) read
//      every built page and so read this one without a line here.
//
// A gate whose refusal has never been seen to fire is a gate nobody knows the shape of, so the
// last case mutates the **built page** — one edit per refusal, taken from the artifact itself
// rather than from a fixture that approximates it — and proves each refusal arrives naming what
// is wrong. Standard library only; no network (Global Constraint 4).

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { DIST, REPO, findAll, parse, textOf } from './lib/html.mjs'
import { loadLanding } from '../src/lib/landing.mjs'

/** The route the plan fixes, and the file the build serves it from. */
const ROUTE = '/pricing/'
const BUILT_PAGE = path.join(DIST, 'pricing', 'index.html')

/** The page's `<h1>` and the word its `<title>` is built from. */
const TITLE = 'Pricing'

/** The subscriptions plan: the tier sentence and the tier table the page prints. */
const SPEC = path.join(REPO, 'docs', 'subscriptions', 'spec.md')

/**
 * The line the three tiers' own words are read from, and the header of the table the rows are
 * read from. Both are pinned as the shapes this reader knows: a plan that rewrote either one is
 * a plan this file has to be re-read against, and a reader that guessed would be a reader holding
 * the page to its own invention. The tier list ends where the plan's own sentence does — at the
 * `)` of the last tier, a full stop after it — so a later sentence on the same line is not read
 * as part of the list.
 */
const TIERS_LINE = 'Three tiers: '
const TIERS_END = ').'
const TABLE_HEADER = ['Feature', 'Community', 'Team', 'Enterprise']

/**
 * The Team price the page states while the figure is unpublished: what stands in the price line
 * until the owner's publication (S7.4) puts the number there. Pinned, because it is copy — and
 * because the case below refuses any other price line on the page, so a figure that arrived here
 * would have to move this line to pass.
 */
const TEAM_PRICE = 'Price published at launch'

/** The Enterprise tier's way in, as the spec's sentence words it and the page labels it. */
const CONTACT_LABEL = 'Contact us'

/** The three questions under the table, each a page this one sends a reader to. */
const NOTE_LINKS = ['/features/', '/caps-and-costs/', '/legal/subscription-terms/']

// The currency and rate shapes the repository's own scan refuses, as the page's own reading of
// them (`internal/cli/docs_subscriptions_test.go`: a currency amount, a percentage). A page's
// stylesheet is dropped before the scan, which is what lets a layout width be a percentage.
const CURRENCY = /[$€£¥]\s?[\d,]+|\b\d[\d,.]*\s?(?:USD|EUR|GBP|dollars?|euros?|pounds?)\b/i
// The percent sign carries no boundary of its own after it, which is the repository's own shape: a
// discount is written "20% off", with a space where a `\b` would be, so a reader demanding a word
// character after the sign would miss the commonest way a page prints one.
const PERCENT = /[0-9][0-9,]*(?:\.[0-9]+)?\s?(?:%|(?:percent|per cent)\b)/i

/** A page's text as a reader reads it: one line, one space between two words. */
function flat(text) {
  return text.replace(/\s+/g, ' ').trim()
}

/** Whether an element carries a class, among the scope class Astro adds beside it. */
function hasClass(el, name) {
  return (el.attrs?.get('class') ?? '').split(/\s+/).includes(name)
}

// ---- the plan, as the plan writes it ---------------------------------------------------

/** The repository file the page's rows are read from, as text. */
function specText() {
  return readFileSync(SPEC, 'utf8')
}

/**
 * The spec's three tiers, in the order its sentence names them: the tier's name and the words it
 * uses for how the tier is bought. The sentence is the plan's own summary of the table's three
 * columns, so it is also the check that the table's columns are the three tiers a reader is
 * offered.
 */
export function specTiers(text = specText()) {
  const line = text.split('\n').find((one) => one.startsWith(TIERS_LINE))
  const end = line === undefined ? -1 : line.indexOf(TIERS_END, TIERS_LINE.length)
  assert.ok(
    line !== undefined && end !== -1,
    `${SPEC} states no "${TIERS_LINE}…${TIERS_END}" sentence, and that sentence is where the three tiers' names and the words this page prints for how each one is bought are read from (S7.2). A plan that reworded it is a plan this file has to be re-read against.`,
  )
  const list = line.slice(TIERS_LINE.length, end + 1)
  const tiers = [...list.matchAll(/([A-Z][\w-]*) \(([^)]+)\)/g)].map((m) => ({ name: m[1], sold: m[2] }))
  assert.equal(
    tiers.length,
    3,
    `the plan's tier sentence reads ${JSON.stringify(list)}, and this reader found ${tiers.length} tiers in it: the sentence names each tier and puts how it is bought in brackets, which is the shape the page prints.`,
  )
  return tiers
}

/**
 * The spec's "Tiers and features" table, row for row: the feature's name and which of the three
 * tiers includes it. A cell that is neither `Yes` nor `No` throws rather than reading as false,
 * because a cell is an answer and a reader that guessed at one would hold the page to a word the
 * plan did not write.
 */
export function specRows(text = specText()) {
  const lines = text.split('\n')
  const header = lines.findIndex((line) => line.trim() === `| ${TABLE_HEADER.join(' | ')} |`)
  assert.ok(
    header !== -1,
    `${SPEC} carries no "| ${TABLE_HEADER.join(' | ')} |" table header, and that table is the rows this page prints and the columns it prints them under (S7.2). The section is "Tiers and features", and a plan that reflowed it is a plan this file has to be re-read against.`,
  )
  const tiers = specTiers(text)
  const rows = []
  for (const line of lines.slice(header + 2)) {
    if (!line.startsWith('|')) break
    const cells = line
      .replace(/^\||\|$/g, '')
      .split('|')
      .map((cell) => cell.trim())
    assert.equal(
      cells.length,
      TABLE_HEADER.length,
      `the plan's row ${JSON.stringify(line)} carries ${cells.length} cells, and the table has ${TABLE_HEADER.length} columns: a row the page cannot lay out is a row it may not print.`,
    )
    const [name, ...answers] = cells
    const row = { name }
    for (const [at, tier] of tiers.entries()) {
      const answer = answers[at]
      assert.ok(
        answer === 'Yes' || answer === 'No',
        `the plan answers ${JSON.stringify(answer)} under ${tier.name} for ${JSON.stringify(name)}, and a cell is a Yes or a No: a figure a later revision of the plan puts in this table (the price row the excerpt of D430 leaves out, say) is a cell this page may not print at all (T22).`,
      )
      row[tier.name.toLowerCase()] = answer === 'Yes'
    }
    rows.push(row)
  }
  assert.ok(rows.length > 0, `${SPEC}'s tier table carries no row below its header, so the case below would hold the page to nothing.`)
  return rows
}

// ---- the built page, as a reader receives it -------------------------------------------

/** The built page, as the artifact it is — the file a reader receives. */
export function builtPage(html = null) {
  if (html !== null) return html
  try {
    return readFileSync(BUILT_PAGE, 'utf8')
  } catch {
    throw new Error(
      `${ROUTE} was not built: ${path.relative(DIST, BUILT_PAGE)} is not there, and this check reads the artifact rather than the source — run \`npm run check\`, which builds the site first, rather than this file alone.`,
    )
  }
}

/** The page's own part of the document: the article `pricing.astro` renders, so the layout's
 * chrome is not what these cases read. */
function articleOf(html) {
  return findAll(parse(html), (el) => el.name === 'article' && el.attrs.has('data-pricing-page'))[0] ?? null
}

/** The three tier cards, in document order: the tier's key, its name heading and the lines under
 * it — how it is bought, its price line (Team's) and the links in the card. */
export function cardsOf(html) {
  const article = articleOf(html)
  if (article === null) return []
  const items = findAll(article, (el) => el.name === 'li' && el.attrs.has('data-tier'))
  return items.map((item) => ({
    key: item.attrs.get('data-tier'),
    name: flat(textOf(findAll(item, (el) => el.name === 'h3')[0] ?? { children: [] })),
    sold: flat(textOf(findAll(item, (el) => hasClass(el, 'pricing-tier-sold'))[0] ?? { children: [] })),
    price: flat(textOf(findAll(item, (el) => hasClass(el, 'pricing-tier-price'))[0] ?? { children: [] })),
    links: findAll(item, (el) => el.name === 'a').map((a) => ({ label: flat(textOf(a)), href: a.attrs.get('href') ?? '' })),
  }))
}

/** The page's one table, as the rows a reader reads: the feature's name and the three answers,
 * in the column order the table draws. A cell counts as included when it carries the page's own
 * mark, which is what the page prints for an included feature and the whole of what it prints. */
export function rowsOf(html) {
  const article = articleOf(html)
  if (article === null) return { table: false, heads: [], rows: [] }
  const table = findAll(article, (el) => el.name === 'table' && hasClass(el, 'pricing-table'))[0]
  if (table === undefined) return { table: false, heads: [], rows: [] }
  const heads = findAll(table, (el) => el.name === 'th' && el.attrs.get('scope') === 'col').map((th) => flat(textOf(th)))
  const body = findAll(table, (el) => el.name === 'tbody')[0]
  const rows = findAll(body ?? { children: [] }, (el) => el.name === 'tr').map((tr) => {
    const name = flat(textOf(findAll(tr, (el) => el.name === 'th')[0] ?? { children: [] }))
    const answers = findAll(tr, (el) => el.name === 'td').map((td) => flat(textOf(td)) !== '')
    return { name, answers }
  })
  return { table: true, heads, rows }
}

// ---- the rules -------------------------------------------------------------------------

/** Every way the page's tiers are not the plan's three, or []. */
export function tierFailures(html, tiers = specTiers()) {
  const cards = cardsOf(html)
  const out = []
  if (cards.length !== tiers.length) {
    out.push(`the built page draws ${cards.length} tier cards, and the plan names ${tiers.length} tiers: a tier a reader is not offered and a tier the page invented are both a price list that is not the plan's`)
    return out
  }
  for (const [at, tier] of tiers.entries()) {
    const card = cards[at]
    if (card.name !== tier.name) {
      out.push(`the built page's card ${at + 1} is ${JSON.stringify(card.name)}, and the plan's tier ${at + 1} is ${JSON.stringify(tier.name)}: the cards are the plan's tiers in the plan's order (S7.2)`)
    }
    // The words are the plan's; the capital is the page's. The plan's sentence is prose and its
    // brackets sit mid-sentence ("free forever"), while the page prints each one as the line
    // under a tier's name — and a case that refused the capital would be refusing the page for
    // setting a line as a line rather than for writing words the plan does not.
    if (card.sold.toLowerCase() !== tier.sold.toLowerCase()) {
      out.push(`the built page sells ${JSON.stringify(card.name)} with ${JSON.stringify(card.sold)}, and the plan words it ${JSON.stringify(tier.sold)}: how a tier is bought is the plan's sentence, and a page that reworded it is a page arguing with its own plan`)
    }
  }
  return out
}

/** Every way the page states a price it may not, or []. */
export function priceFailures(html, expected = TEAM_PRICE) {
  const text = flat(
    textOf(parse(html.replace(/<style\b[\s\S]*?<\/style>/gi, ' ').replace(/<script\b[\s\S]*?<\/script>/gi, ' '))),
  )
  const out = []
  for (const [what, pattern] of [
    ['a currency amount', CURRENCY],
    ['a percentage', PERCENT],
  ]) {
    const found = pattern.exec(text)
    if (found !== null) {
      out.push(`the built page carries ${what} (${JSON.stringify(found[0])}): the figure is published at the owner's publication and never enters this repository (D430, T22), so a price, a discount, a fee rate or the Enterprise minimum belongs in the owner's private business repository — and this page is where the Team price would land (S0.1, S7.4)`)
    }
  }

  const cards = cardsOf(html)
  const priced = cards.filter((card) => card.price !== '')
  if (priced.length !== 1) {
    out.push(`the built page states a price on ${priced.length} of its ${cards.length} tier cards (${priced.map((card) => card.key).join(', ') || 'none'}): one tier is sold for a figure that is published at S7.4, and a tier that states a price the plan has not published is a price list nobody wrote`)
    return out
  }
  const [card] = priced
  if (card.key !== 'team') {
    out.push(`the built page states a price on the ${JSON.stringify(card.key)} card, and the tier whose price is published at S7.4 is Team: the Community tier is free forever and the Enterprise tier is invoiced, so neither has a figure to print`)
  }
  if (card.price !== expected) {
    out.push(`the built page's Team price line reads ${JSON.stringify(card.price)}, and the placeholder this repository carries is ${JSON.stringify(expected)}: the figure is the owner's to publish (S7.4), and the commit that puts it there moves this pin and S0.1's allow-list with it rather than leaving a sentence where a price belongs`)
  }
  return out
}

/** Every way the Enterprise card's one call to action is not the file's booking link, or []. */
export function contactFailures(html, bookingURL = loadLanding().calendlyURL) {
  const cards = cardsOf(html)
  const enterprise = cards.find((card) => card.key === 'enterprise')
  if (enterprise === undefined) {
    return ['the built page draws no Enterprise card, so the tier T22 answers with "contact us" has nowhere to send a buyer']
  }
  const out = []
  const elsewhere = cards.filter((card) => card.key !== 'enterprise' && card.links.length > 0)
  for (const card of elsewhere) {
    out.push(`the built page's ${JSON.stringify(card.key)} card carries ${card.links.length} link(s) (${card.links.map((link) => link.href).join(', ')}): the one call to action on this page is the Enterprise tier's — the free tier asks for nothing and the Team tier is self-serve (T22)`)
  }
  if (enterprise.links.length !== 1) {
    out.push(`the built page's Enterprise card carries ${enterprise.links.length} links, and it carries one: "contact us" is the tier's whole answer, and a second link is a second route nobody decided on`)
    return out
  }
  const [link] = enterprise.links
  if (link.label !== CONTACT_LABEL) {
    out.push(`the built page labels the Enterprise link ${JSON.stringify(link.label)}, and the tier's answer is ${JSON.stringify(CONTACT_LABEL)} (T22)`)
  }
  if (link.href !== bookingURL) {
    out.push(`the built page's Enterprise link goes to ${JSON.stringify(link.href)}, and website/data/landing.yaml writes ${JSON.stringify(bookingURL)}: the booking link has one home, and a buyer sent to a second address is a lead nobody is watching (R3)`)
  }
  return out
}

/** Every way the page's rows are not the plan's table, or []. */
export function rowFailures(html, spec = specRows(), tiers = specTiers()) {
  const { table, heads, rows } = rowsOf(html)
  const out = []
  const wanted = ['Feature', ...tiers.map((tier) => tier.name)]
  if (!table) {
    out.push(`the built page carries no table of the plan's rows, and the page's half of its offer is that table: a reader choosing a tier reads which features each one includes (S7.2)`)
    return out
  }
  if (heads.join(' | ') !== wanted.join(' | ')) {
    out.push(`the built page's table is headed ${JSON.stringify(heads)}, and the plan's table is headed ${JSON.stringify(wanted)}: the columns are the three tiers a reader is choosing between`)
  }
  if (rows.length !== spec.length) {
    out.push(`the built page prints ${rows.length} feature rows, and the plan's table carries ${spec.length}: a row the page dropped is a feature a buyer is not told about, and a row the page added is a claim the plan does not make`)
    return out
  }
  for (const [at, row] of spec.entries()) {
    const printed = rows[at]
    if (printed.name !== row.name) {
      out.push(`the built page's row ${at + 1} is ${JSON.stringify(printed.name)}, and the plan's row ${at + 1} is ${JSON.stringify(row.name)}: the rows are the plan's, in the plan's order (S7.2)`)
      continue
    }
    const answers = tiers.map((tier) => row[tier.name.toLowerCase()])
    if (printed.answers.join(',') !== answers.join(',')) {
      out.push(`the built page answers ${JSON.stringify(printed.answers)} for ${JSON.stringify(row.name)}, and the plan answers ${JSON.stringify(answers)} under ${JSON.stringify(tiers.map((tier) => tier.name))}: a mark under the wrong tier sells a feature to a tier that does not have it`)
    }
  }
  return out
}

/** Every way the page's own links are not the four it promises, or []. */
export function linkFailures(html, bookingURL = loadLanding().calendlyURL) {
  const article = articleOf(html)
  if (article === null) return ['the built page carries no <article data-pricing-page>, and that marker is the page\'s own: this case reads the page and not the layout\'s chrome']
  const hrefs = findAll(article, (el) => el.name === 'a').map((a) => a.attrs.get('href') ?? '')
  const wanted = [bookingURL, ...NOTE_LINKS]
  const out = []
  for (const href of wanted) {
    const n = hrefs.filter((one) => one === href).length
    if (n !== 1) {
      out.push(`the built page carries ${n} links to ${JSON.stringify(href)}, and it carries one: a buyer's question this page answers and a question it sends away are each asked once`)
    }
  }
  for (const href of hrefs) {
    if (!wanted.includes(href)) {
      out.push(`the built page links ${JSON.stringify(href)} and this page answers four things: the Enterprise tier's contact, what the features do, what the software costs beyond the subscription, and the terms the paid tiers are sold under`)
    }
  }
  return out
}

// ---- the gate --------------------------------------------------------------------------

test('the page is built, and it is the page the plan names', () => {
  const html = builtPage()
  const doc = parse(html)

  const title = findAll(doc, (el) => el.name === 'title')[0]
  assert.equal(
    flat(textOf(title ?? { children: [] })),
    `${TITLE} · Fugaro`,
    `the built page's title reads ${JSON.stringify(title ? flat(textOf(title)) : '')}, and the layout builds it from the page's own word`,
  )

  const h1s = findAll(doc, (el) => el.name === 'h1').map((el) => flat(textOf(el)))
  assert.deepEqual(h1s, [TITLE], `the built page carries the h1 headings ${JSON.stringify(h1s)}, and the page's own word is one heading (a11y.mjs asserts the count over every page; this is the page's own name)`)

  assert.ok(
    articleOf(html) !== null,
    'the built page carries no <article data-pricing-page>: that marker is what this file reads the page\'s own part of the document by, and the layout\'s chrome is not the page (S7.2)',
  )
})

test('the three tiers are the plan\'s three, in the plan\'s order, in the plan\'s words', () => {
  const failures = tierFailures(builtPage())
  assert.deepEqual(failures, [], `the built page's tiers are not the plan's:\n  ${failures.join('\n  ')}`)
})

test('the Team price is the placeholder, and the page prints no figure', () => {
  const failures = priceFailures(builtPage())
  assert.deepEqual(failures, [], `the built page states a price it may not:\n  ${failures.join('\n  ')}`)
})

test('the Enterprise card carries one link, and it is the file\'s booking link', () => {
  const failures = contactFailures(builtPage())
  assert.deepEqual(failures, [], `the built page's Enterprise call to action is not the one T22 answers with:\n  ${failures.join('\n  ')}`)
})

test('the feature rows are the plan\'s table, row for row', () => {
  const failures = rowFailures(builtPage())
  assert.deepEqual(failures, [], `the built page does not print the plan's tier table:\n  ${failures.join('\n  ')}`)
})

test('the page links the four things it promises, and nothing else', () => {
  const failures = linkFailures(builtPage())
  assert.deepEqual(failures, [], `the built page's links are not this page's four:\n  ${failures.join('\n  ')}`)
})

// ---- the refusals, kept ----------------------------------------------------------------

/**
 * The page's own part of the document, as the bytes the build wrote: from its `<article` to the
 * `</article>` that closes it. The cases below edit this rather than the whole document, because
 * the words they move are the page's own and the layout's chrome is not: the menu carries a
 * **Community** link, and a rename that landed on the menu instead of on a card would prove
 * nothing about the page.
 */
function articleHTML(html) {
  const open = html.indexOf('<article')
  const close = html.indexOf('</article>', open)
  assert.ok(
    open !== -1 && close !== -1,
    'the built page carries no <article>…</article> for the refusals below to edit, and each of them edits the page\'s own part of the document',
  )
  return html.slice(open, close + '</article>'.length)
}

/** A built page with one edit, so a refusal is proved against the artifact rather than a fixture
 * that approximates it. An edit that did not land throws rather than reading as a page the rule
 * is happy with. */
function mutated(html, from, to) {
  const out = html.replace(from, to)
  assert.notEqual(
    out,
    html,
    `the built page carries no ${from instanceof RegExp ? from.source : JSON.stringify(from)}, so this case's edit would not land and its refusal would prove nothing`,
  )
  return out
}

/** The named refusal, from a set of failure lines. */
function refuses(t, what, out, want) {
  t.diagnostic(`refused: ${what} — ${out[0] ?? '(nothing)'}`)
  assert.notEqual(out.length, 0, `${what} was accepted: a gate that never fires is a gate nobody knows the shape of`)
  assert.ok(
    out.join('\n').includes(want),
    `${what} is refused as ${JSON.stringify(out.join(' '))}, and no line names ${JSON.stringify(want)}`,
  )
}

test('a figure on the page, a moved row and a dropped placeholder are each refused by name', (t) => {
  const page = articleHTML(builtPage())

  refuses(
    t,
    'a price in the page\'s copy',
    priceFailures(mutated(page, 'Self-serve, per seat', 'Self-serve, per seat, $20 a month')),
    'a currency amount',
  )
  refuses(
    t,
    'a discount in the page\'s copy',
    priceFailures(mutated(page, 'Annual, invoiced', 'Annual, invoiced, 20% off the first year')),
    'a percentage',
  )
  refuses(
    t,
    'a price line where the placeholder is',
    priceFailures(mutated(page, TEAM_PRICE, 'Contact us for pricing')),
    JSON.stringify('Contact us for pricing'),
  )
  refuses(
    t,
    'a tier renamed',
    tierFailures(mutated(page, '>Community</h3>', '>Freeloader</h3>')),
    'Freeloader',
  )
  refuses(
    t,
    'a feature row renamed',
    rowFailures(mutated(page, 'Contract terms, security review, invoicing', 'Invoicing')),
    'the plan\'s row',
  )
  // The one cell the edit empties is the article's first mark, in the first row's Community
  // column, and the plan says that row is in Community: the reading is the artifact's own marks,
  // so the edit has to reach one of them without depending on how the page spells the glyph.
  refuses(
    t,
    'a mark under the wrong tier',
    rowFailures(mutated(page, /(<td[^>]*class="[^"]*\bpricing-included\b[^"]*"[^>]*>)[\s\S]*?(<\/td>)/, '$1$2')),
    'a mark under the wrong tier',
  )
  refuses(
    t,
    'a second route into Enterprise',
    contactFailures(mutated(page, '>Contact us<', '>Contact sales<')),
    'Contact us',
  )
  refuses(
    t,
    'a fifth link in the page',
    linkFailures(mutated(page, '>features page<', '>features page</a> and the <a href="/faq/">FAQ<')),
    '/faq/',
  )
})
