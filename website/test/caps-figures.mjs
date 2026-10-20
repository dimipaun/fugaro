// website/test/caps-figures.mjs — the caps-and-costs figures on a phone (the owner's feedback
// of 2026-10-19: "these render terrible on mobile").
//
// The page's figures are its right-hand part on a desktop and they were the part that did not
// survive a phone. Two tables carry them — what a job costs (`#typical-costs`, six columns of
// nowrap figures) and the aggregation's per-recipe ranges (`#costs`, five columns whose header
// text alone is longer than a phone's line) — and both were read as tables at 360 px: the first
// inside an `overflow-x: auto` box, which is six columns scrolling sideways inside their own
// box, and the second with no box at all, whose minimum content width is what widens the
// document. A phone's content box is 328 px (360 px less `main`'s own gutter, read out of the
// stylesheet rather than typed).
//
// **What the page does instead** (P4's own answer to the same class of defect, and the shape
// `base-styles.mjs` holds the run's timeline to): below the site's own 48 rem breakpoint each
// row is a block, each cell on a line of its own, each cell labelled with **its column's own
// header text** through `data-label`, and the label printed by one rule
// (`content: attr(data-label)` on a cell's `::before`) rather than written into every cell. The
// figures are the same figures in the same columns in the same order; the page adds no label of
// its own and no claim, and `body`'s size is the smallest type a stacked figure is set in.
//
// **What this gate reads, and why each one is a defect rather than a preference:**
//
//   1. **every cell carries its column's own name.** A `data-label` that is missing, empty, or
//      something other than the head's text for that column is a figure whose label a reader
//      cannot check against the table it came from — and a label written beside the head is a
//      second name for one column, which is how a header and its label drift apart. The row
//      header (`<th scope="row">`) is a cell like any other here: stacked, the block's title is
//      the row's first cell, and the column that names it names it too.
//   2. **the figures are stacked, and the head is out of the layout.** At 360 and at 390 px the
//      table, its body, its rows and both kinds of cell are blocks, and the head is out of the
//      flow. Each is separately enough to leave a rule reading correctly and a reader seeing a
//      table: a head left in the flow with every cell a block is six unlabelled words above the
//      first row, and a cell left laid out as a table cell is a column of the table this page
//      replaced. The five elements are read one at a time and by their own names, because a
//      selector that names four of them is a rule that looks like this one and stacks four of
//      the five.
//   3. **the rule that prints the labels selects the cells it prints them on.** `content:
//      attr(data-label)` on a cell's `::before` is the whole of the printing, and a rule whose
//      selector matches no cell on the page — a scope attribute the build never wrote, a class
//      the page does not carry — prints nothing at all while reading exactly like the rule that
//      works. So the reader matches each printing rule's own selector against the page's own
//      cells, and refuses a cell no printing rule reaches, naming the cell and the selector.
//   4. **nothing at a phone's width holds a figure on one line or in a scroll box.** The wide
//      table's own fit — `white-space: nowrap` on the figures and the `overflow-x` box around
//      them — belongs to the layout above the breakpoint; in force at 360 it is the defect the
//      owner read, and a figure that cannot break runs out of its card at 360 whatever the
//      table around it does.
//   5. **a fixed width fits the phone's content box.** A cell or a table given a length wider
//      than 328 px is a box wider than the screen it is read on — the arithmetic the run's own
//      gate does, over the boxes this page draws.
//   6. **the figures are at least the body's size.** `base.css` sets every `table` at 15 px
//      (`--font-size-sm`) where the body is 17 px: a table's smaller type for six columns, and
//      not a phone's one figure under its name.
//   7. **above the breakpoint the wide table's own fit is there.** The same two declarations of
//      4 are read again at 768 px — the site's own breakpoint, and the first width at which the
//      wide layout is in force — and at 1280 px: a block that was deleted, or moved to a wider
//      query, leaves a window from the breakpoint up in which the figures wrap mid-number with
//      no box to scroll them in, which is the defect the owner read one breakpoint along.
//
// **How the two tables are both read.** This checkout builds the empty state — `costs.json` is
// the owner-run aggregation's output and is absent here (D96) — so `#costs` renders no table at
// all and a gate that only read the checkout's `dist/` would hold the second table to nothing.
// The case below therefore builds a copy of the repository with a fixture `costs.json` in it, the
// same fixture shape `caps-and-costs.mjs` uses and no data about anybody's runs, and reads both
// tables on that artifact; the checkout's own build is read for the first.
//
// Everything else is the shape the site's other layout gates keep: a pure failure function over
// one built page's HTML and CSS, a case that reads the built artifact, and one fixture case per
// named refusal, each built by taking the one thing it names out of the artifact's own bytes.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { DIST, cssDeclarations, cssRules, find, parse, textOf, walk } from './lib/html.mjs'
import { buildCopy } from './lib/build.mjs'

const ROUTE = 'caps-and-costs/index.html'

