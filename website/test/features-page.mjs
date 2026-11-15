// website/test/features-page.mjs — the features page
// (`docs/feature-matrix/implementation-plan.md`, Task T4a; the owner's answers D2440, D2443 and
// D2448 of 2026-11-15).
//
// **The page's job is to print the matrix, so the rule here is that it prints it.** Every row on
// the page — the name, the explanation, the status and the three plan columns — comes out of the
// snapshot `website/src/content.ts`'s `loadFeatures()` answers with, and the page writes no cell
// of its own (the plan's "Conventions every task follows": the page writes no claim of its own).
// The snapshot's shape is the contract's own business (`website/src/lib/feature-matrix-model.mjs`,
// task C1, with its own cases in `website/test/feature-matrix-model.mjs`) and the generator's is
// task T1's; **nothing of that is repeated here**. What this file
// is for is the half neither of those can see: the *built page*, and whether a reader gets the
// matrix the snapshot carries.
//
//   1. **One table, the snapshot's rows, in the snapshot's order.** The page's one `<table>`, its
//      five columns (the feature, the status and the three plans of D2440), one row per snapshot
//      row, each with the name and the explanation the snapshot gives it. A row that stopped
//      rendering, a row the page invented and a feature whose explanation the page dropped are the
//      ways a page drifts from its snapshot, and each one shows up here as a count or a name. The
//      prose is read here rather than left to the word gates because the page is exempt from the
//      whole-page ceiling (`test/lib/exempt.mjs`, D2448): a template edit that dropped an
//      explanation would lose a reader the matrix's own words and nothing else would say so.
//   2. **Every status is a mark and a word, and they are the legend's.** The legend is read out of
//      the built page and the rows are held to it: a status whose cell carries a different mark, or
//      a word the legend does not define, is refused. This is what "not colour alone" means in a
//      test — the *word* is the status a reader reads, the mark beside it is what they scan for,
//      and the tone is the third telling and never the first.
//   3. **The three plan columns carry a mark exactly where the snapshot says the row is in the
//      plan, and nothing where it says it is not.** A cell is the matrix's own data: a check where
//      the plan holds the feature and an empty cell where it does not, so the page chooses no word
//      for the state that is not a mark (reconciliation O1 — a footnote about the Community column
//      is the owner's to write, at the preview, and not this page's to invent).
//   4. **Nothing the page may not print.** No spec name, no status.md section name, no task id and
//      no date anywhere on the built page (`leakProblems`, with the contract's own fixture spec
//      list), and no price: a currency amount or a percentage in the page's own text, read with
//      its stylesheet and its script dropped, which is the shape the repository's own Go scan
//      reads (`internal/cli/docs_subscriptions_test.go`, S0.1).
//   5. **The page is reachable, and the one link off it resolves.** `/features/` carries exactly
//      one link to `/compatibility/` — what runs where is that page's question — and the
//      page-wide gates (`a11y.mjs`, `budget.mjs`, `copy.mjs`, `fold.mjs`, `linkcheck.mjs`,
//      `meta.mjs`, `origins.mjs`, `os-cards.mjs`) read every built page and so read this one
//      without a line here.
//
// **The cases below hold the page to `loadFeatures()`, not to the fixture.** The snapshot is the
// contract's fixture while the stub is the reader's stand-in (`website/src/lib/feature-matrix-stub.mjs`,
// task C1), and task I1 replaces that with the matrix out of the repository; a case that read the
// fixture would go red on the day the real rows landed, which is the day nobody would be looking
// at this file. So the shape, the legend and the plans are read against the loader's own answer —
// the same call the build makes — and the one case that is about the fixture is skipped once the
// stub is deleted, naming the file that takes it over.
//
// A gate whose refusal has never been seen to fire is a gate nobody knows the shape of, so the
// last case mutates the **built page** — one edit per refusal, taken from the artifact itself
// rather than from a fixture that approximates it — and proves each refusal arrives naming what is
// wrong. Standard library only; no network (Global Constraint 4).

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { DIST, SITE, builtPages, findAll, parse, textOf } from './lib/html.mjs'
import { loadFeatures, FEATURE_PLANS, FEATURE_STATUSES } from '../src/content.ts'
import { FIXTURE_SPECS, leakProblems, readFixtureSnapshot } from '../src/lib/feature-matrix-model.mjs'

