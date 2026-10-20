// website/test/slider.mjs — the homepage's one slider (the owner's approved draft "Home, draft 2
// (slider)", 2026-10-19).
//
// The draft puts the whole of what the page has to show into one block with three tabs, and the
// one thing that shape can get wrong without anyone noticing is the thing that makes it a
// slider: a panel that is not in the HTML a reader receives, a tab that opens nothing, or a
// switch that only works once a script has run. A reader with no JavaScript is not a corner
// case on this site — `privacy.astro` states that the page asks them for nothing, and the copy
// control is the site's one script — so the tabs are read here as what they are: three fragment
// links and a stylesheet.
//
//   1. **Three tabs, three panels, all of them in the HTML.** The tabs are a list of three
//      links, each one's `href` is the fragment of the panel beside it, and each panel carries
//      the tab's own label as its heading. A tab whose `href` names an id no element carries is
//      a tab that opens nothing, and it is the defect the pairing below exists for.
//   2. **The panel the fragment names is the panel the stylesheet shows, per tab.** The
//      stylesheet hides every panel, shows the targeted one, shows the first one while nothing
//      is targeted, and marks the tab that belongs to each panel — read one tab at a time, so a
//      stylesheet that pairs the Why Fugaro? tab with the See it panel is refused rather than
//      counted as three pairs present.
//   3. **No script switches them.** Nothing in the block is a script, the site's one script
//      names none of the block's ids or classes, and the tabs carry no `role="tab"` /
//      `aria-selected` — the roles describe a widget whose selection is scripted, and this
//      selection is the address.
//   4. **A 44 px target that wraps, and the ring the site draws.** The tab is a link at least
//      44 px tall, the row wraps rather than scrolls, no rule on the tabs removes the focus
//      outline, the page carries the site's `:focus-visible` ring, and nothing in the block is
//      wider than a 360 px phone's content box or refuses to break a line.
//   5. **The three steps are the repository's own lines.** Each step's line is a line of the
//      section of the document it cites, in that section's order — the same reading the
//      component's own build guard takes, held here against the page a reader receives, so a
//      guard removed from the component is a red gate rather than a page that paraphrases.
//
// Every check below is a reading of `dist/index.html` and of the repository, and each one is
// the mechanical form of one of the rules above rather than a check on the component's own
// text: what a page says about itself is not evidence, and the artifact is what a reader gets.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { REPO, DIST, builtPages, cssDeclarations, cssRules, findAll, find, parse, textOf, walk } from './lib/html.mjs'

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
 */