/** The two widths the owner named: a small phone, and the width the site is designed against. */
export const FIGURE_WIDTHS = [360, 390]

/**
 * The widths above the breakpoint, where the wide table's own fit has to be in force: 768 px is
 * the site's breakpoint (48 rem) and therefore the first width the wide layout is meant to hold
 * at, and 1280 px is a desktop. A block that was deleted, or moved behind a wider query, is
 * green on every phone width and leaves a window from the breakpoint up with no fit at all.
 */
export const WIDE_WIDTHS = [768, 1280]

/** The phone whose content box nothing the figures draw may exceed. */
const PHONE = 360

/** The sections the figures are rendered in. Every `<table>` under one of them is read here. */
const FIGURE_SECTIONS = new Set(['typical-costs', 'costs'])

// The selector parts this gate reads, as the shapes this site writes them in. A part is matched
// by the elements it names rather than by its own text, so Astro's scope attribute —
// `table[data-astro-cid-…]` — is a part of every one of them and none of these reads depends on
// which hash the build emitted.
const FIGURE = /(^|[^\w-])(table|thead|tbody|tr|th|td|table-scroll)(?![\w-])/
const TABLE = /(^|[^\w-])table(?![\w-])/
const HEAD = /(^|[^\w-])thead(?![\w-])/
const BODY_ROW = /(^|[^\w-])tbody(?![\w-])/
const ROW = /(^|[^\w-])tr(?![\w-])/
const HEADER_CELL = /(^|[^\w-])th(?![\w-])/
const DATA_CELL = /(^|[^\w-])td(?![\w-])/
const CELL = /(^|[^\w-])(th|td)(?![\w-])/
const SCROLL = /(^|[^\w-])table-scroll(?![\w-])/
const BODY = /^body$/
const MAIN = 'main'

/**
 * The five elements the stacked layout is made of, each with its own name: the rule below reads
 * them one at a time, because a selector that names four of the five is a rule that looks like a
 * stacked table and leaves the fifth laid out as a cell.
 */
const STACKED = [
  [TABLE, 'a table'],
  [BODY_ROW, 'the body'],
  [ROW, 'a row'],
  [HEADER_CELL, 'a header cell'],
  [DATA_CELL, 'a data cell'],
]

/** A string with its whitespace flattened, for comparisons across markup. */
function collapse(text) {
  return text.replace(/\s+/g, ' ').trim()
}

// ---- the CSS, read just far enough ------------------------------------------------------
//
// The readers below are this file's own, in the shape `base-styles.mjs` uses and for the same
// reason: a gate reads the stylesheet as a browser reads it — the rules in force at a width, the
// most specific of them winning, the last winning a tie — rather than as text. They are the
// subset this page's own stylesheet needs: element names, classes, ids, attribute selectors and
// `:where()`, which is what Astro's scoping and this site's rules are written in.

/** The CSS a page carries: its inline `<style>` blocks, and any first-party stylesheet it links. */
function pageCss(html, dist = DIST) {
  const inline = [...html.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((match) => match[1])
  const linked = [...html.matchAll(/<link[^>]*rel="stylesheet"[^>]*href="(\/[^"]+)"/g)].map((match) =>
    readFileSync(path.join(dist, match[1]), 'utf8'),
  )
  return [...inline, ...linked].join('\n')
}

/** True when a rule is in force at a viewport width: no `@media`, or a width range it falls in. */
function inForce(rule, width) {
  for (const query of rule.conditions ?? []) {
    const min = /width\s*>=\s*([\d.]+)rem/.exec(query)
    const max = /width\s*<=\s*([\d.]+)rem/.exec(query)
    // A query with no width in it — `prefers-reduced-motion`, `@supports` — is a rule the width
    // does not decide, and it is not read as applying at any width.
    if (min === null && max === null) return false
    if (min !== null && Number(min[1]) * 16 > width) return false
    if (max !== null && Number(max[1]) * 16 < width) return false
  }
  return true
}

/** A selector list's parts, split on the commas outside any parentheses. */
function splitTop(text, separator) {
  const out = []
  let depth = 0
  let start = 0
  for (let i = 0; i < text.length; i += 1) {
    const ch = text[i]
    if (ch === '(' || ch === '[') depth += 1
    else if (ch === ')' || ch === ']') depth -= 1
    else if (depth === 0 && ch === separator) {
      out.push(text.slice(start, i))
      start = i + 1
    }
  }
  out.push(text.slice(start))
  return out.map((part) => part.trim()).filter(Boolean)
}

/**
 * A selector part's specificity as `[ids, classes, elements]`, read off its **subject** — the
 * last compound, which is the element the part is about. `:where()` contributes nothing, which
 * is why it is stripped first: `base.css` writes `:where(table)` and the page's own rule writes
 * `table[data-astro-cid-…]`, and a reader that scored the two alike would read the table's
 * `font-size` out of the wrong one — the built stylesheet puts the page's rules *before*
 * `base.css`, so document order alone answers this one backwards.
 */