/** The built page these cases are about. */
const BUILT_PAGE = path.join(DIST, 'features', 'index.html')

/** The page's route, which is the menu's href once task T4b lands and the page's own directory. */
const ROUTE = '/features/'

/** The compatibility page, which is the one link this page carries off itself (task T7 links back). */
const COMPATIBILITY = '/compatibility/'

/** The page's `<h1>`, and the word the page's title is built from. */
const TITLE = 'Features'

/**
 * The page's three plan headings (D2440), in the words a reader reads over the columns: the
 * contract's own keys are `FEATURE_PLANS`, and these are the copy the page prints for them. They
 * are pinned here because the headings are copy — the site's tests pin the words a page prints —
 * and they are looked up by plan, so the order the table draws them in is the contract's rather
 * than a second one this file keeps.
 */
const PLAN_LABELS = { community: 'Community', team: 'Team', enterprise: 'Enterprise' }

/** The stub task I1 deletes: while it is there, `loadFeatures()` answers with the fixture. */
const STUB = path.join(SITE, 'src', 'lib', 'feature-matrix-stub.mjs')

/** A page's text as a reader reads it: one line, one space between two words. */
function flat(text) {
  return text.replace(/\s+/g, ' ').trim()
}

/** Whether an element carries a class, among the scope class Astro adds beside it. */
function hasClass(el, name) {
  return (el.attrs?.get('class') ?? '').split(/\s+/).includes(name)
}

/** The built page, as the artifact it is — the file a reader receives. */
function builtPage() {
  assert.ok(
    existsSync(BUILT_PAGE),
    `${ROUTE} was not built: ${path.relative(DIST, BUILT_PAGE)} is not there, and this check reads the artifact rather than the source: run \`npm run check\`, which builds the site first, rather than this file alone`,
  )
  return readFileSync(BUILT_PAGE, 'utf8')
}

/** The snapshot the page is built from, read through the loader the build itself calls. */
function facts() {
  const doc = loadFeatures()
  assert.ok(
    Array.isArray(doc.rows) && doc.rows.length > 0,
    'the snapshot carries no row, so the cases below would check the page against nothing. The page is one table of the matrix\'s rows, and an empty matrix is the contract\'s own business first (`website/test/feature-matrix-model.mjs` admits an empty list; the generator that fills it is task T1)',
  )
  return doc
}

/**
 * The legend, as the built page draws it: each item's status, the mark it draws and the word it
 * prints. A mark or a word that is not there is read as an empty string so the rule below can
 * refuse it — that is the whole point of reading the legend out of the artifact rather than out
 * of the page's source.
 */
function legendOf(html) {
  const doc = parse(html)
  const items = findAll(doc, (el) => el.name === 'li' && hasClass(el, 'feat-legend-item'))
  const entries = new Map()
  for (const item of items) {
    const status = item.attrs.get('data-status') ?? ''
    const marks = findAll(item, (el) => hasClass(el, 'feat-mark'))
    const mark = marks.length === 1 ? flat(textOf(marks[0])) : ''
    entries.set(status, { mark, word: flat(textOf(item)).replace(mark, '').trim() })
  }
  return { entries, items }
}

/** The status a cell draws: the span's `data-status`, its mark and its word. */
function statusOf(cell) {
  const spans = findAll(cell, (one) => hasClass(one, 'feat-status'))
  if (spans.length !== 1) return { status: '', mark: '', word: '', marks: 0, spans: spans.length }
  const el = spans[0]
  const marks = findAll(el, (one) => hasClass(one, 'feat-mark'))
  const mark = marks.length === 1 ? flat(textOf(marks[0])) : ''
  return {
    status: el.attrs.get('data-status') ?? '',
    mark,
    word: flat(textOf(el)).replace(mark, '').trim(),
    marks: marks.length,
    spans: 1,
  }
}

