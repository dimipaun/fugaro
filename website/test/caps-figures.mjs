// website/test/caps-figures.mjs — the caps-and-costs figures on a phone (the owner's feedback
// of 2026-10-19: "these render terrible on mobile"), as the bold redesign of 2026-10-21 left
// them.
//
// **What this file used to hold, and why the rules below are not those rules.** The page's
// figures were two tables — what a job costs (`#typical-costs`, six columns of nowrap figures)
// and the aggregation's per-recipe ranges (`#costs`, five columns whose header text alone is
// longer than a phone's line) — and both were read as tables at 360 px: the first inside an
// `overflow-x: auto` box, which is six columns scrolling sideways inside their own box, and the
// second with no box at all, whose minimum content width is what widened the document. The fix
// was to stack them below the site's breakpoint: every row a block, every cell a line labelled
// with its column's own header text through `data-label`, printed by one rule
// (`content: attr(data-label)`) rather than written into every cell. This gate held that.
//
// The redesign deleted both tables. The figures are three job cards now — a name, the runs
// behind it, the median, and three labelled figures under it — and the caps are cards. There is
// no `<table>` on the page, so the stacking rules, the `data-label`s and the rule that printed
// them are gone with the tables, and this file was rewritten rather than kept: a gate whose
// refusals cannot fire is worse than no gate, because it reads like one. What the cards changed
// is the *shape* of each rule, not the defect each one is for:
//
//   1. **every figure is under the name it is for.** The label is the markup's own now — a
//      `<dt>` before its `<dd>` — and this is the same rule the `data-label` check was: a figure
//      a reader cannot name is a figure they cannot check against the run it came from. A `<dd>`
//      with no `<dt>` before it, a `<dt>` that names nothing, a figure that prints nothing and
//      two figures sharing one name in a card are the four ways the card can lose a name, and
//      each is read here.
//   2. **a phone reads one column of cards.** `.caps-jobs` is a grid, and three columns of
//      figures in a 328 px content box is the crushed-table defect the owner read, one
//      redesign along: at 360 and at 390 px the grid has one track, read out of the page's own
//      `grid-template-columns` rather than assumed from a class name.
//   3. **a labelled row wraps rather than pushing its figure out of the card.** The row is a
//      flex line with the name at one end and the figure at the other (`justify-content:
//      space-between`); a row that cannot wrap is a figure off the edge of the card the moment
//      either side is longer than the card, and the figure a reader came for is the one that
//      leaves.
//   4. **nothing at a phone's width holds a figure on one line or in a scroll box.** The wide
//      table's own fit — `white-space: nowrap` on the figures and the `overflow-x` box around
//      them — belonged to the layout above the breakpoint; in force at 360 it is the defect the
//      owner read, and a figure that cannot break runs out of its card at 360 whatever the grid
//      around it does. The cards need neither at any width, and a scroll box on a phone is the
//      sideways scroll this page's layout exists to remove.
//   5. **a fixed width fits the phone's content box.** A card or a grid given a length wider
//      than 328 px is a box wider than the screen it is read on — the arithmetic the run's own
//      gate does, over the boxes this page draws.
//   6. **the figures are at least the body's size.** `base.css` sets every `table` at 15 px
//      (`--font-size-sm`) where the body is 17 px, and a card's rows set their own sizes; the
//      reading is the most specific `font-size` any rule in force naming a figure row declares,
//      which is what a browser would apply to the row's own box.
//   7. **above the breakpoint the three cards are side by side.** This page widens at 60 rem
//      (960 px, its own breakpoint and not the site's 48 rem), and a block that was deleted or
//      moved behind a wider query leaves one column of cards on a desktop — the comparison the
//      band is for, and the mirror of the wide table's fit this gate used to read at 768 px.
//   8. **the bar's own sentence names the marks the bar draws, and only those.** The bar is a
//      picture of the figures above it, and its `aria-label` is what a reader who cannot see it
//      has: a label that names a mark the bar does not draw describes a picture that is not
//      there (the review job has no middle half and no worst run, so its bar draws neither),
//      and a label that drops one leaves a mark unexplained. The component's own comment states
//      this contract and nothing else held it: the sentence and the drawn marks are read
//      against each other, mark by mark.
//   9. **the hero's two actions are 44 px targets.** The owner's brief of 2026-10-19 measures
//      every control a page asks a reader to tap against 44 px, and this page's two are the
//      hero's filled call to action and the quiet link beside it. The reading is
//      `test/lib/controls.mjs`'s, which the homepage's own controls are held by as well — one
//      rule read in both places, over each page's own list.
//
// **What this gate does not read.** The figures themselves: whether the card prints the figure
// the file carries, under the field it is for, is `caps-and-costs.mjs`'s subject, and the two
// files are split that way because a page can print the right figures in a card a phone cannot
// read and vice versa. The runs line is a caption rather than a figure under a name, so rule 6
// does not reach it, and the bar's geometry — where each mark sits along the cap — is the
// component's arithmetic over the file's own figures, which rule 8 reads only as far as *which*
// marks are drawn.
//
// Everything else is the shape the site's other layout gates keep: a pure failure function over
// one built page's HTML and CSS, a case that reads the built artifact, and one fixture case per
// named refusal, each built by taking the one thing it names out of the artifact's own bytes.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { DIST, cssDeclarations, cssRules, parse, textOf, walk } from './lib/html.mjs'
import { controlTargetFailures } from './lib/controls.mjs'