function appliesAt(conditions, width) {
  for (const condition of conditions) {
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

/**
 * A selector with its functional pseudo-classes' arguments removed: what the selector's last
 * compound names, which is the element a rule draws. A panel named inside `:has(.panel:target)`
 * is a panel the rule *looks for*, not one it draws, and a reading that took the two for the
 * same would call a rule that marks a tab selected a rule that shows a panel.
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

const TAB = /\.tab(?![\w-])/
const TAB_BAR = /\.tab-bar(?![\w-])/
const PANEL = /\.panel(?![\w-])/

/**
 * The slider's own classes: everything the block's stylesheet draws, so the phone reading below
 * looks at the block rather than at the page.
 */
const SLIDER_CLASSES =
  /\.(home-slider|tab-bar|tab-item|tab|panels|panel|panel-title|easy-steps|easy-step|step-number|step-icon|step-name|step-line|watch-shot|watch-block|watch-command|watch-note)(?![\w-])/

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
 * Everything the slider can be wrong about, as sentences a reader of a red run can act on. The
 * plants at the foot of this file are each one of these refusals, and the homepage as it ships
 * is the case that passes.
 */
export function sliderFailures(css, html) {
  const out = []
  const doc = parse(html)
  const sliders = findAll(doc, (el) => HAS(el, 'home-slider'))
  if (sliders.length !== 1) {
    out.push(
      `the homepage carries ${sliders.length} sliders, and the draft asks for one block with three tabs: ${sliders.length === 0 ? 'nothing here is the block the tabs are read from' : 'a second one is a second set of panels with its own tabs'}`,
    )
    return out
  }
  const slider = sliders[0]

  // 1. the three tabs, the three panels, and the pairing between them.
  const tabs = findAll(slider, (el) => el.name === 'a' && HAS(el, 'tab'))
  const panels = findAll(slider, (el) => HAS(el, 'panel'))
  const LABELS = ['Easy to use', 'Why Fugaro?', 'See it']
  if (tabs.length !== 3) out.push(`the slider carries ${tabs.length} tabs, and the draft asks for three ("Easy to use", "Why Fugaro?", "See it")`)
  if (panels.length !== 3) out.push(`the slider carries ${panels.length} panels, and every tab has one: a panel that is not in the HTML is a tab that opens nothing`)
  if (tabs.length === 3 && panels.length === 3) {
    for (const [index, tab] of tabs.entries()) {
      const label = textOf(tab).trim()
      const href = tab.attrs.get('href') ?? ''
      const panel = panels[index]
      const id = panel.attrs.get('id') ?? ''
      if (label !== LABELS[index]) {
        out.push(`tab ${index + 1} reads ${JSON.stringify(label)} where the draft's third tab is ${JSON.stringify(LABELS[index])}: the three labels are "Easy to use", "Why Fugaro?" and "See it", in that order`)
      }
      if (!/^#home-panel-[\w-]+$/.test(href)) {
        out.push(`tab ${index + 1} (${JSON.stringify(label)}) links to ${JSON.stringify(href)}, and a tab is a fragment link to the panel it opens: without the fragment the stylesheet has nothing to key the panel's \`:target\` rule on`)
      } else if (href !== `#${id}`) {
        out.push(`tab ${index + 1} (${JSON.stringify(label)}) links to ${href} where the panel beside it is #${id}: a tab whose fragment names another element opens nothing, and the address is the whole of the switching`)
      }
      const headings = findAll(panel, 'h2')
      const heading = headings.length ? textOf(headings[0]).trim() : ''
      if (heading !== label) {
        out.push(`the panel tab ${index + 1} opens is headed ${JSON.stringify(heading)} where its tab reads ${JSON.stringify(label)}: a reader who arrives on a panel — by tab, by screen reader or in the HTML — reads the label of the tab that opens it`)
      }
      if (textOf(panel).trim() === '') {
        out.push(`the panel tab ${index + 1} (${JSON.stringify(label)}) opens carries no text, so the tab opens an empty box`)
      }
    }
  }

  // The block is one slider: the panels are siblings inside it, and nothing about them is built
  // at runtime. A `hidden` attribute or an `aria-hidden` panel is a panel the page ships and a
  // reader cannot reach.
  for (const panel of panels) {
    if (panel.attrs.has('hidden') || panel.attrs.get('aria-hidden') === 'true') {
      out.push(`the panel #${panel.attrs.get('id') ?? '(no id)'} is marked hidden in the markup, and every panel is one of the three a reader can open: the stylesheet is what hides the two that are not targeted, and it is what shows them again`)
    }
  }

  // 2. the stylesheet: hide them all, show the targeted one, show the first while none is
  // targeted, and mark one tab per panel.
  const rules = rulesAt(css, 1280)
  const hidden = rules.some(
    (rule) => PANEL.test(subject(rule.selector)) && !/:target/.test(rule.selector) && declarationsOf(rule).get('display') === 'none',
  )
  if (!hidden) {
    out.push('no rule hides the panels, and the block is a slider rather than three panels one under the other: the stylesheet shows the panel the address names and takes the other two out of the layout')
  }
  const shown = rules.some((rule) => {
    const display = declarationsOf(rule).get('display')
    return PANEL.test(subject(rule.selector)) && /:target/.test(subject(rule.selector)) && display !== undefined && display !== 'none'
  })
  if (!shown) {
    out.push('no rule shows a targeted panel, so a tab that is clicked (or reached by a fragment in the address) switches nothing: the panel the address names has to be the one the stylesheet shows')
  }
  if (tabs.length === 3) {
    const first = tabs[0].attrs.get('href') ?? ''
    const opening = rules.some(
      (rule) =>
        rule.selector.includes(first) &&
        /:not\(/.test(rule.selector) &&
        declarationsOf(rule).get('display') !== undefined &&
        declarationsOf(rule).get('display') !== 'none',
    )
    if (!opening) {
      out.push(`no rule shows ${first} while no panel is targeted, and the first panel is the one the page a reader arrives on has to be: with every panel hidden and no fragment in the address, the block opens empty`)
    }
    // A selector list is read one selector at a time, the way a browser reads it: the pair is
    // the tab and the panel *in one selector*, and a rule that carries three pairs in a list is
    // three pairs rather than one selector naming three tabs.
    const selectors = rules.flatMap((rule) =>
      rule.selector.split(',').map((part) => ({ part: part.trim(), declarations: declarationsOf(rule) })),
    )
    for (const tab of tabs) {
      const href = tab.attrs.get('href') ?? ''
      if (!href.startsWith('#')) continue
      const id = href.slice(1)
      const marked = selectors.filter(({ part, declarations }) => part.includes(`#${id}:target`) && declarations.size > 0)
      if (marked.length === 0) {
        out.push(`no rule marks the tab for ${href} as the selected one (no selector names #${id}:target and the tab beside it): the reader has no way to see which of the three panels they are looking at`)
        continue
      }
      for (const { part } of marked) {
        const names = /\.tab(?![\w-])(?:\[[^\]]*\])*\[href\s*=\s*["']?\\?#([\w-]+)/.exec(part)
        if (!names || `#${names[1]}` !== href) {
          out.push(
            `the selector \`${part}\` marks a tab for #${id} but names ${names ? `#${names[1]}` : 'no tab at all'}: the pair of a tab and the panel it opens is the whole of what makes a tab switch, and a selector that names one panel and another tab marks the wrong one selected`,
          )
        }
      }
    }
  }

  // 3. no script, and no scripted widget's roles.
  if (findAll(slider, (el) => el.name === 'script').length > 0) {
    out.push('the slider carries a script, and the tabs are the address and the stylesheet: a reader whose script never arrives is a reader with no tabs at all, which is the one thing this pattern exists to avoid')
  }
  for (const el of [...tabs, ...panels]) {
    const role = el.attrs.get('role')
    if (role === 'tab' || role === 'tabpanel' || el.attrs.has('aria-selected')) {
      out.push(
        `the element carrying ${JSON.stringify(el.attrs.get('class') ?? '')} declares role=${JSON.stringify(role)}${el.attrs.has('aria-selected') ? ' / aria-selected' : ''}, and those roles describe a widget whose selection is scripted: the selection here is the address's fragment, and a role promising \`aria-selected\` is a promise only a script can keep`,
      )
    }
  }
  for (const tab of tabs) {
    if (tab.attrs.get('tabindex') === '-1') {
      out.push(`a tab carries tabindex="-1", and every tab is a control a keyboard reaches: a tab the tab key skips is a panel the keyboard cannot open`)
    }
  }

  // 4. the target size, the wrap, the ring, and the phone.
  for (const width of [PHONE, 1280]) {
    const height = lengthPx(declaredAt(css, width, TAB, 'min-height'), css)
    if (height === null || height < 44) {
      out.push(
        `at ${width} px the tabs declare \`min-height: ${declaredAt(css, width, TAB, 'min-height') ?? '(nothing)'}\`, and the owner's brief asks a control for a 44 px target: a tab is the whole of what a reader touches to open a panel`,
      )
    }
  }
  if (declaredAt(css, PHONE, TAB_BAR, 'flex-wrap') !== 'wrap') {
    out.push(
      `the tab row declares \`flex-wrap: ${declaredAt(css, PHONE, TAB_BAR, 'flex-wrap') ?? '(nothing)'}\` at ${PHONE} px, and three tabs on a phone are a row that wraps rather than one that runs off the side: a row that cannot wrap is a page the phone scrolls sideways`,
    )
  }
  for (const rule of rules) {
    if (!TAB.test(rule.selector)) continue
    const declarations = declarationsOf(rule)
    for (const property of ['outline', 'outline-style', 'outline-width']) {
      const value = declarations.get(property)
      if (value !== undefined && /^(none|0)(px)?\b/.test(value) && !/:not\(\s*:focus-visible\s*\)/.test(rule.selector)) {
        out.push(
          `\`${rule.selector}\` removes the focus outline (\`${property}: ${value}\`), and a tab is a control the keyboard moves through: the ring is how a reader sees where they are, and a rule that takes it off the tabs takes it off the one control the block has`,
        )
      }
    }
  }
  const ring = rulesAt(css, 1280).some((rule) => /:focus-visible/.test(rule.selector) && /outline\s*:/.test(rule.body))
  if (!ring) {
    out.push('the page carries no `:focus-visible` rule drawing an outline, and the ring a keyboard reader follows through the tabs is that rule: without it a tab the keyboard is on looks like every other tab')
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
        `\`${rule.selector}\` is \`white-space: nowrap\`, and the slider's own lines are prose and a row of tabs: a line that cannot break is a line the phone cannot fit, and the wrap is what keeps the block inside the page`,
      )
    }
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

test('the slider is three tabs and three panels, all of them in the HTML a reader receives', () => {
  const { html, css } = built()
  assert.deepEqual(sliderFailures(css, html), [], `the homepage's slider is not the block the draft asks for:\n  ${sliderFailures(css, html).join('\n  ')}`)
})

test('no script switches the tabs, and the site’s one script does not know they exist', () => {
  const { html } = built()
  const script = readFileSync(path.join(REPO, 'website/public/scripts/site.js'), 'utf8')
  const doc = parse(html)
  const slider = findAll(doc, (el) => HAS(el, 'home-slider'))[0]
  const names = [...new Set(findAll(slider, (el) => el.attrs.has('id')).map((el) => el.attrs.get('id')))]
  assert.ok(names.length >= 3, `the slider carries ${names.length} ids and this case holds the script to each of them`)
  for (const name of names) {
    assert.ok(!script.includes(name), `website/public/scripts/site.js names ${name}, and the tabs are the address and the stylesheet: a slider whose state the script sets is a slider a reader without it does not have`)
  }
  for (const klass of ['home-slider', 'tab-bar', 'easy-step']) {
    assert.ok(!script.includes(klass), `website/public/scripts/site.js names ${klass}, and nothing in this block is scripted: the switching is the fragment and the stylesheet`)
  }
})

test('the three steps are the repository’s own lines, in the order their sections write them', () => {
  const { html } = built()
  assert.deepEqual(stepFailures(html), [], `the slider's first panel is not the reader's three steps:\n  ${stepFailures(html).join('\n  ')}`)
})

// ---- the plants ---------------------------------------------------------------------------
//
// Each case below is one of the refusals above, planted: the built page with one thing about it
// changed to the defect the sentence names, and the reading held to that sentence. A check that
// cannot fail is not a check, and the homepage as it ships is the case above.

test('every refusal the slider reading makes fires on the defect it names', () => {
  const { html, css } = built()
  const cases = [
    [
      'a panel that is not in the HTML',
      css,
      html.replace(/<div class="panel panel-see"[\s\S]*?<\/div><\/div><\/section>/, '</div></section>'),
      /carries 2 panels/,
    ],
    [
      'a tab whose fragment names no element',
      css,
      html.replace('href="#home-panel-benefits"', 'href="#home-panel-benefts"'),
      /links to #home-panel-benefts where the panel beside it is #home-panel-benefits/,
    ],
    [
      'a panel whose heading is not the tab that opens it',
      css,
      html.replace('<h2 class="panel-title" data-astro-cid-d3mmljir>See it</h2>', '<h2 class="panel-title" data-astro-cid-d3mmljir>Watch it</h2>'),
      /is headed "Watch it" where its tab reads "See it"/,
    ],
    [
      'a script in the block',
      css,
      html.replace('<ul class="tab-bar"', '<script>document.querySelectorAll(".tab")[0].click()</script><ul class="tab-bar"'),
      /carries a script/,
    ],
    [
      'the roles of a scripted tab widget',
      css,
      html.replace('<a class="tab" href="#home-panel-easy"', '<a class="tab" role="tab" aria-selected="true" href="#home-panel-easy"'),
      /role="tab" \/ aria-selected/,
    ],
    [
      'no rule hiding the panels',
      css.replace(/\.panel\[data-astro-cid-\w+\]\{display:none\}/, '.panel[data-astro-cid-d3mmljir]{display:grid}'),
      html,
      /no rule hides the panels/,
    ],
    [
      'no rule showing a targeted panel',
      css.replace(/\.panel\[data-astro-cid-\w+\]:target,/, ''),
      html,
      /no rule shows a targeted panel/,
    ],
    [
      'no rule showing the panel the page opens on',
      css.replace(/,\s*\.home-slider\[data-astro-cid-\w+\]:not\(:has\(\.panel:target\)\) #home-panel-easy\[data-astro-cid-\w+\]/, ''),
      html,
      /no rule shows #home-panel-easy while no panel is targeted/,
    ],
    [
      'a tab the stylesheet never marks as selected',
      css.replace(/,\s*\.home-slider\[data-astro-cid-\w+\]:has\(#home-panel-see:target\) \.tab\[data-astro-cid-\w+\]\[href=\\#home-panel-see\]/, ''),
      html,
      /no rule marks the tab for #home-panel-see/,
    ],
    [
      'a rule pairing one panel with another tab',
      css.replace(/(:has\(#home-panel-see:target\) \.tab\[data-astro-cid-\w+\]\[href=\\#)home-panel-see\]/, '$1home-panel-easy]'),
      html,
      /marks a tab for #home-panel-see but names #home-panel-easy/,
    ],
    [
      'a tab below the 44 px target',
      css.replace(/(\.tab\[data-astro-cid-\w+\]\{[^}]*?)min-height:44px/, '$1min-height:20px'),
      html,
      /the owner's brief asks a control for a 44 px target/,
    ],
    [
      'a tab that is 44 px only on a phone',
      `${css.replace(/(\.tab\[data-astro-cid-\w+\]\{[^}]*?)min-height:44px/, '$1min-height:20px')}
@media (width<=30rem){.tab[data-astro-cid-d3mmljir]{min-height:44px}}`,
      html,
      /at 1280 px the tabs declare `min-height: 20px`/,
    ],
    [
      'a tab row that cannot wrap',
      css.replace(/(\.tab-bar\[data-astro-cid-\w+\]\{[^}]*?)flex-wrap:wrap;/, '$1'),
      html,
      /a row that cannot wrap is a page the phone scrolls sideways/,
    ],
    [
      'a tab that removes the focus ring',
      css.replace(/(\.tab\[data-astro-cid-\w+\]\{)/, '$1outline:none;'),
      html,
      /removes the focus outline/,
    ],
    [
      'a panel wider than the phone',
      css.replace(/(\.panel\[data-astro-cid-\w+\]\{display:none)/, '$1;min-width:420px'),
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
      'a panel marked hidden in the markup',
      css,
      html.replace('<div class="panel panel-benefits" id="home-panel-benefits"', '<div class="panel panel-benefits" id="home-panel-benefits" hidden'),
      /is marked hidden in the markup/,
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