/** One cell's content, by the class the page marks it with; null when the cell carries none. */
function single(el, name) {
  const found = findAll(el, (one) => hasClass(one, name))
  return found.length === 1 ? found[0] : null
}

/** The page's own article: the part of the document this page writes, chrome excluded. */
function articleOf(html) {
  return findAll(parse(html), (el) => el.name === 'article' && el.attrs.has('data-features-page'))[0] ?? null
}

/**
 * Every way the built page fails to render the snapshot it is built from, as failure lines. The
 * shape is the whole of it: the page's one table, the five columns (the feature, the status and
 * the three plans in `FEATURE_PLANS`' own order), the snapshot's rows in the snapshot's order,
 * each row's name, its explanation, its status and a mark under each plan it is in, and the
 * snapshot's stamp.
 */
export function shapeFailures(html, doc) {
  const out = []
  const root = parse(html)
  const legend = legendOf(html).entries

  const tables = findAll(root, (el) => el.name === 'table')
  if (tables.length !== 1) {
    out.push(
      `the built page carries ${tables.length} table(s) and the matrix is one: a reader compares one feature with the next down one table, and a second table is a second reading of the same snapshot`,
    )
    return out
  }
  const table = tables[0]

  // The table is a section's, and the section is headed: a table with nothing over it is a table
  // a reader reaches from nowhere, and a heading is also what a screen reader lists it under.
  const sections = findAll(root, (el) => el.name === 'section' && findAll(el, (one) => one.name === 'table').includes(table))
  const heading = sections.length === 1 ? findAll(sections[0], (el) => el.name === 'h2')[0] : undefined
  if (heading === undefined || flat(textOf(heading)) === '') {
    out.push('the page\'s table does not sit under a section\'s own `<h2>`: the table is a block of the page, and a block nothing heads is a block a reader arrives at without knowing what it is')
  }

  // The snapshot's stamp, the one version-shaped fact the page carries: a reader who cannot see
  // which release the matrix was read against cannot tell how old a status is.
  const stamps = findAll(root, (el) => el.name === 'p' && hasClass(el, 'feat-as-of'))
  if (stamps.length !== 1) {
    out.push(
      `the built page carries ${stamps.length} stamp line(s) and the snapshot carries one release: the line under the legend is the page's one version-shaped fact, and a page without it is a matrix a reader cannot date`,
    )
  } else if (!flat(textOf(stamps[0])).includes(doc.asOf)) {
    out.push(
      `the page's stamp line is ${JSON.stringify(flat(textOf(stamps[0])))} and the snapshot was read against ${JSON.stringify(doc.asOf)}: the line prints the release the rows were read at, and a line that does not carry it tells a reader the wrong release`,
    )
  }

  // The columns: the feature, the status, and one per plan **in the contract's own order**
  // (D2440) — which is what `FEATURE_PLANS` carries, and why the labels are looked up by plan
  // rather than written in the order the page happens to draw them. The three labels are the
  // page's own vocabulary (the plan's keys are not words a reader reads) and they are written here
  // because a heading is copy: a column renamed on the page fails this case with both lists in its
  // message, and a fourth plan the contract gains and this file has no label for fails it rather
  // than reading as a column the page drew and the test never named.
  const columns = ['Feature', 'Status', ...FEATURE_PLANS.map((plan) => PLAN_LABELS[plan])]
  const head = findAll(table, (el) => el.name === 'thead')[0]
  const header = (head === undefined ? [] : findAll(head, (el) => el.name === 'th')).map((el) => flat(textOf(el)))
  if (header.join(' ') !== columns.join(' ')) {
    out.push(
      `the table is headed ${JSON.stringify(header)} and its columns are ${JSON.stringify(columns)}: three plan columns, in the contract's order, is the owner's answer of 2026-11-15 (D2440), and a column that moved or went is a matrix a reader cannot compare with the one FEATURES.md carries`,
    )
  }

  const body = findAll(table, (el) => el.name === 'tbody')[0]
  const bodyRows = (body?.children ?? []).filter((child) => child.name === 'tr')
  if (bodyRows.length !== doc.rows.length) {
    out.push(
      `the table carries ${bodyRows.length} row(s) and the snapshot carries ${doc.rows.length}: every row of the snapshot is a row a reader reads, so a page that dropped one is a fact about the product that reached nobody — and this page is exempt from the whole-page word ceiling, so no other gate counts them (D2448)`,
    )
  }

  doc.rows.forEach((row, index) => {
    const drawn = bodyRows[index]
    if (drawn === undefined) return
    const cells = drawn.children.filter((child) => child.name === 'th' || child.name === 'td')
    if (cells.length !== columns.length) {
      out.push(
        `the row ${JSON.stringify(row.name)} carries ${cells.length} cell(s) and the table has ${columns.length} column(s): a row that lost its status or a plan is a row a reader reads as a different claim`,
      )
      return
    }

    const what = cells[0]
    if (what.name !== 'th' || what.attrs.get('scope') !== 'row') {
      out.push(`the row ${JSON.stringify(row.name)} does not open with a <th scope="row">: the row's name is its header cell, and a table whose rows have no headers is a table a screen reader reads as cells without names`)
    }
    const name = single(what, 'feat-name')
    const explanation = single(what, 'feat-explanation')
    if (name === null || flat(textOf(name)) !== row.name) {
      out.push(
        `the row is named ${JSON.stringify(name === null ? '' : flat(textOf(name)))} and the snapshot names it ${JSON.stringify(row.name)}: the page prints the snapshot's own name for a row`,
      )
    }
    if (explanation === null || flat(textOf(explanation)) !== row.explanation) {
      out.push(
        `the row ${JSON.stringify(row.name)} renders ${JSON.stringify(explanation === null ? '' : flat(textOf(explanation)))} and the snapshot's explanation for it is ${JSON.stringify(row.explanation)}: what the feature does is the snapshot's own sentence, and a page that rewrote it would be claiming something nothing holds`,
      )
    }

    const status = statusOf(cells[1])
    const entry = legend.get(status.status)
    if (entry === undefined) {
      out.push(`the row ${JSON.stringify(row.name)} draws the status ${JSON.stringify(status.status)}, which the legend above the table does not define: a mark a reader cannot look up is a status a reader cannot read`)
    } else {
      if (status.marks !== 1 || status.mark !== entry.mark) {
        out.push(`the row ${JSON.stringify(row.name)} draws ${status.marks} mark(s) (${JSON.stringify(status.mark)}) for ${JSON.stringify(row.status)} and the legend draws ${JSON.stringify(entry.mark)}: the mark is the status's, and it means the same thing in the legend and in a row`)
      }
      if (status.word !== entry.word) {
        out.push(`the row ${JSON.stringify(row.name)} prints ${JSON.stringify(status.word)} for ${JSON.stringify(row.status)} and the legend prints ${JSON.stringify(entry.word)}: the legend is the page's key to its own statuses, so the same status is one word wherever it is drawn`)
      }
    }

    FEATURE_PLANS.forEach((plan, at) => {
      const cell = cells[at + 2]
      if (!hasClass(cell, 'feat-plan')) {
        out.push(`the cell under the ${JSON.stringify(plan)} column of the row ${JSON.stringify(row.name)} is not the page's plan cell: the three plan columns are read as the plan cells they are, so a column that stopped being one is a mark this rule would otherwise read out of whatever cell is there`)
        return
      }
      const checks = findAll(cell, (el) => hasClass(el, 'feat-check'))
      const text = flat(textOf(cell))
      if (row.plans[plan] === true && (checks.length !== 1 || text === '')) {
        out.push(
          `the row ${JSON.stringify(row.name)} is in the ${JSON.stringify(plan)} plan and its cell carries ${checks.length} mark(s): the mark is what a reader scans the column for, and a row the snapshot puts in a plan with no mark is a feature a reader cannot find there`,
        )
      }
      if (row.plans[plan] === false && (checks.length !== 0 || text !== '')) {
        out.push(
          `the row ${JSON.stringify(row.name)} is not in the ${JSON.stringify(plan)} plan and its cell carries ${JSON.stringify(text)}: the matrix prints a mark where the plan holds the feature and nothing where it does not, so a page that filled the empty cell is the page writing a word the snapshot never said (reconciliation O1)`,
        )
      }
    })
  })

  // The feature's own name is what a reader looks a row up by, and the snapshot's ids are public
  // (its rows carry them into FEATURES.md): the rows are held to the snapshot's order, so a page
  // that sorted them is refused rather than read as the same matrix.
  const drawn = bodyRows.map((row) => {
    const what = row.children.filter((child) => child.name === 'th' || child.name === 'td')[0]
    const name = what === undefined ? null : single(what, 'feat-name')
    return name === null ? '' : flat(textOf(name))
  })
  const wanted = doc.rows.map((row) => row.name)
  if (drawn.length > 0 && drawn.join(' | ') !== wanted.join(' | ')) {
    out.push(`the page prints the features as ${JSON.stringify(drawn)} and the snapshot carries them as ${JSON.stringify(wanted)}: the page prints the snapshot's rows in the snapshot's order, which is the matrix's own order and not a second one the page chose`)
  }

  return out
}

