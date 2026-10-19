// website/test/spacing.mjs — the site's words are separated by spaces (round 3 of the
// security page review, 2026-10-19).
//
// **The rule: no built page runs two words together.** Astro compresses a page's HTML as it
// writes it, and the compression drops a text node that is nothing but whitespace. A page
// whose source put an inline element at the start of a line therefore loses the space before
// it, and a reader gets `back andfugaro secrets` where the page meant `back and` + `fugaro
// secrets`. The source reads correctly, so does the built file to anyone skimming it, and only
// the reader sees the defect — which is the class of defect a gate has to hold, because the
// next person to write a sentence across two lines will make it again.
//
// **Why this reads the artifact the way a browser does, and not the way a checker usually
// does.** `security.mjs` read a node's children with `children.map(text).join(' ')` — it put a
// space between two neighbours to keep their words apart, which is precisely the space the
// build had removed. A reader that repairs the artifact cannot see damage to it. So the rule
// here — `test/lib/html.mjs`'s `runTogether()`, which this file and `security.mjs` share —
// reads each direct text child as its own run, invents nothing between two of them, and takes
// a run's last letter followed by the next run's first letter as one word.
//
// **What counts as a boundary a reader sees.** A block element ends a run of words, and so
// does anything the page's own stylesheet lays out as a block, and so does anything inside a
// container the stylesheet lays its children out as flex or grid — a flex or grid container
// blockifies its children, so they are separate boxes with a gap between them even where the
// source writes no space. The docs hub's `.path` is the first kind: a `<code>` holding
// `docs/gcp-setup.md` under a guide's title, which the hub's own stylesheet gives
// `display: block` and a reader therefore sees on its own line rather than glued to the title
// above it. The homepage's hero action row is the second: `.hero-actions` is `display: flex`
// with a gap, so its two links are laid out apart and "started" beside "How" is not a seam a
// reader reads. This file reads each page's inlined stylesheet and hands both answers to the
// shared reader. Without them the gate would report the design's own layouts as failures, and
// a gate that cries wolf on the design is a gate somebody turns off. Neither exemption is a
// list of names kept here: a class gains it by what the artifact does with it, and the cases
// below prove both halves — a seam the stylesheet lays apart is not refused, and the same seam
// is refused the moment the stylesheet lays it out inline.
//
// This is the rule that found the console download row: `<a>macOS .dmg</a>` and the published
// checksum `<code>` beside it had no space between them and no rule that lays them apart, so
// the page read "macOS .dmga434f3…". That is the defect this gate exists for, on a page this
// change does not otherwise touch, and the space is in the component now.
//
// The rule is deliberately about letters, in either case: a seam is refused when a letter sits
// directly against a letter, which is what a lost space inside a sentence looks like
// (`andfugaro`, `read-onlyfugaro`, `thePrivacy`). A digit against a word (`1Start`, the
// numbered step marker beside its title) and punctuation at the boundary (`built with
// <code>go</code>.`) are the page's own choices and are not refused. Round 5 of the security
// page review found the earlier rule was lowercase-only on both sides, which missed a lost
// space before a capitalised element — and the reason it was narrowed that way was not safety
// but the boundary model above: the hero row's capitalised links would have been false
// positives. Reading the flex and grid parents is what lets the rule be about letters rather
// than about lower case.
//
// A gate whose refusal has never been seen to fire is a gate nobody knows the shape of, so the
// last case plants the defect in a page of its own and proves the failure arrives naming the
// page and the word a reader reads. Standard library only; no network (Global Constraint 4).

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { DIST, BLOCK, builtPages, cssDeclarations, cssRules, findAll, parse, runTogether } from './lib/html.mjs'

/** The `display` values that take an element out of the run of words beside it. */
const BLOCK_DISPLAY = /^(block|flow-root|flex|grid|list-item|table|table-row|table-cell)$/

/**
 * The `display` values that make an element's own children block-level boxes: a flex or grid
 * container blockifies its children, so two of them are laid out as separate boxes and a
 * reader sees a boundary between them even where the source writes no space — the hero's row
 * of two links is the site's own case.
 */
