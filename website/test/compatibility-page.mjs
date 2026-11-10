// website/test/compatibility-page.mjs — the compatibility page
// (`docs/compatibility-matrix/implementation-plan.md`, Task CM2; the owner's answers
// D1520–D1539 of 2026-11-07, and the owner's approval of the navigation entry of 2026-11-10).
//
// **The page's job is to print the file, so the rule here is that it prints it.** Every claim on
// the page comes out of `website/data/compatibility.yaml` (D1537), which
// `website/test/compatibility.mjs` is the gate for: that file is the one place a compatibility
// claim is written, and its own test holds its rows to their rules — the closed set of statuses
// and editions, the evidence path behind every `supported` and `built-not-enabled` row, the
// `never_say` phrases, no price and no date. **Nothing of that is repeated here.** What this
// file is for is the half that file cannot see: the *built page*, and whether a reader gets the
// matrix the file carries. So it reads the same parsed file through that module's
// `compatibilityFacts()` (one reader, not two) and holds the artifact to it:
//
//   1. **One table per group, with that group's rows.** The section named by the group's `id`,
//      the group's own title over it, one table, the group's rows in the file's order, each with
//      the name, the note, the status and the edition the file gives it. A row that stopped
//      rendering, a group whose section moved, and a row the page invented are the three ways a
//      page drifts from its file, and each one shows up here as a count or a name.
//   2. **Every status is a mark and a word, and they are the legend's.** The legend is read out
//      of the built page and the rows are held to it: a status whose cell carries a different
//      mark, or a word the legend does not define, is refused. This is what "not colour alone"
//      means in a test — the *word* is the status a reader reads, the mark beside it is what
//      they scan for, and the colour is the third telling and never the first.
//   3. **None of the file's `never_say` phrases is in the built HTML.** The file carries the
//      words the page may not be made of — the ones a reader of the spec would expect and the
//      product cannot back — and the list is read from the file rather than repeated here, so a
//      phrase added there is refused on the page in the same commit.
//   4. **The page is reachable, and by its own name.** The site's menu carries
//      `/compatibility/` on every page that draws the menu (the owner's approval of 2026-11-10,
//      recorded in `src/layouts/Base.astro` and pinned in `test/base-styles.mjs`), the page
//      itself is the one the menu marks current, and the docs hub carries it under the page's
//      own `<h1>` — which is the check that the hub's typed entry title and the page agree. The
//      page-wide gates (`a11y.mjs`, `linkcheck.mjs`, `budget.mjs`, `origins.mjs`, `copy.mjs`,
//      `fold.mjs`, `meta.mjs`) read every built page and so read this one without a line here.
//
// A gate whose refusal has never been seen to fire is a gate nobody knows the shape of, so the
// last case mutates the **built page** — one edit per refusal, taken from the artifact itself
// rather than from a fixture that approximates it — and proves each one arrives naming what is
// wrong. Standard library only; no network (Global Constraint 4).

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { DIST, builtPages, findAll, parse, textOf } from './lib/html.mjs'
import { compatibilityFacts } from './compatibility.mjs'

/** The built page these cases are about. */
const BUILT_PAGE = path.join(DIST, 'compatibility', 'index.html')

/** The page's route, which is the menu's href, the hub's href and the page's own `<h1>`. */
const ROUTE = '/compatibility/'

/** The page's title, as the layout renders it: the page's own word, then the site's name. */
const TITLE = 'Compatibility · Fugaro'

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
    `${path.relative(DIST, BUILT_PAGE)} is not there, and this check reads the artifact rather than the source: run \`npm run check\`, which builds the site first, rather than this file alone`,
  )
  return readFileSync(BUILT_PAGE, 'utf8')
}

/** The file the page renders, read by the reader that is the file's own gate. */
function facts() {
  const doc = compatibilityFacts()
  assert.ok(
    Array.isArray(doc.groups) && doc.groups.length > 0,
    'website/data/compatibility.yaml carries no group, so the cases below would check the page against nothing. The page has one section per group, and a file with none is the file\'s own gate\'s business first (website/test/compatibility.mjs)',
  )
  return doc
}

/**
 * The legend, as the built page draws it: each item's status, the mark it draws and the word it
 * prints. A mark or a word that is not there is a legend item a reader cannot read, and it is
 * refused rather than read as an empty string — that is the whole point of reading the legend
 * out of the artifact rather than out of the page's source.
 */