/**
 * Every way the legend fails to be a key to the page, as failure lines: a status with no mark or
 * no word, two statuses drawn with the same mark, and a legend that does not hold the contract's
 * own three statuses (D2443).
 *
 * The last one is one-directional, and that is the whole of the difference between this page and
 * the compatibility page: a status no row uses today is still a status a reader meets tomorrow
 * (a feature that slips from Available to Coming soon), so the legend lists the contract's three
 * and the rule here refuses a fourth, rather than refusing one the snapshot happens not to use.
 */
export function legendFailures(html, doc) {
  const out = []
  const { entries, items } = legendOf(html)

  if (items.length !== entries.size) {
    out.push(`the legend carries ${items.length} item(s) and ${entries.size} status(es): two items of the legend are the same status`)
  }
  for (const [status, entry] of entries) {
    if (status === '') {
      out.push('a legend item carries no data-status: the item is the page\'s key, and a key with no status marked on it reads as an entry nothing points to')
      continue
    }
    if (entry.mark === '') out.push(`the legend draws no mark for ${JSON.stringify(status)}: the mark is what a reader scans the rows for, and an empty one is a status drawn in tone alone — which is the one telling this page may not rely on`)
    if (entry.word === '') out.push(`the legend prints no word for ${JSON.stringify(status)}: the word is the status a reader reads, whatever their sight or their printer`)
  }

  const marks = [...entries.values()].map((entry) => entry.mark).filter((mark) => mark !== '')
  if (new Set(marks).size !== marks.length) {
    out.push(`the legend draws the marks ${JSON.stringify(marks)}, and two statuses share one: a mark a reader cannot tell from another mark is a mark that answers nothing`)
  }

  const statuses = new Set(doc.rows.map((row) => row.status))
  for (const status of statuses) {
    if (!entries.has(status)) out.push(`the snapshot gives rows the status ${JSON.stringify(status)} and the legend does not define it: the page prints a status its own key does not carry`)
  }
  if (entries.size !== FEATURE_STATUSES.length) {
    out.push(`the legend lists ${entries.size} status(es) and the contract's closed set has ${FEATURE_STATUSES.length} (${JSON.stringify([...FEATURE_STATUSES])}, D2443): the legend is the page's key to the statuses a row may carry, so the three are the three`)
  }
  for (const status of entries.keys()) {
    if (status !== '' && !FEATURE_STATUSES.includes(status)) {
      out.push(`the legend defines ${JSON.stringify(status)}, which is not one of the contract's statuses (${JSON.stringify([...FEATURE_STATUSES])}): the page would be keying a status no snapshot may carry (D2443)`)
    }
  }

  return out
}

