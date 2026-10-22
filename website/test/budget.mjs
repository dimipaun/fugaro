// website/test/budget.mjs — the byte budget (website plan, W1.11 step 4; reconciliation
// finding 56, D105).
//
// D105 asks for the performance contract "as numbers a test enforces": a static-first page
// with no client JavaScript except deliberate islands, and a JavaScript budget. The plan
// names the budgets but gives no figures, so the figures below are A DELIBERATE CHOICE FOR
// THE OWNER, recorded where the plan says they live — `website/README.md`, "The gates" —
// which this file reads, so the README's numbers are the enforced ones and cannot drift
// from them.
//
// The README's numbers are DELIBERATELY LOOSE (owner's ruling, 2026-10-17: "ignore them for
// now and optimize later"): each is three times the tight derivation below, until a later
// optimisation pass tightens them back towards these figures. The tight derivation is the
// build of 2026-10-20 — THE MERGED ONE, and this is one account of it rather than the two the
// figures arrived by: `main`'s site, which ships the one script (P6's copy control, P3's
// menu, W3.2's consent gate and analytics bootstrap in that same file, and the menu's
// hover-open on a pointer that hovers — the owner's brief of 2026-10-18, item 1 — with the
// corrections of that day and the next to it, named in the first bullet below), `main`'s
// `fugaro watch` picture on the homepage (#655), and this branch's redesigned pages on top of
// them (the homepage's order and run animation, P4, P7 and P8) together with its two changes
// from the owner's brief of **2026-10-19**: the menu's drawer replaced by the group
// labels in the header's row (item 1), and the console's link in the homepage's call to
// action (item 6), which renders from the console's flag — and the console's release (W3.1),
// which turned that flag on and so put the link, the console's downloads and Getting Started's
// console option into this build — and, from the same brief, the security page's cut to three
// things (the policy document's own *In short* section, its own reporting section and a link to
// the document), together with the 2 bytes the new spacing gate's own find cost the console
// page, and the boundary diagram the owner's brief of 2026-10-20 has that page open with (its labels,
// the facts under it and its review round's declared geometry) — and, last, the two rounds of 2026-10-19/20 this reading re-derived the figures for:
// the CLI's bare `--repo` usage strings, which moved the CLI reference and caps-and-costs by
// 1,126 bytes and which the round that made them left un-derived, and the owner's 2048 px logo,
// which regenerated every icon the site serves and so moved the site figure down by 10,345 and
// the page weight up by 72. **The logo round is the one this build's two byte figures come
// from** (the script is unchanged, so its figure is the one the round before set), and every
// round is named in the list below — plus 25 %, which is D192's rule and the rule the README's
// loose figures are three times.
//
// The count below is read off **the gates' own build of the site**, which is the artifact the
// derivation case at the end of this file measures (see "the build the README's figures are a
// derivation OF"), rather than carried from the merge. **That artifact is a copy of the
// repository in a temp directory rather than the checkout's `dist/`** — the copy is the build
// the gates can make in every environment, with no `.git` and no dependence on what the
// checkout happens to carry. Until the owner's brief of 2026-10-19 the difference was one a
// figure could not survive: the changelog rendered each release file's "not yet released"
// state where `git tag --list` found no tag (`src/lib/changelog.mjs`), so a shallow checkout —
// `website-preview.yml`'s, and so the deployed site's — built a larger site than this
// repository's own clone, which carries the tags, by exactly that page's own bytes, and a README
// figure derived from one was refused by the other. Which releases are published is the
// plugin's own version now, so no page of the site depends on the checkout's git state:
// `website/README.md` writes the two shapes as one figure, and the case below reads the pair
// there and holds them to it. The copy stays the derivation's subject because it is the
// artifact the gate can measure everywhere.
//
// **The three figures themselves are not written here, and that is the correction of
// 2026-10-19.** They live in `website/README.md`'s budget lines, which the case below reads and
// holds to the build it makes itself; a comment that repeated them was a second place to forget,
// and it was forgotten — a round re-derived the site figure in the README and left the copy here
// stating the build before it. What the three bullets below keep is the **rule** each figure is
// derived by, and the round-by-round history that follows them lives in the README as well: the
// same rounds, the same causes and the same deltas, each re-derived in the commit that moved it,
// with no figure repeated here (D192).
//
//   - JavaScript per page: the one script the site ships,
//     `public/scripts/site.js` (P6, P3, D183, W3.2's gate, the cookie deletion the owner's
//     ruling of 2026-10-18 added to a withdrawal, and that ruling's brief item 1: a group's
//     submenu opens when the pointer arrives at it, bound only where `matchMedia` reports a
//     pointer that hovers and only for a pointer that reports itself a mouse or a pen, and
//     re-decided whenever that answer changes), × 1.25, rounded up. The event
//     that says which pointer arrived, and the query's `change` that says the device's answer has
//     moved, re-derived all three figures here on 2026-10-19; the same day's second correction
//     did it again — the focus a click or a tap moves is the pointer's and opens nothing (the
//     click that brought it toggles the group itself), the keyboard's focus still opens one, and
//     a group the browser closes because a sibling in its `name` set opened hands the reader's
//     focus back — and the third did it once more: that handback is only the browser's, since a
//     press on the page's own text moves the focus to the body without a `focusin` to hear, so a
//     close the reader asks for from outside the menu gives nothing back — and the fourth did it
//     again: a group has two readers, and the pointer's close is owed when nobody is left reading,
//     so a keyboard reader standing in the group holds it open, the close lands when the focus
//     goes, and a focus arriving inside `CLOSE_AFTER_MS` cancels the close the pointer asked for
//     — and the fifth re-derived all three again: the keyboard's way into a group is bound beside
//     the browser's own state rather than inside the hover behaviour, because the keyboard is the
//     device's and not the pointer's, so a phone or a tablet with a keyboard attached answers the
//     hover query no and still has a second level Tab can enter; and the guard that keeps a close
//     from reopening the group it handed the focus back to lasts for that focus and no longer.
//     Every page
//     loads the script exactly once — so the file's own size IS the whole of a page's
//     JavaScript — which is why the case below reads the artifact rather than trusting this
//     line, and why D192 requires the README's figure to be re-derived in the same commit that
//     changes the script.
//   - Page weight per page: the heaviest page — the console page today, since
//     the four screenshots landed and its weight is its HTML plus the pictures it renders, where
//     it was `docs/index.html` before them, whose HTML is the whole generated CLI reference —
//     plus the first-party files the page fetches: the script above and the logo and icons the
//     layout links — × 1.25, rounded up. One ceiling for every page rather than one per page, so
//     a page that grows with the product (the changelog, the reference) or with the pictures it
//     carries is held to the same line as the rest instead of to its own history. **Which page
//     is the heaviest is a fact about the build and not about this list**: the figure in
//     `website/README.md` names the page, a round that adds pictures can move that page from the
//     docs hub to the console page and back, and this derivation only runs under
//     `FUGARO_DERIVE_BUDGETS=1` — so a round that changes which page it is re-derives there in
//     the commit that does, and the README's sentence moves with it.
//   - Whole site: every file in `dist/` — thirteen pages, the script, the brand icons, the manifest,
//     the two `fugaro watch` assets, and the console's four screenshots and the manifest they
//     came with — × 1.25, rounded up. It is the build every checkout makes alike, tags or no
//     tags, since the changelog reads the plugin's version rather than the checkout's tags.
//
// **Where this build's three numbers come from** is written in `website/README.md` too, one
// bullet up from this file's name in its gates list: the same rounds, the same causes and the
// same deltas — `main`'s fix rounds and this branch's redesign's own; the `fugaro watch`
// picture landing on the homepage; the menu's drawer coming out and the script's comments going
// back in step with it; the console's release turning `features.console_downloads` on;
// Getting Started's three bands; the console's screenshot slider and the review round that made
// its moves instant and its first reading the track's; the run on How It Works
// becoming one vertical timeline; the caps-and-costs rework; the changelog's product
// summaries; the homepage's prompt line, benefits, pull request caption, the benefits'
// block rule and the four icons and the type step the points were given after it; the privacy
// statement's software part, the phone app's reporting written into
// it and the deletion section carved out from under it; the app's reporting made always-on, with
// no switch in the app (the owner's ruling of 2026-10-19); the security page cut to three
// things, with its review rounds; the header put on a layer of its own; the CLI's bare `--repo`
// usage strings reaching the reference and the caps-and-costs page; and the owner's 2048 px logo
// with every icon regenerated from it — each re-derived
// in the commit that moved it (D192). The last two share one re-derivation, because the CLI
// round was merged with its figures left un-derived and the round after it re-derived from the
// figures rather than from the build. None of those figures is
// repeated here, because a figure in a comment is a figure that moves with
// nothing when the build moves, which is what happened to the copy that used to be in this one;
// the case below reads the README's figures and holds them to the build it makes itself.
//
// What is counted, all of it raw (uncompressed) bytes, because that is what the artifact
// holds and what a test can compute without guessing a server's compression:
//   - a page's weight is its HTML (inline `<style>` and `<script>` included) plus every
//     first-party file it makes the browser fetch: linked stylesheets, scripts, module
//     preloads, preloaded fonts, and images — each counted once per page;
//   - a page's JavaScript is its executable inline scripts plus the script files and module
//     preloads it references;
//   - the whole site is every file in `dist/`.
// A failure names the page and its sizes. Lighthouse (D105's performance ≥ 95 target) is
// the owner-run check, not this one: there is no CI on pull requests, and the tests are
// hermetic (F3, F58).
//
// It reads two artifacts, and they are not the same one. **The ceilings** — every case that
// measures a page or the site against a number — read `dist/`, which `npm run check` builds
// first (`FUGARO_GATES_DIST` points them at a copy, which is how the refusals were
// mutation-proved). **The derivation** — the case that holds each README figure to the build
// it names — reads the gates' own build of the site instead, which is the build the gate can
// make in every environment rather than the one the checkout happens to hold; the section
// above that case says why in full. Standard library only; no network.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, existsSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { DIST, SITE, builtPages, filesUnder, find, parse, servedFile, walk } from './lib/html.mjs'
import { buildCopy } from './lib/build.mjs'
import { DOCUMENT_PAGES } from './lib/exempt.mjs'