function specificityOf(part) {
  const text = part.replace(/:where\([^)]*\)/g, '')
  const subject = text.split(/[\s>+~]+/).filter(Boolean).at(-1) ?? ''
  return [
    (subject.match(/#[\w-]+/g) ?? []).length,
    (subject.match(/\.[\w-]+|\[[^\]]*\]/g) ?? []).length,
    (subject.match(/(^|[^\w-])(table|thead|tbody|tr|th|td)(?![\w-])/g) ?? []).length,
  ]
}

/** True when `a` is the more specific of two `[ids, classes, elements]` ratios. */
function moreSpecific(a, b) {
  for (let i = 0; i < 3; i += 1) {
    if (a[i] !== b[i]) return a[i] > b[i]
  }
  return false
}

/**
 * The pseudo-element a selector part ends in, as the text it is written with, or `null` when it
 * names none. The label is printed by a pseudo-element's `content`, so this is what tells the
 * rule that prints a label from every other rule that declares a `content`.
 *
 * **A pseudo-class is not a pseudo-element**, and the difference is load-bearing here: `::before`
 * (which the minifier writes `:before`) is the label's own box, and `:where(table)` — `base.css`'s
 * own way of setting a table's font size — is the table itself. A reader that took every leading
 * colon for a pseudo-element would throw away the rule that sets the table's type and read the
 * figures as though nothing had set their size at all.
 */
const PSEUDO_ELEMENTS = new Set(['before', 'after', 'first-line', 'first-letter'])

function pseudoOf(part) {
  const match = /(::?)([a-z-]+)(\([^)]*\))?$/.exec(part.trim())
  if (match === null) return null
  if (match[1] === '::' || PSEUDO_ELEMENTS.has(match[2])) return match[0]
  return null
}

/**
 * Whether a selector part's **subject compound** selects an element of the built page: the part
 * with its pseudo-element taken off, read against the element's own tag name, classes and
 * attributes. `true`, `false`, or `null` for a compound this reader cannot evaluate (a selector
 * with a descendant, an attribute operator it does not know) — and `null` is never a match, so a
 * rule that cannot be read is a rule that is not credited with printing anything.
 *
 * This is the half that a reader of the **declaration** alone cannot see: `content:
 * attr(data-label)` on `th[data-astro-cid-…]::before` prints the label, and the same declaration
 * on a scope attribute the build never wrote prints nothing whatever, though the two rules read
 * the same in the stylesheet. The page's own cells are what the rule is held to, so the answer
 * comes from the markup the gate is reading.
 */
function selectsElement(part, el) {
  const pseudo = pseudoOf(part)
  const compound = (pseudo === null ? part : part.trim().slice(0, part.trim().length - pseudo.length)).trim()
  const shape = /^([a-z][a-z0-9]*)?((?:\.[A-Za-z_][-\w]*|\[[^\]]*\])*)$/.exec(compound)
  if (shape === null) return null
  const [, tag, rest] = shape
  if (tag !== undefined && tag !== el.name) return false
  const classes = new Set((el.attrs.get('class') ?? '').split(/\s+/).filter(Boolean))
  for (const token of rest.match(/\.[A-Za-z_][-\w]*|\[[^\]]*\]/g) ?? []) {
    if (token.startsWith('.')) {
      if (!classes.has(token.slice(1))) return false
      continue
    }
    const inner = token.slice(1, -1)
    const equal = /^([\w-]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s\]]+))$/.exec(inner)
    if (equal !== null) {
      if (el.attrs.get(equal[1]) !== (equal[2] ?? equal[3] ?? equal[4])) return false
      continue
    }
    if (/^[\w-]+$/.test(inner)) {
      if (!el.attrs.has(inner)) return false
      continue
    }
    // An operator this reader does not evaluate: the compound is not a match, and the failure
    // it produces says so rather than crediting the rule with a selector nobody read.
    return null
  }
  return true
}

/** The tokens a stylesheet declares, as `name -> px`. */
function lengthTokens(css) {
  const out = new Map()
  for (const match of css.matchAll(/--([a-z0-9-]+):\s*([\d.]+)(rem|px)\b/g)) {
    out.set(match[1], Number(match[2]) * (match[3] === 'rem' ? 16 : 1))
  }
  return out
}

/** A CSS length as pixels: a token, a `rem`/`px` length, or a bare zero. `NaN` when unreadable. */
function toPx(value, tokens) {
  if (value === undefined) return NaN
  const text = value.trim()
  if (/^0(px|rem|em)?$/.test(text)) return 0
  const variable = /^var\(--([a-z0-9-]+)\)$/.exec(text)
  if (variable) return tokens.get(variable[1]) ?? NaN
  const rem = /^([\d.]+)rem$/.exec(text)
  if (rem) return Number(rem[1]) * 16
  const px = /^([\d.]+)px$/.exec(text)
  return px ? Number(px[1]) : NaN
}