/**
 * Every way the page prints what it may not, as failure lines: a spec name, status.md's section
 * names, a task id or a date anywhere in the page's own text (R1 and R8), read by the contract's
 * own `leakProblems` with the fixture's spec list — the list the contract carries while the
 * snapshot is its fixture, and the hyphenated names this page's rows could leak.
 */
export function leakFailures(html) {
  const text = flat(textOf(parse(html)))
  const out = []
  if (text === '') {
    out.push('the built page reads as no text at all, so this case would find no leak in a page that has none to find: the reading is the page\'s words, and an empty one means the reader is broken rather than the page clean')
    return out
  }
  for (const problem of leakProblems(text, FIXTURE_SPECS)) {
    out.push(`the built page carries ${problem}, and a spec name, a status page section name, a task id and a date are the four the page may not print (R1, R8): the matrix's rows are user-language and its stamp is a release`)
  }
  return out
}

// The currency and rate shapes the repository's own scan refuses, as the page's own reading of
// them (`internal/cli/docs_subscriptions_test.go`: a currency amount, a percentage). A page's
// stylesheet is dropped before the scan, which is what lets a layout width be a percentage.
const CURRENCY = /[$€£¥]\s?[\d,]+|\b\d[\d,.]*\s?(?:USD|EUR|GBP|dollars?|euros?|pounds?)\b/i
// The percent sign carries no boundary of its own after it, which is the repository's own shape: a
// discount is written "20% off", with a space where a `\b` would be, so a reader demanding a word
// character after the sign would miss the commonest way a page prints one.
const PERCENT = /[0-9][0-9,]*(?:\.[0-9]+)?\s?(?:%|(?:percent|per cent)\b)/i

