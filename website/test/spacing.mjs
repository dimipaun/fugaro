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
// does anything the page's own stylesheet lays out as a block. The docs hub's `.path` is one:
// a `<code>` holding `docs/gcp-setup.md` under a guide's title, which the hub's own stylesheet
// gives `display: block` and a reader therefore sees on its own line rather than glued to the
// title above it. This file reads each page's inlined stylesheet, collects the classes whose
// rules set a block-level `display`, and hands that answer to the shared reader. Without it
// the gate would report the hub's own seams as failures, and a gate that cries wolf on the
// design is a gate somebody turns off. The exemption is not a list of names kept here: a class
// gains it by being `display: block` in the artifact, which is what a reader sees, and the
// case below proves both halves — a seam the stylesheet lays apart is not refused, and the
// same seam is refused the moment the stylesheet lays it out inline.
//
// This is the rule that found the console download row: `<a>macOS .dmg</a>` and the published
// checksum `<code>` beside it had no space between them and no rule that lays them apart, so
// the page read "macOS .dmga434f3…". That is the defect this gate exists for, on a page this
// change does not otherwise touch, and the space is in the component now.
//
// The rule is deliberately about letters: a seam is refused when a lowercase letter sits
// directly against another lowercase letter, which is what a lost space inside a sentence
// looks like (`andfugaro`, `read-onlyfugaro`). A digit against a word (`1Start`, the numbered
// step marker beside its title) and punctuation at the boundary (`built with <code>go</code>.`)
// are the page's own choices and are not refused.
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

/** The classes a selector names, whatever else it carries (`[data-astro-cid-x]`, a tag, `>`). */
function classNames(selector) {
  return [...selector.matchAll(/\.([A-Za-z_][-\w]*)/g)].map((match) => match[1])
}

/**
 * The classes a page's own inlined stylesheet lays out as a block. Astro inlines every
 * stylesheet a page uses into its `<style>` blocks, so this is the whole of the CSS that page
 * carries; a rule that sets a block-level `display` on a class is the page saying "a reader
 * sees a boundary here".
 */
function blockClasses(doc) {
  const classes = new Set()
  for (const style of findAll(doc, 'style')) {
    for (const rule of cssRules(style.raw ?? '')) {
      if (BLOCK_DISPLAY.test(cssDeclarations(rule.body).find(([property]) => property === 'display')?.[1] ?? '')) {
        for (const name of classNames(rule.selector)) classes.add(name)
      }
    }
  }
  return classes
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
    const laidOut = blockClasses(doc)
    const isBlock = (el) =>
      BLOCK.has(el.name) || (el.attrs?.get('class') ?? '').split(/\s+/).some((name) => laidOut.has(name))
    for (const glued of runTogether(doc, isBlock)) {
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
})