/** The inline component of a `padding` shorthand: the room a side takes. */
function inlineToken(value) {
  const parts = (value ?? '').trim().split(/\s+/).filter(Boolean)
  if (parts.length === 4) return parts[3]
  return parts.length > 1 ? parts[1] : parts[0]
}

// ---- the reading -----------------------------------------------------------------------

/**
 * Every way the caps page's figures fail a phone, as the lines a reader gets. `css` is one
 * built page's stylesheet and `html` its markup; both are the artifact's own bytes, so this can
 * be run over the checkout's build, over a copy built with an aggregation published, and over
 * either one with a single thing planted in it.
 */
export function figureLayoutFailures(css, html) {
  const out = []
  const fail = (why) => out.push(why)

  // ---- the markup: one label per cell, and the label is the column's own name -------------

  const doc = parse(html)
  const tables = []
  for (const section of walk(doc)) {
    if (section.name !== 'section' || !FIGURE_SECTIONS.has(section.attrs.get('id'))) continue
    for (const table of walk(section)) {
      if (table.name === 'table') tables.push({ id: section.attrs.get('id'), table })
    }
  }
  if (tables.length === 0) {
    fail(
      `the page renders no figures table under ${[...FIGURE_SECTIONS].map((id) => `#${id}`).join(' or ')}: this check reads the tables the page draws, so a page that lost them passes every rule below without one of them being read`,
    )
    return out
  }

  // Every cell the figures tables render, with where it is: the label check below holds each of
  // them to the rule that prints it, and a cell nothing prints on is a figure with no name.
  const EVERY_CELL = []

  tables.forEach(({ id, table }, index) => {
    const where = `figures table ${index + 1} (#${id})`
    const head = find(table, 'thead')
    const columns = head === null ? [] : [...walk(head)].filter((el) => el.name === 'th').map((el) => collapse(textOf(el)))
    if (columns.length === 0) {
      fail(`${where} names no column: its head is what every cell's label is held to, and a table with no head has nothing to hold them to`)
      return
    }
    const body = find(table, 'tbody')
    const rows = body === null ? [] : body.children.filter((el) => el.name === 'tr')
    if (rows.length === 0) fail(`${where} carries no row: the figures this check reads are the cells of a row`)

    rows.forEach((row, at) => {
      const cells = row.children.filter((el) => el.name === 'th' || el.name === 'td')
      if (cells.length !== columns.length) {
        fail(
          `${where} row ${at + 1} carries ${cells.length} cell(s) where its head names ${columns.length} columns: stacked, a row is one labelled line per figure, so a cell the head has no name for is a figure no reader can name`,
        )
        return
      }
      cells.forEach((cell, column) => {
        const label = cell.attrs.get('data-label')
        EVERY_CELL.push({ el: cell, where: `${where} row ${at + 1}`, column: columns[column] ?? `column ${column + 1}` })
        if (label === undefined || collapse(label) === '') {
          fail(
            `${where} row ${at + 1} carries no label on the cell for ${JSON.stringify(columns[column])}: below the site's breakpoint the figures are labelled lines, and a cell with no \`data-label\` reads as a figure under nothing (the owner's feedback of 2026-10-19)`,
          )
        } else if (collapse(label) !== columns[column]) {
          fail(
            `${where} row ${at + 1} labels a cell ${JSON.stringify(collapse(label))} where its column's head reads ${JSON.stringify(columns[column])}: the label is the column's own name, so a label written beside the head is a second name for one column and the two drift apart`,
          )
        }
      })
    })
  })

  // ---- the stylesheet: the phone's layout, and the wide table's fit kept above it ---------

  const rules = cssRules(css)
  const tokens = lengthTokens(css)

  /**
   * The value a property has on the elements a test names, at a viewport width: the last of the
   * rules in force whose parts match, the most specific of them winning, `undefined` when no
   * rule states it.
   *
   * A part that names a **pseudo-element** is not read here: `th::before { display: block }` is a
   * declaration about the label printed on a cell and not about the cell, and crediting it to the
   * cell is how a rule that stacks four elements instead of five reads as one that stacks all
   * five — the label rule declares `display: block` for exactly that reason.
   */
  const declared = (width, test, property) => {
    let best = null
    for (const rule of rules) {
      if (!inForce(rule, width)) continue
      const value = new Map(cssDeclarations(rule.body)).get(property)
      if (value === undefined) continue
      let ratio = null
      for (const part of splitTop(rule.selector, ',')) {
        if (pseudoOf(part) !== null) continue
        if (!test.test(part)) continue
        const reading = specificityOf(part)
        if (ratio === null || moreSpecific(reading, ratio)) ratio = reading
      }
      if (ratio === null) continue
      if (best === null || !moreSpecific(best.ratio, ratio)) best = { value, ratio }
    }
    return best?.value
  }

  /**
   * The rules in force at a width that print a label on a cell's `::before`, as
   * `{ selector, content, prints }`: the part of the selector list that carries the declaration,
   * what it declares, and whether what it declares is the cell's own `data-label`.
   *
   * It is read apart from `declared` because it is a pseudo-element's declaration, and it is what
   * the refusals below hold to **the page's own cells**: a rule that declares `content:
   * attr(data-label)` on a selector that matches no cell on the page is a rule that prints
   * nothing, and reading the declaration alone cannot tell it from the rule that works.
   */
  const printingRules = (width) => {
    const out = []
    for (const rule of rules) {
      if (!inForce(rule, width)) continue
      const content = new Map(cssDeclarations(rule.body)).get('content')
      if (content === undefined) continue
      for (const part of splitTop(rule.selector, ',')) {
        if (!/^::?before$/.test(pseudoOf(part) ?? '')) continue
        if (!EVERY_CELL.some(({ el }) => selectsElement(part, el) === true)) continue
        out.push({ selector: part, content, prints: /^attr\(\s*data-label\s*\)$/.test(content.trim()) })
      }
    }
    return out
  }

  // The phone's content box: the viewport less the gutter `main` gives the page at it, read out
  // of the same stylesheet rather than typed, so the arithmetic cannot drift from the page.
  const gutter = toPx(
    inlineToken(
      rules
        .filter((rule) => rule.selector === MAIN && inForce(rule, PHONE))
        .map((rule) => new Map(cssDeclarations(rule.body)).get('padding'))
        .filter(Boolean)
        .pop(),
    ),
    tokens,
  )
  const phoneBox = PHONE - 2 * gutter
  if (Number.isNaN(gutter) || phoneBox <= 0) {
    fail(
      `\`main\`'s inline padding cannot be read from the built stylesheet at ${PHONE} px, so the box a phone gives the figures is unknown: nothing here can be shown to fit a phone without it (the owner's feedback of 2026-10-19)`,
    )
  }

  // The body's own size, which is the floor a stacked figure is held to.
  const bodyPx = toPx(declared(PHONE, BODY, 'font-size'), tokens)
  if (Number.isNaN(bodyPx) || bodyPx <= 0) {
    fail(`the body's own font size cannot be read from the built stylesheet at ${PHONE} px, so the size the figures have to reach is unknown: \`base.css\` is the file that sets it, and this check cannot hold a figure to a size nobody stated`)
  }

  for (const width of FIGURE_WIDTHS) {
    const at = `${width} px`

    // The stacking, one element at a time: a selector that names four of the five is a rule that
    // looks like a stacked table and leaves the fifth laid out as a cell of the one it replaced.
    for (const [test, what] of STACKED) {
      if (declared(width, test, 'display') !== 'block') {
        fail(
          `at ${at} ${what} is not \`display: block\`: the figures are one labelled line each at ${at}, and a table, its body, its rows or its cells still laid out as a table is the six-column table this page replaced — a table read as a table at ${at} is a sideways scroll or six crushed columns in a ${phoneBox} px content box (the owner's feedback of 2026-10-19)`,
        )
      }
    }
    if (declared(width, HEAD, 'display') !== 'none') {
      fail(
        `at ${at} the head row is still laid out: with every cell a block the names are six unlabelled words above the first row's figures instead of the labels of the cells under them`,
      )
    }

    // The labels: a rule in force that prints the cell's own `data-label`, and **that rule
    // selects the cells it is meant to label**. The declaration on its own is not the claim — a
    // rule on a selector that matches no cell on the page prints nothing whatever, and reads
    // exactly like the rule that works — so every cell the tables render is held to a printing
    // rule that its own selector reaches.
    const printing = printingRules(width)
    const labels = printing.filter((rule) => rule.prints)
    if (labels.length === 0) {
      fail(
        `at ${at} no rule prints the cell's own label on a cell the page renders (\`content: ${printing[0]?.content ?? '(nothing declared on a before-pseudo-element of a cell)'}\`, where \`attr(data-label)\` is the label the markup carries): the labels are in the markup and a reader at ${at} is shown something else, or nothing at all`,
      )
    } else {
      for (const { el, where, column } of EVERY_CELL) {
        if (labels.some((rule) => selectsElement(rule.selector, el) === true)) continue
        fail(
          `at ${at} no rule prints a label on the ${el.name} of ${where} for ${JSON.stringify(column)}: the rules in force that declare \`content: attr(data-label)\` are ${labels.map((rule) => JSON.stringify(rule.selector)).join(', ')}, and this check cannot show any of them selecting that cell — a label rule whose selector reaches no cell prints nothing at ${at}, which is the defect this gate exists for`,
        )
      }
    }

    // Nothing at a phone's width may hold a figure on one line, put it in a box that scrolls
    // sideways, or make it wider than the screen — the three ways a page gains a sideways scroll.
    for (const rule of rules) {
      if (!inForce(rule, width)) continue
      const parts = splitTop(rule.selector, ',').filter((part) => FIGURE.test(part))
      if (parts.length === 0) continue
      const decls = new Map(cssDeclarations(rule.body))
      const inside = (rule.conditions ?? []).length > 0 ? ` inside ${rule.conditions.join(' ')}` : ''
      if ((decls.get('white-space') ?? '').trim() === 'nowrap') {
        fail(
          `at ${at} ${JSON.stringify(parts[0])}${inside} holds the figures on one line (\`white-space: nowrap\`): a figure that cannot break runs out of the line it is stacked in, and the wide table's own fit belongs above the site's breakpoint (the owner's feedback of 2026-10-19)`,
        )
      }
      if (/^(auto|scroll)$/.test((decls.get('overflow-x') ?? '').trim())) {
        fail(
          `at ${at} ${JSON.stringify(parts[0])}${inside} is a box that scrolls sideways (\`overflow-x: ${decls.get('overflow-x')}\`): the figures fit the box they are stacked in at ${at}, and the wide table's own scroll box is what belongs above the breakpoint`,
        )
      }
      for (const property of ['width', 'min-width']) {
        const value = decls.get(property)
        if (value === undefined) continue
        const text = value.trim()
        if (/%$/.test(text) || /^(auto|none|fit-content|max-content|min-content)$/.test(text)) continue
        const px = toPx(text, tokens)
        if (Number.isNaN(px)) {
          fail(
            `at ${at} ${JSON.stringify(parts[0])}${inside} declares \`${property}: ${text}\`, which is not a length this check can turn into pixels, and a length it cannot read is a length nothing checked`,
          )
        } else if (px > phoneBox) {
          fail(
            `at ${at} ${JSON.stringify(parts[0])}${inside} gives the figures \`${property}: ${text}\` — ${Math.round(px)} px against the ${Math.round(phoneBox)} px a phone's content box gives them: a box wider than the screen it is read on is the sideways scroll this page's layout exists to remove`,
          )
        }
      }
    }

    // And the size the figures are set in, which is the one thing a stacked layout cannot leave
    // to a table's own rule: `base.css` sets every `table` below the body's size.
    const size = declared(width, FIGURE, 'font-size')
    if (size !== undefined && !Number.isNaN(bodyPx)) {
      const px = toPx(size, tokens)
      if (Number.isNaN(px)) {
        fail(
          `at ${at} the figures' \`font-size\` is \`${size}\`, which is not a length this check can turn into pixels, so the size a phone reads them at is a size nothing checked`,
        )
      } else if (px < bodyPx) {
        fail(
          `at ${at} the figures are set at ${Math.round(px)} px where the body is ${Math.round(bodyPx)} px: \`base.css\` sets every \`table\` at the table's smaller type (\`--font-size-sm\`), which is a table's own size for six columns and not a phone's one figure under its name (the owner's feedback of 2026-10-19)`,
        )
      }
    }
  }

  // ---- above the breakpoint: the wide table's own fit is there ---------------------------
  //
  // The two declarations the phone layout moved out of the base rules have to be in force again
  // somewhere above the site's breakpoint, and this is the half a gate over phone widths alone
  // cannot see: a block that was deleted, or moved behind a wider query, passes every phone rule
  // above and leaves a window from the breakpoint up in which six columns of figures wrap
  // mid-number with no box to scroll them in — the defect the owner read, one breakpoint along.
  // 768 px (48 rem) is the site's own breakpoint and therefore the first width the wide layout is
  // meant to hold at; 1280 px is a desktop.

  for (const width of WIDE_WIDTHS) {
    const at = `${width} px`
    const box = declared(width, SCROLL, 'overflow-x')
    if (!/^(auto|scroll)$/.test((box ?? '').trim())) {
      fail(
        `at ${at} the box the figures tables are wrapped in does not scroll sideways (\`overflow-x: ${box ?? '(nothing declared)'}\` on \`.table-scroll\`): above the site's breakpoint six columns of nowrap figures are wider than a narrow desktop, and with no scroll box they widen the document instead — the phone layout is what makes them fit below the breakpoint, and above it this box is what does (the owner's feedback of 2026-10-19, and the wide layout's own fit)`,
      )
    }
    const wrap = declared(width, FIGURE, 'white-space')
    if ((wrap ?? '').trim() !== 'nowrap') {
      fail(
        `at ${at} the figures are not held on their own lines (\`white-space: ${wrap ?? '(nothing declared)'}\` where the wide layout writes \`nowrap\`): a figure that breaks mid-number is a figure a reader cannot read, and this is the fit the phone layout turned off below the breakpoint and the page owes again above it`,
      )
    }
  }

  return out
}