/** Every price the built page prints, as failure lines. */
export function priceFailures(html) {
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
      out.push(`the built page carries ${what} (${JSON.stringify(found[0])}): a price is a figure the subscription documents own, and the site's pages may not print one (S0.1) — neither the plans' nor the product's`)
    }
  }
  return out
}

// ---- the gate --------------------------------------------------------------------------

test('the page is built, and it is the page the layout names', () => {
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
})

test('the built page renders the snapshot: one table, the rows, the statuses and the plans', () => {
  const failures = shapeFailures(builtPage(), facts())
  assert.deepEqual(failures, [], `the built page does not render the feature matrix:\n  ${failures.join('\n  ')}`)
})

test('every status is the legend\'s mark and the legend\'s word, and the legend is the contract\'s three', () => {
  const doc = facts()
  const failures = legendFailures(builtPage(), doc)
  assert.deepEqual(failures, [], `the page's legend is not a key to its own statuses:\n  ${failures.join('\n  ')}`)

  // The statuses the snapshot uses, each with the mark and the word the page draws for it — the
  // whole of what "not by colour alone" means, read off the artifact. The tone is the third
  // telling; the mark and the word are the first two, and both are here.
  const legend = legendOf(builtPage()).entries
  for (const status of new Set(doc.rows.map((row) => row.status))) {
    const entry = legend.get(status)
    assert.ok(entry, `the page's legend carries no entry for ${JSON.stringify(status)}`)
    assert.notEqual(entry.mark, '', `the legend draws ${JSON.stringify(status)} with no mark`)
    assert.notEqual(entry.word, '', `the legend draws ${JSON.stringify(status)} with no word`)
  }
})

test('nothing the page may not print is on it, and it prints no price', () => {
  const html = builtPage()
  const leaks = leakFailures(html)
  assert.deepEqual(leaks, [], `the built page carries something R1 keeps off it:\n  ${leaks.join('\n  ')}`)
  const prices = priceFailures(html)
  assert.deepEqual(prices, [], `the built page prints a price:\n  ${prices.join('\n  ')}`)
})