// ---- the numbers, from the README ------------------------------------------------------

const BUDGET_LINES = {
  js: /^[ \t]*- \*\*JavaScript per page:\*\* ([\d,]+) bytes\b/m,
  page: /^[ \t]*- \*\*Page weight per page:\*\* ([\d,]+) bytes\b/m,
  site: /^[ \t]*- \*\*Whole site:\*\* ([\d,]+) bytes\b/m,
  lead: /^[ \t]*- \*\*Words in a page's lead:\*\* ([\d,]+) words\b/m,
  words: /^[ \t]*- \*\*Words per page:\*\* ([\d,]+) words\b/m,
}

/** The budgets as website/README.md states them: bytes, and the word ceilings of D188. */
export function readmeBudgets(readme = readFileSync(path.join(SITE, 'README.md'), 'utf8')) {
  const out = {}
  for (const [key, re] of Object.entries(BUDGET_LINES)) {
    const m = re.exec(readme)
    assert.ok(m, `website/README.md does not state the ${key} budget in the line this check reads (${re}): the budget is a fact in the README, not an aspiration in a test`)
    out[key] = Number(m[1].replace(/,/g, ''))
  }
  return out
}

// ---- measuring -------------------------------------------------------------------------

const EXECUTABLE = new Set(['', 'text/javascript', 'application/javascript', 'module'])