// ---- the artifact ------------------------------------------------------------------------

const TODAY = () => {
  const file = path.join(DIST, ROUTE)
  assert.ok(
    existsSync(file),
    `dist/${ROUTE} is missing: run \`npm run build\` (\`npm run check\` does it first)`,
  )
  const html = readFileSync(file, 'utf8')
  return { html, css: pageCss(html) }
}

test('the caps page\'s figures are labelled rows on a phone, and the checkout build passes', () => {
  const { html, css } = TODAY()
  const failures = figureLayoutFailures(css, html)
  assert.deepEqual(
    failures,
    [],
    `the caps page's figures do not survive a phone:\n  ${failures.join('\n  ')}`,
  )
  assert.ok(/<table\b/.test(html), 'the caps page renders no table at all, so this case checked nothing')
})

/**
 * The figures a costs file holds, in the aggregation's own `--json` shape: two groups above
 * D97's minimum of 20 runs, and the disclaimer the command publishes them with. It is the
 * fixture `caps-and-costs.mjs` builds, and it is **not** data about anybody's runs: it lives for
 * the length of one build in a temp directory, and the site's own published file is the
 * owner's (D96, Global Constraint 10).
 */
const FIGURES = {
  project: 'sandbox',
  since: '2026-01-01',
  until: '2026-02-01',
  min_runs: 20,
  recipes: [
    {
      recipe: 'standard',
      runs: 40,
      compute_not_estimated: 2,
      duration_s: { min: 60, median: 120.5, p90: 240 },
      cost_usd: { min: 0.1, median: 0.2, p90: 0.3 },
    },
    {
      recipe: 'review-heavy',
      runs: 24,
      compute_not_estimated: 0,
      duration_s: { min: 900, median: 3600, p90: 7200 },
      cost_usd: { min: 1.5, median: 3.25, p90: 7.5 },
    },
  ],
  totals: { runs: 64, recipes: 2, runs_below_min_runs: 3, runs_unknown_recipe: 1, runs_not_estimated: 2, runs_without_a_figure: 1 },
  not_a_promise:
    'A cap is a ceiling, not an estimate: in observe a run counts and logs what it spent, and in enforce it halts. ' +
    "These figures are percentiles over one window of one installation's own run records: what those runs did, not a promise about another run. " +
    "Every figure prints the number of runs behind it, and the p90 whenever 0.9 times (runs minus one) is a whole number.",
}