test('the page carries one link to the compatibility page, and it resolves', () => {
  const html = builtPage()
  const article = articleOf(html)
  assert.ok(article, 'the built page has no <article data-features-page>: this case reads the page\'s own part of the document, and the marker is the page\'s own')
  const links = findAll(article, (el) => el.name === 'a' && el.attrs.get('href') === COMPATIBILITY)
  assert.equal(
    links.length,
    1,
    `the page carries ${links.length} link(s) to ${COMPATIBILITY}, and it carries one: what runs where is the compatibility matrix's question, and the page that answers it is the one a reader is sent to (the feature matrix's plan, Task T4a)`,
  )
  assert.notEqual(flat(textOf(links[0])), '', `the page's link to ${COMPATIBILITY} has no text, so a screen reader reads it as a link with no name (a11y.mjs refuses that over every page)`)

  // And the route the link points at is a page of this build rather than a name: the page-wide
  // linkcheck is the gate that follows every link, and this is the one link this page owns.
  assert.ok(
    builtPages(DIST).some((page) => page.url === COMPATIBILITY),
    `the build produced no page at ${COMPATIBILITY}, and this page links it: the two tables are read together, and a link to a route nothing serves is a reader sent nowhere`,
  )
})

/**
 * Why the fixture case below does not run, or false while it does. The stub is task I1's to
 * delete, and a case that ran after the deletion would be checking the loader against a file
 * nothing reads any more.
 */
const STUB_IS_GONE = existsSync(STUB)
  ? false
  : 'the stub is gone: task I1 deleted it, so loadFeatures() reads the matrix out of the repository, and website/test/feature-matrix-stamp.mjs is the test that holds the built page to the block it reads'

test('the page renders the contract\'s own fixture while the stub is the snapshot', { skip: STUB_IS_GONE }, () => {
  // Task T4a's own interface line: the page consumes the contract's snapshot shape through the
  // stub, and its test renders against the fixture. The fixture is read here rather than through
  // the stub, because the stub is deleted by I1 while this file stays (`src/lib/feature-matrix-model.mjs`
  // keeps the fixture for exactly that reason).
  const snapshot = readFixtureSnapshot()
  assert.deepEqual(
    loadFeatures(),
    { asOf: snapshot.as_of, rows: snapshot.rows },
    'the loader answers with something other than the contract\'s fixture while the stub is still the snapshot it reads: the stub is the stand-in for the real builder, and the page would be rendering a matrix no test holds',
  )
})

// ---- the refusals, kept ------------------------------------------------------------------

/**
 * The built page with one edit, and what the reader says about it. `last` edits the anchor's last
 * occurrence instead of its first, for a string the page carries twice: the lead is both its own
 * paragraph and the head's `<meta name="description">`, and the description stands first, so a
 * first-occurrence plant would land in an attribute — where this reading, which takes the page's
 * text, cannot see it, and the case would pass without proving anything. The anchor itself is
 * always required to be there: an anchor that drifted is a case that stopped testing.
 */
function mutated(from, to, last = false) {
  const html = builtPage()
  const where = last ? html.lastIndexOf(from) : html.indexOf(from)
  assert.ok(
    where !== -1,
    `this case's anchor ${JSON.stringify(from)} is not in the built page, so the edit it makes proves nothing — fix the anchor against the artifact, never the rule`,
  )
  return html.slice(0, where) + to + html.slice(where + from.length)
}