function legendOf(html) {
  const doc = parse(html)
  const items = findAll(doc, (el) => el.name === 'li' && hasClass(el, 'compat-legend-item'))
  const out = new Map()
  for (const item of items) {
    const status = item.attrs.get('data-status') ?? ''
    const marks = findAll(item, (el) => hasClass(el, 'compat-mark'))
    const mark = marks.length === 1 ? flat(textOf(marks[0])) : ''
    const word = flat(textOf(item)).replace(mark, '').trim()
    out.set(status, { mark, word, item })
  }
  return { entries: out, items }
}

/** The status a cell draws: the span's `data-status`, its mark and its word. */
function statusOf(cell) {
  const spans = findAll(cell, (one) => hasClass(one, 'compat-status'))
  if (spans.length !== 1) return { status: '', mark: '', word: '', marks: 0, spans: spans.length }
  const el = spans[0]
  const status = el.attrs.get('data-status') ?? ''
  const marks = findAll(el, (one) => hasClass(one, 'compat-mark'))
  const mark = marks.length === 1 ? flat(textOf(marks[0])) : ''
  return { status, mark, word: flat(textOf(el)).replace(mark, '').trim(), marks: marks.length, spans: 1 }
}

/**
 * Whether a group's table carries an Edition column. The file gives a group's rows an edition or
 * it does not, and never a mix — its own gate refuses a row with one in a group whose others have
 * none (`website/test/compatibility.mjs` keeps the group list as `EDITION_GROUPS`) — so the rows
 * themselves answer this, and this file keeps no second list of group ids to drift from that one.
 */
function carriesEdition(group) {
  return group.rows.some((row) => row.edition !== undefined)
}

/** One cell's content, by the class the page marks it with; null when the cell carries none. */
function single(el, name) {
  const found = findAll(el, (one) => hasClass(one, name))
  return found.length === 1 ? found[0] : null
}

/**
 * Every way the built page fails to render the file it is built from, as failure lines. The
 * shape is the whole of it: the groups, in the file's order, one section and one table each,
 * the file's rows in the file's order, and each row's name, note, status and edition.
 */