test('with an aggregation published, both figures tables pass the same reading', async (t) => {
  // The second table is the one the checkout cannot read: `costs.json` is absent here, so
  // `#costs` renders its empty state and a gate over `dist/` alone would hold the per-recipe
  // table to nothing — which is the table whose own header text is wider than a phone.
  const built = buildCopy(
    t,
    ({ site }) => writeFileSync(path.join(site, 'content', 'costs.json'), JSON.stringify(FIGURES)),
    'fugaro-caps-figures-',
  )
  assert.equal(built.status, 0, `the build of the copy failed:\n${built.output}`)
  const html = readFileSync(path.join(built.dist, ROUTE), 'utf8')
  const css = pageCss(html, built.dist)
  assert.match(html, /data-recipe="standard"/, 'the copy built no per-recipe table, so this case read one table where it means to read two')

  assert.deepEqual(
    figureLayoutFailures(css, html),
    [],
    `the per-recipe figures do not survive a phone:\n  ${figureLayoutFailures(css, html).join('\n  ')}`,
  )

  // And the reading reaches that table's markup and not only the first one's: the last column
  // of the second table is a label no other cell on the page carries, so removing it is a
  // planted defect only this table can show.
  const without = html.replace(/ data-label="Runs with no compute estimate"/, '')
  assert.notEqual(without, html, 'the second table carries no "Runs with no compute estimate" label to plant away, so this case proves nothing about the table it is about')
  const failures = figureLayoutFailures(css, without)
  assert.ok(
    failures.some((line) => /no label on the cell for "Runs with no compute estimate"/.test(line)),
    `the second table's own cells are not read: ${failures.join(' | ') || '(no failure)'}`,
  )
})