const ITEM_DISPLAY = /^(flex|inline-flex|grid|inline-grid)$/

/** The classes a selector names, whatever else it carries (`[data-astro-cid-x]`, a tag, `>`). */
function classNames(selector) {
  return [...selector.matchAll(/\.([A-Za-z_][-\w]*)/g)].map((match) => match[1])
}

/**
 * The `display` each class is given by a page's own inlined stylesheet. Astro inlines every
 * stylesheet a page uses into its `<style>` blocks, so this is the whole of the CSS that page
 * carries. A later rule wins, as it does in a browser, for the class it names.
 */
function displaysOf(doc) {
  const displays = new Map()
  for (const style of findAll(doc, 'style')) {
    for (const rule of cssRules(style.raw ?? '')) {
      const display = cssDeclarations(rule.body).find(([property]) => property === 'display')?.[1] ?? ''
      for (const name of classNames(rule.selector)) displays.set(name, display)
    }
  }
  return displays
}

/**
 * What the page itself says a reader sees a boundary: an element the page lays out as a block,
 * and an element whose **parent** lays its children out as flex or grid — a flex or grid
 * container blockifies its children, so they are separate boxes whether or not the source wrote
 * a space between them. Both halves are read from the page's own stylesheet, so a class gains
 * the exemption by what the artifact does with it rather than by being named here.
 *
 * The parent half is what lets the rule below be about letters rather than about lower case
 * (round 5 of the security page review): the hero's action row is `display: flex` with a gap,
 * so its two links are a boundary a reader sees — a rule that did not know that would report
 * "startedHow" on the homepage, and narrowing the rule to `a`–`z` on both sides was hiding the
 * hole rather than closing it.
 */
function boundaryOf(doc) {
  const displays = displaysOf(doc)
  const classesOf = (el) => (el.attrs?.get('class') ?? '').split(/\s+/).filter(Boolean)
  const lays = (el, re) => classesOf(el).some((name) => re.test(displays.get(name) ?? ''))
  return (el) => {
    if (BLOCK.has(el.name)) return true
    if (lays(el, BLOCK_DISPLAY)) return true
    return el.parent !== undefined && el.parent.attrs !== undefined && lays(el.parent, ITEM_DISPLAY)
  }
}

/**
 * Every place a built page runs two words together, as the sentences a reader of the failure
 * gets: the page, the word a reader reads, and the two runs it was made of.
 */
export function spacingFailures(dist = DIST) {
  const out = []
  for (const page of builtPages(dist)) {
    let doc
    try {
      doc = parse(page.html)
    } catch (err) {
      out.push(`${page.rel}: the page could not be read as HTML, so its words were not checked: ${err.message}`)
      continue
    }
    for (const glued of runTogether(doc, boundaryOf(doc))) {
      out.push(
        `${page.url}: two words run together — the page renders "${glued.glued}", where ${JSON.stringify(glued.before)} is followed by ${JSON.stringify(glued.after)} with no space between them. Astro's HTML compression drops a whitespace-only text node, so an inline element written at the start of a source line sits flush against the word before it; the space belongs inside the line that carries it (or as an entity), not between two lines (round 3 of the security page review).`,
      )
    }
  }
  return out
}

/** A temp dist holding one page, and the gate's verdict on it. */
function verdict(t, html) {
  const dist = mkdtempSync(path.join(tmpdir(), 'fugaro-spacing-'))
  t.after(() => rmSync(dist, { recursive: true, force: true }))
  mkdirSync(path.join(dist, 'planted'), { recursive: true })
  writeFileSync(path.join(dist, 'planted', 'index.html'), html)
  return spacingFailures(dist)
}

// ---------------------------------------------------------------------------------------

test('no built page runs two words together', () => {
  const failures = spacingFailures(DIST)
  assert.deepEqual(failures, [], `a built page runs two words together:\n  ${failures.join('\n  ')}`)
})