/** A page's weight and JavaScript, with the parts that make them up. */
export function measurePage(html, rel, dist) {
  const doc = parse(html)
  const fetched = new Map() // served file -> 'js' | 'other'
  let inlineJS = 0
  let inlineCSS = 0
  const take = (url, kind) => {
    if (!url || /^[a-z][a-z0-9+.-]*:|^\/\//i.test(url)) return // remote or data: — origins.mjs's
    const pageURL = new URL('/' + rel.replace(/index\.html$/, ''), 'https://x/')
    const served = servedFile(dist, new URL(url, pageURL).pathname)
    if (served && !fetched.has(served)) fetched.set(served, kind)
  }
  for (const el of walk(doc)) {
    if (el.name === 'style') inlineCSS += Buffer.byteLength(el.raw ?? '')
    if (el.name === 'script') {
      if (el.attrs.has('src')) take(el.attrs.get('src'), 'js')
      else if (EXECUTABLE.has((el.attrs.get('type') ?? '').toLowerCase())) inlineJS += Buffer.byteLength(el.raw ?? '')
    }
    if (el.name === 'link') {
      const rels = (el.attrs.get('rel') ?? '').toLowerCase().split(/\s+/)
      if (rels.includes('modulepreload')) take(el.attrs.get('href'), 'js')
      else if (rels.includes('stylesheet') || rels.includes('preload') || rels.includes('icon')) take(el.attrs.get('href'), 'other')
    }
    if (el.name === 'img' || el.name === 'source') take(el.attrs.get('src'), 'other')
  }
  const html_ = Buffer.byteLength(html)
  let files = 0
  let fileJS = 0
  for (const [served, kind] of fetched) {
    const size = statSync(path.join(dist, served)).size
    files += size
    if (kind === 'js') fileJS += size
  }
  return { rel, html: html_, inlineCSS, inlineJS, files, weight: html_ + files, js: inlineJS + fileJS }
}

/** Every budget failure of a built artifact, against `budgets`. */
export function budgetFailures(dist, budgets) {
  const out = []
  for (const page of builtPages(dist)) {
    const m = measurePage(page.html, page.rel, dist)
    if (m.js > budgets.js) {
      out.push(`${m.rel} ships ${m.js} bytes of JavaScript (${m.inlineJS} inline + ${m.js - m.inlineJS} in files); the budget is ${budgets.js} bytes per page (website/README.md)`)
    }
    if (m.weight > budgets.page) {
      out.push(`${m.rel} weighs ${m.weight} bytes (HTML ${m.html}, of which inline CSS ${m.inlineCSS} and inline JS ${m.inlineJS}; fetched files ${m.files}); the budget is ${budgets.page} bytes per page (website/README.md)`)
    }
  }
  const total = filesUnder(dist).reduce((sum, rel) => sum + statSync(path.join(dist, rel)).size, 0)
  if (total > budgets.site) out.push(`the whole site is ${total} bytes; the budget is ${budgets.site} bytes (website/README.md)`)
  return out
}

// ---- the word ceilings, from the README (D188) -----------------------------------------
//
// D188's other half: a page's bytes are not the only thing a reader is asked to carry, and
// "the gist above the fold, with very few words" is a promise a byte budget cannot keep.
// The two ceilings below are the words a reader reads, counted over the built page's `main`
// region — the page and not its chrome, so the navigation and the footer are not a page's
// fault.
//
// What counts as a word is what a reader sees: every text run inside `main`, with the
// script and style bodies (raw text, never rendered) and anything `hidden` left out, and an
// image's `alt` standing in for the image, which is how `textOf` in `test/lib/html.mjs`
// already reads a page. A command block is a command a reader copies, and it is counted
// like any other visible text: the ceiling is a claim about the page, not about its prose.
//
// The three reference pages D188 exempts — `/docs/`, `/security/` and `/changelog/` — are
// exempt from the **page** ceiling because each renders a document of this repository
// rather than a page of the site; their leads are still held to the lead ceiling, since a
// lead is a page's own and every page has one.
//
// The list itself is not written here: it is `test/lib/exempt.mjs`'s `DOCUMENT_PAGES`, which
// `test/wording.mjs` skips for the same reason and holds equal to D188's own row. Two lists
// would be two rules, and the page that ends up on one of them is exempt from a ceiling and
// held to a wording rule — or the reverse — with both gates green.

/** The words of a piece of text, the way a reader counts them. */
function words(text) {
  return text.trim().split(/\s+/).filter(Boolean).length
}

/** The text a reader sees inside a node: no raw script or style body, nothing hidden. */
function visibleText(node) {
  if (node.text !== undefined) return node.text
  if (node.name === 'img') return node.attrs.get('alt') ?? ''
  if (node.raw !== undefined) return ''
  if (node.attrs?.has('hidden')) return ''
  return (node.children ?? []).map(visibleText).join(' ')
}

/**
 * `main`'s descendants in document order, as a flat list: every element, and every text run
 * and image where it stands. A hidden subtree is left out entirely, which is what keeps a
 * `hidden` control from being counted as a word a reader reads.
 */
function flatten(node, out = []) {
  for (const child of node.children ?? []) {
    if (child.text !== undefined || child.name === 'img') {
      out.push(child)
      continue
    }
    if (child.name === undefined) continue
    if (child.attrs?.has('hidden')) continue
    out.push(child)
    flatten(child, out)
  }
  return out
}

/** True when `node` is `ancestor` or sits inside it. */
function within(node, ancestor) {
  for (let n = node; n; n = n.parent) if (n === ancestor) return true
  return false
}

/**
 * The hero's illustration — `figure.hero-shot`, the terminal card the bold redesign draws
 * beside the copy (the owner's approved board, `website/design-ref/HomeV3.dc.html`) — or
 * `null` on a page that draws none.
 *
 * It is taken out of the lead's word count, and `website/test/fold.mjs`'s rule 1 takes it out
 * of the lead region in the same way, because it is a picture rather than prose: the request
 * drawn in it, the three job rows and the caption that says it is an illustration are the
 * contents of a card a reader looks at, and counting them as the page's opening paragraph
 * would make the ceiling measure the artwork. The two gates read one lead between them, and a
 * page whose card held the promise itself would be refused by the ceiling as soon as the card
 * were gone — the words are still the page's own, and the illustration is drawn on every
 * width.
 */
function heroIllustration(main) {
  return flatten(main).find((node) => (node.attrs?.get('class') ?? '').split(/\s+/).includes('hero-shot')) ?? null
}

/** True when `node` comes after `other` in `main`'s document order. */
function comesAfter(main, node, other) {
  const items = flatten(main)
  return items.indexOf(node) > items.indexOf(other)
}

/**
 * The visible text of `main` between two of its elements, in document order. The caller
 * checks the two are in that order before calling — a page's headings can be the other way
 * round, and that is a shape a reader measures rather than one it refuses — so a call that
 * cannot find them is a defect in the reader and not in the page.
 */
function textBetween(main, from, to) {
  const items = flatten(main)
  const start = items.indexOf(from)
  const end = items.indexOf(to)
  assert.ok(start !== -1 && end > start, 'the lead reader could not find the headings it reads between, which it checks for before this call: this is a defect in the reader, not in the page')
  const picture = heroIllustration(main)
  return items
    .slice(start + 1, end)
    .filter((item) => !within(item, from) && (picture === null || !within(item, picture)))
    .map((item) => (item.text !== undefined ? item.text : item.name === 'img' ? item.attrs.get('alt') ?? '' : ''))
    .join(' ')
}

/**
 * The words of a built page: the whole of `main`, and its lead.
 *
 * The lead is the visible text between the page's `h1` and its first `h2` — the paragraph a
 * page opens with before its first section, which is the text D188's 40-word ceiling is
 * about. That is the whole of the definition, and it is the plan's own: **the region between
 * the two headings in document order**, wherever either heading sits. It used to be read
 * from the `h1`'s container instead when the first `h2` was outside it, and the two readings
 * are the same number only while a page keeps every block between its `h1` and its first `h2`
 * inside the heading's own container. A gate whose number depends on which end it is read
 * from is two gates (P4's reviewer asked which one this is, and P5 answered it), so the
 * region is the reading and the container is not.
 *
 * **Where the homepage stands today** (2026-10-20, the bold redesign): its `h1` is the
 * headline, "Lambdas for coding agents" — the two words the stylesheet paints with the
 * gradient are a `span` inside it and not a second thing in the region — its first `h2` is
 * the slider's own line over its control ("Say it. Get pull requests.", the bold redesign's
 * heading for the block the owner's approved draft of 2026-10-19 put first under the
 * promise), and the region between the two headings is the hero's lead paragraph and its row
 * of links: **16 words**, the number the built page measures. The hero's illustration is
 * drawn between them and is taken out of the count — see `heroIllustration` below — and so
 * is the block the first `h2` belongs to, which `website/test/fold.mjs` takes out of the
 * region in the same way, so the two gates read one lead between them. Before the redesign
 * the same region held the request the hero printed under the headline, the promise, its
 * three calls to action and the slider's three tab labels, and it measured 30 words; the
 * request moved into the illustration card and the tab labels are no longer in the region
 * because the slider's heading now comes before its control.
 *
 * The region is what is enforced, on this page and on any page that grows a block between its
 * `h1` and its first `h2` later. An earlier homepage read 24 words from the `h1`'s container
 * where the region said 134, which is why the region and not the container is the reading.
 *
 * A page with no `h2` at all has no such region, and its lead is the paragraph run under the
 * `h1` — the text D188's ceiling is about, read from the other end. A page whose first `h2`
 * comes *before* its `h1` has no such region either — the region would have to be read
 * backwards, and the text in front of a page's `h1` is not the page's lead — so it is
 * measured the same way, as the paragraph run under the `h1`, rather than refused. The
 * whitespace a
 * template leaves between the `h1` and the paragraph under it is a text node and not a
 * paragraph: it is skipped, because reading it as the end of the opening would measure the
 * lead of every indented page — every built page — as zero words.
 *
 * A page with no `main` is not measured here: `a11y.mjs` is the gate that refuses a page
 * without the landmark, and this one says nothing about a region it cannot find.
 */
export function pageWords(html) {
  const main = find(parse(html), 'main')
  if (main === null) return null
  const h1 = find(main, 'h1')
  if (h1 === null) return { words: words(visibleText(main)), lead: 0 }
  const firstH2 = find(main, 'h2')
  // The region between the headings exists only when the `h2` comes after the `h1`: a page
  // whose `h2` comes first is a page with no such region rather than a page this reader
  // refuses, and `textBetween` measures forwards, so the order is checked here and the
  // paragraph run under the `h1` answers for the rest.
  if (firstH2 !== null && comesAfter(main, firstH2, h1)) {
    return { words: words(visibleText(main)), lead: words(textBetween(main, h1, firstH2)) }
  }
  const siblings = h1.parent.children
  const opening = []
  for (const sibling of siblings.slice(siblings.indexOf(h1) + 1)) {
    // A style or script body is not text, a hidden subtree is not rendered, and a comment is
    // not text either: none of them ends the opening paragraph run, which is what keeps the
    // lead the same paragraph whatever the page puts around it.
    if (sibling.name === 'style' || sibling.name === 'script' || sibling.attrs?.has('hidden')) continue
    if (sibling.raw !== undefined) continue
    // A template's newline is a text node between the `h1` and the paragraph under it, and a
    // newline is not a paragraph: a run of whitespace is skipped rather than taken for the
    // end of the opening, which is what keeps a page whose markup is indented — every built
    // page — from measuring a lead of zero words. Text that is not whitespace is text a
    // reader reads, so it is part of the lead and does not end the run.
    if (sibling.text !== undefined) {
      if (sibling.text.trim() !== '') opening.push(sibling.text)
      continue
    }
    if (sibling.name !== 'p') break
    opening.push(visibleText(sibling))
  }
  return { words: words(visibleText(main)), lead: words(opening.join(' ')) }
}

/** Every word-ceiling failure of a built artifact, against `budgets`. */
export function wordFailures(dist, budgets) {
  const out = []
  for (const page of builtPages(dist)) {
    const counts = pageWords(page.html)
    if (counts === null) continue
    if (counts.lead > budgets.lead) {
      out.push(`${page.rel} opens with a ${counts.lead}-word lead; the ceiling is ${budgets.lead} words in a page's lead (website/README.md)`)
    }
    if (!DOCUMENT_PAGES.has(page.url) && counts.words > budgets.words) {
      out.push(`${page.rel} is ${counts.words} words; the ceiling is ${budgets.words} words per page (website/README.md), and the pages D188 exempts are ${[...DOCUMENT_PAGES].join(', ')}`)
    }
  }
  return out
}

// ---- the gate --------------------------------------------------------------------------

test('every page, and the whole site, is within the byte budget the README states', () => {
  const failures = budgetFailures(DIST, readmeBudgets())
  assert.deepEqual(failures, [], `the built site is over its byte budget:\n  ${failures.join('\n  ')}`)
})

// ---- the one script, counted once per page (P6, D183) -----------------------------------

// The site's JavaScript is one file, and this is the case that says so about the ARTIFACT
// rather than about the budget line above it: every page loads `public/scripts/site.js`
// exactly once, however many times it references it, and the artifact holds no other script
// at all. A page that grew a second script — or an inline one — fails here first, with the
// page named, before it fails the budget with a number.
const SCRIPT = 'scripts/site.js'

test('the site ships one script file, and every page counts its bytes once', (t) => {
  const file = path.join(DIST, SCRIPT)
  assert.ok(
    existsSync(file),
    `${SCRIPT} is not in the built artifact: the copy control is driven by one first-party script file (D183, P6), and the budget above counts it as every page's JavaScript`,
  )
  const bytes = statSync(file).size

  const scripts = filesUnder(DIST).filter((rel) => /\.m?js$/.test(rel))
  assert.deepEqual(
    scripts,
    [SCRIPT],
    `the artifact holds ${scripts.length} script files (${scripts.join(', ')}): the site ships one, and the JavaScript budget is a statement about that one file's bytes`,
  )

  const pages = builtPages(DIST)
  assert.ok(pages.length > 0, 'the build produced no page, so this case would check nothing')
  for (const page of pages) {
    const m = measurePage(page.html, page.rel, DIST)
    assert.equal(
      m.js,
      bytes,
      `${page.rel} counts ${m.js} bytes of JavaScript; the site's script is ${bytes} bytes and a page loads it once, however many times it names it`,
    )
    assert.equal(
      m.inlineJS,
      0,
      `${page.rel} ships ${m.inlineJS} bytes of inline JavaScript: the site's one script is a file, which the deployed policy admits as script-src 'self' rather than by a hash`,
    )
  }

  // The other half of "once", as a fixture rather than as an observation about today's
  // layout: a page that names the same file twice — a preload beside the script tag, which
  // is what a bundler emits — is one download and one charge.
  const dist = distOf(t, {
    'index.html': shell(`<script src="/${SCRIPT}"></script><link rel="modulepreload" href="/${SCRIPT}">`),
    [SCRIPT]: 'x'.repeat(bytes),
  })
  assert.equal(
    measurePage(readFileSync(path.join(dist, 'index.html'), 'utf8'), 'index.html', dist).js,
    bytes,
    'a page that references the script twice is charged for it twice, and the two references are one download',
  )
})

test('every page is within the word ceilings the README states', () => {
  const failures = wordFailures(DIST, readmeBudgets())
  assert.deepEqual(failures, [], `the built site is over its word ceilings:\n  ${failures.join('\n  ')}`)
})

// ---- the refusals, kept ----------------------------------------------------------------

function distOf(t, files) {
  const dist = mkdtempSync(path.join(tmpdir(), 'fugaro-budget-'))
  t.after(() => rmSync(dist, { recursive: true, force: true }))
  for (const [rel, body] of Object.entries(files)) {
    mkdirSync(path.dirname(path.join(dist, rel)), { recursive: true })
    writeFileSync(path.join(dist, rel), body)
  }
  return dist
}

const shell = (head, body = '') => `<!DOCTYPE html><html lang="en"><head><title>t</title>${head}</head><body>${body}</body></html>`

test('an over-budget page is refused with its name and its sizes', (t) => {
  const dist = distOf(t, {
    'index.html': shell('', 'x'.repeat(2000)),
    'small/index.html': shell(''),
  })
  const f = budgetFailures(dist, { js: 0, page: 1000, site: 1_000_000 })
  assert.equal(f.length, 1, f.join('\n'))
  assert.match(f[0], /^index\.html weighs 20\d\d bytes \(HTML 20\d\d, .*\); the budget is 1000 bytes per page/)
})

test('an island over the JavaScript budget is refused, inline or as a file, counted once per page', (t) => {
  const dist = distOf(t, {
    'index.html': shell('<script type="module" src="/_astro/island.js"></script><link rel="modulepreload" href="/_astro/island.js">', '<script>let a=1</script>'),
    '_astro/island.js': 'x'.repeat(500),
    'data/index.html': shell('<script type="application/ld+json">{"a":1}</script>'),
  })
  const f = budgetFailures(dist, { js: 100, page: 1_000_000, site: 1_000_000 })
  assert.equal(f.length, 1, f.join('\n'))
  assert.match(f[0], /^index\.html ships 507 bytes of JavaScript \(7 inline \+ 500 in files\)/)
})

test('a linked stylesheet counts toward the page, and every file toward the site', (t) => {
  const dist = distOf(t, {
    'index.html': shell('<link rel="stylesheet" href="/_astro/a.css">'),
    '_astro/a.css': 'a{}'.repeat(400),
    'orphan.bin': 'y'.repeat(3000),
  })
  const f = budgetFailures(dist, { js: 0, page: 1000, site: 3000 })
  assert.equal(f.length, 2, f.join('\n'))
  assert.match(f[0], /fetched files 1200/)
  assert.match(f[1], /^the whole site is \d+ bytes; the budget is 3000 bytes/)
})

test('the README numbers themselves still refuse: one byte over each fails', (t) => {
  const b = readmeBudgets()
  const over = shell('', 'x'.repeat(b.page + 1))
  const dist = distOf(t, {
    'index.html': over,
    'js/index.html': shell('', `<script>${'x'.repeat(b.js + 1)}</script>`),
    'pad.bin': 'y'.repeat(b.site),
  })
  const f = budgetFailures(dist, b)
  assert.ok(f.some((l) => /^index\.html weighs \d+ bytes .* the budget is \d+ bytes per page/.test(l)), f.join('\n'))
  assert.ok(f.some((l) => /^js\/index\.html ships \d+ bytes of JavaScript/.test(l)), f.join('\n'))
  assert.ok(f.some((l) => /^the whole site is \d+ bytes/.test(l)), f.join('\n'))
})

test('the README budget lines are read strictly', () => {
  assert.deepEqual(
    readmeBudgets(
      '- **JavaScript per page:** 0 bytes.\n- **Page weight per page:** 1,234 bytes.\n- **Whole site:** 5,678 bytes.\n' +
        "- **Words in a page's lead:** 40 words.\n- **Words per page:** 250 words.\n",
    ),
    { js: 0, page: 1234, site: 5678, lead: 40, words: 250 },
  )
  assert.throws(() => readmeBudgets('- **JavaScript per page:** zero\n'), /does not state the js budget/)
  assert.throws(
    () =>
      readmeBudgets(
        '- **JavaScript per page:** 0 bytes.\n- **Page weight per page:** 1,234 bytes.\n- **Whole site:** 5,678 bytes.\n' +
          "- **Words in a page's lead:** 40 words.\n- **Words per page:** a few\n",
      ),
    /does not state the words budget/,
  )
})

// ---- the numbers are the derivation, not a figure picked loose (D192) -------------------

// D192: each README number is three times the build it was derived from plus 25 %, and the
// README says which build. These read the derivation the README writes beside each number and
// hold the number to it, so raising a ceiling means re-deriving it from a build the artifact
// can be measured against — not editing the headline figure.
//
// **And the build each number names is the one the site builds** (P8's review, and the fixes
// round on this branch): all three, not only the script's. A derivation that names a build the
// artifact is not is a number nobody can check against today's build — it drifts down as pages
// grow, and a budget derived from a smaller build than the site has is a ceiling tighter than
// the README says it is, which is a lie in the reader's favour that still leaves the × 3
// meaningless. So a byte that moves re-derives its figure in the commit that moves it, which is
// D192's own rule and the plan's ("later parts that add bytes re-derive in their own commit").
// The refusal this replaced — a stated build more than a tenth above the real one — is a case
// of this one, which is why the strictness only ever went up.
//
// **Which build the number names is a build whose bytes are the same in every checkout**, and
// that is the correction of 2026-10-19 (`website/README.md` states the same figures and the same
// reason). `console-option.mjs` builds the site with the console's flag flipped — the state the
// site was in before the console's release, which the checkout no longer builds — and runs the
// artifact gates over that copy, so the flag's two states are both held to the ceilings. A
// derivation held to that copy would be the flag's own bytes rather than the site's: it is
// smaller at the site figure by exactly the console option's markup on the three pages that
// render it — the console page's downloads section, Getting Started's option and the homepage's
// call to action — and by nothing at all at the page weight. The direction is the flag's own:
// turning `features.console_downloads` on added those bytes, so the copy that flips it back off
// is the smaller build. The checkout's own `dist/` is the other artifact the numbers may not
// name: it is the build the checkout happens to hold, and while the changelog read
// `git tag --list` its bytes moved with that checkout's git state (above). What is left is the
// gates' own build: a copy of the repository with no `.git`, made by
// `test/lib/build.mjs`'s `buildCopy`, which is what the case below measures (`documentedBuild`).
const DERIVATIONS = {
  js: /\*\*JavaScript per page:\*\* ([\d,]+) bytes\. Three times ([\d,]+): the one script[\s\S]*?\(([\d,]+) bytes/,
  page: /\*\*Page weight per page:\*\* ([\d,]+) bytes\. Three times ([\d,]+): the heaviest page[\s\S]*? at ([\d,]+) bytes/,
  site: /\*\*Whole site:\*\* ([\d,]+) bytes\. Three times ([\d,]+) \(every file in `dist\/`, ([\d,]+)\s+bytes/,
}

/** Every way the README's numbers depart from their own derivation, given what is built. */
export function derivationFailures(readme, measured) {
  const out = []
  const n = (s) => Number(s.replace(/,/g, ''))
  for (const [key, re] of Object.entries(DERIVATIONS)) {
    const m = re.exec(readme)
    if (!m) {
      out.push(`website/README.md does not write the ${key} budget as "N bytes. Three times T … (B bytes": D192 derives each number from a named build`)
      continue
    }
    const [budget, tight, build] = [n(m[1]), n(m[2]), n(m[3])]
    if (budget !== 3 * tight) out.push(`the ${key} budget is ${budget} bytes and three times ${tight} is ${3 * tight} (D192)`)
    if (tight !== Math.ceil(build * 1.25)) out.push(`the ${key} tight figure is ${tight} bytes and ${build} plus 25 % is ${Math.ceil(build * 1.25)} (D192)`)
    if (build !== measured[key]) {
      out.push(
        `the README derives the ${key} budget from a ${build}-byte build and this one is ${measured[key]} bytes: re-derive it in the commit that moves those bytes (D192)`,
      )
    }
  }
  return out
}

/**
 * The build the README's figures are a derivation OF: **the gates' own build of the site** — a
 * copy of the repository in a temp directory, built with the site's installed Astro by
 * `test/lib/build.mjs`'s `buildCopy`, which is the same builder the gates that stage a copy of
 * the repository already use.
 *
 * It is deliberately not `DIST`, and the difference is the whole of why this is a build of its
 * own rather than the artifact the run was pointed at. `FUGARO_GATES_DIST` is the mechanism the
 * gates document for reading another artifact, and the case that uses it — `console-option.mjs`'s
 * build with the console's flag flipped off, the state the checkout no longer builds — is a
 * DIFFERENT build rather than another copy of this one: flipping `features.console_downloads`
 * adds the console option's markup to the three pages that render it (the console page's
 * downloads section, Getting Started's option and the homepage's call to action), and it is the
 * SMALLER of the two, since the copy is the flag-off state and this build is the flag-on one.
 * The derivation is not held to it, and no byte count for it is written here: `console-option.mjs`
 * is the case that measures that copy, and a figure repeated in this comment would move with
 * nothing the next time the flag's markup does.
 *
 * Nor is it held to `website/dist`, which `npm run check` builds first — and that is the
 * correction this reading carries (2026-10-19). `dist/` is the site built from the checkout the
 * gates are running in, and one page of the site used to depend on that checkout's git state:
 * the changelog rendered each release file's "not yet released" state where `git tag --list`
 * found no tag (`src/lib/changelog.mjs`), which is every entry in a shallow checkout. So the
 * same commit built to two different site figures — the larger in a checkout with no tags,
 * which is what `.github/workflows/website-preview.yml`'s default checkout is, and the smaller
 * with the tags this repository's own clone carries, the changelog page's own "not yet
 * released" labels being the whole of the difference. A README figure derived from either one
 * was a figure the other environment refused, which is exactly what happened: `website-preview`
 * could not deploy, because the derivation named a build the run could not measure.
 *
 * That dependence is gone — which releases are published is the plugin's own version now, not a
 * tag (the owner's brief of 2026-10-19) — and the derivation stays where it is all the same: a
 * copy of the repository, with no `.git` at all, is the build the gates can make in every
 * environment, and the site's own `dist/` is held to the README's ceilings like any other
 * artifact. The README's figures are derived from the copy.
 */
let DOCUMENTED_BUILD = null

/** The gates' own build of the site — made once, removed when this process exits. */
function documentedBuild() {
  if (DOCUMENTED_BUILD !== null) return DOCUMENTED_BUILD
  const built = buildCopy({ after: (fn) => process.once('exit', fn) }, () => {}, 'fugaro-budget-doc-')
  assert.equal(
    built.status,
    0,
    `the build the README's figures are a derivation of — a copy of this repository built with the site's own Astro — failed, so the derivation cannot be measured against the build it names:\n${built.output}`,
  )
  DOCUMENTED_BUILD = built.dist
  return DOCUMENTED_BUILD
}

function measuredNow(dist = documentedBuild()) {
  const pages = builtPages(dist).map((p) => measurePage(p.html, p.rel, dist))
  return {
    js: statSync(path.join(dist, SCRIPT)).size,
    page: Math.max(...pages.map((m) => m.weight)),
    site: filesUnder(dist).reduce((sum, rel) => sum + statSync(path.join(dist, rel)).size, 0),
  }
}

// The owner's ruling of 2026-10-20: the README's budget figures are re-derived ONLY when the
// owner says so, so a build that moved a few hundred bytes no longer fails the preview. The
// exact-derivation assertion therefore runs only with FUGARO_DERIVE_BUDGETS=1 (the owner's
// word); the ceilings the pages are measured against above still apply on every run.
const DERIVE_ON_REQUEST = process.env.FUGARO_DERIVE_BUDGETS === '1' ? false : 'budgets are re-derived only when the owner asks (set FUGARO_DERIVE_BUDGETS=1)'

test("the README's budgets are D192's derivation from a build this one can be measured against", { skip: DERIVE_ON_REQUEST }, () => {
  const readme = readFileSync(path.join(SITE, 'README.md'), 'utf8')
  assert.ok(
    existsSync(path.join(SITE, 'dist')),
    `the site's own build is not at ${path.join(SITE, 'dist')}, so the ceilings above were read against nothing: \`npm run check\` builds it before the gates read it`,
  )
  assert.deepEqual(derivationFailures(readme, measuredNow()), [])
})

test('a budget raised without re-deriving it is refused', () => {
  const readme = readFileSync(path.join(SITE, 'README.md'), 'utf8')
  const now = measuredNow()
  const js = /\*\*JavaScript per page:\*\* ([\d,]+) bytes/.exec(readme)[1]
  assert.match(derivationFailures(readme.replace(`**JavaScript per page:** ${js} bytes`, '**JavaScript per page:** 100,000 bytes'), now).join('\n'), /three times/)
  // Every one of the three, not only the script's: a build that moves under a stated
  // derivation — a page that grows, a site that shrinks — is a figure to re-derive.
  for (const key of ['js', 'page', 'site']) {
    assert.match(
      derivationFailures(readme, { ...now, [key]: now[key] + 1 }).join('\n'),
      /re-derive it/,
      `the ${key} figure is not held to the build this one can measure`,
    )
    assert.match(
      derivationFailures(readme, { ...now, [key]: Math.floor(now[key] / 2) }).join('\n'),
      /re-derive it/,
      `a ${key} derivation far above the real build is not refused`,
    )
  }
})

// ---- the site's two checkout shapes, held to one figure ---------------------------------
//
// The derivation above is measured on ONE build, on purpose: a copy of the repository with no
// `.git`. The site's other shape — a checkout that carries the release tags — is not one a gate
// here can build, so its figure is not gate-enforced, and it drifted once: a round re-derived
// the site figure by the bytes it added and left the clone figure where it was. Until the owner's
// brief of 2026-10-19 the two shapes were a changelog page apart, the page printing "not yet
// released" wherever `git tag --list` found no tag; the changelog reads the plugin's own version
// now (`src/lib/changelog.mjs`), so the two shapes are one build and the pair is one figure.
// What can be held without a tagged build is that statement, in the one place this repository
// writes the pair as figures: `website/README.md`'s paragraph about the two shapes — and that
// the changelog still reads no tag, which is what makes the statement true. **The pair is read
// here from the README and from nowhere else**: the figures a reader sees are stated in one
// place, and this file's prose repeats none of them.
//
// **And the one figure the two shapes are stated as is the site's own**, which is the second
// half of the same claim and the correction of 2026-10-21: the paragraph says the two shapes are
// *this* build, so the figure it states is the byte count the whole-site budget above derives
// from — and the pair being equal to itself is not that. A round that re-derived the site figure
// and left both shapes where they were would agree with itself a round behind, which is the
// drift this case exists for and the one it could not see: the two numbers were read against
// each other and against nothing else for as long as they have existed. The build figure beside
// the whole-site budget is the README's own, read here rather than written here, so the pair is
// held to the site without a byte of it appearing in this file.
const PAIR =
  /\*\*([\d,]+) bytes in a checkout with no tags\*\*[\s\S]*?\*\*([\d,]+) bytes with the\s+tags this repository's own clone carries\*\*/

/** Every way the README's two checkout shapes depart from what the paragraph claims, as lines. */
export function pairFailures(readme) {
  const out = []
  const n = (s) => Number(s.replace(/,/g, ''))
  const pair = PAIR.exec(readme)
  if (!pair) {
    out.push(
      'website/README.md no longer states the site\'s two checkout shapes in the form this case reads — "**N bytes in a checkout with no tags** … **N bytes with the tags this repository\'s own clone carries**" — so the pair is not held anywhere: that paragraph is the one place the two figures are written, and a rewrite that drops one of them leaves the other stated with nothing reading it',
    )
    return out
  }
  const [noTags, clone] = [n(pair[1]), n(pair[2])]
  if (clone !== noTags) {
    out.push(
      `website/README.md states the no-tags build as ${noTags} bytes and the clone build as ${clone}: no page reads the checkout's git state, so the two shapes are one build and a re-derivation that moves one of them moves the other`,
    )
  }

  const site = DERIVATIONS.site.exec(readme)
  if (!site) {
    out.push(
      'website/README.md does not write the whole-site budget as "N bytes. Three times T (every file in `dist/`, B bytes": that B is the build the two checkout shapes claim to be, and without it the pair is a figure nothing can disagree with',
    )
  } else if (n(site[3]) !== noTags || n(site[3]) !== clone) {
    out.push(
      `the two checkout shapes are stated as ${noTags} and ${clone} bytes and the whole-site budget derives from a ${n(site[3])}-byte build: the paragraph claims the two shapes are one build — this one — so a pair that agrees with itself a round behind is the drift this case is for, and it is re-derived in the commit that moves those bytes (D192)`,
    )
  }
  return out
}

test('the site’s two checkout shapes are stated as one figure, and the changelog reads no tag', () => {
  const readme = readFileSync(path.join(SITE, 'README.md'), 'utf8')
  const failures = pairFailures(readme)
  assert.deepEqual(failures, [], `website/README.md's two checkout shapes do not hold:\n  ${failures.join('\n  ')}`)

  const changelog = readFileSync(path.join(SITE, 'src', 'lib', 'changelog.mjs'), 'utf8')
  assert.doesNotMatch(
    changelog,
    /node:child_process|['"]tag['"]\s*,\s*['"]--list['"]/,
    'src/lib/changelog.mjs reads the checkout\'s git state again: the site\'s two checkout shapes would build two figures, and the README\'s one-figure pair would be false',
  )
})

test('a pair of checkout shapes a round behind is refused, whether the two agree or not', () => {
  // The reading above is only worth its lines if it disagrees with the two ways the pair goes
  // wrong: one shape moved and the other left behind — the drift the pair was written for, and
  // the one the case above could see — and **both moved together with neither of them the
  // site's own build**, which is the drift it could not. The second is what this repository did
  // on 2026-10-21: a round re-derived the site figure and left the pair where it had been.
  const readme = readFileSync(path.join(SITE, 'README.md'), 'utf8')
  const site = Number(DERIVATIONS.site.exec(readme)[3].replace(/,/g, ''))
  const restate = (noTags, clone) =>
    readme.replace(PAIR, `**${noTags} bytes in a checkout with no tags** and to **${clone} bytes with the tags this repository's own clone carries**`)
  assert.deepEqual(pairFailures(readme), [])

  assert.match(
    pairFailures(restate(site - 1, site)).join('\n'),
    /the two shapes are one build and a re-derivation that moves one of them moves the other/,
    'two shapes stated as different figures are not refused, and no page reads the checkout\'s git state: the two are one build',
  )
  assert.match(
    pairFailures(restate(site - 1, site - 1)).join('\n'),
    /a pair that agrees with itself a round behind/,
    'two shapes stated as the same stale figure are not refused: a pair the site has moved past is a figure a reader is told is this build and is not',
  )
  // And a paragraph rewritten out of the form this case reads is held, not passed over.
  assert.match(
    pairFailures(readme.replace(PAIR, 'and the two shapes build alike')).join('\n'),
    /no longer states the site's two checkout shapes/,
    'a README that stops stating the pair at all is not refused, so the figures a reader sees would be held by nothing',
  )
})

// ---- the word ceilings' own refusals, kept ----------------------------------------------
//
// D188's two numbers are the whole of the gate above, so each of them needs a case that
// proves it can fail: a page one word over the ceiling, and a lead one word over it. A
// fixture page is written word by word (`n` words, one per space) so that "one over" is a
// number this file chooses rather than a page somebody later edits.

/** A fixture page whose `main` carries `leadWords` words in its lead and `pageWords` in all. */
function fixture(leadWords, pageWords) {
  // The two headings are one word each ("t" and "s") and a reader reads them, so they are part
  // of `pageWords`: the fixture's total is exact, which is what makes "one word over" a number
  // this file chose rather than one a later edit moves.
  const rest = pageWords - leadWords - 2
  assert.ok(rest >= 0, 'a fixture page cannot carry fewer words in its body than its headings and its lead')
  return shell(
    '',
    `<main><h1>t</h1><p>${Array.from({ length: leadWords }, () => 'w').join(' ')}</p>` +
      `<h2>s</h2><p>${Array.from({ length: rest }, () => 'w').join(' ')}</p></main>`,
  )
}

test('a page one word over the page ceiling is refused, and one word under it is not', (t) => {
  const budgets = { ...readmeBudgets(), lead: 1000 }
  const dist = distOf(t, {
    'over/index.html': fixture(0, budgets.words + 1),
    'under/index.html': fixture(0, budgets.words),
  })
  const f = wordFailures(dist, budgets)
  assert.equal(f.length, 1, f.join('\n'))
  assert.match(f[0], new RegExp(`^over/index\\.html is ${budgets.words + 1} words; the ceiling is ${budgets.words} words per page`))
})

test('a lead one word over the lead ceiling is refused, and one word under it is not', (t) => {
  const budgets = { ...readmeBudgets(), words: 100000 }
  // The total carries the two headings as well as the lead, so a page whose lead is exactly
  // at the ceiling is `lead + 2` words long and one whose lead is one over is `lead + 3`.
  const dist = distOf(t, {
    'over/index.html': fixture(budgets.lead + 1, budgets.lead + 3),
    'under/index.html': fixture(budgets.lead, budgets.lead + 2),
  })
  const f = wordFailures(dist, budgets)
  assert.equal(f.length, 1, f.join('\n'))
  assert.match(f[0], new RegExp(`^over/index\\.html opens with a ${budgets.lead + 1}-word lead; the ceiling is ${budgets.lead} words`))
})

test('a page with no h2 measures the paragraph under its h1, through the newline between them', (t) => {
  // A page whose `main` has no `h2` at all has no region between two headings, so its lead is
  // the run of paragraphs under its `h1` — the same text D188's ceiling is about, read from
  // the other end. Astro indents the template it compiles,
  // so the `h1` and the paragraph are separated by a text node holding a newline and spaces —
  // which is not a paragraph, and not the end of the opening run. A reader that stopped at it
  // would measure the lead as zero words and the lead ceiling would pass on this page without
  // reading it, so the newline is written into the fixture and the lead is measured through
  // it: one word over the ceiling still fails, one word under it still does not.
  const budgets = { ...readmeBudgets(), words: 100000 }
  const page = (n) =>
    shell('', `<main>\n  <h1>t</h1>\n  <p>${Array.from({ length: n }, () => 'w').join(' ')}</p>\n</main>`)
  assert.deepEqual(pageWords(page(3)), { words: 4, lead: 3 })

  const dist = distOf(t, {
    'over/index.html': page(budgets.lead + 1),
    'under/index.html': page(budgets.lead),
  })
  const f = wordFailures(dist, budgets)
  assert.equal(f.length, 1, f.join('\n'))
  assert.match(f[0], new RegExp(`^over/index\\.html opens with a ${budgets.lead + 1}-word lead`))
})

test('the lead is the h1 -> first h2 region wherever the first h2 sits, and what follows it is not the lead', (t) => {
  // The homepage's shape before P5 put an `h2` above its install block: the hero's section
  // holds the `h1`, and the first `h2` of `main` is a step of a figure that sits outside that
  // section. The whole region between the two headings is the lead — the hero's own paragraph
  // and the blocks that follow it, which is where a page's install commands and chat were —
  // because that is the region the plan defines the lead as, and a page that puts its commands
  // between its `h1` and its first `h2` is a page whose lead they are part of. The run of
  // paragraphs under the `h1` is not the answer here: it is shorter than the region — 38 words
  // where the region is 40 on the `under/` fixture, 39 where it is 41 on `over/` — so a
  // reader that stopped at the hero's own container would pass this fixture and
  // let the page's whole gist through unmeasured. The fixture is not hypothetical: it is the
  // homepage's own shape of two rounds ago, where the hero held the `h1`, the install blocks
  // and the chat sat between it and the flow's step headings, and the container reading
  // measured a 134-word region as 24 words.
  const budgets = { ...readmeBudgets(), words: 100000 }
  const x = (n) => Array.from({ length: n }, () => 'x').join(' ')
  const page = (n) =>
    shell(
      '',
      `<main>\n <section class="hero">\n  <h1>t</h1>\n  <p>${x(n - 2)}</p>\n </section>\n` +
        ` <div class="hero-install"><p>${x(2)}</p></div>\n` +
        ` <ol><li><h2>step</h2></li><li><p>${x(200)}</p></li></ol>\n</main>`,
    )
  // The lead is the region between the headings — the hero's paragraph and the block that
  // follows the hero's section — and the 200 words after the `h2` are a section of their own
  // and are not part of it.
  assert.deepEqual(pageWords(page(6)), { words: 1 + 6 + 1 + 200, lead: 6 })

  const dist = distOf(t, { 'over/index.html': page(budgets.lead + 1), 'under/index.html': page(budgets.lead) })
  const f = wordFailures(dist, budgets)
  assert.equal(f.length, 1, f.join('\n'))
  assert.match(f[0], new RegExp(`^over/index\\.html opens with a ${budgets.lead + 1}-word lead`))
})

test('a page whose first h2 comes before its h1 is measured, not refused', (t) => {
  // The two headings can be the other way round — a page whose `main` opens with a section
  // heading and puts its `h1` under it, which is a shape a template can produce and the
  // reader has no business about. There is no region between them to read (the text in front
  // of a page's `h1` is not its lead), so the lead is the paragraph run under the `h1`, the
  // same answer as a page with no `h2` — the point being that this case measures rather than
  // aborts. A reader that routed every page with an `h2` into the region reader would assert
  // here and take the whole run down with a message about the reader, which is the failure
  // this case is about.
  const budgets = { ...readmeBudgets(), words: 100000 }
  const x = (n) => Array.from({ length: n }, () => 'x').join(' ')
  const page = (n) =>
    shell('', `<main>\n <section>\n  <h2>a section</h2>\n  <h1>t</h1>\n  <p>${x(n - 1)}</p>\n </section>\n</main>`)

  assert.deepEqual(pageWords(page(11)), { words: 2 + 1 + 10, lead: 10 })

  const dist = distOf(t, { 'over/index.html': page(42), 'under/index.html': page(41) })
  const f = wordFailures(dist, budgets)
  assert.equal(f.length, 1, f.join('\n'))
  assert.match(f[0], new RegExp(`^over/index\\.html opens with a ${budgets.lead + 1}-word lead`))
})


test('the three reference pages are exempt from the page ceiling, and their leads are not', (t) => {
  const budgets = readmeBudgets()
  const long = fixture(budgets.lead + 1, budgets.words + 1)
  const dist = distOf(t, {
    'docs/index.html': long,
    'security/index.html': long,
    'changelog/index.html': long,
    'faq/index.html': long,
  })
  const f = wordFailures(dist, budgets)
  // The four pages are over both ceilings, and the three D188 exempts only answer for the
  // lead: five failures, not eight.
  assert.equal(f.length, 5, f.join('\n'))
  for (const route of ['docs', 'security', 'changelog']) {
    assert.ok(
      !f.some((line) => line.startsWith(`${route}/index.html is`)),
      `${route}/index.html is D188's own exemption and the page ceiling was applied to it:\n${f.join('\n')}`,
    )
    assert.ok(
      f.some((line) => line.startsWith(`${route}/index.html opens with a`)),
      `${route}/index.html is not exempt from the lead ceiling, and it was not measured against it:\n${f.join('\n')}`,
    )
  }
  assert.ok(f.some((line) => line.startsWith('faq/index.html is')), 'a page that is not exempt was not measured against the page ceiling')
})

test('the word reader counts what a reader reads, and nothing else', (t) => {
  // The lead is the text between the `h1` and the first `h2`, and not the section under it:
  // a lead that counted the whole page would make the two ceilings one ceiling. The headings
  // are words too — the reader counts the `h1`'s and the `h2`'s own text, which is why the
  // whole page here is seven words and not five.
  const lead = pageWords(shell('', '<main><h1>t</h1><p>one two</p><h2>s</h2><p>three four five</p></main>'))
  assert.deepEqual(lead, { words: 7, lead: 2 })

  // A page with no `h2` has no lead region, and its lead is the paragraphs that follow its
  // `h1` rather than everything the page carries after it.
  assert.deepEqual(
    pageWords(shell('', '<main><h1>t</h1><p>one two</p><div><p>three four five</p></div></main>')),
    { words: 6, lead: 2 },
  )

  // A script or style body is not text a reader reads, a `hidden` subtree is not rendered,
  // and an image's `alt` stands in for the image — the reader of `test/lib/html.mjs`.
  assert.deepEqual(
    pageWords(shell('', '<main><h1>t</h1><style>p{color:red}</style><script>let a=1</script><p hidden>not counted</p><p>one two</p><img src="/x.png" alt="three four"></main>')),
    { words: 5, lead: 2 },
  )

  // A page with no `main` is not this gate's subject: `a11y.mjs` refuses the landmark.
  assert.equal(pageWords(shell('', '<h1>t</h1>')), null)
})

// ---- the words ceiling is derived, and the raise is one page's ---------------------------
//
// The words ceiling stopped being D188's own number on 2026-10-19 (the owner's request), and the
// reason is the privacy statement: it is the page the App Store and Play listings link as a
// privacy policy, so it carries "The software" as well as "The website" and is the one page
// of the site that is not the site's pitch. **The number is derived from that page and from
// nothing else** — its own count on this build plus 25 %, which is D192's headroom figure,
// rounded up — and this section is the pair of rules that derivation owes:
//
//   1. the README's number is that page's own count plus a quarter, so a sentence added
//      there re-derives the line in the commit that adds it, the way a byte that moves
//      re-derives its figure; and
//   2. **every other page of the site stays within D188's own number**, so the raised ceiling is a
//      ceiling one page reaches rather than a licence the other eight can grow into. That
//      half is the one worth having: without it, raising the number for one page would have
//      silently retired D188's promise for every page of the site, and nothing would say so.
//
// Both halves are one function over the built artifact, in the shape the other gates keep
// their refusals in, and each half has a fixture case below that plants its defect and
// watches the named refusal come back.

/**
 * D188's own words-per-page ceiling, the number the raise above it is measured against: 250 as
 * D188 set it, 300 since the owner's feedback of 2026-10-19 asked the homepage for a fourth
 * required block (its benefits section — headed "Why Fugaro?" since the owner's ruling of
 * 2026-10-20 — 294 words with the three before it), **500 since the bold redesign of
 * 2026-10-20**, which is the owner's approved board `website/design-ref/HomeV3.dc.html` built:
 * the homepage is the board's five blocks — the hero, the slider, the run band, the grid of
 * what a run leaves behind, and the closing call to action — and they measure 468 words, so
 * the smallest round ceiling above the page that carries every block its briefs and its boards
 * require is 500 — and **600 since the 2026-10-21 brief**, which asks the homepage's "See it"
 * panel for a slider of four slides led by the console's own screenshots
 * (`docs/website/zoom-diagrams/spec.md`, D251, D252): the three pictures that lead it are the
 * console page's, so they carry the console page's own `alt` texts — one picture has one
 * account of what it shows — and an image's `alt` is text a reader reads, which is the whole
 * of what the four slides add to the page: 461 → 563 words, and the smallest round ceiling
 * above it is 600. D188's own rule is what moves it and not convenience: the number is the
 * ceiling above the page that carries the required blocks, and a page that wants the room has
 * to carry one. website/README.md records every reading beside the words line.
 *
 * **900 since the 2026-10-22 brief**, which replaces the four zoom levels with the owner's four
 * approved design boards (`website/design-ref/zoom-levels/level1.dc.html` … `level4.dc.html`)
 * redrawn as the site's inline SVG: a board's labels are strings the drawing renders, and a
 * string the drawing renders is text a reader reads, so the four levels' labels are words on How
 * It Works the way the slider's `alt` texts are words on the homepage. The page goes 492 → **868
 * words** — the boards' own labels, word for word, which is the whole of what the brief asks it
 * to draw — and the smallest round ceiling above it is 900.
 */
export const D188_WORDS = 900

/** The one page the words ceiling is derived from, by its built URL. */
const WORDS_CEILING_PAGE = '/privacy/'

/**
 * Every way the words ceiling departs from its derivation, or a page departs from D188's own
 * number. `dist` defaults to the same build the byte figures above are derived from — the
 * gates' own build of the site, made by `buildCopy` — so the README's two kinds of number are
 * held to one build rather than to two.
 */
export function wordsCeilingFailures(readme, dist = documentedBuild()) {
  const out = []
  const budgets = readmeBudgets(readme)
  const pages = builtPages(dist)
  const source = pages.find((page) => page.url === WORDS_CEILING_PAGE)
  assert.ok(
    source,
    `the build carries no ${WORDS_CEILING_PAGE} page, so the words ceiling has nothing to be derived from: the number in website/README.md is that page's own count plus a quarter (D192)`,
  )
  const counts = pageWords(source.html)
  assert.ok(counts, `${source.rel} has no \`main\`, so this reader measures nothing and the ceiling below would be a number nothing holds`)

  const derived = Math.ceil(counts.words * 1.25)
  if (budgets.words !== derived) {
    out.push(
      `website/README.md states ${budgets.words} words per page and ${WORDS_CEILING_PAGE} is ${counts.words} words: re-derive the ceiling in the commit that moves the page, because ${counts.words} plus 25 % is ${derived} (D192)`,
    )
  }

  for (const page of pages) {
    if (page.url === WORDS_CEILING_PAGE || DOCUMENT_PAGES.has(page.url)) continue
    const other = pageWords(page.html)
    if (other === null) continue
    if (other.words > D188_WORDS) {
      out.push(
        `${page.rel} is ${other.words} words: the ceiling of ${budgets.words} is the privacy statement's own, and every other page of the site stays within D188's ${D188_WORDS} — a page that needs more is a page that says so in website/README.md`,
      )
    }
  }
  return out
}

test("the words ceiling is the privacy statement's own count plus a quarter, and D188's own number still holds every other page", () => {
  assert.deepEqual(
    wordsCeilingFailures(readFileSync(path.join(SITE, 'README.md'), 'utf8')),
    [],
    'the words ceiling and the pages it governs are not the ones website/README.md states',
  )
})

test('a words ceiling raised without re-deriving it is refused', () => {
  const readme = readFileSync(path.join(SITE, 'README.md'), 'utf8')
  const stated = /^[ \t]*- \*\*Words per page:\*\* ([\d,]+) words/m.exec(readme)
  assert.ok(stated, 'the README states no words-per-page ceiling to raise')
  const raised = readme.replace(`**Words per page:** ${stated[1]} words`, '**Words per page:** 1,500 words')
  assert.match(
    wordsCeilingFailures(raised).join('\n'),
    /re-derive the ceiling in the commit that moves the page/,
    'a words ceiling raised without being re-derived from the page that sets it is not refused',
  )
})

test("a page that grows into the privacy statement's ceiling is refused, and one that stays inside D188's is not", (t) => {
  // A fixture build, because this half is a rule about every page and the real artifact's
  // pages are all inside D188's number today: two pages, the one that sets the ceiling and
  // one that grows into it. The ceiling is derived from the first, so the second's failure is
  // the second rule's and not the first's.
  const dist = distOf(t, {
    'privacy/index.html': fixture(0, 300),
    'inside/index.html': fixture(0, D188_WORDS),
    'over/index.html': fixture(0, D188_WORDS + 1),
  })
  // The five lines `readmeBudgets` reads, with the words line derived from the fixture above
  // and the rest out of the way: this case is about the second rule, so the first must pass.
  const readme = ['- **JavaScript per page:** 0 bytes', '- **Page weight per page:** 1 bytes', '- **Whole site:** 1 bytes', "- **Words in a page's lead:** 100000 words", `- **Words per page:** ${Math.ceil(300 * 1.25)} words`].join('\n')
  const failures = wordsCeilingFailures(readme, dist)
  assert.equal(failures.length, 1, failures.join('\n'))
  assert.match(failures[0], new RegExp(`^over/index\\.html is ${D188_WORDS + 1} words`))
  assert.ok(
    !failures.some((line) => /inside/.test(line)),
    `a page exactly at D188's ${D188_WORDS} words is refused: the ceiling is a ceiling, not a floor at one word less`,
  )
})