// ---- the refusals, kept -------------------------------------------------------------------
//
// Each case below is the artifact's own bytes with the one thing it names taken out, rather than
// a smaller page the rule happens to accept: `plant` refuses to build a fixture it cannot make,
// because a pattern that matches nothing would leave a passing page behind and the case would
// pass for the wrong reason.

/** Today's built page with one fragment replaced — the fixture every case below is made of. */
function plant(pattern, replacement = '', which = 'html') {
  const page = TODAY()
  const source = which === 'css' ? page.css : page.html
  const matches = typeof pattern === 'string' ? source.includes(pattern) : pattern.test(source)
  assert.ok(
    matches,
    `this fixture is built by editing ${JSON.stringify(String(pattern))} in the built page's ${which}, and the page no longer carries it: the case would pass without planting anything`,
  )
  const edited = source.replace(pattern, replacement)
  assert.notEqual(edited, source, `the fixture's own edit changed nothing, so ${JSON.stringify(String(pattern))} is not a defect this page can be given`)
  return which === 'css' ? { ...page, css: edited } : { ...page, html: edited }
}

test('the figures reading passes on the built page, so each refusal below is the one edit it names', () => {
  const { html, css } = TODAY()
  assert.deepEqual(figureLayoutFailures(css, html), [])
})