const ROUTE = 'caps-and-costs/index.html'

/** The band the figures are rendered in. The cards below it are what this file reads. */
const BAND = 'typical-costs'

/** The two widths the owner named: a small phone, and the width the site is designed against. */
export const FIGURE_WIDTHS = [360, 390]

/**
 * The widths above this page's own breakpoint, where the three cards are side by side: 960 px is
 * 60 rem, the width this page widens at (its own choice — the site's breakpoint is 48 rem, and
 * three cards of labelled figures need more room than the site's own two-column layouts), and
 * 1280 px is a desktop. A block that was deleted, or moved behind a wider query, is green on
 * every phone width and leaves one column of cards on a desktop.
 */
export const WIDE_WIDTHS = [960, 1280]

/** The phone whose content box nothing the figures draw may exceed. */
const PHONE = 360

// The selector parts this gate reads, as the shapes this site writes them in. A part is matched
// by the names it carries rather than by its own text, so Astro's scope attribute —
// `.caps-jobs[data-astro-cid-…]` — is a part of every one of them and none of these reads
// depends on which hash the build emitted.
const JOBS = /(^|[^\w-])caps-jobs(?![\w-])/
const FIGURE_ROW = /(^|[^\w-])caps-figure-row(?![\w-])/
const FIGURE = /caps-(job|figure)/

/**
 * The controls this page asks a reader to tap, and what a failure calls each one: the hero's
 * filled call to action and the quiet link beside it. Held to the 44 px target by
 * `test/lib/controls.mjs`, which the homepage's own controls are held by as well.
 */
const CONTROL_TARGETS = [
  ['caps-cta', "the hero's filled call to action"],
  ['caps-cta-quiet', "the hero's quiet call to action"],
]

/**
 * The marks a bar can draw, each with the phrase the bar's own sentence uses for it. The order
 * is the order the bar draws them in, and the review job draws neither of the two ends: it has
 * no middle half and no worst run, so its bar is the median and the nine-in-ten mark alone.
 */
const BAR_MARKS = [
  ['caps-bar-range', 'the middle half'],
  ['caps-bar-median', 'the median'],
  ['caps-bar-nine', 'nine runs in ten stayed under this'],
  ['caps-bar-worst', 'the worst run'],
]

/** A string with its whitespace flattened, for comparisons across markup. */
function collapse(text) {
  return text.replace(/\s+/g, ' ').trim()
}