export function shapeFailures(html, doc) {
  const out = []
  const root = parse(html)
  const legend = legendOf(html).entries

  const sections = findAll(root, (el) => el.name === 'section' && findAll(el, (one) => one.name === 'table').length > 0)
  const tables = findAll(root, (el) => el.name === 'table')
  if (sections.length !== doc.groups.length || tables.length !== doc.groups.length) {
    out.push(
      `the built page carries ${sections.length} section(s) holding a table and ${tables.length} table(s), and the file carries ${doc.groups.length} group(s): the page is one section and one table per group, so a group it stopped rendering and a table it invented both read here (D1537)`,
    )
  }

  doc.groups.forEach((group, at) => {
    const named = sections.filter((el) => el.attrs.get('id') === group.id)
    if (named.length !== 1) {
      out.push(`the built page carries ${named.length} section(s) with the id ${JSON.stringify(group.id)}: the group is one section, and it is the anchor the menu and a link to the page name`)
      return
    }
    const section = named[0]
    if (sections[at] !== section) {
      out.push(`the group ${JSON.stringify(group.id)} is section ${sections.indexOf(section) + 1} of ${sections.length} on the page and it is group ${at + 1} of the file: the page prints the file's groups in the file's order (the spec's own order; src/content.ts refuses a file whose groups moved)`)
    }

    const heading = findAll(section, (el) => el.name === 'h2')[0]
    if (heading === undefined || flat(textOf(heading)) !== group.title) {
      out.push(`the section ${JSON.stringify(group.id)} is headed ${JSON.stringify(heading ? flat(textOf(heading)) : '')} and the file names the group ${JSON.stringify(group.title)}: a group's heading is the file's own title`)
    }

    const table = findAll(section, (el) => el.name === 'table')[0]
    if (table === undefined) {
      out.push(`the section ${JSON.stringify(group.id)} carries no table: the group's rows are a table, and a group rendered as prose is a matrix a reader cannot compare across`)
      return
    }

    const edits = carriesEdition(group)
    const columns = ['What', 'Status', ...(edits ? ['Edition'] : [])]
    const headerCells = findAll(findAll(table, (el) => el.name === 'thead')[0] ?? table, (el) => el.name === 'th')
    const header = headerCells.map((el) => flat(textOf(el)))
    if (header.join(' ') !== columns.join(' ')) {
      out.push(`the table for ${JSON.stringify(group.id)} is headed ${JSON.stringify(header)} and its columns are ${JSON.stringify(columns)}: the Edition column is the file's own property of a group (D1522), and a table that prints it for a group the file gives none for is a column nothing can fill`)
    }

    const body = findAll(table, (el) => el.name === 'tbody')[0]
    const bodyRows = (body?.children ?? []).filter((child) => child.name === 'tr')
    if (bodyRows.length !== group.rows.length) {
      out.push(`the table for ${JSON.stringify(group.id)} carries ${bodyRows.length} row(s) and the file gives the group ${group.rows.length}: every row of the file is a row a reader reads, and a page that dropped one is a claim about the product (D1537)`)
    }

    group.rows.forEach((row, index) => {
      const drawn = bodyRows[index]
      if (drawn === undefined) return
      const cells = drawn.children.filter((child) => child.name === 'th' || child.name === 'td')
      if (cells.length !== columns.length) {
        out.push(`the row ${JSON.stringify(row.name)} carries ${cells.length} cell(s) and the table has ${columns.length} column(s): a row that lost its status or its edition is a row a reader reads as a different claim`)
        return
      }

      const what = cells[0]
      const name = single(what, 'compat-name')
      const note = single(what, 'compat-row-note')
      if (what.name !== 'th' || what.attrs.get('scope') !== 'row') {
        out.push(`the row ${JSON.stringify(row.name)} does not open with a <th scope="row">: the row's name is its header cell, and a table whose rows have no headers is a table a screen reader reads as cells without names`)
      }
      if (name === null || flat(textOf(name)) !== row.name) {
        out.push(`the row is named ${JSON.stringify(name === null ? '' : flat(textOf(name)))} and the file names it ${JSON.stringify(row.name)}: the page prints the file's own name for a row`)
      }
      if (note === null || flat(textOf(note)) !== row.note) {
        out.push(`the row ${JSON.stringify(row.name)} renders ${JSON.stringify(note === null ? '' : flat(textOf(note)))} and the file's note for it is ${JSON.stringify(row.note)}: what the row says besides its name is the file's own sentence`)
      }

      const status = statusOf(cells[1])
      const legendEntry = legend.get(status.status)
      if (legendEntry === undefined) {
        out.push(`the row ${JSON.stringify(row.name)} draws the status ${JSON.stringify(status.status)}, which the legend above the tables does not define: a mark a reader cannot look up is a status a reader cannot read`)
      } else {
        if (status.marks !== 1 || status.mark !== legendEntry.mark) {
          out.push(`the row ${JSON.stringify(row.name)} draws ${status.marks} mark(s) (${JSON.stringify(status.mark)}) for ${JSON.stringify(row.status)} and the legend draws ${JSON.stringify(legendEntry.mark)}: the mark is the status's, and it means the same thing in the legend and in a row`)
        }
        if (status.word !== legendEntry.word) {
          out.push(`the row ${JSON.stringify(row.name)} prints ${JSON.stringify(status.word)} for ${JSON.stringify(row.status)} and the legend prints ${JSON.stringify(legendEntry.word)}: the legend is the page's key to its own statuses, so the same status is one word wherever it is drawn`)
        }
      }

      if (edits) {
        const edition = flat(textOf(cells[2]))
        if (row.edition === undefined) {
          out.push(`the row ${JSON.stringify(row.name)} is in the group ${JSON.stringify(group.id)}, whose rows carry an edition, and the file gives it none: a row of that group with an empty Edition cell is a reader looking at a cell nothing fills (D1522)`)
        } else if (edition === '') {
          out.push(`the row ${JSON.stringify(row.name)} carries the edition ${JSON.stringify(row.edition)} and the page prints an empty Edition cell: the cell is the licence the row is under, and an empty one says the reader can read it nowhere`)
        }
      }
    })
  })

  return out
}