test('every figures refusal fires on the defect it names', () => {
  const cases = [
    // The markup, which is where the labels are.
    ['a page with no figures table', plant(/section id="typical-costs"/, 'section id="not-the-figures"'), /renders no figures table/],
    ['a cell with no label', plant(/ data-label="Runs"/, ''), /no label on the cell for "Runs"/],
    ['a cell labelled something other than its column', plant(/data-label="Median"/, 'data-label="Average"'), /labels a cell "Average" where its column's head reads "Median"/],
    ['a cell with an empty label', plant(/data-label="Runs"/, 'data-label=" "'), /no label on the cell for "Runs"/],
    ['a row the head has no column for', plant(/<td data-label="Worst"[^>]*>[^<]*<\/td>/, ''), /carries 5 cell\(s\) where its head names 6 columns/],
    // The phone's layout, one declaration at a time.
    ['no phone breakpoint at all', plant(/\(width<=47\.98rem\)/, '(width<=9.98rem)', 'css'), /a table is not `display: block`/],
    ['a head row left in the layout', plant(/thead\[[^\]]*\]\{display:none\}/, '', 'css'), /the head row is still laid out/],
    ['the table body left out of the stacking rule', plant(/,tbody\[[^\]]*\]/, '', 'css'), /the body is not `display: block`/],
    ['the header cells left out of the stacking rule', plant(/,th\[[^\]]*\](?=,td\[[^\]]*\]\{display:block\})/, '', 'css'), /a header cell is not `display: block`/],
    ['the data cells left out of the stacking rule', plant(/,td\[[^\]]*\](?=\{display:block\})/, '', 'css'), /a data cell is not `display: block`/],
    ['the label rule gone', plant(/th\[[^\]]*\]\[data-label\]:before,td\[[^\]]*\]\[data-label\]:before\{[^}]*\}/, '', 'css'), /no rule prints the cell's own label/],
    ['a label that prints something else', plant(/content:attr\(data-label\)/, 'content:"figure"', 'css'), /no rule prints the cell's own label/],
    ['a label rule whose selector reaches no cell', plant(/th\[[^\]]*\]\[data-label\]:before/, (part) => part.replace('[data-label]', '[data-astro-cid-0000000000][data-label]'), 'css'), /no rule prints a label on the th of/],
    ['the figures left at the table\'s smaller type', plant(/table\[[^\]]*\]\{font-size:var\(--font-size-base\)\}/, '', 'css'), /where the body is 17 px/],
    // The wide table's own fit, left in force at 360: the defect the owner read.
    ['the wide layout\'s nowrap left in force', plant(/@media \(width<=47\.98rem\)\{/, '#typical-costs td{white-space:nowrap}@media (width<=47.98rem){', 'css'), /holds the figures on one line/],
    ['the wide table\'s scroll box left in force', plant(/@media \(width<=47\.98rem\)\{/, '.table-scroll{overflow-x:auto}@media (width<=47.98rem){', 'css'), /is a box that scrolls sideways/],
    ['a fixed width on the cells', plant(/th\[[^\]]*\],td\[[^\]]*\]\{border-bottom:none;padding:0\}/, (rule) => rule.replace('padding:0}', 'padding:0;width:400px}'), 'css'), /against the 328 px a phone's content box/],
    ['a width this check cannot measure', plant(/th\[[^\]]*\],td\[[^\]]*\]\{border-bottom:none;padding:0\}/, (rule) => rule.replace('padding:0}', 'padding:0;width:calc(20rem + 1px)}'), 'css'), /not a length this check can turn into pixels/],
    // And the same fit above the breakpoint, which is the half a gate over phone widths alone
    // cannot see: gone, or moved behind a wider query, it leaves the window from 768 px up.
    ['the wide layout\'s block deleted', plant(/@media \(width>=48rem\)\{[^}]*\{[^}]*\}[^}]*\{[^}]*\}\}/, '', 'css'), /does not scroll sideways/],
    ['the wide layout\'s block moved behind a wider query', plant(/\(width>=48rem\)/, '(width>=60rem)', 'css'), /does not scroll sideways/],
    ['the nowrap figures deleted from it', plant(/white-space:nowrap/, 'white-space:normal', 'css'), /are not held on their own lines/],
  ]
  for (const [what, page, expected] of cases) {
    const failures = figureLayoutFailures(page.css, page.html)
    assert.ok(
      failures.some((line) => expected.test(line)),
      `${what} was not refused (${expected}); the reading said:\n  ${failures.join('\n  ') || '(nothing)'}`,
    )
  }
})
