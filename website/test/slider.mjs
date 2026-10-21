// website/test/slider.mjs — the homepage's one slider (the owner's approved draft "Home, draft 2
// (slider)", 2026-10-19; the owner's feedback of 2026-10-21, item 3, which turned the strip of
// tabs into a slider).
//
// The draft puts the whole of what the page has to show into one block, and the one thing that
// shape can get wrong without anyone noticing is the thing that makes it a slider: a slide that
// is not in the HTML a reader receives, a dot that opens nothing, or a switch that only works
// once a script has run. A reader with no JavaScript is not a corner case on this site —
// `privacy.astro` states that the page asks them for nothing, and the copy control is the site's
// one script — so the block is read here as what it is: three panels, three fragment links and a
// stylesheet, with the clock as something laid over the top of them.
//
//   1. **Three slides, three panels, all of them in the HTML.** The dots are a list of three
//      links, each one's `href` is the fragment of the panel it opens, and each panel carries the
//      slide's own label as its heading. A dot whose `href` names an id no element carries is a
//      dot that opens nothing, and it is the defect the pairing below exists for.
//   2. **The stylesheet shows one panel, in both states, and marks the dot of the slide on
//      screen.** Every panel is hidden; the script's state shows the panel the block's own name
//      points at; the fragment's state shows the targeted panel, and shows the first one while
//      nothing is targeted; and each state is paired with the dot it belongs to, read one slide
//      at a time so a stylesheet that pairs the Why Fugaro? dot with the See it panel is refused
//      rather than counted as three pairs present. The fragment's rules stand down while the
//      script is driving (`:not([data-active])`), because a reader who arrived on a fragment and
//      then let the block advance would otherwise be shown two panels at once.
//   3. **The block is a slider with no script in it, and the site's one script names nothing in
//      it.** The two step controls are emitted `hidden` — a reader whose browser runs no script
//      is shown no control that steps nowhere (D183) — the block carries no `<script>`, and the
//      script joins itself to the block through `data-` attributes alone, so neither file has to
//      be edited when the other changes.
//   4. **A 44 px target that wraps, and the ring the site draws.** A dot and a step control are
//      each at least 44 px tall, the two rows wrap rather than scroll, no rule on either removes
//      the focus outline, the page carries the site's `:focus-visible` ring, and nothing in the
//      block is wider than a 360 px phone's content box or refuses to break a line.
//   5. **The behaviour, run.** The file the site ships, against the built homepage, in a stub
//      DOM: the clock steps the block on and wraps, a pointer or a focus in the block holds it,
//      a page nobody is shown runs no clock, a reader who asked for less motion gets none of it,
//      the dots and the two controls and the arrow keys all move the block, and a reader who
//      arrives on a fragment opens on that slide. Each case is planted below with the line it
//      reads taken out of the script, so a case that cannot fail is caught here.
//   6. **The page's second slider, run too** (D246, D250). The block's "See it" panel renders
//      the console page's own component, which is a slider in its own right — a second
//      `[data-slider]`, whose four slides, four dots, count, two arrows and six-second clock are
//      the console page's, in the homepage's markup. Two of the cases in 5 are about it: that it
//      counts, marks, steps and clocks its OWN four rows while the block around it is not moved
//      by any of it, and that it stands still — reading the first of its four, marking the first
//      dot — while its panel is `display: none`, because a track the page lays out nowhere can
//      be scrolled nowhere and a reading that moved anyway would tell a reader who opens the
//      panel about a slide they are not shown. No other gate runs it: `home.mjs` reads its
//      markup, `console-screens.mjs` runs it on the console page, and this is the page in
//      between.
//   7. **The three steps are the repository's own lines.** Each step's line is a line of the
//      section of the document it cites, in that section's order — the same reading the
//      component's own build guard takes, held here against the page a reader receives, so a
//      guard removed from the component is a red gate rather than a page that paraphrases.
//
// Every check below is a reading of `dist/index.html`, of the repository, or of a run of the
// script the site ships, and each one is the mechanical form of one of the rules above rather
// than a check on the component's own text: what a page says about itself is not evidence, and
// the artifact is what a reader gets.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import vm from 'node:vm'
import { REPO, builtPages, cssDeclarations, cssRules, findAll, find, parse, textOf, walk } from './lib/html.mjs'

/** The homepage, as the artifact: the page the checks below are about. */
function homepage() {
  const page = builtPages().find((p) => p.rel === 'index.html')
  assert.ok(page, 'the build produced no index.html, and this file is a reading of the homepage')
  return page
}