/**
 * Every way the legend fails to be a key to the page, as failure lines: a status with no mark
 * or no word, two statuses drawn with the same mark, or a mark the legend invented that no row
 * uses. The last one is what keeps the legend from growing a sixth status the file cannot give.
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
    if (entry.mark === '') out.push(`the legend draws no mark for ${JSON.stringify(status)}: the mark is what a reader scans the tables for, and an empty one is a status drawn in colour alone — which is the one telling this page may not rely on`)
    if (entry.word === '') out.push(`the legend prints no word for ${JSON.stringify(status)}: the word is the status a reader reads, whatever their sight or their printer`)
  }

  const marks = [...entries.values()].map((entry) => entry.mark).filter((mark) => mark !== '')
  if (new Set(marks).size !== marks.length) {
    out.push(`the legend draws the marks ${JSON.stringify(marks)}, and two statuses share one: a mark a reader cannot tell from another mark is a mark that answers nothing`)
  }

  const statuses = new Set(doc.groups.flatMap((group) => group.rows.map((row) => row.status)))
  for (const status of statuses) {
    if (!entries.has(status)) out.push(`the file gives rows the status ${JSON.stringify(status)} and the legend does not define it: the page prints a status its own key does not carry`)
  }
  for (const status of entries.keys()) {
    if (status !== '' && !statuses.has(status)) {
      out.push(`the legend defines ${JSON.stringify(status)} and no row of the file carries it: the legend is the key to the statuses the tables draw`)
    }
  }

  return out
}

/** Every phrase of the file's `never_say` list that the built page carries, as failure lines. */
export function neverSayFailures(html, doc) {
  const phrases = Array.isArray(doc.never_say) ? doc.never_say : []
  const out = []
  if (phrases.length === 0) {
    out.push(
      'website/data/compatibility.yaml carries no `never_say` phrase, so this case would check the page against nothing: the list is the words the page may not be made of, and the file\'s own gate refuses an empty one (website/test/compatibility.mjs)',
    )
  }
  const text = html.toLowerCase()
  for (const phrase of phrases) {
    if (typeof phrase !== 'string' || phrase === '') {
      out.push(`the file's \`never_say\` list carries ${JSON.stringify(phrase)}, which is not a phrase: the list is read as the words to refuse`)
      continue
    }
    const at = text.indexOf(phrase.toLowerCase())
    if (at === -1) continue
    const around = flat(html.slice(Math.max(0, at - 80), at + phrase.length + 80))
    out.push(
      `the built page carries ${JSON.stringify(phrase)}, which the file forbids: "…${around}…". It is a phrase a reader of the spec would expect and the product cannot back, which is why the reconciliation removed it (D1520–D1539) and why the page is not where it comes back`,
    )
  }
  return out
}

/** Every page that draws the site's menu and does not carry the route, as failure lines. */
export function navFailures(pages) {
  const out = []
  const drawn = pages.filter((page) => page.html.includes('class="site-nav"'))
  if (drawn.length < 10) {
    out.push(`only ${drawn.length} built page(s) draw the site's menu, and the site's pages are more than that: this case would check the menu on a handful of pages and call the route linked`)
  }
  for (const page of drawn) {
    if (!page.html.includes(`href="${ROUTE}"`)) {
      out.push(`${page.rel} draws the site's menu and the menu does not carry ${ROUTE}: the compatibility page is a section of the site, and a reader reaches it from the menu on every page (the owner's approval of 2026-11-10)`)
    }
  }
  return out
}

// ---- the gate --------------------------------------------------------------------------

test('the page is built, and it is the page the layout names', () => {
  const html = builtPage()
  const doc = parse(html)
  const title = findAll(doc, (el) => el.name === 'title')[0]
  assert.equal(flat(textOf(title ?? { children: [] })), TITLE, `the built page's title reads ${JSON.stringify(title ? flat(textOf(title)) : '')}, and the page names itself ${JSON.stringify(TITLE)}`)

  const h1s = findAll(doc, (el) => el.name === 'h1').map((el) => flat(textOf(el)))
  assert.deepEqual(h1s, ['Compatibility'], `the built page carries the h1 headings ${JSON.stringify(h1s)}, and the page's own word is one heading (a11y.mjs asserts the count over every page; this is the page's own name)`)
})

test('the built page renders the file: one table per group, with the group\'s own rows', () => {
  const failures = shapeFailures(builtPage(), facts())
  assert.deepEqual(failures, [], `the built page does not render website/data/compatibility.yaml:\n  ${failures.join('\n  ')}`)
})

test('every status is the legend\'s mark and the legend\'s word, and nothing else', () => {
  const doc = facts()
  const failures = legendFailures(builtPage(), doc)
  assert.deepEqual(failures, [], `the page's legend is not a key to its own statuses:\n  ${failures.join('\n  ')}`)

  // The statuses the file uses, each with the mark and the word the page draws for it — the
  // whole of what "not by colour alone" means, read off the artifact.
  const legend = legendOf(builtPage()).entries
  for (const status of new Set(doc.groups.flatMap((group) => group.rows.map((row) => row.status)))) {
    const entry = legend.get(status)
    assert.ok(entry, `the page's legend carries no entry for ${JSON.stringify(status)}`)
    assert.notEqual(entry.mark, '', `the legend draws ${JSON.stringify(status)} with no mark`)
    assert.notEqual(entry.word, '', `the legend draws ${JSON.stringify(status)} with no word`)
  }
})

test('no phrase the file forbids is on the built page', () => {
  const failures = neverSayFailures(builtPage(), facts())
  assert.deepEqual(failures, [], `the built page carries a phrase the file forbids:\n  ${failures.join('\n  ')}`)
})