/** The class list of an element, as a list. */
function classesOf(el) {
  return (el.attrs?.get('class') ?? '').split(/\s+/).filter(Boolean)
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
 * `.caps-figure-row[data-astro-cid-…]`, and a reader that scored the two alike would read a
 * figure's `font-size` out of the wrong one — the built stylesheet puts the page's rules
 * *before* `base.css`, so document order alone answers this one backwards.
 */
function specificityOf(part) {
  const text = part.replace(/:where\([^)]*\)/g, '')
  const subject = text.split(/[\s>+~]+/).filter(Boolean).at(-1) ?? ''
  return [
    (subject.match(/#[\w-]+/g) ?? []).length,
    (subject.match(/\.[\w-]+|\[[^\]]*\]/g) ?? []).length,
    (subject.match(/(^|[^\w-])(table|thead|tbody|tr|th|td|dt|dd)(?![\w-])/g) ?? []).length,
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
 * names none. A declaration on `::before` is a declaration about a box the element does not
 * have, so crediting it to the element is how a rule that styles a decoration reads as one that
 * styles the figure.
 *
 * **A pseudo-class is not a pseudo-element**, and the difference is load-bearing here:
 * `::before` (which the minifier writes `:before`) is a box of its own, and `:where(table)` —
 * `base.css`'s own way of setting a table's font size — is the table itself. A reader that took
 * every leading colon for a pseudo-element would throw away the rule that sets a figure's type
 * and read the figures as though nothing had set their size at all.
 */
const PSEUDO_ELEMENTS = new Set(['before', 'after', 'first-line', 'first-letter'])

function pseudoOf(part) {
  const match = /(::?)([a-z-]+)(\([^)]*\))?$/.exec(part.trim())
  if (match === null) return null
  if (match[1] === '::' || PSEUDO_ELEMENTS.has(match[2])) return match[0]
  return null
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

/**
 * A `grid-template-columns` value's track count: `repeat(n, …)` is n, and anything else is its
 * top-level words (`minmax(0, 1fr)` is one track, `minmax(0, 1fr) minmax(0, 24rem)` is two).
 * `null` for a value this reader cannot count — no declaration, or `none` — and `null` is never
 * a pass, so a grid whose columns nobody can read is a grid nothing here has shown to be one
 * column.
 */
function trackCount(value) {
  if (value === undefined) return null
  const text = value.trim()
  if (text === '' || text === 'none') return null
  const repeat = /^repeat\(\s*(\d+)\s*,/.exec(text)
  if (repeat !== null) return Number(repeat[1])
  let depth = 0
  let tracks = 0
  let inWord = false
  for (const ch of text) {
    if (ch === '(') depth += 1
    else if (ch === ')') depth -= 1
    if (depth === 0 && /\s/.test(ch)) {
      inWord = false
      continue
    }
    if (!inWord) {
      tracks += 1
      inWord = true
    }
  }
  return tracks === 0 ? null : tracks
}

// ---- the reading -----------------------------------------------------------------------

/**
 * Every way the caps page's figures fail a phone, as the lines a reader gets. `css` is one
 * built page's stylesheet and `html` its markup; both are the artifact's own bytes, so this can
 * be run over the checkout's build and over the same bytes with a single thing planted in them.
 */
export function figureLayoutFailures(css, html) {
  const out = []
  const fail = (why) => out.push(why)

  // ---- the controls: every one the page asks a reader to tap is a 44 px target -----------
  //
  // Read first, and over the whole stylesheet, because it is not about the figures: a page that
  // lost the band below still has to be tappable, and the two checks would otherwise be read as
  // one. The rule is `test/lib/controls.mjs`'s, the list is this page's.
  for (const line of controlTargetFailures(css, CONTROL_TARGETS)) fail(line)

  // ---- the markup: every figure under the name it is for ---------------------------------

  const doc = parse(html)
  let band = null
  for (const el of walk(doc)) {
    if (el.name === 'section' && el.attrs.get('id') === BAND) {
      band = el
      break
    }
  }
  if (band === null) {
    fail(
      `the page renders no #${BAND} band: this check reads the figures the page draws, so a page that lost the band passes every rule below without one of them being read`,
    )
    return out
  }

  const cards = [...walk(band)].filter((el) => el.attrs.has('data-job'))
  if (cards.length === 0) {
    fail(
      `the page renders no job card under #${BAND}: the figures are one card per kind of run, and a band whose cards are gone is a band this check would otherwise read as passing`,
    )
    return out
  }

  cards.forEach((card) => {
    const where = `the card for ${JSON.stringify(card.attrs.get('data-job'))}`

    /** The one element of the card carrying a class, or a failure naming what the card lost. */
    const oneOf = (klass, what) => {
      const found = [...walk(card)].filter((el) => classesOf(el).includes(klass))
      if (found.length === 0) {
        fail(`${where} carries no ${what}: the card's figures are read by it, and a card without one is a card whose figures this check cannot name`)
        return
      }
      if (found.length > 1) {
        fail(`${where} carries ${found.length} ${what}s where it needs one: two boxes for one thing is a reader shown the same figure twice, or a second copy that drifts from the first`)
        return
      }
      if (collapse(textOf(found[0])) === '') {
        fail(`${where} carries a ${what} that says nothing: it is a name for a figure and it is empty, so the figure under it reads as one nobody named`)
      }
    }

    oneOf('caps-job-name', 'job name')
    oneOf('caps-job-median-label', 'label on its median')
    oneOf('caps-job-median-value', 'median')

    // The bar, and the sentence that stands in for it. The bar is a picture of the figures the
    // card prints above it — it carries no text a reader can see — so its `aria-label` is the
    // whole of what a reader who cannot see it is told, and the component's own comment states
    // the contract this reads: the sentence names the marks the bar draws, and only those. The
    // review job is where the two drift apart: it has no middle half and no worst run, so its
    // bar draws neither, and a sentence that promised them describes a picture that is not
    // there.
    const bar = [...walk(card)].find(
      (el) => el.name === 'svg' && classesOf(el).includes('caps-bar'),
    )
    if (bar === undefined) {
      fail(`${where} draws no bar: the bar is the card's figures as a picture against the per-run cap, and a card without one is a card this check reads no further than`)
    } else {
      const label = collapse(bar.attrs.get('aria-label') ?? '')
      if (label === '') {
        fail(`${where} draws a bar with no accessible name: the bar is a picture and carries no text of its own, so a reader who cannot see it is told nothing about the figures it draws`)
      } else {
        for (const [klass, phrase] of BAR_MARKS) {
          const drawn = [...walk(bar)].some((el) => classesOf(el).includes(klass))
          const named = label.includes(phrase)
          if (drawn && !named) {
            fail(`${where} draws the ${klass} mark and its sentence does not name ${JSON.stringify(phrase)} — ${JSON.stringify(label)}: a mark the bar draws and the sentence does not mention is a figure a reader who cannot see the bar is never told about`)
          }
          if (!drawn && named) {
            fail(`${where} draws no ${klass} mark and its sentence names ${JSON.stringify(phrase)} — ${JSON.stringify(label)}: the sentence describes a picture that is not there, which is what the review job's bar does when it is given a middle half it has no figure for`)
          }
        }
      }

      // And every mark the bar draws is **inside the drawing** (the owner's feedback of
      // 2026-10-22). A mark is a coordinate the component computed from the file's own figures,
      // and the drawing has no width beyond its `viewBox`: a mark placed past it is a figure a
      // reader never sees, drawn off the card. The scale is what decides it — the bar is drawn
      // against the per-run cap the figures were **measured under**, and a bar scaled by the cap
      // in force today would put the owner's own worst figures past the end of the track, which
      // is the defect this rule exists for: the cap the page prints is $5 and two of the figures
      // it prints are above it. The reader lowercases an attribute's name, as a browser does
      // (`viewBox` is written `viewbox`), which is how the drawing's own scale is read here.
      const declared = bar.attrs.get('viewbox') ?? ''
      const box = declared.trim().split(/\s+/).map(Number)
      const boxWidth = box[2]
      if (!(boxWidth > 0)) {
        fail(`${where} draws a bar whose viewBox ${JSON.stringify(declared)} names no width, and the marks it draws are placed in that coordinate system`)
      } else {
        for (const mark of walk(bar)) {
          if (mark.name !== 'rect') continue
          const klass = classesOf(mark).find((name) => name.startsWith('caps-bar-')) ?? 'a rect'
          const x = Number(mark.attrs.get('x') ?? 0)
          const width = Number(mark.attrs.get('width') ?? 0)
          if (!(x >= 0) || !(x + width <= boxWidth)) {
            fail(`${where} draws ${klass} from x=${mark.attrs.get('x')} to x=${Number((x + width).toFixed(2))}, and the bar's own viewBox ends at ${boxWidth}: a mark past the end of the drawing is a figure a reader never sees, and the bar is scaled by the per-run cap the figures were measured under rather than the one in force today`)
          }
        }
      }
    }

    // The card's own `<dl>`: the labelled figures, held to the name each one is under. The
    // label is the markup's own here — the `<dt>` before its `<dd>` — so the four ways a card
    // can lose a name are all read in one pass over the list.
    const list = [...walk(card)].find((el) => el.name === 'dl')
    if (list === undefined) {
      fail(`${where} carries no <dl> of labelled figures: the figures a job costs are a name and its figure, and a card that prints them without one is a card whose figures no reader can check against the run they came from`)
      return
    }
    let label = null
    let figures = 0
    const named = new Set()
    for (const el of walk(list)) {
      if (el.name === 'dt') {
        label = collapse(textOf(el))
        if (label === '') {
          fail(`${where} carries a <dt> that names nothing: the figure under it is a figure a reader reads under a blank line, which is the defect this rule exists for (the owner's feedback of 2026-10-19)`)
          label = null
          continue
        }
        if (named.has(label)) {
          fail(`${where} prints two figures under the name ${JSON.stringify(label)}: a name is what tells one figure from the next, so two figures sharing one leaves a reader unable to say which is which`)
        }
        named.add(label)
      } else if (el.name === 'dd') {
        const figure = collapse(textOf(el))
        if (label === null) {
          fail(`${where} prints the figure ${JSON.stringify(figure)} with no <dt> before it: a figure with no name is a figure a reader cannot check against the run it came from (the owner's feedback of 2026-10-19)`)
        } else if (figure === '') {
          fail(`${where} prints no figure under the name ${JSON.stringify(label)}: a name with nothing under it reads as a figure the page lost`)
        } else {
          figures += 1
        }
        label = null
      }
    }
    if (figures === 0) {
      fail(`${where} prints no labelled figure at all: the band is the page's figures, and a card of names with nothing under them is a page that lost them`)
    }
  })

  // ---- the stylesheet: the phone's layout, and the wide layout kept above it -------------

  const rules = cssRules(css)
  const tokens = lengthTokens(css)

  /**
   * The value a property has on the elements a test names, at a viewport width: the last of the
   * rules in force whose parts match, the most specific of them winning, `undefined` when no
   * rule states it.
   *
   * A part that names a **pseudo-element** is not read here: `.caps-figure-row::before { display:
   * block }` is a declaration about a box the row does not have, and crediting it to the row is
   * how a rule about a decoration reads as one about the figure.
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

  // The phone's content box: the viewport less the gutter `main` gives the page at it, read out
  // of the same stylesheet rather than typed, so the arithmetic cannot drift from the page.
  const gutter = toPx(
    inlineToken(
      rules
        .filter((rule) => rule.selector === 'main' && inForce(rule, PHONE))
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

  // The body's own size, which is the floor a card's figures are held to.
  const bodyPx = toPx(declared(PHONE, /^body$/, 'font-size'), tokens)
  if (Number.isNaN(bodyPx) || bodyPx <= 0) {
    fail(`the body's own font size cannot be read from the built stylesheet at ${PHONE} px, so the size the figures have to reach is unknown: \`base.css\` is the file that sets it, and this check cannot hold a figure to a size nobody stated`)
  }

  for (const width of FIGURE_WIDTHS) {
    const at = `${width} px`

    // One column of cards: three columns of labelled figures in a 328 px content box is the
    // crushed-table defect the owner read, one redesign along.
    const columns = declared(width, JOBS, 'grid-template-columns')
    if (trackCount(columns) !== 1) {
      const read = trackCount(columns)
      fail(
        `at ${at} the job cards are laid out in ${read === null ? 'a grid this check cannot count' : `${read} columns`} (\`grid-template-columns: ${columns ?? '(nothing declared)'}\` on \`.caps-jobs\`): the cards are one column at ${at}, where three columns of figures in a ${Math.round(phoneBox)} px content box is the sideways scroll or the crushed columns this page's layout exists to remove (the owner's feedback of 2026-10-19)`,
      )
    }

    // The row that carries a name and its figure: it wraps rather than pushing the figure out.
    const wrap = declared(width, FIGURE_ROW, 'flex-wrap')
    if ((wrap ?? '').trim() !== 'wrap') {
      fail(
        `at ${at} the rows that carry a figure and its name cannot wrap (\`flex-wrap: ${wrap ?? '(nothing declared)'}\` on \`.caps-figure-row\`): the row is a name at one end and its figure at the other, and a row that cannot wrap pushes the figure — the half a reader came for — out of the card the moment either side is longer than the card is wide`,
      )
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
          `at ${at} ${JSON.stringify(parts[0])}${inside} holds the figures on one line (\`white-space: nowrap\`): a figure that cannot break runs out of the card it is read in, and the wide table's own fit belonged above the site's breakpoint (the owner's feedback of 2026-10-19)`,
        )
      }
      if (/^(auto|scroll)$/.test((decls.get('overflow-x') ?? '').trim())) {
        fail(
          `at ${at} ${JSON.stringify(parts[0])}${inside} is a box that scrolls sideways (\`overflow-x: ${decls.get('overflow-x')}\`): the figures fit the card they are read in at ${at}, and a scroll box around them is the sideways scroll this page's layout exists to remove`,
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

    // And the size the figures are set in, which is the one thing a card's rows can get wrong
    // where a table had a rule of its own: `base.css` sets every `table` below the body's size,
    // and a row is free to do the same.
    const size = declared(width, FIGURE_ROW, 'font-size')
    if (size !== undefined && !Number.isNaN(bodyPx)) {
      const px = toPx(size, tokens)
      if (Number.isNaN(px)) {
        fail(
          `at ${at} the figures' \`font-size\` is \`${size}\`, which is not a length this check can turn into pixels, so the size a phone reads them at is a size nothing checked`,
        )
      } else if (px < bodyPx) {
        fail(
          `at ${at} the figures are set at ${Math.round(px)} px where the body is ${Math.round(bodyPx)} px: a labelled figure under its name is body text, and the smaller type is what a table's six columns used to need (the owner's feedback of 2026-10-19)`,
        )
      }
    }
  }

  // ---- above the breakpoint: the three cards are side by side -----------------------------
  //
  // This is the half a gate over phone widths alone cannot see: a block that was deleted, or
  // moved behind a wider query, passes every phone rule above and leaves one column of cards on
  // a desktop — the comparison the band is for, and the mirror of the wide table's own fit this
  // gate used to read at 768 px.
  for (const width of WIDE_WIDTHS) {
    const at = `${width} px`
    const columns = declared(width, JOBS, 'grid-template-columns')
    const tracks = trackCount(columns)
    if (tracks === null || tracks < 3) {
      fail(
        `at ${at} the job cards are ${tracks === null ? 'laid out in a grid this check cannot count' : `laid out in ${tracks} column(s)`} (\`grid-template-columns: ${columns ?? '(nothing declared)'}\` on \`.caps-jobs\`): the three cards are side by side above this page's own 60 rem breakpoint, and one column of cards on a desktop is the comparison this band is for, lost`,
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
  assert.ok(/data-job=/.test(html), 'the caps page renders no job card at all, so this case checked nothing')
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
    // The markup, which is where the names are.
    ['a page with no figures band', plant(/section id="typical-costs"/, 'section id="not-the-figures"'), /renders no #typical-costs band/],
    ['a band whose cards are gone', plant(/ data-job="(?:task|follow_up|review)"/g, ''), /renders no job card under #typical-costs/],
    ['a card with no name', plant(/class="caps-job-name"/, 'class="caps-job-heading"'), /carries no job name/],
    ['a card whose name is empty', plant(/>A first task run<\/h3>/, '></h3>'), /carries a job name that says nothing/],
    ['a median under no label', plant(/<span class="caps-job-median-label"[^>]*>Median<\/span>/, ''), /carries no label on its median/],
    ['a median with no figure', plant(/<span class="caps-job-median-value"[^>]*>\$0\.36<\/span>/, ''), /carries no median/],
    ['a figure with no name', plant(/<dt[^>]*>9 in 10 under<\/dt>/, ''), /prints the figure "\$1\.27" with no <dt> before it/],
    ['a name with nothing under it', plant(/>Middle half<\/dt>/, '></dt>'), /carries a <dt> that names nothing/],
    ['two figures under one name', plant(/>Worst<\/dt>/, '>9 in 10 under</dt>'), /prints two figures under the name "9 in 10 under"/],
    ['a figure that prints nothing', plant(/>\$5\.24<\/dd>/, '></dd>'), /prints no figure under the name "Worst"/],
    ['a card with no list of figures', plant(/<dl class="caps-job-figures"[^>]*>([\s\S]*?)<\/dl>/, (_all, inner) => `<div class="caps-job-figures">${inner}</div>`), /carries no <dl> of labelled figures/],
    ['a card with no bar', plant(/class="caps-bar"/, 'class="caps-not-the-bar"'), /draws no bar/],
    ['a bar with no accessible name', plant(/aria-label="A review-only run against the \$10\.00 per-run cap these runs ran under: [^"]*"/, 'aria-label=""'), /draws a bar with no accessible name/],
    // The bar's sentence and the marks it draws, read against each other — the pair the review
    // job's own figures pull apart.
    ['a bar whose sentence drops a mark it draws', plant(/aria-label="A first task run against the \$10\.00 per-run cap these runs ran under: the middle half, /, 'aria-label="A first task run against the $10.00 per-run cap these runs ran under: '), /draws the caps-bar-range mark and its sentence does not name "the middle half"/],
    ['a bar whose sentence names a mark it does not draw', plant(/aria-label="A review-only run against the \$10\.00 per-run cap these runs ran under: the median/, 'aria-label="A review-only run against the $10.00 per-run cap these runs ran under: the middle half, the median'), /draws no caps-bar-range mark and its sentence names "the middle half"/],
    ['a bar whose sentence loses the worst run it draws', plant(/ and the worst run\."/, '."'), /draws the caps-bar-worst mark and its sentence does not name "the worst run"/],
    // And the drawing the marks are placed in: a bar whose coordinate system is narrower than the
    // marks it draws is a bar whose figures are drawn off the card, which is the shape the scale
    // itself takes when the bar is drawn against the wrong cap (the owner's feedback of
    // 2026-10-22: the cap is $5 now and the figures were measured under $10).
    ['a bar whose marks are drawn outside its own viewBox', plant(/viewBox="0 0 100 22"/, 'viewBox="0 0 80 22"'), /the bar's own viewBox ends at 80/],
    // The phone's layout, one declaration at a time.
    ['the cards left in three columns at a phone width', plant(/grid-template-columns:1fr/, 'grid-template-columns:repeat(3,minmax(0,1fr))', 'css'), /laid out in 3 columns/],
    ['a row that cannot wrap', plant(/\.caps-figure-row\[[^\]]*\]\{[^}]*\}/, (rule) => rule.replace('flex-wrap:wrap;', ''), 'css'), /cannot wrap/],
    ['the wide layout\'s nowrap left in force', plant(/\.caps-band\[[^\]]*\]\{/, (rule) => `.caps-figure-row{white-space:nowrap}${rule}`, 'css'), /holds the figures on one line/],
    ['a scroll box around the figures', plant(/\.caps-band\[[^\]]*\]\{/, (rule) => `.caps-jobs{overflow-x:auto}${rule}`, 'css'), /is a box that scrolls sideways/],
    ['a fixed width on the cards', plant(/grid-template-columns:1fr/, 'grid-template-columns:1fr;width:400px', 'css'), /against the 328 px a phone's content box/],
    ['a width this check cannot measure', plant(/grid-template-columns:1fr/, 'grid-template-columns:1fr;width:calc(20rem + 1px)', 'css'), /not a length this check can turn into pixels/],
    ['the figures left at a table\'s smaller type', plant(/\.caps-figure-row\[[^\]]*\] dd\[[^\]]*\]\{[^}]*\}/, (rule) => rule.replace('font-size:var(--font-size-lg)', 'font-size:var(--font-size-sm)'), 'css'), /where the body is 17 px/],
    // And the wide layout, which is the half a gate over phone widths alone cannot see: gone,
    // or moved behind a wider query, it leaves one column of cards on a desktop.
    ['the wide layout\'s block deleted', plant(/\.caps-jobs\[[^\]]*\]\{grid-template-columns:repeat\(3,minmax\(0,1fr\)\)\}/, '', 'css'), /laid out in 1 column\(s\)/],
    ['the wide layout\'s block moved behind a wider query', plant(/\(width>=60rem\)/, '(width>=100rem)', 'css'), /laid out in 1 column\(s\)/],
    // And the page's own two controls, held to the target size the owner's brief of 2026-10-19
    // measures every control a reader is asked to tap against. The declaration is the whole of
    // what the rule can read — a filled link's own height is a fraction of a pixel short with
    // nothing in the markup to say so — so the two ways it can go are a smaller minimum and no
    // minimum at all, and the third is a minimum that holds at one width only.
    ['a hero action under the 44 px target', plant(/\.caps-cta\[[^\]]*\]\{[^}]*\}/, (rule) => rule.replace('min-height:44px', 'min-height:20px'), 'css'), /the hero's filled call to action is a 20 px target/],
    ['a hero action with no minimum at all', plant(/\.caps-cta-quiet\[[^\]]*\]\{[^}]*\}/, (rule) => rule.replace('min-height:44px;', ''), 'css'), /the hero's quiet call to action is no declared target/],
    ['a hero action whose minimum holds only above a breakpoint', plant(/\.caps-cta\[[^\]]*\]\{[^}]*\}/, (rule) => `${rule.replace('min-height:44px;', '')}@media (min-width:48rem){.caps-cta{min-height:56px}}`, 'css'), /the hero's filled call to action is no declared target/],
  ]
  for (const [what, page, expected] of cases) {
    const failures = figureLayoutFailures(page.css, page.html)
    assert.ok(
      failures.some((line) => expected.test(line)),
      `${what} was not refused (${expected}); the reading said:\n  ${failures.join('\n  ') || '(nothing)'}`,
    )
  }
})