/** The page's stylesheet: the site inlines it, so the page carries the whole of it. */
function pageCss(html) {
  return [...html.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((m) => m[1]).join('\n')
}

/** The site's one script, which is what the behaviour cases below run. */
const SCRIPT_SOURCE = readFileSync(path.join(REPO, 'website/public/scripts/site.js'), 'utf8')

/** A string with its markdown markers and its whitespace removed — how a step's line and the
 *  section it claims to come from are compared. The component's build guard reads it the same
 *  way, so the two cannot disagree about what a quotation is. */
function comparable(text) {
  return text.replace(/[`*]/g, '').replace(/\s+/g, '')
}

/** One `## <heading>` section of a document of this repository, up to the next `## `. */
function section(document, heading) {
  const text = readFileSync(path.join(REPO, document), 'utf8')
  const at = [...text.matchAll(/^## (.+)$/gm)].find((m) => m[1].replace(/`/g, '') === heading)
  assert.ok(at, `${document} carries no "## ${heading}" section, and a step's line is quoted from it`)
  const rest = text.slice(at.index)
  const next = rest.indexOf('\n## ', 1)
  return next === -1 ? rest : rest.slice(0, next)
}

const CLASSES_OF = (el) => (el.attrs.get('class') ?? '').split(/\s+/).filter(Boolean)
const HAS = (el, name) => CLASSES_OF(el).includes(name)

/**
 * The block's OWN elements matching `pred`: everything inside it that belongs to no nearer
 * slider. The homepage's "See it" panel renders a slider of its own — the console page's
 * `ScreenshotSlider`, whose rows are `[data-slider-slide]` and whose dots are `[data-slider-dot]`
 * — and the two blocks share those two hook names (D246). So this case reads the outer block's
 * three panels and three dots and not the inner block's four slides: the same rule `site.js`'s
 * own `own()` reads, which is what keeps this reading and the script's in step.
 */
function ownOf(block, pred) {
  return findAll(block, pred).filter((el) => {
    for (let at = el.parent; at; at = at.parent) {
      if (at !== block && at.name !== undefined && at.attrs.has('data-slider')) return false
    }
    return true
  })
}

/** The three slides, in the order the page reads them: the labels the dots are announced with
 *  and the headings the panels carry. */
const LABELS = ['Easy to use', 'Why Fugaro?', 'See it']

/** A length as pixels, resolving one level of `var(--token)` out of the stylesheet's own
 *  declarations, or `null` when it is not a length this reading can turn into pixels. */
function lengthPx(value, css) {
  const text = (value ?? '').trim()
  const reference = /^var\(\s*(--[\w-]+)\s*(?:,\s*([^)]+))?\)$/.exec(text)
  if (reference) {
    const declared = new RegExp(`${reference[1]}\\s*:\\s*([^;}]+)`).exec(css)
    if (declared) return lengthPx(declared[1], css)
    return reference[2] ? lengthPx(reference[2], css) : null
  }
  const number = /^(-?[\d.]+)(px|rem|em)?$/.exec(text)
  if (!number) return null
  const amount = Number(number[1])
  return number[2] === 'px' ? amount : amount * 16
}

/**
 * True when a rule's enclosing conditions hold at `width` px. A rule with no condition at all is
 * one the width does not gate, and it holds. A condition the width does not decide is treated as
 * *not* holding, the way `base-styles.mjs`'s own `appliesAt` treats it: a query with no width in
 * it (`prefers-reduced-motion`, `hover`) and a width in a unit this reading cannot turn into
 * pixels (`em`, `ch`) are both rules the reading cannot place, and a check that refuses things
 * does not refuse a rule it cannot place.
 *
 * The two forms a width is written in are read as one list: `min-width: 48rem` and `width>=48rem`
 * are the same bound, and a query carrying both forms carries both bounds.
 *
 * **An `@supports` condition holds.** It is not a question about the width — it is asked of the
 * browser — and this reading is taken in a browser that answers it: the block's no-script state
 * is written inside `@supports selector(:has(*))` (the component says why there), and a reading
 * that treated the condition as not holding would report a block that opens empty on every
 * browser, which is the opposite of what it does.
 */
function appliesAt(conditions, width) {
  for (const condition of conditions) {
    if (/^@supports\b/i.test(condition)) continue
    const at = /@media([^{]*)/i.exec(condition)
    if (!at) return false
    const bounds = [
      ...at[1].matchAll(/\(\s*(min-width|max-width)\s*:\s*([\d.]+)(px|rem)\s*\)/gi),
      ...at[1].matchAll(/\(\s*width\s*(>=|<=|>|<)\s*([\d.]+)(px|rem)\s*\)/gi),
    ]
    if (bounds.length === 0) return false
    for (const [, named, amount, unit] of bounds) {
      const limit = unit.toLowerCase() === 'px' ? Number(amount) : Number(amount) * 16
      const least = /^min-width$/i.test(named) || named === '>=' || named === '>'
      if (least && width < limit) return false
      if (!least && width > limit) return false
    }
  }
  return true
}

/** The rules of the page's stylesheet that hold at `width`, in the order a browser applies them. */
function rulesAt(css, width) {
  return cssRules(css).filter((rule) => rule.selector !== undefined && appliesAt(rule.conditions, width))
}

/** A rule's declarations as a map, later declarations winning over earlier ones. */
function declarationsOf(rule) {
  return new Map(cssDeclarations(rule.body))
}

/** The value of a property in the last rule naming `names` that holds at `width`, or `null`. */
function declaredAt(css, width, names, property) {
  let found = null
  for (const rule of rulesAt(css, width)) {
    if (!names.test(rule.selector)) continue
    const declared = declarationsOf(rule).get(property)
    if (declared !== undefined) found = declared
  }
  return found
}

/** Whether a rule's declarations take a panel out of the layout or put it back. */
function shows(declarations) {
  const display = declarations.get('display')
  return display !== undefined && display !== 'none'
}

/**
 * A selector list split at its **top-level** commas: the commas inside `:has(…)`, `:not(…)` and
 * `:is(…)` are part of one selector, not separators between two. `main:has(> .hero, >
 * [data-security-page])` is one selector and not two, and a reading that cut it in half would
 * report a `:has()` selector beside one that has none where there is only the one selector.
 */
function selectorParts(selector) {
  const parts = []
  let depth = 0
  let quote = ''
  let start = 0
  for (let i = 0; i < selector.length; i += 1) {
    const c = selector[i]
    if (quote) {
      if (c === quote) quote = ''
    } else if (c === '"' || c === "'") quote = c
    else if (c === '(') depth += 1
    else if (c === ')') depth -= 1
    else if (c === ',' && depth === 0) {
      parts.push(selector.slice(start, i).trim())
      start = i + 1
    }
  }
  parts.push(selector.slice(start).trim())
  return parts.filter(Boolean)
}

/**
 * A selector with its functional pseudo-classes' arguments removed: what the selector's last
 * compound names, which is the element a rule draws. A panel named inside `:has(.panel:target)`
 * is a panel the rule *looks for*, not one it draws, and a reading that took the two for the
 * same would call a rule that marks a dot the current one a rule that shows a panel.
 */
function subject(selector) {
  let out = selector
  for (const pseudo of [':has(', ':not(', ':is(', ':where(']) {
    let at = out.indexOf(pseudo)
    while (at !== -1) {
      let depth = 0
      let end = -1
      for (let i = at + pseudo.length - 1; i < out.length; i += 1) {
        if (out[i] === '(') depth += 1
        else if (out[i] === ')') {
          depth -= 1
          if (depth === 0) {
            end = i
            break
          }
        }
      }
      if (end === -1) break
      out = out.slice(0, at) + out.slice(end + 1)
      at = out.indexOf(pseudo)
    }
  }
  return out
}

const DOT = /\.slider-dot(?![\w-])/
const STEP = /\.slider-step(?![\w-])/
const DOTS = /\.slider-dots(?![\w-])/
const CONTROLS = /\.slider-controls(?![\w-])/
const PANEL = /\.panel(?![\w-])/

/**
 * The slider's own classes: everything the block's stylesheet draws, so the phone reading below
 * looks at the block rather than at the page.
 */
const SLIDER_CLASSES =
  /\.(home-slider|slider-title|slider-controls|slider-step|slider-arrow|slider-dots|slider-dot-item|slider-dot|panels|panel|panel-title|easy-steps|easy-step|step-number|step-icon|step-name|step-line|watch-shot|watch-block|watch-command|watch-note)(?![\w-])/

/** The phone the block has to fit, and the content box it has to fit in: the page's own gutter,
 *  read out of the same stylesheet rather than typed, so the arithmetic cannot drift. */
const PHONE = 360

function phoneBox(css) {
  const padding = declaredAt(css, PHONE, /^main$/, 'padding')
  const inline = (padding ?? '').split(/\s+/)
  const gutter = lengthPx(inline.length > 1 ? inline[1] : inline[0], css)
  assert.ok(
    gutter !== null,
    `\`main\`'s inline padding cannot be read from the built stylesheet at ${PHONE} px (\`padding: ${padding ?? '(nothing)'}\`), so the box the slider's own widths are held to is unknown: nothing in the block can be shown to fit a phone without it`,
  )
  return PHONE - 2 * gutter
}

/**
 * Everything the slider's markup and stylesheet can be wrong about, as sentences a reader of a
 * red run can act on. The plants at the foot of this file are each one of these refusals, and the
 * homepage as it ships is the case that passes.
 */
export function sliderFailures(css, html) {
  const out = []
  const doc = parse(html)
  const blocks = findAll(doc, (el) => HAS(el, 'home-slider'))
  if (blocks.length !== 1) {
    out.push(
      `the homepage carries ${blocks.length} sliders, and the draft asks for one block with three slides: ${blocks.length === 0 ? 'nothing here is the block the slides are read from' : 'a second one is a second set of slides with its own controls'}`,
    )
    return out
  }
  const block = blocks[0]

  // 1. the three slides, the three dots, and the pairing between them.
  const panels = ownOf(block, (el) => el.attrs.has('data-slider-slide'))
  const dots = ownOf(block, (el) => el.attrs.has('data-slider-dot'))
  if (!block.attrs.has('data-slider')) {
    out.push(
      'the block carries no `data-slider` attribute, and that attribute is the whole of how the site’s one script finds it: without it the block is three panels and three links with no clock, no step controls and no mark on the dot a reader is looking at',
    )
  }
  if (panels.length !== 3) {
    out.push(`the block carries ${panels.length} slides, and the draft asks for three: the panels, each one carrying its own name in \`data-slider-slide\``)
  }
  if (dots.length !== 3) {
    out.push(`the block carries ${dots.length} dots, and every slide has one: the dots are what tell a reader which of the three they are looking at and how many there are`)
  }
  if (panels.length === 3 && dots.length === 3) {
    for (const [index, panel] of panels.entries()) {
      const label = LABELS[index]
      const name = panel.attrs.get('data-slider-slide') ?? ''
      const id = panel.attrs.get('id') ?? ''
      const dot = dots[index]
      const href = dot.attrs.get('href') ?? ''
      const said = dot.attrs.get('aria-label') ?? ''
      if (id !== `home-panel-${name}`) {
        out.push(
          `slide ${index + 1} is named ${JSON.stringify(name)} and carries the id ${JSON.stringify(id)}, and the two are one name: the dot links to the id, the stylesheet keys the panel's \`:target\` rule on it, and the script writes the name on the block — a name that is not the id is a dot that opens nothing`,
        )
      }
      if (dot.name !== 'a') {
        out.push(
          `dot ${index + 1} is a <${dot.name}>, and a dot is a fragment link: the link is the whole of the switching with no script, and the script's own state is laid over the top of it`,
        )
      }
      if (!/^#home-panel-[\w-]+$/.test(href)) {
        out.push(
          `dot ${index + 1} links to ${JSON.stringify(href)}, and a dot is a fragment link to the panel it shows: without the fragment neither the stylesheet nor the script has anything to open the panel with`,
        )
      } else if (href !== `#${id}`) {
        out.push(
          `dot ${index + 1} links to ${href} where the slide beside it is #${id}: a dot whose fragment names another element opens nothing, and the fragment is the whole of the switching with no script`,
        )
      }
      if (dot.attrs.get('data-slider-dot') !== name) {
        out.push(
          `dot ${index + 1} carries ${JSON.stringify(dot.attrs.get('data-slider-dot') ?? '')} where the slide it opens is named ${JSON.stringify(name)}: the script finds the dot of the slide on screen by that name, and a dot named after another slide marks the wrong one as current`,
        )
      }
      if (said !== `Slide ${index + 1} of ${LABELS.length}: ${label}`) {
        out.push(
          `dot ${index + 1} is announced as ${JSON.stringify(said)}, and a dot is a circle with no text in it: its name is the whole of what a screen reader reads, so it is the slide and the reader's place in the three ("Slide ${index + 1} of ${LABELS.length}: ${label}")`,
        )
      }
      const headings = findAll(panel, 'h2')
      const heading = headings.length ? textOf(headings[0]).trim() : ''
      if (heading !== label) {
        out.push(
          `the panel dot ${index + 1} opens is headed ${JSON.stringify(heading)} where its dot reads ${JSON.stringify(label)}: a reader who arrives on a panel — by the dots, by the clock, by a screen reader or in the HTML — reads the label of the slide it is`,
        )
      }
      if (textOf(panel).trim() === '') {
        out.push(`the panel dot ${index + 1} (${JSON.stringify(label)}) opens carries no text, so the dot opens an empty box`)
      }
    }
  }

  // The two step controls: the script's, emitted `hidden` and revealed by it, each one a button
  // with a name (D183's "no broken control").
  for (const [what, attribute] of [
    ['previous', 'data-slider-prev'],
    ['next', 'data-slider-next'],
  ]) {
    const found = ownOf(block, (el) => el.attrs.has(attribute))
    if (found.length !== 1) {
      out.push(`the block carries ${found.length} ${what}-slide controls, and the draft asks for one of each: a reader who has only the dots cannot step through the block without moving their hands`)
      continue
    }
    const control = found[0]
    if (control.name !== 'button' || (control.attrs.get('type') ?? '') !== 'button') {
      out.push(`the ${what}-slide control is a <${control.name}> of type ${JSON.stringify(control.attrs.get('type') ?? '')}, and it steps a block rather than going anywhere: a button, so that nothing about it is a navigation and no browser offers it a new tab`)
    }
    if (!control.attrs.has('hidden')) {
      out.push(
        `the ${what}-slide control is not emitted \`hidden\`, and the site's script is what reveals it: a control rendered visible by the markup is a button that does nothing for a reader whose browser runs no script (D183)`,
      )
    }
    if ((control.attrs.get('aria-label') ?? '').trim() === '') {
      out.push(`the ${what}-slide control carries no name, and it is a button with a drawing in it: an arrow is not text, so the name is the whole of what a screen reader reads`)
    }
  }

  // Nothing about the block is built at runtime, and no panel is shipped hidden.
  if (findAll(block, 'script').length > 0) {
    out.push('the block carries a script, and every slide is in the HTML a reader receives: a reader whose script never arrives is a reader with no slides at all, which is the one thing this pattern exists to avoid')
  }
  for (const panel of panels) {
    if (panel.attrs.has('hidden') || panel.attrs.get('aria-hidden') === 'true') {
      out.push(
        `the panel #${panel.attrs.get('id') ?? '(no id)'} is marked hidden in the markup, and every panel is one of the three a reader can open: the stylesheet is what hides the two that are not on screen, and it is what shows them again`,
      )
    }
  }
  for (const el of [...panels, ...dots]) {
    const role = el.attrs.get('role')
    if (role === 'tab' || role === 'tabpanel' || el.attrs.has('aria-selected')) {
      out.push(
        `the element carrying ${JSON.stringify(el.attrs.get('class') ?? '')} declares role=${JSON.stringify(role)}${el.attrs.has('aria-selected') ? ' / aria-selected' : ''}, and those roles describe a widget whose selection is scripted: the slides here are reachable with no script at all, and a role promising \`aria-selected\` is a promise only a script can keep`,
      )
    }
  }
  for (const dot of dots) {
    if (dot.attrs.get('tabindex') === '-1') {
      out.push('a dot carries tabindex="-1", and every dot is a control a keyboard reaches: a dot the tab key skips is a slide the keyboard cannot open')
    }
  }

  // 2. the stylesheet: hide them all, show one per state, and mark one dot per slide.
  const rules = rulesAt(css, 1280)
  const hidden = rules.some(
    (rule) =>
      PANEL.test(subject(rule.selector)) &&
      !/:target/.test(rule.selector) &&
      !/data-active/.test(rule.selector) &&
      declarationsOf(rule).get('display') === 'none',
  )
  if (!hidden) {
    out.push('no rule hides the panels, and the block is a slider rather than three panels one under the other: the stylesheet shows the panel the block is on and takes the other two out of the layout')
  }
  // A selector list is read one selector at a time, the way a browser reads it: the pair is the
  // slide and the panel *in one selector*, and a rule that carries three pairs in a list is three
  // pairs rather than one selector naming three slides.
  const parts = rules.flatMap((rule) => rule.selector.split(',').map((part) => ({ part: part.trim(), declarations: declarationsOf(rule) })))

  const targeted = parts.filter(({ part, declarations }) => PANEL.test(subject(part)) && /:target/.test(subject(part)) && shows(declarations))
  if (targeted.length === 0) {
    out.push('no rule shows a targeted panel, so a dot that is clicked (or reached by a fragment in the address) switches nothing: the panel the address names has to be the one the stylesheet shows')
  }
  for (const { part } of targeted) {
    if (!/:not\(\[data-active/.test(part)) {
      out.push(
        `the selector \`${part}\` shows the panel the address names and does not stand down while the script is driving (\`:not([data-active])\`): a reader who arrived on a fragment and then let the block advance would be shown two panels at once`,
      )
    }
  }
  const opening = parts.some(
    ({ part, declarations }) => /#home-panel-easy/.test(part) && /:not\(:has\(\.panel:target\)\)/.test(part) && shows(declarations),
  )
  if (!opening) {
    out.push(
      'no rule shows #home-panel-easy while no panel is targeted, and the first slide is the one the page a reader arrives on has to be: with every panel hidden and no fragment in the address, the block opens empty',
    )
  }
  for (const panel of panels) {
    const name = panel.attrs.get('data-slider-slide') ?? ''
    const id = panel.attrs.get('id') ?? ''
    if (!id) continue
    const scripted = parts.some(
      ({ part, declarations }) =>
        new RegExp(`\\[data-active\\s*=\\s*["']?${name}["']?\\]`).test(part) && part.includes(`#${id}`) && shows(declarations),
    )
    if (!scripted) {
      out.push(
        `no rule shows #${id} while the block is on the slide ${JSON.stringify(name)}: the script writes that name on the block and the stylesheet is what shows the panel it belongs to, and without the rule the block advances to a panel nothing draws`,
      )
    }
  }
  for (const [index, dot] of dots.entries()) {
    const href = dot.attrs.get('href') ?? ''
    if (!href.startsWith('#')) continue
    const id = href.slice(1)
    const marked = parts.filter(({ part, declarations }) => part.includes(`#${id}:target`) && DOT.test(subject(part)) && /accent/.test(declarations.get('background') ?? ''))
    if (marked.length === 0) {
      out.push(
        `no rule marks the dot for ${href} as the slide on screen (no selector pairs #${id}:target with the dot that links to it, in the accent): a reader who has opened a slide has nothing on the page that says which of the three they are looking at`,
      )
    }
    for (const { part } of marked) {
      const named = /\[href\s*=\s*["']?\\?#([\w-]+)/.exec(part)
      if (!named || `#${named[1]}` !== href) {
        out.push(
          `the selector \`${part}\` marks a dot for #${id} but names ${named ? `#${named[1]}` : 'no dot at all'}: the pair of a dot and the panel it opens is the whole of what makes a dot switch a slide, and a selector that names one panel and another dot marks the wrong one as current`,
        )
      }
    }
    if (index === 0 && !parts.some(({ part, declarations }) => DOT.test(subject(part)) && /\[aria-current\s*=\s*["']?true/.test(part) && /accent/.test(declarations.get('background') ?? ''))) {
      out.push(
        'no rule marks the dot the script calls the current one (`aria-current="true"`): with the script driving, the block’s own name for the slide is what the mark is read from, and without the rule a reader who lets the block advance loses the dot that says where they are',
      )
    }
  }

  // 3. the target size, the wrap, the ring, and the phone.
  for (const width of [PHONE, 1280]) {
    for (const [what, pattern] of [
      ['dots', DOT],
      ['step controls', STEP],
    ]) {
      const height = lengthPx(declaredAt(css, width, pattern, 'min-height'), css)
      if (height === null || height < 44) {
        out.push(
          `at ${width} px the ${what} declare \`min-height: ${declaredAt(css, width, pattern, 'min-height') ?? '(nothing)'}\`, and the owner's brief asks a control for a 44 px target: ${what === 'dots' ? 'a dot is the whole of what a reader taps to open a slide' : 'a step control is the whole of what a reader taps to step one way'}`,
        )
      }
    }
  }
  for (const [what, pattern] of [
    ['dots', DOTS],
    ['control row', CONTROLS],
  ]) {
    if (declaredAt(css, PHONE, pattern, 'flex-wrap') !== 'wrap') {
      out.push(
        `the ${what} declare \`flex-wrap: ${declaredAt(css, PHONE, pattern, 'flex-wrap') ?? '(nothing)'}\` at ${PHONE} px, and three dots and two arrows on a phone are a row that wraps rather than one that runs off the side: a row that cannot wrap is a page the phone scrolls sideways`,
      )
    }
  }
  for (const rule of rules) {
    if (!DOT.test(rule.selector) && !STEP.test(rule.selector)) continue
    const declarations = declarationsOf(rule)
    for (const property of ['outline', 'outline-style', 'outline-width']) {
      const value = declarations.get(property)
      if (value !== undefined && /^(none|0)(px)?\b/.test(value) && !/:not\(\s*:focus-visible\s*\)/.test(rule.selector)) {
        out.push(
          `\`${rule.selector}\` removes the focus outline (\`${property}: ${value}\`), and a dot and a step control are controls the keyboard moves through: the ring is how a reader sees where they are, and a rule that takes it off the block's controls takes it off the only way the block is driven by hand`,
        )
      }
    }
  }
  const ring = rulesAt(css, 1280).some((rule) => /:focus-visible/.test(rule.selector) && /outline\s*:/.test(rule.body))
  if (!ring) {
    out.push('the page carries no `:focus-visible` rule drawing an outline, and the ring a keyboard reader follows through the dots is that rule: without it a dot the keyboard is on looks like every other dot')
  }
  const box = phoneBox(css)
  for (const rule of rulesAt(css, PHONE)) {
    if (!SLIDER_CLASSES.test(rule.selector)) continue
    const declarations = declarationsOf(rule)
    for (const property of ['width', 'min-width']) {
      const declared = declarations.get(property)
      if (declared === undefined) continue
      const px = lengthPx(declared, css)
      if (px !== null && px > box) {
        out.push(
          `\`${rule.selector}\` is \`${property}: ${declared}\` — ${Math.round(px)} px, and a ${PHONE} px phone's content box is ${Math.round(box)} px: a box wider than the phone is a page the phone scrolls sideways`,
        )
      }
    }
    if (declarations.get('white-space') === 'nowrap') {
      out.push(
        `\`${rule.selector}\` is \`white-space: nowrap\`, and the slider's own lines are prose and a row of controls: a line that cannot break is a line the phone cannot fit, and the wrap is what keeps the block inside the page`,
      )
    }
  }

  // A selector list is not forgiving. One selector a browser cannot read makes the whole rule
  // invalid, so a rule carrying a `:has()` selector beside one that has none is a rule that
  // hides the panel a dot opens — and the panel the script's own name points at — from every
  // browser too old to answer `:has()`. The block has exactly one selector that asks for it, the
  // one that shows the first panel while the fragment names none, and the component keeps it
  // inside an `@supports selector(:has(*))` rule of its own for this reason. Writing it as a
  // sibling of the rule above is not enough: the build merges adjacent rules that carry the same
  // declaration, and `display: block` is what both of them say — the built stylesheet put all
  // five selectors back into one rule when that was tried. This reads the artifact, so it holds
  // whichever way the component is written.
  for (const rule of cssRules(css)) {
    if (rule.selector === undefined) continue
    const parts = selectorParts(rule.selector)
    const asking = parts.filter((part) => /:has\(/.test(part))
    if (asking.length === 0 || asking.length === parts.length) continue
    out.push(
      `\`${rule.selector}\` carries ${asking.length} selector(s) asking for \`:has()\` beside ${parts.length - asking.length} that do not, and a selector list is not forgiving: a browser too old to answer \`:has()\` drops the whole rule, and every panel those other selectors show goes with it. The \`:has()\` selector belongs in an \`@supports selector(:has(*))\` rule of its own`,
    )
  }

  return out
}

/**
 * The three steps, as the page a reader receives carries them: each one's line held to the
 * section of the document it cites, in that section's order. Two of the three quote README.md's
 * `## Why Fugaro`, so a source alone would let them be shuffled with each still a quotation.
 */
export function stepFailures(html) {
  const out = []
  const doc = parse(html)
  const steps = findAll(doc, (el) => HAS(el, 'easy-step'))
  if (steps.length !== 3) {
    out.push(`the slider's first panel carries ${steps.length} steps, and the draft asks for three: you tell your agent, Fugaro runs it in your cloud, you get a draft pull request`)
    return out
  }
  const cursors = new Map()
  for (const [index, step] of steps.entries()) {
    const source = step.attrs.get('data-source') ?? ''
    const line = textOf(find(step, 'p') ?? step).trim()
    const number = textOf(find(step, 'span') ?? step).trim()
    const name = find(step, 'h3')
    if (number !== String(index + 1)) {
      out.push(`step ${index + 1} is numbered ${JSON.stringify(number)}, and the three steps are a numbered list in the draft's order`)
    }
    if (!name) {
      out.push(`step ${index + 1} has no name: a step is a name and the repository's line about it, and the name is the heading a reader scans`)
    }
    const icon = find(step, 'svg')
    if (!icon || icon.attrs.get('stroke') !== 'currentColor' || icon.attrs.get('aria-hidden') !== 'true') {
      out.push(
        `step ${index + 1}'s icon is ${icon ? `an <svg> with stroke=${JSON.stringify(icon.attrs.get('stroke'))} and aria-hidden=${JSON.stringify(icon.attrs.get('aria-hidden'))}` : 'missing'}: every step is drawn with an inline glyph in \`currentColor\`, hidden from a screen reader because the step's name and line carry the meaning`,
      )
    } else if (walk(icon).next().done) {
      out.push(`step ${index + 1}'s icon holds no shape, so the step renders an empty <svg> nothing can see`)
    }
    const at = /^([^#]+)#(.+)$/.exec(source)
    if (!at) {
      out.push(`step ${index + 1} cites ${JSON.stringify(source)}, which names no section of a document of this repository`)
      continue
    }
    const document = comparable(section(at[1], at[2]))
    const quoted = comparable(line)
    const from = cursors.get(source) ?? 0
    const found = document.indexOf(quoted, from)
    if (found === -1) {
      out.push(
        `step ${index + 1}'s line ${JSON.stringify(line)} is not a line of ${source} at or after the step before it: every line under a step is the repository's own words in the order its document writes them, and a paraphrase — or a line that moved to the step above — is the page inventing what the product says`,
      )
      continue
    }
    cursors.set(source, found + quoted.length)
  }
  return out
}

/** The built homepage's stylesheet and markup, as the readings above take them. */
function built() {
  const page = homepage()
  return { html: page.html, css: pageCss(page.html) }
}

test('the slider is three slides and three panels, all of them in the HTML a reader receives', () => {
  const { html, css } = built()
  assert.deepEqual(sliderFailures(css, html), [], `the homepage's slider is not the block the draft asks for:\n  ${sliderFailures(css, html).join('\n  ')}`)
})

test('the site’s one script is joined to the block by its data attributes and by nothing else', () => {
  const { html } = built()
  const doc = parse(html)
  const block = findAll(doc, (el) => HAS(el, 'home-slider'))[0]
  const names = [...new Set(findAll(block, (el) => el.attrs.has('id')).map((el) => el.attrs.get('id')))]
  assert.ok(names.length >= 3, `the block carries ${names.length} ids and this case holds the script to each of them`)
  for (const name of names) {
    assert.ok(
      !SCRIPT_SOURCE.includes(name),
      `website/public/scripts/site.js names ${name}, and the block is joined to the script by its \`data-\` attributes: a script keyed to this block's ids is a script that has to be edited when the block is`,
    )
  }
  for (const klass of ['home-slider', 'slider-dot', 'slider-step']) {
    // The name as a *class*: in a selector (`.slider-dot`) or in a quoted `classList` argument.
    // The block's own `data-` attribute names carry the same words (`[data-slider-dot]`), and
    // reading those is exactly how the two files find each other.
    const asAClass = [`.${klass}`, `'${klass}'`, `"${klass}"`]
    assert.ok(
      !asAClass.some((written) => SCRIPT_SOURCE.includes(written)),
      `website/public/scripts/site.js names the class ${klass}, and the block is joined to the script by its \`data-\` attributes: a class is a drawing, and a script that reads one is a script that stops working the day the drawing is renamed`,
    )
  }
  for (const hook of ['[data-slider]', '[data-slider-slide]', '[data-slider-dot]', '[data-slider-prev]', '[data-slider-next]']) {
    assert.ok(
      SCRIPT_SOURCE.includes(hook),
      `website/public/scripts/site.js never reads ${hook}, and that attribute is how the block and the script find each other: the clock, the two step controls and the mark on the current dot are all bound to it`,
    )
  }
  assert.ok(
    SCRIPT_SOURCE.includes('(prefers-reduced-motion: reduce)'),
    'website/public/scripts/site.js never names the reduced-motion query, and the owner asked the block to stop moving on its own for a reader who has asked their system for less motion',
  )
  assert.ok(
    SCRIPT_SOURCE.includes('6000'),
    'website/public/scripts/site.js names no interval for the clock, and the block is asked to advance every few seconds',
  )
})

/**
 * The hook reads in `site.js` that are not `own()`'s: every `querySelector(All)` of a slider hook
 * written on something other than the document (the door every `[data-slider]` block is found
 * through) or the track (`SLIDER_TRACK` having resolved the block's own track already). A read
 * left on the block is the read the homepage's nested blocks make wrong: the "See it" panel
 * holds a slider of the block's own, whose slides, dots and arrows carry the same four hook names
 * the block's do, so `block.querySelector('[data-slider-prev]')` answers with whichever is
 * written first in the document and is right only by document order.
 */
function bareHookReads(source) {
  return [...source.matchAll(/([\w$]+)\.(querySelector|querySelectorAll)\((SLIDER_[A-Z]+)\)/g)]
    .map(([, receiver, method, hook]) => `${receiver}.${method}(${hook})`)
    .filter((read) => !/^(document|track)\./.test(read))
}

test('every slider hook is read through own(), so a nested slider cannot be handed the wrong elements', () => {
  const bare = bareHookReads(SCRIPT_SOURCE)
  assert.deepEqual(
    bare,
    [],
    `website/public/scripts/site.js reads ${bare.join(', ')} straight off a slider block: the homepage's block holds a slider of its own, whose elements carry the same hooks, and \`own(block, hook)\` is the rule this file's readings, the script's drivers and the case above all use`,
  )
  // The plant: the read the drivers carried before, in the one place it is visible — the block's
  // own Previous arrow, which today is the block's and would be the inner slider's the day the
  // two are written the other way round.
  const planted = SCRIPT_SOURCE.replace('    var prev = own(section, SLIDER_PREV)[0]\n', '    var prev = section.querySelector(SLIDER_PREV)\n')
  assert.notEqual(planted, SCRIPT_SOURCE, 'the planted edit changed nothing in the script, so this case proves nothing')
  assert.deepEqual(bareHookReads(planted), ['section.querySelector(SLIDER_PREV)'], 'a hook read straight off a block was not refused by this case')
})

test('the three steps are the repository’s own lines, in the order their sections write them', () => {
  const { html } = built()
  assert.deepEqual(stepFailures(html), [], `the slider's first panel is not the reader's three steps:\n  ${stepFailures(html).join('\n  ')}`)
})

// ---- the behaviour, run ---------------------------------------------------------------------
//
// The cases below run the file the site ships against the built homepage, in a stub DOM, and read
// what it does. That is the only way to hold the clock, the pauses and the two states of the
// address to anything: the stylesheet readings above say what the block looks like in each state,
// and these say which state it is put in and when.
//
// The stub is deliberately not a browser. It is the surface the slider path touches — elements
// with attributes and listeners, a clock, the page's visibility, the reader's system preference
// and the address — and it runs the whole file, so the menu and the consent banner load in it
// too. What it does NOT do is stand in for those two readings: `nav-hover.mjs` and `consent.mjs`
// are where the menu and the gate are read, and here the pointer query is answered "no" (the
// menu's hover behaviour is never bound) and the banner is left alone. Each case is planted
// below with the line it reads taken out of the script, so a case that cannot fail is caught.

/** The clock the block is asked for, and the two queries the file asks the browser. */
const CLOCK_MS = 6000
const CALM_QUERY = '(prefers-reduced-motion: reduce)'

/** The width a laid-out track reports in this stub: one number, because a slide is one track
 *  wide (`grid-auto-columns: 100%`) and every move the cases below make is a whole slide. */
const BOX = 1000

/** A built page as a document the site's one script can run in. */
function stubPage(html, { hash = '', hidden = false, calm = false, script = SCRIPT_SOURCE } = {}) {
  const root = parse(html)
  const wrapped = new Map()
  const timers = []
  const queries = []
  const media = []
  const hashWrites = []
  const windowListeners = new Map()
  const state = { activeElement: null }
  // Where each scroll box stands, by node: the track the shared slider drives is the one that
  // reads and writes this, and a case reads it back to see where a move landed.
  const scrolled = new Map()
  let shown = !hidden
  // The reader's system preference is not fixed: `setCalm` below is that reader turning reduced
  // motion on (or off) under a page that has already loaded, which a browser reports to the lists
  // subscribed to *that* query as a `change` — and the pointer's own list hears nothing, because
  // the pointer did not move.
  let reduced = calm

  // Compound selectors joined by descendant spaces, in a comma list: `tag`, `.class`,
  // `[attr]` — the selectors the site's script writes, and a refusal for anything else. The
  // refusal is the point: a selector this stub silently matched nothing with would make every
  // case below pass by doing nothing.
  function compound(node, part) {
    if (!/^[a-z]*(\.[\w-]+|\[[\w-]+\])*$/.test(part)) throw new Error(`the slider stub does not understand the selector part ${part}`)
    const tag = /^[a-z]+/.exec(part)?.[0]
    if (tag && node.name !== tag) return false
    const cls = (node.attrs.get('class') ?? '').split(/\s+/)
    for (const [, c] of part.matchAll(/\.([\w-]+)/g)) if (!cls.includes(c)) return false
    for (const [, a] of part.matchAll(/\[([\w-]+)\]/g)) if (!node.attrs.has(a)) return false
    return true
  }

  function matches(node, selector) {
    return selector.split(',').some((one) => {
      const parts = one.trim().split(/\s+/)
      if (!compound(node, parts[parts.length - 1])) return false
      let at = node.parent
      for (let i = parts.length - 2; i >= 0; i--) {
        while (at && at.name !== '#document' && !compound(at, parts[i])) at = at.parent
        if (!at || at.name === '#document') return false
        at = at.parent
      }
      return true
    })
  }

  const isPanel = (node) => (node.attrs.get('class') ?? '').split(/\s+/).includes('panel')

  /**
   * Whether the page lays a node out, which in this stub is what a `clientWidth` comes to. A
   * browser lays nothing out inside an element carrying `hidden`, and nothing out inside a panel
   * its block has not put on screen: the homepage's block shows one of its three panels at a
   * time — the name the script wrote on the block, or the fragment the reader arrived on, or the
   * first panel while the page says neither, which is the stylesheet's own three rules — and
   * everything inside the panels it is not showing reports a zero `clientWidth`.
   *
   * **That zero is the whole of what the "See it" panel means to the shared slider inside it**,
   * and the case below about a slider in a panel nobody has opened is written on it. It is a
   * stub's reading of a browser's layout, and it says so: what a real panel does to a real track
   * is the owner's own check of the page.
   */
  function laidOut(node) {
    for (let at = node; at && at.name !== '#document'; at = at.parent) {
      if (at.attrs.has('hidden')) return false
      if (!isPanel(at)) continue
      let block = null
      for (let up = at.parent; up && up.name !== '#document'; up = up.parent) {
        if (up.attrs.has('data-slider')) {
          block = up
          break
        }
      }
      if (!block) continue
      const panels = [...walk(block)].filter(isPanel)
      const active = block.attrs.get('data-active')
      const named = active ? at.attrs.get('data-slider-slide') === active : panels[0] === at || at.attrs.get('id') === hash.replace(/^#/, '')
      if (!named) return false
    }
    return true
  }

  /** One parsed element, with the DOM surface the site's script touches. */
  class Element {
    constructor(node) {
      this.node = node
      this.listeners = new Map()
    }
    get name() {
      return this.node.name
    }
    /** The box the page gives this element: the stub's one width while the page lays it out,
     *  and nothing at all while it does not. The script reads it off the track, which is where
     *  the difference between a slider a reader is looking at and one inside a panel they have
     *  not opened lives. */
    get clientWidth() {
      return laidOut(this.node) ? BOX : 0
    }
    /** Where this scroll box stands, and the move a `scrollTo` makes: the offset the script
     *  asked for, and the `scroll` event the box hears afterwards, which is what the script's
     *  own reading of a scroll position is bound to. */
    get scrollLeft() {
      return scrolled.get(this.node) ?? 0
    }
    set scrollLeft(value) {
      scrolled.set(this.node, Number(value))
    }
    scrollTo(init = {}) {
      this.scrollLeft = typeof init.left === 'number' ? init.left : 0
      this.dispatch('scroll', { target: this })
    }
    get attrs() {
      return this.node.attrs
    }
    getAttribute(name) {
      return this.attrs.has(name) ? this.attrs.get(name) : null
    }
    hasAttribute(name) {
      return this.attrs.has(name)
    }
    setAttribute(name, value) {
      this.attrs.set(name, String(value))
    }
    get classList() {
      const classes = (this.attrs.get('class') ?? '').split(/\s+/)
      return { contains: (name) => classes.includes(name) }
    }
    get hidden() {
      return this.attrs.has('hidden')
    }
    set hidden(value) {
      if (value) this.attrs.set('hidden', '')
      else this.attrs.delete('hidden')
    }
    /** A `<details>`'s own state. The menu's two levels are read in `nav-hover.mjs`; here it
     *  only has to be a property the file can set without throwing. */
    get open() {
      return this.attrs.has('open')
    }
    set open(value) {
      if (value) this.attrs.set('open', '')
      else this.attrs.delete('open')
    }
    get parentElement() {
      const parent = this.node.parent
      return parent && parent.name !== '#document' ? dom.wrap(parent) : null
    }
    contains(other) {
      for (let node = other?.node; node; node = node.parent) if (node === this.node) return true
      return false
    }
    matches(selector) {
      return matches(this.node, selector)
    }
    closest(selector) {
      for (let node = this.node; node && node.name !== '#document'; node = node.parent) {
        if (matches(node, selector)) return dom.wrap(node)
      }
      return null
    }
    querySelectorAll(selector) {
      return [...walk(this.node)].filter((node) => matches(node, selector)).map((node) => dom.wrap(node))
    }
    querySelector(selector) {
      return this.querySelectorAll(selector)[0] ?? null
    }
    getClientRects() {
      return [{}]
    }
    focus() {
      state.activeElement = this
      this.dispatch('focusin', { target: this, bubbles: true })
    }
    addEventListener(type, handler) {
      if (!this.listeners.has(type)) this.listeners.set(type, [])
      this.listeners.get(type).push(handler)
    }
    removeEventListener(type, handler) {
      const bound = this.listeners.get(type) ?? []
      const at = bound.indexOf(handler)
      if (at >= 0) bound.splice(at, 1)
    }
    /** Dispatch at this element, for its own listeners. A `pointerenter` does not bubble and
     *  a `focusin` does; every listener the cases below reach is on the element the event is
     *  dispatched at, so the two are the same here. */
    dispatch(type, init = {}) {
      const event = {
        type,
        defaultPrevented: false,
        preventDefault() {
          this.defaultPrevented = true
        },
        ...init,
      }
      if (event.target === undefined) event.target = this
      for (const handler of this.listeners.get(type) ?? []) handler.call(this, event)
      return event
    }
  }

  const dom = {
    wrap(node) {
      if (!wrapped.has(node)) wrapped.set(node, new Element(node))
      return wrapped.get(node)
    },
  }

  const document = dom.wrap(root)
  document.readyState = 'complete'
  document.createElement = (name) => ({ name, src: '', async: false })
  document.head = { appendChild: () => {} }
  document.getElementById = (id) => {
    const node = [...walk(root)].find((one) => one.attrs.get('id') === id)
    return node ? dom.wrap(node) : null
  }
  Object.defineProperty(document, 'activeElement', { get: () => state.activeElement, configurable: true })
  Object.defineProperty(document, 'hidden', { get: () => !shown, configurable: true })

  // The address, with its fragment readable and its writes recorded: the script has no business
  // moving a reader's page, and a case below is what says so.
  const location = {}
  Object.defineProperty(location, 'hash', { get: () => hash, set: (value) => hashWrites.push(value), configurable: true })

  const window = {
    location,
    // The reader's system preference and the pointer's capability. The pointer is answered "no"
    // on purpose: the menu's hover behaviour belongs to `nav-hover.mjs`, and a stub that bound
    // it here would be reading the menu with half a DOM.
    matchMedia(query) {
      queries.push(query)
      const listeners = []
      const list = {
        media: query,
        get matches() {
          return query === CALM_QUERY ? reduced : false
        },
        addEventListener(type, handler) {
          if (type === 'change') listeners.push(handler)
        },
        // The name a MediaQueryList had for the same subscription before `addEventListener`
        // reached it. A browser old enough to need this one still reports the change.
        addListener(handler) {
          listeners.push(handler)
        },
      }
      media.push({ query, list, listeners })
      return list
    },
    setTimeout(handler, ms) {
      const timer = { handler, ms, cancelled: false, kind: 'timeout' }
      timers.push(timer)
      return timer
    },
    clearTimeout(timer) {
      if (timer) timer.cancelled = true
    },
    // The shared slider's clock is an interval and the homepage's block's is a timeout, and the
    // readers below keep the two apart: `pending()` is the block's own clock and `beats()` is the
    // shared slider's, so neither is counted as the other's.
    setInterval(handler, ms) {
      const timer = { handler, ms, cancelled: false, kind: 'interval' }
      timers.push(timer)
      return timer
    },
    clearInterval(timer) {
      if (timer) timer.cancelled = true
    },
    localStorage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
    addEventListener(type, handler) {
      if (!windowListeners.has(type)) windowListeners.set(type, [])
      windowListeners.get(type).push(handler)
    },
    dispatchEvent: () => true,
  }

  class StubEvent {
    constructor(type, init = {}) {
      this.type = type
      this.detail = init.detail
    }
  }

  vm.runInNewContext(script, { window, document, navigator: {}, CustomEvent: StubEvent })

  const block = document.querySelectorAll('[data-slider]')[0] ?? null
  assert.ok(block, 'the built homepage carries no `[data-slider]` block for the script to find')
  // The block's OWN dots and slides. The "See it" panel holds a slider of its own — the console
  // page's component, whose slides and dots carry the same two hooks (D246) — so the block's
  // readings are taken with `ownOf`'s rule here too: a case that counted the inner block's four
  // dots as this block's would be reading a slider the script did not move.
  const own = (selector) => block.querySelectorAll(selector).filter((el) => el.closest('[data-slider]') === block)
  const dots = own('[data-slider-dot]')
  const slides = own('[data-slider-slide]')
  const back = own('[data-slider-prev]')[0] ?? null
  const on = own('[data-slider-next]')[0] ?? null
  // The block's clock, as distinct from the shared slider's: both are six seconds, and what a
  // case fires by hand is the one it is about.
  const pending = () => timers.filter((timer) => !timer.cancelled && timer.kind === 'timeout')

  // **The shared slider, as this page renders it: the console page's own component, in the
  // homepage's "See it" panel** (D246, D250). It is a second `[data-slider]` inside the block
  // above, and the cases below drive it as a reader does — its count, the mark on its dots, where
  // its track stands and its own clock — because it is the page's second slider and no other gate
  // runs it: `home.mjs` reads its markup statically, this file's cases above drive the block
  // around it, and `console-screens.mjs` drives the same component on the console page.
  const innerBlock = document.querySelectorAll('[data-slider]').find((el) => el !== block) ?? null
  const innerOwn = (selector) => (innerBlock ? innerBlock.querySelectorAll(selector).filter((el) => el.closest('[data-slider]') === innerBlock) : [])
  const innerTrack = innerOwn('[data-slider-track]')[0] ?? null
  const innerCount = innerOwn('[data-slider-counter]')[0] ?? null
  const innerDots = innerOwn('[data-slider-dot]')
  const inner = innerBlock && {
    block: innerBlock,
    track: innerTrack,
    count: innerCount,
    dots: innerDots,
    slides: innerTrack ? innerTrack.querySelectorAll('[data-slider-slide]') : [],
    back: innerOwn('[data-slider-prev]')[0] ?? null,
    on: innerOwn('[data-slider-next]')[0] ?? null,
    /** What the reader is told: the count the script writes, as `n / N`. */
    reading: () => (innerCount?.textContent ?? '').trim(),
    /** Which of the slides the dots mark as the one on screen, as their 1-based numbers. */
    marked: () => innerDots.map((dot, at) => (dot.getAttribute('aria-current') === 'true' ? at + 1 : null)).filter((at) => at !== null),
    /** Where the shared slider's track stands, in pixels. */
    at: () => innerTrack?.scrollLeft ?? null,
    /** Whether the shared slider's clock is running. */
    running: () => timers.filter((timer) => !timer.cancelled && timer.kind === 'interval').length,
    /** Six seconds of the shared slider's clock: one beat of the interval it asked for. */
    beat() {
      const timer = timers.find((one) => !one.cancelled && one.kind === 'interval' && one.ms === CLOCK_MS)
      assert.ok(timer, `the shared slider has no clock to run: ${timers.length} timer(s) have been asked for and none of them is its ${CLOCK_MS} ms interval`)
      timer.handler()
    },
  }

  return {
    block,
    dots,
    slides,
    back,
    on,
    inner,
    timers,
    queries,
    hashWrites,
    /** The slide the block is on, as the name the script wrote on it. */
    active: () => block.getAttribute('data-active'),
    /** The names of the dots the block marks as the slide on screen. */
    marked: () =>
      dots
        .filter((dot) => dot.getAttribute('aria-current') === 'true')
        .map((dot) => dot.getAttribute('data-slider-dot')),
    /** The block's clock, as the timeouts it has asked for and not cancelled. The shared
     *  slider's interval is a clock of its own and is not counted here. */
    pending,
    /** Six seconds passing. */
    tick() {
      const timer = pending().find((one) => one.ms === CLOCK_MS)
      assert.ok(timer, `the block has no clock to run: ${pending().length} of its timers are pending, and none of them is the ${CLOCK_MS} ms step`)
      timer.handler()
    },
    click: (element) => element.dispatch('click', { target: element }),
    key: (element, key) => element.dispatch('keydown', { target: element, key }),
    enter: () => block.dispatch('pointerenter', { target: block }),
    leave: () => block.dispatch('pointerleave', { target: block }),
    focusIn: (element) => block.dispatch('focusin', { target: element }),
    focusOut: (relatedTarget) => block.dispatch('focusout', { target: block, relatedTarget }),
    /** The reader coming back to the tab, which is what a browser reports as a change of the
     *  page's visibility. */
    reveal() {
      shown = true
      document.dispatch('visibilitychange', { type: 'visibilitychange' })
    },
    /** Every list the page asked the browser for, with what it subscribed to each one: a case
     *  below reads whether the block watched the reduced-motion answer at all. */
    media,
    /** The reader turning reduced motion on (or off) without reloading, which a browser reports
     *  to the lists subscribed to that query as a `change` carrying the new answer. */
    setCalm(value) {
      reduced = value
      for (const one of media) {
        if (one.query !== CALM_QUERY) continue
        for (const handler of one.listeners) handler({ type: 'change', media: one.query, matches: value })
      }
    },
  }
}

/**
 * The behaviour the block is read for, one case at a time: the page it runs in, what it must do,
 * and the line of the script the case exists for — which the plants below take out of the file
 * and hold the same case to.
 */
const CASES = [
  {
    name: 'the block opens on its first slide, with both step controls revealed',
    options: {},
    check(page) {
      assert.equal(page.active(), 'easy', `the block opened on ${JSON.stringify(page.active())} rather than the first slide: a reader who has chosen nothing is reading the first thing the block has to say`)
      assert.deepEqual(page.marked(), ['easy'], `the block opened on the first slide and marked ${JSON.stringify(page.marked())} as the dot on screen`)
      assert.equal(page.back.hidden, false, 'the previous-slide control is still `hidden`: the script reveals the two controls the markup emits hidden, and a control that is never revealed is a button no reader is shown')
      assert.equal(page.on.hidden, false, 'the next-slide control is still `hidden`: the script reveals the two controls the markup emits hidden')
      assert.ok(page.pending().some((timer) => timer.ms === CLOCK_MS), 'the block armed no clock: a slider that never moves on its own is the tab strip the owner asked to be rid of')
    },
    plant: ['    show(opening)\n', ''],
  },
  {
    name: 'the clock steps the block on, and wraps at the last slide',
    options: {},
    check(page) {
      page.tick()
      assert.equal(page.active(), 'benefits', 'six seconds passed and the block did not move on')
      assert.deepEqual(page.marked(), ['benefits'], 'the block moved on and the mark on the dots did not follow it: the dot that says where the reader is has to be the dot of the slide on screen')
      page.tick()
      assert.equal(page.active(), 'see', 'the block did not step on a second time')
      page.tick()
      assert.equal(page.active(), 'easy', 'the block did not wrap: a slider that stops at the last slide is a slider whose first slide a reader who waits never sees again')
    },
    plant: ['        step(1)\n', ''],
  },
  {
    name: 'the clock stops while the reader is in the block, and starts again when they leave',
    options: {},
    check(page) {
      page.enter()
      assert.equal(page.pending().length, 0, 'a pointer over the block left the clock running: the slide a reader is reading would move under them')
      page.leave()
      assert.ok(page.pending().some((timer) => timer.ms === CLOCK_MS), 'the clock did not start again when the pointer left: the block stops for good the first time a reader touches it')
      page.focusIn(page.dots[1])
      assert.equal(page.pending().length, 0, 'the keyboard’s focus inside the block left the clock running: a reader tabbing through the dots is reading them')
      page.focusOut(page.dots[0])
      assert.equal(page.pending().length, 0, 'a focus moving from one dot to the next was read as the reader leaving the block')
      page.focusOut(null)
      assert.ok(page.pending().some((timer) => timer.ms === CLOCK_MS), 'the clock did not start again when the focus left the block')
    },
    // The block's own `over = true` and not the console slider's, which is written `over = true;
    // settle()` on one line earlier in the file: a plant that matched that one would edit a
    // slider this case never touches, and the case would pass with its own line still standing.
    plant: ['      over = true\n', '      over = false\n'],
  },
  {
    name: 'the pointer’s hold and the focus’s hold are separate, and neither one lets go of the other',
    options: {},
    check(page) {
      page.enter()
      page.focusIn(page.dots[1])
      page.leave()
      assert.equal(page.pending().length, 0, 'the pointer leaving the block started the clock again while the keyboard’s focus was still inside it: a reader tabbing through the dots has the slide move under them the moment a resting pointer drifts off the block')
      page.enter()
      page.focusOut(null)
      assert.equal(page.pending().length, 0, 'the focus leaving the block started the clock again while the pointer was still over it: a reader reading with the pointer resting on the block has the slide move under them')
      page.leave()
      assert.ok(page.pending().some((timer) => timer.ms === CLOCK_MS), 'the clock did not start again once the reader had left the block both ways: a block that stops for good the first time a pointer crosses it is a block that stopped')
    },
    // The defect this case exists for: one flag for both readers, so the pointer's leaving clears
    // the focus's hold as well.
    plant: ['      over = false\n      arm()\n', '      over = false\n      inside = false\n      arm()\n'],
  },
  {
    name: 'a page nobody is shown runs no clock, and starts one when it is shown again',
    options: { hidden: true },
    check(page) {
      assert.equal(page.active(), 'easy', 'a page that is not being shown left the block with no slide: the slides are the page, and the block is on one of them whatever the browser is doing')
      assert.equal(page.pending().length, 0, 'a page that is not being shown armed the clock: the block would have moved on by the time the reader came back to it')
      page.reveal()
      assert.ok(page.pending().some((timer) => timer.ms === CLOCK_MS), 'the clock did not start when the page was shown again: a reader who comes back to the tab is a reader who wants the slider')
    },
    plant: ['over || inside || !pageShown() || calm()', 'over || inside || calm()'],
  },
  {
    name: 'a reader who asked for less motion gets no clock, and every control still works',
    options: { calm: true },
    check(page) {
      assert.ok(page.queries.includes(CALM_QUERY), `the block never asked the browser whether the reader had asked for less motion: it asked ${JSON.stringify(page.queries)}`)
      assert.equal(page.pending().length, 0, 'a reader who asked their system for less motion got a block that moves on its own')
      page.click(page.dots[2])
      assert.equal(page.active(), 'see', 'less motion took the dots with it: what the reader asked for is that nothing move on its own, not that the block stop working')
      page.click(page.on)
      assert.equal(page.active(), 'easy', 'less motion took the step controls with it')
    },
    plant: ['over || inside || !pageShown() || calm()', 'over || inside || !pageShown()'],
  },
  {
    name: 'a reader who turns less motion on without reloading gets no more of the clock',
    options: {},
    check(page) {
      assert.ok(
        page.media.some((one) => one.query === CALM_QUERY && one.listeners.length > 0),
        'the block never subscribed to the reduced-motion answer: a visitor can turn the setting on under a page that has already loaded, and a query read once and never watched only ever answers about the load',
      )
      assert.ok(page.pending().some((timer) => timer.ms === CLOCK_MS), 'the block armed no clock to stop: this case is about a running slider settling, and there is nothing running to settle')
      page.setCalm(true)
      assert.equal(page.pending().length, 0, 'the reader turned reduced motion on and the block kept its clock: the slide it was standing on moves on anyway, which is the one thing they asked it not to do')
      page.click(page.dots[2])
      assert.equal(page.active(), 'see', 'the block stopped working rather than stopping moving on its own')
      assert.equal(page.pending().length, 0, 'a reader who asked for less motion got the clock back the moment they chose a slide themselves')
      page.setCalm(false)
      assert.ok(page.pending().some((timer) => timer.ms === CLOCK_MS), 'the reader turned reduced motion off again and the block stayed still: a slider that never resumes is a slider that stopped')
    },
    // The subscription and not the reading of the answer: with the block never watching the
    // query, `calm()` still answers about the load and every other assertion in this case — the
    // clock it gets back when the reader chooses a slide themselves — still holds.
    // Both names of the subscription: the stub answers `addListener` as well as
    // `addEventListener`, so a plant that silenced only the first would leave the case standing.
    plant: [
      "      if (motion.addEventListener) motion.addEventListener('change', arm)\n      else if (motion.addListener) motion.addListener(arm)\n",
      "      if (false) motion.addEventListener('change', arm)\n      else if (false) motion.addListener(arm)\n",
    ],
  },
  {
    name: 'the dots, the two step controls and the arrow keys move the block, and the address is left alone',
    options: {},
    check(page) {
      const clicked = page.click(page.dots[1])
      assert.equal(page.active(), 'benefits', 'clicking the second dot did not show the second slide')
      assert.equal(clicked.defaultPrevented, true, 'the dot followed its link: with the script running, the block’s state is its own, and an address that kept the fragment would send the reader back to it on a reload')
      assert.deepEqual(page.hashWrites, [], `the script wrote to the address (${JSON.stringify(page.hashWrites)}): the fragment is the no-script state, and a script that moves it is a script that leaves the browser's own history behind it`)
      page.click(page.back)
      assert.equal(page.active(), 'easy', 'the previous-slide control did not step back')
      page.click(page.back)
      assert.equal(page.active(), 'see', 'the previous-slide control did not wrap from the first slide to the last')
      page.click(page.on)
      assert.equal(page.active(), 'easy', 'the next-slide control did not wrap from the last slide to the first')
      const right = page.key(page.dots[0], 'ArrowRight')
      assert.equal(page.active(), 'benefits', 'the right arrow key did not step the block on')
      assert.equal(right.defaultPrevented, true, 'the right arrow key was left to the browser: a control that steps a block is the one place the key means that')
      page.key(page.dots[0], 'ArrowLeft')
      assert.equal(page.active(), 'easy', 'the left arrow key did not step the block back')
      const other = page.key(page.dots[0], 'Tab')
      assert.equal(other.defaultPrevented, false, 'the block swallowed a key that is not one of its own: Tab has to reach the next control')
      assert.equal(page.active(), 'easy', 'a key that is not an arrow moved the block')
    },
    plant: ["          if (event && typeof event.preventDefault === 'function') event.preventDefault()\n", ''],
  },
  {
    name: 'a reader who arrives on a slide opens on that slide',
    options: { hash: '#home-panel-see' },
    check(page) {
      assert.equal(page.active(), 'see', 'a reader who arrived on the third slide was shown the first: the dots are links, and a link into the block is a link to the slide it names')
      assert.deepEqual(page.marked(), ['see'], 'the block opened on the third slide and marked another dot as the one on screen')
    },
    plant: ['      for (var h = 0; h < slides.length; h++) if (idOf(slides[h]) === hash) opening = slides[h]\n', ''],
  },
  {
    name: 'the shared slider in the block counts its own four slides, and the block around it is not moved by them',
    // The reader is on the "See it" panel: the whole of the shared slider's behaviour is read
    // here, in the one state where the panel the block renders it in is the panel on screen.
    options: { hash: '#home-panel-see' },
    check(page) {
      assert.ok(page.inner, 'the homepage renders one `[data-slider]` block and this case is about the second one — the console page’s slider inside the block’s "See it" panel (D246)')
      const inner = page.inner
      assert.equal(inner.slides.length, 4, `the shared slider renders ${inner.slides.length} slide(s); the panel is the four screenshots its data file lists (D251)`)
      assert.equal(inner.dots.length, 4, `the shared slider carries ${inner.dots.length} dot(s), and one per slide is what the console page's own slider carries`)
      assert.equal(inner.reading(), '1 / 4', `the shared slider opens reading ${JSON.stringify(inner.reading())}: the count is the track’s own position, and a reader who has moved nothing is on the first screenshot`)
      assert.deepEqual(inner.marked(), [1], `the shared slider opened on its first slide and marked ${JSON.stringify(inner.marked())} as the dot on screen`)
      assert.equal(inner.at(), 0, `the shared slider's track is at ${inner.at()}px and its first slide is at 0`)
      assert.equal(inner.running(), 1, `the shared slider runs ${inner.running()} clock(s) with nobody reading it, and the console page's slider advances every six seconds wherever it is rendered`)

      // Six seconds of this slider's own clock: the count, the mark and the track move together,
      // and the block around them — its own slide, its own dots — is left where it was. The two
      // clocks are separate timers on one page, and a driver that moved the other's slides would
      // be reading and writing state it does not own.
      inner.beat()
      assert.equal(inner.reading(), '2 / 4', `six seconds passed and the shared slider did not move on: the count reads ${JSON.stringify(inner.reading())}`)
      assert.deepEqual(inner.marked(), [2], 'the shared slider moved on and the mark on its dots did not follow')
      assert.equal(inner.at(), BOX, `the shared slider stands on its second slide and its track is at ${inner.at()}px of a ${BOX}px-wide box`)
      assert.equal(page.active(), 'see', `the shared slider moved and the block around it went to ${JSON.stringify(page.active())}: the two blocks are driven by one file and each moves only its own slides`)
      assert.deepEqual(page.marked(), ['see'], 'the shared slider moved and the block’s own dots were marked by it')

      // And the shared slider’s own controls, in the panel a reader has opened: the two arrows,
      // which step it and wrap, and a dot, whose anchor the script takes over.
      page.click(inner.on)
      assert.equal(inner.reading(), '3 / 4', 'the shared slider’s Next arrow did not step it on')
      page.click(inner.back)
      assert.equal(inner.reading(), '2 / 4', 'the shared slider’s Previous arrow did not step it back')
      const press = page.click(inner.dots[3])
      assert.equal(inner.reading(), '4 / 4', 'a press on the shared slider’s fourth dot did not show the fourth screenshot')
      assert.equal(press.defaultPrevented, true, 'a press on the shared slider’s dot followed its anchor: with the script running the press is the script’s own move, exactly as it is on the console page')
      assert.deepEqual(inner.marked(), [4], 'the shared slider is on its fourth slide and the mark on its dots says otherwise')
    },
    // The defect: two drivers on one block. The panel's slider is a `[data-slider]` of its own,
    // and the block around it is the case the guard above reads — with the guard gone the block's
    // own driver takes the inner block's four slides for panels, writes its own state on them and
    // marks all four of its dots as the slide on screen, which is the collision D246 exists for.
    plant: ['    if (own(block, SLIDER_TRACK).length > 0) return\n', ''],
  },
  {
    name: 'the shared slider stands still while the panel it is in is not the one on screen',
    // The page as it loads: the block opens on its first slide, so the "See it" panel — and with
    // it the whole of the shared slider — is `display: none`. A track the page lays out nowhere
    // reports no width and can be scrolled nowhere, and a reading that moved anyway would be a
    // count and a mark on a slide the reader is never shown.
    options: {},
    check(page) {
      const inner = page.inner
      assert.ok(inner, 'the homepage renders one `[data-slider]` block and this case is about the second one')
      assert.equal(page.active(), 'easy', 'the block did not open on its first slide, so the panel this case is about is the one on screen and there is nothing to hold still')
      assert.equal(inner.reading(), '1 / 4', `the shared slider opens reading ${JSON.stringify(inner.reading())}, and the panel it is in shows nothing until a reader opens it`)
      assert.deepEqual(inner.marked(), [1], 'the shared slider marked a dot other than its first as the slide on screen, and it is standing on its first')

      // Three beats of its clock with the panel shut: nothing moves, because nothing can. The
      // count and the mark are the track's, and the track has not gone anywhere.
      inner.beat()
      inner.beat()
      inner.beat()
      assert.equal(inner.reading(), '1 / 4', `the shared slider's clock advanced it inside a panel nobody has opened: the count reads ${JSON.stringify(inner.reading())} and the reader who opens the panel is shown the first screenshot`)
      assert.deepEqual(inner.marked(), [1], 'the shared slider marked another dot as the slide on screen while the track it is read from had not moved')
      assert.equal(inner.at(), 0, `the shared slider's track moved to ${inner.at()}px inside a panel the page is not laying out`)

      // The reader opens the panel, and the slider is a slider again: the next beat moves it, and
      // the reading it starts from is the one the track actually held.
      page.click(page.dots[2])
      assert.equal(page.active(), 'see', 'the block did not go to the "See it" panel when its dot was pressed')
      inner.beat()
      assert.equal(inner.reading(), '2 / 4', `the shared slider did not move once its panel was on screen: the count reads ${JSON.stringify(inner.reading())}, and a slider held still is a slider that stopped`)
      assert.equal(inner.at(), BOX, `the shared slider's track is at ${inner.at()}px and its second slide is at ${BOX}`)
      assert.deepEqual(inner.marked(), [2], 'the shared slider moved on once its panel was shown and the mark on its dots did not follow')
    },
    // The defect: a move count without a track that can make it. `clientWidth` is zero for a box
    // inside a `display: none` subtree, so the reading is taken from a slide the reader is never
    // shown, and the count and the dots disagree with the track from the first beat to the moment
    // the panel is opened.
    plant: ['      if (!(track.clientWidth || 0)) return\n', ''],
  },
]

for (const one of CASES) {
  test(one.name, () => {
    const { html } = built()
    one.check(stubPage(html, one.options))
  })
}

test('every behaviour case fails on the script with the line it reads taken out', () => {
  const { html } = built()
  for (const one of CASES) {
    const [from, to] = one.plant
    const planted = SCRIPT_SOURCE.replace(from, to)
    assert.notEqual(planted, SCRIPT_SOURCE, `${one.name}: the plant changed nothing in the script, so this case proves nothing`)
    assert.doesNotThrow(() => new vm.Script(planted), `${one.name}: the plant broke the script rather than the behaviour`)
    assert.throws(
      () => one.check(stubPage(html, { ...one.options, script: planted })),
      `${one.name}: the case passes against the script with the line it reads taken out, so it is a case that cannot fail`,
    )
  }
})

// ---- the plants -----------------------------------------------------------------------------
//
// Each case below is one of the refusals above, planted: the built page with one thing about it
// changed to the defect the sentence names, and the reading held to that sentence. A check that
// cannot fail is not a check, and the homepage as it ships is the case above.

test('every refusal the slider reading makes fires on the defect it names', () => {
  const { html, css } = built()
  const cases = [
    [
      'a homepage with no slider on it',
      css,
      html.replace('<section class="home-slider" data-slot="home-slider" data-slider ', '<section class="home-slides" data-slot="home-slider" data-slider '),
      /the homepage carries 0 sliders/,
    ],
    [
      'a block the script cannot find',
      css,
      html.replace('<section class="home-slider" data-slot="home-slider" data-slider ', '<section class="home-slider" data-slot="home-slider" '),
      /carries no `data-slider` attribute/,
    ],
    [
      'a slide that is not in the HTML',
      css,
      html.replace(/<div class="panel panel-see"[\s\S]*?<\/div><\/div><\/section>/, '</div></section>'),
      /carries 2 slides/,
    ],
    [
      'a dot that is not there',
      css,
      html.replace(/<li class="slider-dot-item"[^>]*><a class="slider-dot" href="#home-panel-see"[^>]*><\/a><\/li>/, ''),
      /carries 2 dots/,
    ],
    [
      'a panel whose id is not the slide it carries',
      css,
      html.replace('id="home-panel-benefits"', 'id="home-panel-why"'),
      /is named "benefits" and carries the id "home-panel-why"/,
    ],
    [
      'a dot named after another slide',
      css,
      html.replace('data-slider-dot="benefits"', 'data-slider-dot="see"'),
      /dot 2 carries "see" where the slide it opens is named "benefits"/,
    ],
    [
      'a dot whose href is not a fragment',
      css,
      html.replace('href="#home-panel-benefits"', 'href="home-panel-benefits"'),
      /a dot is a fragment link to the panel it shows/,
    ],
    [
      'a panel with nothing in it',
      css,
      html.replace(/<h2 class="panel-title"[^>]*>Easy to use<\/h2><ol class="easy-steps"[\s\S]*?<\/ol>/, ''),
      /carries no text, so the dot opens an empty box/,
    ],
    [
      'a step control that is not there',
      css,
      html.replace(/<button type="button" class="slider-step" data-slider-next[\s\S]*?<\/button>/, ''),
      /carries 0 next-slide controls/,
    ],
    [
      'a step control that is a link',
      css,
      html.replace(
        /<button type="button" class="slider-step" data-slider-prev([\s\S]*?)<\/button>/,
        '<a href="#home-panel-easy" class="slider-step" data-slider-prev$1</a>',
      ),
      /the previous-slide control is a <a> of type ""/,
    ],
    [
      'a step control with no name',
      css,
      html.replace(' aria-label="Previous slide"', ''),
      /the previous-slide control carries no name/,
    ],
    [
      'a dot the tab key skips',
      css,
      html.replace('<a class="slider-dot" href="#home-panel-easy"', '<a class="slider-dot" tabindex="-1" href="#home-panel-easy"'),
      /a dot carries tabindex="-1"/,
    ],
    [
      'a dot whose fragment names no element',
      css,
      html.replace('href="#home-panel-benefits"', 'href="#home-panel-benefts"'),
      /links to #home-panel-benefts where the slide beside it is #home-panel-benefits/,
    ],
    [
      'a dot that is not a link',
      css,
      html
        .replace('<a class="slider-dot" href="#home-panel-easy"', '<button type="button" class="slider-dot" href="#home-panel-easy"')
        .replace(/(<button type="button" class="slider-dot"[^>]*>)<\/a>/, '$1</button>'),
      /dot 1 is a <button>/,
    ],
    [
      'a panel whose heading is not the slide it is',
      css,
      html.replace('>See it</h2>', '>Watch it</h2>'),
      /is headed "Watch it" where its dot reads "See it"/,
    ],
    [
      'a dot with no name for a screen reader',
      css,
      html.replace(' aria-label="Slide 1 of 3: Easy to use"', ''),
      /dot 1 is announced as ""/,
    ],
    [
      'a step control emitted visible',
      css,
      html.replace(' aria-label="Next slide" hidden', ' aria-label="Next slide"'),
      /the next-slide control is not emitted `hidden`/,
    ],
    [
      'a script in the block',
      css,
      html.replace('<div class="slider-controls"', '<script>document.querySelectorAll("[data-slider-dot]")[0].click()</script><div class="slider-controls"'),
      /carries a script/,
    ],
    [
      'the roles of a scripted tab widget',
      css,
      html.replace('<a class="slider-dot" href="#home-panel-easy"', '<a class="slider-dot" role="tab" aria-selected="true" href="#home-panel-easy"'),
      /role="tab" \/ aria-selected/,
    ],
    [
      'a panel marked hidden in the markup',
      css,
      html.replace('<div class="panel panel-benefits" id="home-panel-benefits"', '<div class="panel panel-benefits" id="home-panel-benefits" hidden'),
      /is marked hidden in the markup/,
    ],
    [
      'no rule hiding the panels',
      css.replace(/(\.panel\[data-astro-cid-\w+\]\{display:)none/, '$1grid'),
      html,
      /no rule hides the panels/,
    ],
    [
      'no rule showing a targeted panel',
      css.replace(/,\s*\.home-slider\[data-astro-cid-\w+\]:not\(\[data-active\]\) \.panel\[data-astro-cid-\w+\]:target/, ''),
      html,
      /no rule shows a targeted panel/,
    ],
    [
      'a rule showing the panel the address names without standing down for the script',
      css.replace(/(:not\(\[data-active\]\)) (\.panel\[data-astro-cid-\w+\]:target)/, '$2'),
      html,
      /does not stand down while the script is driving/,
    ],
    [
      'no rule showing the slide the page opens on',
      // The panel's own `@supports` rule, named in full: the block has two of them, and taking
      // the first would edit the dot's mark and leave this rule standing.
      css.replace(
        /@supports selector\(:has\(\*\)\)\{\.home-slider\[data-astro-cid-\w+\]:not\(\[data-active\]\):not\(:has\(\.panel:target\)\) #home-panel-easy\[data-astro-cid-\w+\]\{display:block\}\}/,
        '',
      ),
      html,
      /no rule shows #home-panel-easy while no panel is targeted/,
    ],
    [
      'no rule showing the panel the script names',
      css.replace(/\.home-slider\[data-astro-cid-\w+\]\[data-active=see\] #home-panel-see\[data-astro-cid-\w+\],/, ''),
      html,
      /no rule shows #home-panel-see while the block is on the slide "see"/,
    ],
    [
      'a dot the stylesheet never marks as the slide on screen',
      css.replace(/,\s*\.home-slider\[data-astro-cid-\w+\]:not\(\[data-active\]\):has\(#home-panel-see:target\) \.slider-dot\[data-astro-cid-\w+\]\[href=\\#home-panel-see\]:before/, ''),
      html,
      /no rule marks the dot for #home-panel-see/,
    ],
    [
      'a rule marking one panel with another dot',
      css.replace(/(:has\(#home-panel-see:target\) \.slider-dot\[data-astro-cid-\w+\]\[href=\\#)home-panel-see\]/, '$1home-panel-easy]'),
      html,
      /marks a dot for #home-panel-see but names #home-panel-easy/,
    ],
    [
      'no rule marking the dot the script calls current',
      css.replace(/\.slider-dot\[data-astro-cid-\w+\]\[aria-current=true\]:before\{[^}]*\}/, ''),
      html,
      /no rule marks the dot the script calls the current one/,
    ],
    [
      'a dot below the 44 px target',
      css.replace(/(\.slider-dot\[data-astro-cid-\w+\]\{[^}]*?)min-height:44px/, '$1min-height:20px'),
      html,
      /at 1280 px the dots declare `min-height: 20px`/,
    ],
    [
      'a dot that is 44 px only on a phone',
      `${css.replace(/(\.slider-dot\[data-astro-cid-\w+\]\{[^}]*?)min-height:44px/, '$1min-height:20px')}
@media (width<=30rem){.slider-dot[data-astro-cid-d3mmljir]{min-height:44px}}`,
      html,
      /at 1280 px the dots declare `min-height: 20px`/,
    ],
    [
      'a step control below the 44 px target',
      css.replace(/(\.slider-step\[data-astro-cid-\w+\]\{[^}]*?)min-height:44px/, '$1min-height:20px'),
      html,
      /at 1280 px the step controls declare `min-height: 20px`/,
    ],
    [
      'a control row that cannot wrap',
      css.replace(/(\.slider-controls\[data-astro-cid-\w+\]\{[^}]*?)flex-wrap:wrap;/, '$1'),
      html,
      /the control row declare `flex-wrap: \(nothing\)`/,
    ],
    [
      'a row of dots that cannot wrap',
      css.replace(/(\.slider-dots\[data-astro-cid-\w+\]\{[^}]*?)flex-wrap:wrap;/, '$1'),
      html,
      /the dots declare `flex-wrap: \(nothing\)`/,
    ],
    [
      'a page with no focus ring at all',
      css.replace(':focus-visible{outline:var(--focus-ring-width) solid var(--accent);outline-offset:var(--focus-ring-offset)}', ''),
      html,
      /carries no `:focus-visible` rule drawing an outline/,
    ],
    [
      'a dot that removes the focus ring',
      css.replace(/(\.slider-dot\[data-astro-cid-\w+\]\{)/, '$1outline:none;'),
      html,
      /removes the focus outline/,
    ],
    [
      'a dot wider than the phone',
      `${css}\n.slider-dot[data-astro-cid-d3mmljir]{min-width:420px}`,
      html,
      /a box wider than the phone is a page the phone scrolls sideways/,
    ],
    [
      'a step’s line that cannot break',
      css.replace(/(\.step-line\[data-astro-cid-\w+\]\{)/, '$1white-space:nowrap;'),
      html,
      /a line that cannot break/,
    ],
    [
      'a `:has()` selector standing in a list beside selectors that have none',
      `${css}\n.home-slider[data-astro-cid-d3mmljir]:not([data-active]) .panel[data-astro-cid-d3mmljir]:target,.home-slider[data-astro-cid-d3mmljir]:not([data-active]):not(:has(.panel:target)) #home-panel-easy[data-astro-cid-d3mmljir]{display:block}`,
      html,
      /a browser too old to answer `:has\(\)` drops the whole rule/,
    ],
  ]
  for (const [what, plantedCss, plantedHtml, expected] of cases) {
    assert.notEqual(plantedCss + plantedHtml, css + html, `${what}: the plant changed nothing, so this case proves nothing`)
    const failures = sliderFailures(plantedCss, plantedHtml)
    assert.ok(
      failures.some((line) => expected.test(line)),
      `${what}: the reading refused ${failures.length} thing(s) and none of them is this defect:\n  ${failures.join('\n  ') || '(nothing)'}`,
    )
  }
})

// The reading above is a reading *at* a width, and the one way it can stop being one is a media
// query it does not evaluate: every rule then holds at every width, and the width the reading was
// taken at decides nothing. Both directions are held here — a wide rule the phone meets is
// refused, the same rule behind a query the phone does not meet is not — because a reading that
// ignores the width fails the second and a reading that ignores media queries fails the first.
test('a wide box behind a query the phone does not meet is not a box the phone has to fit', () => {
  const { html, css } = built()
  const wide = '.easy-steps[data-astro-cid-d3mmljir]{min-width:900px}'
  const atPhone = `${css}\n${wide}`
  const abovePhone = `${css}\n@media (width>=48rem){${wide}}`
  assert.ok(
    sliderFailures(atPhone, html).some((line) => /a box wider than the phone/.test(line)),
    `a 900 px box declared at every width is not refused at ${PHONE} px, and the phone reading is what keeps the block inside a phone`,
  )
  assert.deepEqual(
    sliderFailures(abovePhone, html),
    [],
    `a 900 px box declared only above 48rem is refused at ${PHONE} px, and the phone never meets that query: a reading that refuses it is a reading the width argument does not reach`,
  )
})

test('every refusal the steps reading makes fires on the defect it names', () => {
  const { html } = built()
  const cases = [
    [
      'a step that is not there',
      html.replace(/<li class="easy-step" data-step="pull-request"[\s\S]*?<\/li>/, ''),
      /carries 2 steps/,
    ],
    [
      'a step’s line paraphrased',
      html.replace('It runs in a throwaway container in your own cloud project', 'It runs in a container that is thrown away in your cloud'),
      /is not a line of README.md#Why Fugaro/,
    ],
    [
      'two steps whose lines are the section’s own but out of its order',
      html
        .replace('It runs in a throwaway container in your own cloud project', '@@swapped@@')
        .replace('Every run that pushed ends in a pull request, a draft one if it failed', 'It runs in a throwaway container in your own cloud project')
        .replace('@@swapped@@', 'Every run that pushed ends in a pull request, a draft one if it failed'),
      /is not a line of README.md#Why Fugaro at or after the step before it/,
    ],
    [
      'a step with no icon',
      html.replace(/<svg class="step-icon"[\s\S]*?<\/svg>/, ''),
      /'s icon is missing/,
    ],
    [
      'a step with no name',
      html.replace(/<h3 class="step-name"[^>]*>Tell your agent<\/h3>/, ''),
      /step 1 has no name/,
    ],
    [
      'a step whose icon draws nothing',
      html.replace('<path d="M4 5h16v11H9l-5 4V5Z"/>', ''),
      /holds no shape, so the step renders an empty <svg> nothing can see/,
    ],
    [
      'a step citing no section of a document',
      html.replace('data-source="README.md#Getting started"', 'data-source="README.md"'),
      /which names no section of a document of this repository/,
    ],
    [
      'a step numbered by hand out of order',
      html.replace('<span class="step-number" aria-hidden="true" data-astro-cid-d3mmljir>2</span>', '<span class="step-number" aria-hidden="true" data-astro-cid-d3mmljir>3</span>'),
      /step 2 is numbered "3"/,
    ],
  ]
  for (const [what, plantedHtml, expected] of cases) {
    assert.notEqual(plantedHtml, html, `${what}: the plant changed nothing, so this case proves nothing`)
    const failures = stepFailures(plantedHtml)
    assert.ok(
      failures.some((line) => expected.test(line)),
      `${what}: the reading refused ${failures.length} thing(s) and none of them is this defect:\n  ${failures.join('\n  ') || '(nothing)'}`,
    )
  }
})