test('the site\'s menu carries the page, and the page is the one it marks current', () => {
  const pages = builtPages(DIST)
  const failures = navFailures(pages)
  assert.deepEqual(failures, [], `the menu does not carry the compatibility page:\n  ${failures.join('\n  ')}`)

  const own = pages.find((page) => page.url === ROUTE)
  assert.ok(own, `the build produced no page at ${ROUTE}`)
  const links = findAll(parse(own.html), (el) => el.name === 'a' && el.attrs.get('href') === ROUTE)
  const marked = links.filter((el) => el.attrs.get('aria-current') === 'page')
  assert.equal(marked.length, 1, `the menu's link to ${ROUTE} on the page itself is marked ${marked.length} time(s): the page a reader is on is the one the menu marks (RF4)`)
  assert.equal(flat(textOf(links[links.length - 1])), 'Compatibility', `the menu's link to ${ROUTE} is labelled ${JSON.stringify(flat(textOf(links[links.length - 1])))}: the owner's approval of 2026-11-10 is for "Compatibility"`)
})

test('the docs hub carries the page, under the page\'s own title', () => {
  const hub = builtPages(DIST).find((page) => page.url === '/docs/')
  assert.ok(hub, 'the build produced no docs hub at /docs/')
  const entries = findAll(parse(hub.html), (el) => el.name === 'li' && hasClass(el, 'guide'))
    .map((el) => findAll(el, (one) => one.name === 'a' && one.attrs.get('href') === ROUTE))
    .filter((found) => found.length > 0)
  assert.equal(entries.length, 1, `the docs hub carries ${entries.length} entr(ies) linking ${ROUTE}: the hub is where a reader looks for the site's reference pages, and the compatibility matrix is one (the compatibility matrix's plan, Task CM2)`)
  assert.equal(
    flat(textOf(entries[0][0])),
    'Compatibility',
    `the hub's entry links ${ROUTE} under the title ${JSON.stringify(flat(textOf(entries[0][0])))}: the entry's title is typed in src/pages/docs.astro and the page's own h1 is read above, so the two are the same word or one of them is wrong`,
  )
})

// ---- the refusals, kept ------------------------------------------------------------------

/** The built page with one edit, and what the gates say about it. */
function mutated(from, to) {
  const html = builtPage()
  assert.ok(
    html.includes(from),
    `this case's anchor ${JSON.stringify(from)} is not in the built page, so the edit it makes proves nothing — fix the anchor against the artifact, never the rule`,
  )
  return html.replace(from, to)
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

  // A group's section gone: the anchor a link to the page names, renamed out from under it.
  refuses(shape(mutated('id="local-environment"', 'id="local-environ"')), 'the id "local-environment"', 'a group whose section the page stopped carrying is not refused')

  // A legend with a status's mark drawn empty: the same defect as colour alone. The legend is
  // drawn before the tables, so the page's first `>✓</span>` is the legend's own mark.
  refuses(legendFailures(mutated('>✓</span>', '></span>'), doc), 'draws no mark for "supported"', 'a legend mark a reader cannot see is not refused')

  // A legend word that is not the word the rows print: the legend stops being a key.
  refuses(shape(mutated('Supported</li>', 'Available</li>')), 'the legend prints "Available"', 'a legend whose word for a status is not the word the rows print is not refused')

  // A row's status word changed: the same defect from the other side. The body's status span
  // closes on the word, and the legend's item closes on `</li>`, so this anchor is a row's.
  refuses(shape(mutated('>✓</span>Supported</span>', '>✓</span>Available</span>')), 'prints "Available" for "supported"', 'a row whose status word the legend does not define is not refused')

  // A row's note gone: what the row says besides its name is the file's own sentence.
  refuses(shape(mutated('class="compat-row-note', 'class="compat-rownote')), 'the file\'s note for it is', 'a row whose note the page dropped is not refused')

  // A forbidden phrase, planted in the lead: the refusal names the phrase and the page.
  refuses(
    neverSayFailures(mutated('What runs where', `${doc.never_say[0]}. What runs where`), doc),
    JSON.stringify(doc.never_say[0]),
    'a phrase the file forbids, planted on the page, is not refused',
  )

  // And the menu: a page that draws the site's menu without the route.
  refuses(
    navFailures([{ rel: 'index.html', html: mutated('href="/compatibility/"', 'href="/compatibility"') }]),
    'does not carry /compatibility/',
    'a page whose menu dropped the route is not refused',
  )
})