test('every refusal fires on the defect it names, on the built page itself', () => {
  const doc = facts()
  const shape = (html) => shapeFailures(html, doc)
  const refuses = (failures, needle, why) => {
    assert.ok(
      failures.some((failure) => failure.includes(needle)),
      `${why}\nThe failures this reading found were:\n${failures.map((failure) => `  - ${failure}`).join('\n') || '  (none)'}`,
    )
  }

  // The page is the artifact these cases read, so each plant is one edit to it — the defect as
  // the build would ship it, not a fixture that approximates the page.
  assert.deepEqual(shape(builtPage()), [], 'the built page does not pass the shape rule, so the plants below prove nothing')

  // The legend drawn before the table, so the page's first `>✓</span>` is the legend's own mark: a
  // mark a reader cannot see, which is the same defect as a status drawn in tone alone.
  refuses(legendFailures(mutated('>✓</span>', '></span>'), doc), 'draws no mark for "available"', 'a legend mark a reader cannot see is not refused')

  // A legend word that is not the word the rows print: the legend stops being a key.
  refuses(shape(mutated('>Available</li>', '>Included</li>')), 'the legend prints "Included"', 'a legend whose word for a status is not the word the rows print is not refused')

  // A row's status word changed: the same defect from the other side. The body's status span closes
  // on the word, and the legend's item closes on `</li>`, so this anchor is a row's.
  refuses(shape(mutated('>✓</span>Available</span>', '>✓</span>Coming soon</span>')), 'prints "Coming soon" for "available"', 'a row whose status word the legend does not define is not refused')

  // A row's explanation gone: what the feature does is the snapshot's own sentence.
  refuses(shape(mutated('class="feat-explanation"', 'class="feat-explain"')), 'the snapshot\'s explanation for it is', 'a row whose explanation the page dropped is not refused')

  // A plan mark the snapshot never set: the page filling a cell the matrix leaves empty. The
  // defect is planted on the other side of the reading — the page is the artifact it is, and the
  // snapshot is held to a plan it does not carry — because a plant in the markup would have to
  // write a mark into a cell whose only anchor is the `data-astro-cid` attribute Astro adds, and
  // an anchor that depends on the build's own scope attribute is a case that stops firing the day
  // the attribute changes. What it proves is the same thing: the reader compares the two.
  const withoutCommunity = { ...doc, rows: doc.rows.map((row) => ({ ...row, plans: { ...row.plans, community: false } })) }
  refuses(shapeFailures(builtPage(), withoutCommunity), 'is not in the', 'a plan cell the page filled where the snapshot says the row is not in the plan is not refused')

  // And the other way: a mark the snapshot set, dropped from the page. The class is the anchor the
  // reader looks the mark up by, so the plant removes the mark from every row at once.
  refuses(shape(mutated('class="feat-check"', 'class="featcheck"')), 'is in the', 'a plan cell whose mark the page dropped is not refused')

  // A plan cell that stopped being the page's plan cell at all: the column a reader reads the mark
  // under.
  refuses(shape(mutated('class="feat-plan"', 'class="featplan"')), 'is not the page\'s plan cell', 'a plan column whose cell stopped being a plan cell is not refused')

  // A row that lost its name: the feature a reader looks up.
  refuses(shape(mutated('class="feat-name"', 'class="featnames"')), 'the snapshot names it', 'a row whose name the page dropped is not refused')

  // The stamp naming a release the snapshot does not: the page's one version-shaped fact. The
  // anchor is built from the snapshot, so this case survives the release that moves it.
  refuses(
    shape(mutated(`at ${doc.asOf}.`, 'at v0.0.0.')),
    'the snapshot was read against',
    'a stamp line that does not carry the snapshot\'s release is not refused',
  )

  // The two copy leaks the page is held to (R1) — a task id and a date — planted in the lead a
  // reader reads. **Where the plant lands matters twice.** The lead stands twice in the document:
  // as its own paragraph, and as the head's `<meta name="description">`, which comes first — so
  // every plant below takes the *last* occurrence, the paragraph, because this reading takes the
  // page's text and a plant into an attribute would prove nothing. And the text runs are joined to
  // one line, so the lead's own first word sits against the `h1`'s last letter ("FeaturesWhat…"),
  // where neither a task id opening a sentence nor a date's leading digit is a whole token — which
  // is what `leakProblems` reads. The lead's second sentence is the place both hold: a boundary on
  // its left, prose on its right.
  const sentence = '. Every row says'
  refuses(leakFailures(mutated(sentence, '. T4a: every row says', true)), 'a task id', 'a task id on the built page is not refused')
  refuses(leakFailures(mutated(sentence, '. As of 2026-11-15, every row says', true)), 'a date', 'a date on the built page is not refused')

  // And a price, in the two shapes the repository's own scan refuses.
  refuses(priceFailures(mutated('>What Fugaro does today', '>What $20 buys today', true)), 'a currency amount', 'a currency amount on the built page is not refused')
  refuses(priceFailures(mutated('>What Fugaro does today', '>What 20% off buys today', true)), 'a percentage', 'a percentage on the built page is not refused')
})