test('the rule refuses a run-together word, and only where a reader sees one', (t) => {
  // The defect itself: text, then an inline element flush against it.
  const glued = verdict(t, '<!doctype html><html><body><p>back and<code>fugaro secrets</code></p></body></html>')
  assert.equal(glued.length, 1, `the gate saw ${glued.length} glued words in a page that carries exactly one: ${glued.join(' | ')}`)
  assert.match(glued[0], /\/planted\//, 'the failure does not name the page it read')
  assert.match(glued[0], /"andfugaro"/, 'the failure does not name the word a reader reads')
  assert.match(glued[0], /"back and"/, 'the failure does not show the run the space is missing from')

  // The mirror: an inline element's own text followed by the word after it.
  const trailing = verdict(t, '<!doctype html><html><body><p><strong>read-only</strong>fugaro commands</p></body></html>')
  assert.equal(trailing.length, 1, `the gate saw ${trailing.length} glued words in a page that carries one: ${trailing.join(' | ')}`)
  assert.match(trailing[0], /"onlyfugaro"/, 'the failure does not name the word a reader reads')

  // The case the lowercase-only rule missed: a lost space before a **capitalised** inline
  // element reads as one word just the same, and is refused the same way.
  const capitalised = verdict(t, '<!doctype html><html><body><p>see the<a href="/privacy/">Privacy</a> page</p></body></html>')
  assert.equal(capitalised.length, 1, `the gate saw ${capitalised.length} glued words in a page that carries one: ${capitalised.join(' | ')}`)
  assert.match(capitalised[0], /"thePrivacy"/, 'the gate does not refuse a lost space before a capitalised element, so the rule is narrower than the defect it exists for')

  // The shapes the rule must stay quiet about, because a gate that refuses these would be
  // refusing the site it is meant to hold: a space that is there, punctuation at the
  // boundary, a block element between the two runs, and a digit against a word.
  for (const [what, html] of [
    ['a space between the two runs', '<p>back and <code>fugaro secrets</code></p>'],
    ['punctuation at the boundary', '<p>built with <code>go</code>. Then a sentence.</p>'],
    ['two paragraphs', '<p>one</p><p>two</p>'],
    ['two list items', '<ul><li>one</li><li>two</li></ul>'],
    ['a line break', '<p>one<br>two</p>'],
    ['a word either side of a link', '<p>whether the agent may <a href="/x">read</a> a file</p>'],
    ['a digit against a word', '<p>step <span>1</span>Start</p>'],
    ['a title beside the body', '<title>Docs</title><p>Skip to content</p>'],
  ]) {
    assert.deepEqual(verdict(t, `<!doctype html><html><head></head><body>${html}</body></html>`), [], `the gate refuses ${what}, which a reader reads as separate words`)
  }

  // And the half the site depends on: a class the page's own stylesheet lays out as a block
  // is a boundary too — and only while the stylesheet says so. The same page with the seam
  // and no such rule is refused, which is the case proving the exemption is read from the
  // artifact rather than granted by name here.
  const stylesheet = (display) =>
    `<!doctype html><html><body><style>.path{color:red;${display ? 'display:block' : 'display:inline'}}</style><a href="/x">the docs guide</a><code class="path">readme.md</code></body></html>`
  assert.deepEqual(verdict(t, stylesheet(true)), [], 'the gate refuses a path the page lays out under its title, which is where a reader sees it')
  assert.match(
    verdict(t, stylesheet(false)).join('\n'),
    /"guidereadme"/,
    'the gate accepts a glued path even when the stylesheet lays it out inline, so the block-display exemption is not being read from the page at all',
  )

  // The other half of the boundary model, and the reason the rule above can be about letters
  // rather than about lower case: a flex container blockifies its children, so a row of links
  // is a boundary a reader sees even where the source writes no space between them. And only
  // while the stylesheet says so: the same row laid out inline is a seam like any other.
  const row = (declaration) =>
    `<!doctype html><html><body><style>.row{${declaration}}</style><div class="row"><a href="/x">Get started</a><a href="/y">How it works</a></div></body></html>`
  assert.deepEqual(verdict(t, row('display:flex')), [], 'the gate refuses a row the page lays out as flex, where a reader sees the two links apart')
  assert.match(
    verdict(t, row('display:inline')).join('\n'),
    /"startedHow"/,
    'the gate accepts a glued row even when the stylesheet does not lay its children out apart, so the flex exemption is not being read from the page at all',
  )
})
