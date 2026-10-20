// website/test/security.mjs — the security page (website plan, Task W1.8; the owner's brief
// of 2026-10-19 and ruling D189).
//
// **The page is four things and nothing else**: the boundary diagram at the top (the owner's
// brief of 2026-10-20), then `SECURITY.md`'s own "In short" section, its own "Reporting a
// vulnerability" section, and a link to the document in the repository. The last three are the
// brief's cut of the page and all three of them are the document's, and D189's answer to the
// one open question — where the summary comes from: **each long page's summary is rendered
// from a file, never written on the page**, and this page's file is `SECURITY.md` itself
// (`docs/website/redesign/reconciliation.md`, D189). The page used to render the document
// whole, behind a contents list built from its headings, and then wrote a summary of its own;
// both are gone, and this file is what holds the page to the new shape.
//
// The diagram is the one thing on the page the page writes, and it is written in the
// document's words: **every label of the drawing is a phrase of `SECURITY.md`**, and each of
// them appears in the document's own "In short" section or in the words the page renders
// beside the drawing. A label that needs a claim the document does not make has nowhere to
// come from, which is why the drawing says "one container per run" and "ends every run with a
// pull request" where the brief's own sketch said "your own cloud project" and "a draft pull
// request you merge": `SECURITY.md` makes neither claim, and a label the policy does not state
// is the defect this gate exists for (D99).
//
// Six things are checked, and each one is the mechanical form of a sentence in the brief:
//
//   1. **The page carries its four regions and no other text** (the brief: "Nothing else").
//      Everything a reader sees inside the page's own `<article>` is its `h1`, the diagram,
//      the summary, the reporting section and the policy link; a paragraph of introduction, a
//      summary of a limit, or a second copy of anything fails here. The rule is applied twice
//      over, because the page can grow two ways: the article's text has to be exactly the five
//      regions' text (so nothing sits between them), and each region has to be the shape the
//      brief gives it — a heading and one list, a heading and the document's paragraphs, the
//      link and the sentence around it (so nothing sits inside one, where the first half of
//      the rule would count it as the region's own text and pass it).
//   2. **The diagram is one named picture, and its labels are the document's own words** (the
//      brief: "ONE inline-SVG diagram (`role="img"` with an `aria-label`, plus the same facts
//      as a short list under it)", and "EVERY label must follow SECURITY.md's 'In short'
//      section"). One `<svg>`, named, carrying the drawing's labels; the facts as one list
//      under it and the two limit lines below that; every label of the picture present in the
//      document's "In short" section or in the words the region carries beside the picture —
//      its heading, those facts and those lines, and not the rest of the page; every word the
//      region carries a phrase of the document; and no script anywhere near it (the brief: "No
//      JavaScript").
//   3. **The summary is `SECURITY.md`'s own "In short" section, bullet for bullet** (the
//      brief: "a short high-level description of the security model and its boundaries";
//      D189: "rendered from a file, never written on the page"). Each rendered bullet is
//      compared with the document's own, through one normalisation — inline markup off,
//      whitespace collapsed — so the page cannot soften a limit the owner states, and cannot
//      lose a space either, and the section is held to a word ceiling so that "short" stays a
//      measured thing. The bullets are the owner's sentences and this file does not shorten
//      them: the ceiling is on the section, and a document edit that grows it re-derives the
//      number in that commit.
//   4. **The reporting section is `SECURITY.md`'s own words** (the brief: "the real reporting
//      instructions, unchanged in substance"). Compared with the document paragraph by
//      paragraph, the same way. The page renders both sections out of
//      `website/content/security.md`, the file `go run ./internal/clidoc` writes from
//      `SECURITY.md`, and this file reads the source document rather than the generator's
//      output: a test written against the thing it is meant to hold agrees with whatever that
//      thing does (plan Review Focus).
//   5. **The link is `SECURITY.md`, at its own path in the repository** (the brief:
//      `https://github.com/dimipaun/fugaro/blob/main/SECURITY.md`). The path is read from the
//      generated file's own `source:` header and the repository from `site.yaml`, both of
//      which `website/src/pages/security.astro` builds the URL from, so the page cannot point
//      at a document this repository does not carry.
//   Both sections carry one thing the word-for-word comparison cannot see: the document's own
//   `[text](#fragment)` cross-references, which the page rewrites to point into `SECURITY.md`
//   in the repository (astro "decision 2", the reason the page keeps its own renderer). Each
//   rendered anchor's text, order and href are pinned to the document's own link and to the
//   heading its fragment names — a fragment dropped or aimed at another heading sends readers
//   to the top of the file, or the wrong passage, and nothing else here would notice (round 4,
//   finding 1).
//   6. **The page's head points at no file the build does not produce** (Global Constraint 9).
//      The one hint the layout composes is the sitemap, and whether the build ships one is
//      read from the resolved `astro.config.mjs` — imported, not pattern-matched, which is the
//      same rule `website/test/readme.mjs` applies to the same file.
//
// **Two readers, and why the text is read the hard way** (round 3 of the review, findings 3 and
// 4). The text of the page is read with `test/lib/html.mjs`'s `verbatimText`, which invents
// nothing between two neighbouring runs of text, and the comparison collapses runs of
// whitespace to one space rather than removing them. Both halves are deliberate and both were
// the bug: the first version of this file read a node's children with `join(' ')` — inventing
// exactly the space the build had removed — and compared the two sides with every space
// deleted, which repairs the same damage a second time. A page whose summary read "is listed
// infollowup.trusted" passed both. `spacing.mjs` is the gate that holds the whole site to that
// defect by name, and the planted-defect case at the end of this file plants it here, so the
// reader and the comparison cannot drift back apart from it.
//
// How it reads the page: the built artifact, `dist/security/index.html`, written by `npm run
// build` — which `npm run check` runs before it runs any file here — through `DIST`, so
// `FUGARO_GATES_DIST` points the whole gate at a planted copy and each of its refusals is
// mutation-proved rather than asserted (the mechanism `budget.mjs` and `fold.mjs` use; the
// case at the end of this file plants them). The regions are found by the marker attributes
// the page's own markup carries (`data-security-page`, `data-security-diagram`,
// `data-security-summary`, `data-security-reporting`, `data-security-policy`).
//
// Two of the cases need a **build** rather than a planted artifact, because the refusals they
// hold are the page's and not the gate's: a document whose summary list grows a paragraph, and
// a reporting section left with nothing under it. Both are made in a copy of the repository
// with `test/lib/build.mjs`'s `buildCopy` — what `origins.mjs` and `console-option.mjs` use —
// and both plants go into the copy's generated `website/content/security.md`, which is the file
// the page reads, because `SECURITY.md` is the owner's and this gate does not edit it.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { DIST, REPO, SITE, findAll, parse, verbatimText } from './lib/html.mjs'
// The builds the two cases below need: a copy of the repository in a temp directory with one
// edit planted in it, built with the site's own Astro (`buildCopy`), because the page's
// refusals are the build's and a refusal nobody has seen fire is a refusal nobody knows the
// shape of.
import { buildCopy } from './lib/build.mjs'
// The page's own slug rule, imported rather than re-implemented: `security.astro` resolves its
// cross-references with this module and this file resolves them again, so the two have to be
// the same rule rather than two approximations of it (the note in `docs.astro`).
import { headingAnchor, headingText } from '../src/lib/github-slug.mjs'

/** The document the page renders, the file it is generated into, and the built page. */
const SOURCE = 'SECURITY.md'
const GENERATED = 'website/content/security.md'
const BUILT = path.join('security', 'index.html')

/** The two sections of the document the page renders, and the ids their headings carry. */
const SUMMARY = 'In short'
const SUMMARY_ID = 'in-short'
const REPORTING = 'Reporting a vulnerability'
const REPORTING_ID = 'reporting-a-vulnerability'

/**
 * The summary's word ceiling, which is the brief's "at most about 8 short bullets" as one
 * number. It is measured against the document's own section — the page adds no words of its
 * own to it — and it is re-derived in the commit that changes the section, the way the
 * README's byte budgets are re-derived in the commit that moves those bytes.
 */
const MAX_SUMMARY_WORDS = 220

/**
 * The words the pointer to the document may spend around its link: "The full policy is … in
 * the repository." is seven, and the ceiling leaves room for the owner to reword it without
 * this file pinning a sentence of the page's own.
 */
const POINTER_WORDS = 12

/**
 * The floor under the drawing's labels, and under the facts listed beside it. The picture
 * draws the trust boundary the document's summary describes — who sends the task, the
 * container and what is in it, what leaves it, and what nothing stops — and these numbers are
 * below the count any drawing of that can carry. Both are floors and not pins: the drawing may
 * gain a label, or the list a fact, without this file changing, and neither number is the
 * picture's size.
 */
const MIN_DIAGRAM_LABELS = 8
const MIN_DIAGRAM_FACTS = 3

// ---------------------------------------------------------------------------------------
// Reading the document, the page, and the text out of either of them.
// ---------------------------------------------------------------------------------------

/** The document's bytes, read from the repository rather than from the build. */
function sourceDocument() {
  return readFileSync(path.join(REPO, SOURCE), 'utf8')
}

/** The repository `site.yaml` names, which the page's link into it is built from. */
function repository() {
  const yaml = readFileSync(path.join(SITE, 'data', 'site.yaml'), 'utf8')
  const m = /^repository:\s*"?https:\/\/github\.com\/([^"\s#]+?)\/?"?\s*(#.*)?$/m.exec(yaml)
  if (!m) throw new Error(`website/data/site.yaml names no repository, so the page has no document to link (${SOURCE})`)
  return `https://github.com/${m[1]}`
}

/** The path the generated file says it was generated from, out of its own header. */
function generatedSource(repo = REPO) {
  const file = path.join(repo, GENERATED)
  if (!existsSync(file)) return null
  const lines = readFileSync(file, 'utf8').split('\n')
  return /^<!--\s*source:\s*(.+?)\s*-->$/.exec(lines[1])?.[1] ?? null
}

/** The built page, or null when the build did not produce one. */
function builtPage(dist) {
  const file = path.join(dist, BUILT)
  return existsSync(file) ? readFileSync(file, 'utf8') : null
}

/**
 * The text a reader sees inside a node. `textRuns` is the shared reader's own answer to what
 * a run of text is — each direct text child, with nothing invented between two neighbours and
 * a block boundary marked as a space — so the words counted below are the words the artifact
 * carries and not the words a reader that joined children with a space would imagine (round 3,
 * finding 4: the space it invented is the space that went missing).
 */
function text(node) {
  return verbatimText(node)
}

/**
 * The text as a reader reads it: runs of whitespace collapsed to one space and the ends
 * trimmed, so a line break, a wrap or an indent cannot differ — and a space that went missing
 * is still a missing space. That last half is the point of not removing whitespace outright:
 * `flatten` in the first version of this file deleted every space on both sides before
 * comparing, which is the same repair as joining a node's children with one, and it hid the
 * defect it was meant to find (round 3, findings 3 and 4).
 */
function prose(s) {
  return s.replace(/\s+/g, ' ').trim()
}

/** The words of a piece of text, the way a reader counts them. */
function words(s) {
  return s.trim().split(/\s+/).filter(Boolean).length
}

/** The element children of a node, in document order: what a region is made of. */
function childElements(node) {
  return (node.children ?? []).filter((child) => child.name !== undefined)
}

/**
 * A copy of a node's subtree with every `<name>` subtree taken out of it. The diagram's
 * labels are text a reader sees, so the page's own text has to be read **without** them: the
 * label rule below asks whether a label is a phrase of the words the page renders *beside* the
 * picture, and a reading that left the picture in would answer that question with the label
 * itself. The copy is read by the same reader as the page's text (`verbatimText`, round 3's
 * block boundaries and all) rather than by a second reader written for the occasion.
 */
function without(node, name) {
  if (node.text !== undefined) return { text: node.text }
  if (node.name === name) return null
  const copy = { name: node.name, attrs: node.attrs, children: [] }
  if (node.raw !== undefined) copy.raw = node.raw
  for (const child of node.children ?? []) {
    const kept = without(child, name)
    if (kept !== null) copy.children.push(kept)
  }
  return copy
}

/**
 * The words a node says itself, with every element child's text taken out: the text a region
 * carries beside the elements the brief allows it. A `<p>` written into the summary, or a
 * second sentence written into the pointer, is what this counts.
 */
function ownWords(node) {
  return words((node.children ?? []).map((child) => (child.name === undefined ? (child.text ?? '') : '')).join(' '))
}

/** One line of the document as a reader reads it, with its inline markup taken off. */
function inlineText(line) {
  return line
    .replace(/^\s*#{1,6}[ \t]+/, '')
    .replace(/^\s*-[ \t]+/, '')
    .replace(/`([^`]*)`/g, '$1')
    .replace(/\[([^\]]*)\]\([^)\s]+\)/g, '$1')
    .replace(/\*\*([^*]+)\*\*/g, '$1')
    .replace(/\*([^*]+)\*/g, '$1')
    .trim()
}

/**
 * `SECURITY.md` as a phrase is looked for in it: its link syntax reduced to the words that
 * carry it, its inline markers off, every run of whitespace collapsed to one space, in lower
 * case. This is the reading `security.astro`'s own `quoted()` compares a label against, and it
 * is here for the same reason: a label that differs from the document in a word, or in a
 * space, is a difference in what the page claims. The page reads
 * `website/content/security.md`, the file `go run ./internal/clidoc` writes from this
 * document; this file reads the document itself, so a label that is a phrase of the generated
 * file and not of the policy shows up here rather than in the build.
 */
function documentPhrases(document) {
  return document
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/[*`]/g, '')
    .replace(/\s+/g, ' ')
    .toLowerCase()
}

/**
 * The document's lines under a `## <heading>`, up to the next heading of the same level or
 * higher — a section the page renders, read from `SECURITY.md` rather than from the
 * generated file or from the page.
 */
function sectionLines(document, heading) {
  const lines = document.split('\n')
  const start = lines.findIndex((line) => line.trim() === `## ${heading}`)
  if (start === -1) {
    throw new Error(`${SOURCE} has no "## ${heading}" heading, so the page this file checks has no such section to render`)
  }
  const rest = lines.slice(start + 1)
  const end = rest.findIndex((line) => /^#{1,2}[ \t]/.test(line))
  return end === -1 ? rest : rest.slice(0, end)
}

/**
 * The document's lines under `## <heading>` as blocks, read the simple way: **one block per
 * line that starts with `- ` (or `* `)**, with the lines that wrap it joined on, and **a line
 * that follows a blank line a block of its own and not a wrap** (`bullet: false`).
 *
 * This reader is deliberately not the page's. `security.astro`'s `bulletLines()` merges a
 * continuation only when it directly follows a bullet or another continuation, and this file
 * used to mirror that rule exactly — a merge whose notion of "a wrapped bullet" was the same on
 * both sides, so a document whose section carried a paragraph under its list was read
 * identically by the page and by the gate, and the page rendered the paragraph inside the last
 * bullet's `<li>` with every check still green (round 5, finding 1). The rule here is the shape
 * a reader sees: a list, its wrapped lines, and anything under a blank line as what it is.
 */
function plainBlocks(document, heading) {
  const blocks = []
  let wrapping = false
  for (const line of sectionLines(document, heading)) {
    const bullet = /^\s*[-*][ \t]+(.*)$/.exec(line)
    if (bullet !== null) {
      blocks.push({ bullet: true, text: bullet[1].trim() })
      wrapping = true
    } else if (line.trim() === '') {
      wrapping = false
    } else if (wrapping && blocks.length > 0) {
      blocks[blocks.length - 1].text += ` ${line.trim()}`
    } else {
      blocks.push({ bullet: false, text: line.trim() })
      wrapping = true
    }
  }
  return blocks
}

/** The blocks of a section that are not bullets: what the page's list has no room for. */
function strayBlocks(document, heading) {
  return plainBlocks(document, heading).filter((block) => !block.bullet).map((block) => block.text)
}

/** The bullets of a section, as a reader reads them. */
function bulletsOf(document, heading) {
  return plainBlocks(document, heading)
    .filter((block) => block.bullet)
    .map((block) => inlineText(block.text))
}

/** A section's own blocks of text, with the markdown left as it is: its bullets, or its
 *  paragraphs when the section is not a list. */
function blocksOf(document, heading, kind) {
  if (kind === 'bullet') return plainBlocks(document, heading).map((block) => block.text)
  return sectionLines(document, heading)
    .join('\n')
    .split(/\n[ \t]*\n/)
    .map((paragraph) => paragraph.replace(/\s+/g, ' ').trim())
    .filter((paragraph) => paragraph !== '')
}

/** The paragraphs of a run of Markdown lines, as a reader reads them. */
function paragraphsOf(lines) {
  return lines
    .join('\n')
    .split(/\n[ \t]*\n/)
    .map((paragraph) => inlineText(paragraph.replace(/\s+/g, ' ').trim()))
    .filter((paragraph) => paragraph !== '')
}

/** The links one line of the document carries, in the order it writes them. */
function linksOf(line) {
  return [...line.matchAll(/\[([^\]]*)\]\(([^)\s]+)\)/g)].map((match) => ({ text: inlineText(match[1]), target: match[2] }))
}

/**
 * The anchors the document's own headings carry, as GitHub slugs them. Read through the slug
 * module the page builds its links with, so the page's rule and this file's rule are one rule
 * rather than two approximations of it.
 */
function documentAnchors(document) {
  return new Set(
    document
      .split('\n')
      .map((line) => /^#{1,6}[ \t]+(.*)$/.exec(line.trim()))
      .filter((match) => match !== null)
      .map((match) => headingAnchor(headingText(match[1]))),
  )
}

/**
 * The anchors the **page itself** renders: the two sections it carries, under the ids its own
 * markup gives their headings. A cross-reference to one of them is a link into this page, not
 * into the document — the section is here, and the page's own anchor is where a reader can
 * follow it. `linkcheck.mjs` holds the other half of that: an internal `#fragment` on a page
 * has to be an `id` of that page, so an anchor named here that the page did not render would
 * fail there as well as here.
 */
const PAGE_ANCHORS = new Set([SUMMARY_ID, REPORTING_ID])

/**
 * Where the page sends a reader for one of the document's own links: the target as the document
 * writes it, with a fragment resolved against the section that answers it. A fragment the page
 * renders itself is the page's own anchor; any other fragment the document carries a heading
 * for is the document in the repository at that heading; and a fragment the document does not
 * carry a heading for has **no target at all** (`null`) — the page renders that reference as
 * its own text, because every link it could build for it would lie: the fragment as written
 * goes nowhere, and the document's own URL lands the reader at the top of the file under a link
 * that reads like all the others. `SECURITY.md` ships one — `[Money](#money)`, whose target is
 * a bold lead in a bullet under "Known limits" and not a heading.
 */
function resolvedTarget(raw, anchors, base) {
  if (!raw.startsWith('#')) return raw
  const fragment = raw.slice(1)
  if (PAGE_ANCHORS.has(fragment)) return `#${fragment}`
  return anchors.has(fragment) ? `${base}#${fragment}` : null
}

/**
 * The page's cross-reference links, pinned to the sections they name (round 4 of the review).
 *
 * The page does not render the document's `[text](#fragment)` links as the document writes
 * them: `security.astro` resolves each one — to the page's own anchor where the page renders
 * the section that fragment names, and otherwise to `SECURITY.md` in the repository at that
 * section — so a reader who follows "Known limits" arrives at the document's own "Known
 * limits" section, and a reader who follows "Reporting a vulnerability" stays on the page they
 * are reading, at the section the page renders below the summary. That resolution is the one
 * part of the page no other check reads: `checkSection` compares the words with the inline
 * markup taken off, and `website/test/linkcheck.mjs` asks only that a fragment which is
 * *present* is a heading. So a rewrite that dropped every fragment, pointed one at a different
 * real heading, or sent the reader to GitHub for a section rendered on the page they are
 * reading, would pass every gate but this one.
 *
 * Both halves are held here: the anchor is the document's own link, text and order, and its
 * href is that link's target resolved the way the page resolves it — the page's own anchor for
 * the fragments the page renders, the document's URL in the repository for the rest, both read
 * through the same slug module the page uses. And a
 * reference the document has no heading for is held to the opposite: it is **not** a link on
 * the page, because the only link the page could build for it would send a reader to the top
 * of the file under a word that promises a section (round 4's fourth finding).
 */
function checkLinks(region, { document, heading, kind, fail, what }) {
  const base = `${repository()}/blob/main/${SOURCE}`
  const anchors = documentAnchors(document)
  const resolved = blocksOf(document, heading, kind)
    .flatMap(linksOf)
    .map((link) => ({ text: link.text, target: link.target, href: resolvedTarget(link.target, anchors, base) }))
  // The references the document writes that name no heading of it: expected as the text the
  // word-for-word comparison already holds, and expected to be nothing but that text.
  const unlinkable = resolved.filter((link) => link.href === null)
  const expected = resolved.filter((link) => link.href !== null)
  const rendered = findAll(region, 'a')

  for (const node of rendered) {
    const seen = prose(text(node))
    const reference = unlinkable.find((link) => prose(link.text) === seen)
    if (reference !== undefined) {
      fail(
        `the ${what} renders ${JSON.stringify(seen)} as a link, and ${SOURCE} writes it as a cross-reference to a section the document has no heading for (${JSON.stringify(reference.target)}): the page renders such a reference as its own text and not as a link, because the fragment it names answers nothing and the document's own URL would land the reader at the top of the file (round 4 of the review)`,
      )
    }
  }

  if (rendered.length !== expected.length) {
    fail(
      `the ${what} carries ${rendered.length} links where ${SOURCE}'s "${heading}" section writes ${expected.length}${unlinkable.length === 0 ? '' : `, the other ${unlinkable.length} of its references naming no heading and so rendering as text`}: the links on the page are the document's own cross-references, and a link added or lost here is one the document does not carry (the owner's brief of 2026-10-19)`,
    )
    return
  }
  for (const [i, node] of rendered.entries()) {
    const href = node.attrs.get('href') ?? ''
    const seen = prose(text(node))
    if (seen !== prose(expected[i].text)) {
      fail(`the ${what}'s link ${i + 1} says ${JSON.stringify(seen)} where ${SOURCE} writes ${JSON.stringify(expected[i].text)}: the page renders the document's own cross-references (the owner's brief of 2026-10-19)`)
    }
    if (href !== expected[i].href) {
      fail(
        `the ${what}'s link ${JSON.stringify(seen)} points at ${JSON.stringify(href)} and not ${JSON.stringify(expected[i].href)}: the page rewrites ${SOURCE}'s own cross-references to point into the document in the repository, and each one has to arrive at the section it names — a fragment dropped, or aimed at another heading, sends a reader to the top of the file or to the wrong passage with every other check still green`,
      )
    }
  }
}

/**
 * What a run of text takes at a size, and the estimate the drawing is laid out against. The
 * labels are set in the reader's own sans (`--font-sans`), whose stack lands on a wide face on
 * more systems than not — DejaVu Sans on most Linux desktops is the widest of them, and about a
 * tenth wider per character than Arial or Inter. `src/components/RunDiagram.astro` lays the How
 * It Works drawing out with the same estimate and refuses an over-wide line at build time; this
 * is the same measurement taken again over the built page.
 */
const CHAR = 0.62

/** The width of a line at a size, at the estimate above. */
const lineWidth = (said, size) => [...said].length * CHAR * size

/**
 * The box a `<text>` of the drawing is drawn in, read off the markup alone: the line's width at
 * the size the node declares, the anchor it is drawn from, and the baseline it sits on. A node
 * without those numbers is a line this check cannot measure, and `null` is what the caller
 * refuses rather than passing.
 */
function drawnBox(node) {
  const size = Number(node.attrs.get('font-size'))
  const x = Number(node.attrs.get('x'))
  const y = Number(node.attrs.get('y'))
  if (![size, x, y].every(Number.isFinite)) return null
  const width = lineWidth(prose(text(node)), size)
  const anchor = node.attrs.get('text-anchor') ?? 'start'
  const x0 = anchor === 'middle' ? x - width / 2 : anchor === 'end' ? x - width : x
  return { size, x, y, width, x0, x1: x0 + width }
}

/**
 * Every label of the drawing, measured against the box it is drawn in: the card whose rect
 * holds the label's baseline, inset by the `data-pad` that card declares, or the drawing's own
 * `viewBox` for a label drawn outside every card. The inset is read on both axes — a card
 * declares the room its contents are drawn in, and a baseline past it is a line on the card's
 * border, whichever edge it is past (`x` for the width of a phrase, `y` for how many lines a
 * card holds and where they sit).
 *
 * The labels of this drawing are hand-placed against fixed card sizes and every one of them is
 * quoted from `SECURITY.md`, which the owner edits — so a phrase that grows a word overruns its
 * card, or one line too many for the card it is drawn in runs past the bottom inset, and the
 * phrase is still a phrase of the document either way. That is why this rule exists rather than
 * the label rule above: the label rule passes a longer phrase the document carries, and the
 * page's byte budget only reports that something moved. The vertical half was added by the
 * review of 2026-10-20, which found two cards drawing their last line below their own inset
 * (the runner's `can read its /proc` 10 units past it, the gateway's last line 4) while the
 * gate measured `x` alone. The How It Works drawing is measured the same way, at build time and
 * over the built page (`website/test/how-it-works.mjs`); here the built page is the whole of
 * it, because the geometry is written by hand in the page rather than generated from the same
 * numbers.
 */
function checkLabelBoxes(picture, drawn, fail) {
  const view = (picture.attrs.get('viewbox') ?? '').trim().split(/\s+/).map(Number)
  const cards = findAll(picture, 'rect').map((el) => ({
    x: Number(el.attrs.get('x')),
    y: Number(el.attrs.get('y')),
    width: Number(el.attrs.get('width')),
    height: Number(el.attrs.get('height')),
    pad: Number(el.attrs.get('data-pad')),
  }))
  for (const [i, node] of drawn.entries()) {
    const what = `the drawing's label ${i + 1}`
    const label = drawnBox(node)
    if (label === null) {
      fail(
        `${what} is drawn without the position or the size it is drawn at, and a label this check cannot measure is a label nothing holds inside its box: every label of the drawing carries its own \`x\`, \`y\` and \`font-size\`, because the size it is drawn at is a length in the picture's own units (the owner's brief of 2026-10-20)`,
      )
      continue
    }
    // The box the label is drawn in: the smallest rect whose own box holds the label's
    // baseline. A label no rect holds is drawn in the open picture, and the picture's own edge
    // is what it has to stay inside.
    const holds = cards
      .filter(
        (card) =>
          [card.x, card.y, card.width, card.height, card.pad].every(Number.isFinite) &&
          label.y >= card.y &&
          label.y <= card.y + card.height &&
          label.x >= card.x &&
          label.x <= card.x + card.width,
      )
      .sort((a, b) => a.width * a.height - b.width * b.height)
    const box = holds[0]
    const left = box === undefined ? view[0] : box.x + box.pad
    const right = box === undefined ? view[0] + view[2] : box.x + box.width - box.pad
    const top = box === undefined ? view[1] : box.y + box.pad
    const bottom = box === undefined ? view[1] + view[3] : box.y + box.height - box.pad
    if (![left, right, top, bottom].every(Number.isFinite)) {
      fail(
        `${what} is drawn in a box this check cannot measure, and a box nothing can measure is a box nothing holds a label inside: the drawing declares the \`viewBox\` its units are in, and every card declares the inset (\`data-pad\`) its contents are drawn at (the owner's brief of 2026-10-20)`,
      )
      continue
    }
    if (label.x0 < left - 0.5 || label.x1 > right + 0.5) {
      fail(
        `${what} says ${JSON.stringify(prose(text(node)))} and is drawn from ${label.x0.toFixed(1)} to ${label.x1.toFixed(1)} units, and the ${box === undefined ? 'drawing' : 'card it is drawn in'} runs ${left.toFixed(1)} to ${right.toFixed(1)}: the picture is laid out for the font the page's stack resolves to on a reader's system (DejaVu Sans and the other wide sans faces are wider per character than Arial or Inter), and a line that does not fit its room is a line over the card's border — or past the picture's own edge. Every label here is hand-placed and every one of them is quoted from ${SOURCE}, which the owner edits, so a phrase that grows a word overruns its box and nothing else in this file would see it (the owner's brief of 2026-10-20)`,
      )
    }
    // And the same inset on the other axis: a card's `data-pad` is the room its contents are
    // drawn in, so a baseline above the top inset or below the bottom one is a line drawn on
    // the card's border. The two cards the review of 2026-10-20 found had room for one line
    // fewer than they drew, which no reading of `x` can see.
    if (label.y < top - 0.5 || label.y > bottom + 0.5) {
      fail(
        `${what} says ${JSON.stringify(prose(text(node)))} and its baseline is drawn at y=${label.y.toFixed(1)}, and the ${box === undefined ? 'drawing' : 'card it is drawn in'} holds a label between y=${top.toFixed(1)} and y=${bottom.toFixed(1)}: a card declares the inset its contents are drawn at (\`data-pad\`) on both axes, and a line drawn past it sits on the card's border — a card with one line more than it has room for, which is what a phrase the owner grows in ${SOURCE} does to a hand-placed drawing. The drawing's own rhythm is the card that holds the lines: a title 26 units below the top edge, the lines 24 apart, and a taller card when a line is added (the owner's brief of 2026-10-20; the review of 2026-10-20)`,
      )
    }
  }
}

/**
 * The boundary diagram, held to the brief's own rules (the owner's brief of 2026-10-20).
 *
 * The rule this gate exists for is the label one: **the picture is one named image, and each
 * of its labels is a phrase of `SECURITY.md`'s own "In short" section or of the words the
 * diagram region carries beside the picture** — its heading, the facts under it and the two
 * lines below them. The drawing is the only place on this page where the page writes
 * anything itself, and a security page is where a sentence the policy does not state does the
 * most damage — so a label that drifts, a claim added or a limit softened, is refused here
 * rather than read as prose (D99). `security.astro` proves the same thing while it builds
 * (`quoted()`); this holds it from the document's side, which is where a label that is a
 * phrase of the generated file and not of the policy is visible.
 *
 * The rest is the shape the brief asks for: one `<svg>` with `role="img"` and a non-empty
 * `aria-label`, the labels inside it, the document's own facts as one list under the picture,
 * the two lines below that, and no script (the brief: "No JavaScript") — and, over the built
 * page, every label measured against the box it is drawn in (`checkLabelBoxes`).
 */
function checkDiagram(region, { document, beside, fail }) {
  const headings = childElements(region).filter((child) => child.name === 'h2')
  if (headings.length !== 1) {
    fail(
      `the diagram carries ${headings.length} <h2> headings where the drawing sits under the document's own sentence for the boundary: the region is that heading and the picture below it (the owner's brief of 2026-10-20)`,
    )
  }

  const figures = childElements(region).filter((child) => child.name === 'figure')
  if (figures.length !== 1) {
    fail(
      `the diagram carries ${figures.length} figures where the boundary diagram is one picture with the facts under it: the brief asks for one inline SVG and one list of the document's own facts (the owner's brief of 2026-10-20)`,
    )
    return
  }
  const figure = figures[0]

  // What is under the picture, and in what order: the drawing, then the facts as one list,
  // then the two lines. The brief names all three, and a list above the picture, or a third
  // line, is text the reader meets in an order nobody chose.
  const inner = childElements(figure)
  const wanted = ['svg', 'ul', 'p', 'p']
  if (inner.length !== wanted.length || inner.some((child, i) => child.name !== wanted[i])) {
    fail(
      `the boundary diagram's figure carries ${inner.map((child) => `<${child.name}>`).join(' ') || 'nothing'} where the brief asks for the picture, the facts as one list under it, and the two lines below that — what the boundaries stop and what nothing stops (the owner's brief of 2026-10-20)`,
    )
  }

  // The picture: one drawing, with a name a screen reader reads before the facts beside it.
  const pictures = findAll(figure, 'svg')
  const labels = []
  if (pictures.length !== 1) {
    fail(
      `the diagram carries ${pictures.length} drawings where the boundary diagram is one picture: the brief asks for ONE inline-SVG diagram (the owner's brief of 2026-10-20)`,
    )
  } else {
    const picture = pictures[0]
    const name = prose(picture.attrs.get('aria-label') ?? '')
    if (picture.attrs.get('role') !== 'img' || name === '') {
      fail(
        `the boundary diagram's drawing carries role=${JSON.stringify(picture.attrs.get('role') ?? '')} and an aria-label of ${JSON.stringify(name)}: the drawing is one image with a name, so a screen reader reads what the picture is before it reads the facts beside it (the owner's brief of 2026-10-20)`,
      )
    } else {
      labels.push({ what: "the drawing's accessible name", text: name })
    }
    const drawn = findAll(picture, 'text')
    for (const [i, node] of drawn.entries()) {
      const said = prose(text(node))
      if (said === '') {
        fail(
          `the drawing's label ${i + 1} is empty: every label of the drawing is a phrase of ${SOURCE}, and a blank one draws nothing a reader can read (the owner's brief of 2026-10-20)`,
        )
        continue
      }
      labels.push({ what: `the drawing's label ${i + 1}`, text: said })
    }
    if (drawn.length < MIN_DIAGRAM_LABELS) {
      fail(
        `the drawing carries ${drawn.length} labels where the boundary diagram draws the trust boundary: the picture is this page's own, and a drawing with ${drawn.length} of them has stopped drawing what the document describes (the owner's brief of 2026-10-20)`,
      )
    }
    checkLabelBoxes(picture, drawn, fail)
  }

  const facts = findAll(figure, 'li')
  if (facts.length < MIN_DIAGRAM_FACTS) {
    fail(
      `the diagram carries ${facts.length} facts under the picture where the brief asks for the same facts as a short list: who sends the task, what the run is, and where the model key sits (the owner's brief of 2026-10-20)`,
    )
  }

  if (findAll(region, 'script').length > 0) {
    fail(
      'the boundary diagram carries a <script>: the drawing is static markup, and the page is read without a script the way every page on this site is (the owner\'s brief of 2026-10-20, and the site\'s one script is `public/scripts/site.js`)',
    )
  }

  // The brief's own rule, by name: every label of the picture appears in the document's
  // "In short" section or in the text the page renders beside the picture. "Beside the
  // picture" is the diagram region with the drawing taken out — the heading over it, the facts
  // under it and the two lines below that — and not the whole page: a label drawn from a
  // sentence that only the reporting section carries, or only the pointer, is a label the page
  // renders *elsewhere*, which is not what the brief asks for and not what this rule says.
  const inShort = documentPhrases(bulletsOf(document, SUMMARY).join(' '))
  const besideText = beside.toLowerCase()
  for (const label of labels) {
    const said = label.text.replace(/[*`]/g, '').replace(/\s+/g, ' ').trim().toLowerCase()
    if (!inShort.includes(said) && !besideText.includes(said)) {
      fail(
        `${label.what} says ${JSON.stringify(label.text)}, which ${SOURCE} does not say: a label of the boundary diagram is a phrase of the document's "${SUMMARY}" section or of the words the page renders beside the drawing, and a label the policy does not state is a security claim the page made up (the owner's brief of 2026-10-20, D99)`,
      )
    }
  }

  // And nothing else in the region: every word it carries — the heading, each fact, each line,
  // each label — is a phrase of the document. The rule above is the brief's and is the
  // narrower one (the picture's labels, against the summary and the page's own words); this is
  // the one `security.astro`'s `quoted()` proves while the page builds, held from the
  // document's side.
  const phrases = documentPhrases(document)
  for (const { what, text: said } of [
    ...headings.map((node) => ({ what: "the diagram's heading", text: prose(text(node)) })),
    ...facts.map((node, i) => ({ what: `the diagram's fact ${i + 1}`, text: prose(text(node)) })),
    ...findAll(figure, 'p').map((node, i) => ({ what: `the diagram's line ${i + 1}`, text: prose(text(node)) })),
    ...labels,
  ]) {
    if (!phrases.includes(said.replace(/[*`]/g, '').replace(/\s+/g, ' ').toLowerCase())) {
      fail(
        `${what} says ${JSON.stringify(said)}, which ${SOURCE} does not say: the page renders the document's words and no others, and the diagram is this page's own drawing written in them (D99)`,
      )
    }
  }
}

// ---------------------------------------------------------------------------------------
// The gate: every way a built security page breaks the four-things rule.
// ---------------------------------------------------------------------------------------

/** The one element carrying a marker attribute, or a failure naming what is missing. */
function marked(doc, marker, fail) {
  const found = findAll(doc, (el) => el.attrs.has(marker))
  if (found.length !== 1) {
    fail(`the page carries ${found.length} "${marker}" regions; the page's own markup is what this gate reads it by, and it is one region`)
    return null
  }
  return found[0]
}

/**
 * Every way the built page breaks the three-things rule, as the sentences a reader of the
 * failure gets. One function over one artifact, so its refusals are kept with planted defects
 * rather than argued (the case below).
 */
export function securityFailures(dist = DIST) {
  const out = []
  const fail = (why) => out.push(`security/: ${why}`)

  const html = builtPage(dist)
  if (html === null) {
    fail(`${BUILT} is not in the artifact: the page this gate checks is the one \`npm run build\` writes, and \`npm run check\` builds before it runs these checks (W1.8)`)
    return out
  }

  let doc
  try {
    doc = parse(html)
  } catch (err) {
    fail(`the page could not be read as HTML: ${err.message}`)
    return out
  }

  const article = marked(doc, 'data-security-page', fail)
  if (article === null) return out

  // 1. The four regions, and nothing else. The `h1` is the page's own title and takes no
  //    marker: it is the whole of what the page says above its first section.
  const h1s = findAll(article, 'h1')
  const diagram = marked(article, 'data-security-diagram', fail)
  const summary = marked(article, 'data-security-summary', fail)
  const reporting = marked(article, 'data-security-reporting', fail)
  const policy = marked(article, 'data-security-policy', fail)

  // The title rule is recorded rather than returned on, and every check below still runs: a
  // second `<h1>` used to stop this function at the line that found it, so a page carrying the
  // one defect also switched off the region balance, both word-for-word comparisons and the
  // policy link — the four checks that would have reported the rest of what was wrong with it
  // (round 4 of the review). Only a missing region, which the checks below read, stops this.
  if (h1s.length !== 1) {
    fail(`the page carries ${h1s.length} <h1> headings and not the one title the brief gives it: the page's own heading is "Security", and a second one is another page's title inside this one (the owner's brief of 2026-10-19, W1.2)`)
  }
  if (h1s.length === 0 || diagram === null || summary === null || reporting === null || policy === null) return out

  const regions = [h1s[0], diagram, summary, reporting, policy]
  if (prose(text(article)) !== prose(regions.map(text).join(' '))) {
    fail(
      `the page carries text outside its title, the boundary diagram, the document's summary, its reporting section and the link to it: the page is those five things and nothing else (the owner's brief of 2026-10-19, and the diagram of 2026-10-20), so a sentence written anywhere else on it is a security claim the document does not make (D99)`,
    )
  }

  // 1b. And each region is the shape the brief gives it: the diagram is its heading and the
  //     picture, the summary is its heading and its one list, the reporting section is its
  //     heading and the document's own paragraphs (compared below), and the pointer is the
  //     link and the few words that point at it. The balance above cannot see a sentence added
  //     *inside* a region — its text is counted either way — so without this rule a paragraph
  //     slipped into the summary, or a second sentence slipped into the pointer, would read as
  //     part of the page's own text and pass.
  for (const { region, what, shape, allowed, own } of [
    { region: diagram, what: 'the boundary diagram', shape: 'its heading and the picture with the facts under it', allowed: ['h2', 'figure'], own: 0 },
    { region: summary, what: 'the summary', shape: 'its heading and its one list', allowed: ['h2', 'ul'], own: 0 },
    { region: reporting, what: "the document's reporting section", shape: 'its heading and the document\'s paragraphs', allowed: ['h2', 'p'], own: 0 },
    { region: policy, what: 'the pointer to the document', shape: 'the link and the one sentence around it', allowed: ['a'], own: POINTER_WORDS },
  ]) {
    const foreign = childElements(region).filter((child) => !allowed.includes(child.name))
    if (foreign.length > 0) {
      fail(
        `${what} carries a <${foreign[0].name}> that is not part of it: ${what} is ${shape}, and an element inside it is text the page wrote that the balance above counts as if the brief had asked for it (the owner's brief of 2026-10-19)`,
      )
    }
    const spoken = ownWords(region)
    if (spoken > own) {
      fail(
        `${what} says ${spoken} words of its own around ${shape}: those words are the page's own, and the page's own text is the one sentence pointing at the document (${own} words at most there; the owner's brief of 2026-10-19)`,
      )
    }
  }

  // 1c. The diagram itself: one named picture, the document's words on it, the facts as a list
  //     under it and the two lines below that. "The words beside the drawing" are the region's
  //     own words with the picture taken out — the heading, the facts and the two lines — and
  //     not the rest of the page: the rule reads the words the drawing sits among, so a label
  //     the page carries only in another region is refused.
  checkDiagram(diagram, {
    document: sourceDocument(),
    beside: prose(text(without(diagram, 'svg'))),
    fail,
  })

  // 2. The summary is the document's own "In short" section, bullet for bullet.
  checkSection(summary, {
    document: sourceDocument(),
    heading: SUMMARY,
    id: SUMMARY_ID,
    fail,
    what: 'summary',
    kind: 'bullet',
    label: 'the page describes the security model in the document\'s own words, and the summary is the file those words live in (D189)',
  })
  checkLinks(summary, { document: sourceDocument(), heading: SUMMARY, kind: 'bullet', fail, what: 'summary' })

  // 3. The reporting section is the document's own, word for word.
  checkSection(reporting, {
    document: sourceDocument(),
    heading: REPORTING,
    id: REPORTING_ID,
    fail,
    what: 'reporting',
    kind: 'paragraph',
    label: `the page renders the document's section and does not reword it, because a reporter follows those words and a paraphrase is a claim the document does not make (D99)`,
  })
  checkLinks(reporting, { document: sourceDocument(), heading: REPORTING, kind: 'paragraph', fail, what: `document's reporting section` })

  // 3b. The summary is short, and it is a list: the ceiling is the brief's "at most about 8
  //     short bullets", and a nested list is a boundary the reader has to assemble.
  const lists = findAll(summary, 'ul')
  const items = findAll(summary, 'li')
  if (lists.length !== 1) {
    fail(`the summary carries ${lists.length} lists; the page's summary is one list of bullets (the owner's brief of 2026-10-19)`)
  } else if (items.some((b) => findAll(b, 'li').length > 0)) {
    fail('the summary carries a nested list: each bullet is one line of the document, and a bullet inside a bullet is a boundary the reader has to assemble')
  }
  const summaryWords = words(text(summary))
  if (summaryWords > MAX_SUMMARY_WORDS) {
    fail(`the summary is ${summaryWords} words long; the page's summary is at most ${MAX_SUMMARY_WORDS} words (the owner's brief of 2026-10-19), and a document edit that grows it re-derives the number in that commit`)
  }

  // 4. The link is the document, at its own path in the repository.
  const links = findAll(policy, 'a')
  const wanted = `${repository()}/blob/main/${SOURCE}`
  if (links.length !== 1) {
    fail(`the page carries ${links.length} links where the document's own path belongs: the page sends a reader to ${SOURCE} in the repository, once (the owner's brief of 2026-10-19)`)
  } else {
    if (links[0].attrs.get('href') !== wanted) {
      fail(`the page links ${JSON.stringify(links[0].attrs.get('href') ?? '')} and not ${wanted}: the page sends a reader to the document it renders, and the repository and the path are read from site.yaml and the generated file's own header rather than typed into the page (W1.8 step 1)`)
    }
    if (prose(text(links[0])) !== prose(SOURCE)) {
      fail(`the page's link says ${JSON.stringify(text(links[0]).trim())} and not the document's own path ${JSON.stringify(SOURCE)}`)
    }
  }
  const declared = generatedSource()
  if (declared === null) {
    fail(`${GENERATED} carries no "source:" header line, so the page cannot say which document it renders: regenerate it with \`go run ./internal/clidoc\` from the repository root`)
  } else if (declared !== SOURCE) {
    fail(`${GENERATED} says it was generated from ${JSON.stringify(declared)}, and the page this file checks is ${SOURCE}'s`)
  }
  return out
}

/**
 * One rendered section against the document's own: the heading, its id, and its text line by
 * line — bullets for the summary, paragraphs for the reporting section. The comparison is
 * over the flattened text, so whitespace cannot differ and the markup the page adds (its
 * `<strong>`, its `<code>`, its rewritten links) is not read as a difference in the words.
 */
function checkSection(region, { document, heading, id, fail, what, kind, label }) {
  const headings = findAll(region, 'h2')
  if (headings.length !== 1 || prose(text(headings[0])) !== prose(heading)) {
    fail(`the ${what} is not under the document's own "${heading}" heading: the page renders that section of ${SOURCE}, heading included (D189)`)
  } else if (headings[0].attrs.get('id') !== id) {
    fail(`the ${what}'s heading carries the id ${JSON.stringify(headings[0].attrs.get('id') ?? '')} and not "${id}": the id is what a link into the section points at`)
  }

  const rendered = findAll(region, kind === 'bullet' ? 'li' : 'p')
  const expected =
    kind === 'bullet' ? bulletsOf(document, heading) : paragraphsOf(sectionLines(document, heading))
  if (rendered.length === 0) {
    fail(`the ${what} carries no ${kind}: ${SOURCE} writes the section's own ${kind}s, and a section with none of them renders nothing`)
  } else if (expected.length === 0) {
    fail(`read no ${kind} out of ${SOURCE}'s "${heading}" section: the document has moved, so this check saw nothing`)
  } else if (rendered.map((node) => prose(text(node))).join('\u0000') !== expected.map(prose).join('\u0000')) {
    fail(`${BUILT} does not render ${SOURCE}'s own ${what}: ${label}`)
  }
}

// ---- the refusals, kept ----------------------------------------------------------------

/** A temp dist holding one built page, and the gate's verdict on it. */
function verdict(t, html) {
  const dist = mkdtempSync(path.join(tmpdir(), 'fugaro-security-'))
  t.after(() => rmSync(dist, { recursive: true, force: true }))
  mkdirSync(path.join(dist, 'security'), { recursive: true })
  writeFileSync(path.join(dist, BUILT), html)
  return securityFailures(dist)
}

/** The good page, with one edit planted in it. The edit has to land: a fixture that silently
 *  changed nothing would make its case pass for the wrong reason. */
function planted(html, re, replacement) {
  const edited = html.replace(re, replacement)
  if (edited === html) {
    throw new Error(`the fixture did not land: ${re} is not in the built page, so the gate it is meant to redden cannot be the one this case proves`)
  }
  return edited
}

/** One edit planted in a copy's generated document, which is the file the page reads. The edit
 *  has to land, for the same reason `planted`'s has. */
function patchGenerated(site, re, replacement) {
  const file = path.join(site, 'content', 'security.md')
  const before = readFileSync(file, 'utf8')
  const after = before.replace(re, replacement)
  if (after === before) {
    throw new Error(`the fixture did not land: ${re} is not in the copy's ${GENERATED}, so the build it is meant to stop cannot be the one this case proves`)
  }
  writeFileSync(file, after)
}

// ---------------------------------------------------------------------------------------

test('the built security page is four things and nothing else', () => {
  assert.deepEqual(securityFailures(DIST), [], `the security page is not the four things the owner's briefs of 2026-10-19 and 2026-10-20 ask for:\n  ${securityFailures(DIST).join('\n  ')}`)
})

test('the summary the page renders is SECURITY.md\'s own "In short" section', () => {
  const document = sourceDocument()
  const bullets = bulletsOf(document, SUMMARY)
  assert.ok(bullets.length >= 4, `read ${bullets.length} bullets out of ${SOURCE}'s "${SUMMARY}" section: the document has moved, so this check saw nothing`)
  assert.ok(bullets.join(' ').length > 200, `the "${SUMMARY}" section of ${SOURCE} is ${bullets.join(' ').length} characters: the section this page renders is the document's, so a stub would make every check in this file vacuous`)

  // The section is a list and nothing else, read by this file's own reader rather than by the
  // page's: a paragraph written under the list — after a blank line, so not a wrapped bullet —
  // is text the page has no room for, and the page's reader used to fold it into the bullet
  // above it (round 5, finding 1). The page stops the build there now; this is the same fact
  // held from the document's side, where it can be seen even when the page's rule changes.
  const stray = strayBlocks(document, SUMMARY)
  assert.deepEqual(
    stray,
    [],
    `${SOURCE}'s "${SUMMARY}" section carries ${stray.length} block(s) that are not bullets (${stray.map((block) => JSON.stringify(block)).join(', ')}): the page renders that section as the list it is, and a paragraph under the list is text with nowhere to go — it stopped the build rather than being rendered into the last bullet's list item until round 5 of the review`,
  )

  assert.deepEqual(securityFailures(DIST), [])
})

test('the summary is rendered from the document, and is not written on the page', () => {
  // D189's rule, held from the other side: the summary lives in `SECURITY.md` and the page
  // renders it, so the page's own source may not carry the summary's sentences. This is the
  // check that notices the shape the owner's ruling rejected — a summary typed into the
  // `.astro` file — which `securityFailures()` cannot see, because the built page is the same
  // either way.
  // The boundary diagram (the owner's brief of 2026-10-20) quotes the summary: two of its
  // bullets are the two lines under the picture, and the drawing's labels are phrases of the
  // summary's own sentences. So this pin is the one that survives that, and it is the property
  // rather than the wording: a summary sentence may appear in the page's source **only where
  // the built page's boundary diagram renders it as its own text** — which is the document's
  // own words, proved to be phrases of `SECURITY.md` while the page builds (`quoted()`). A
  // summary sentence typed into the `.astro` file that the diagram does not draw is refused
  // here exactly as it was before.
  const source = readFileSync(path.join(SITE, 'src', 'pages', 'security.astro'), 'utf8')
  const bullets = bulletsOf(sourceDocument(), SUMMARY)
  assert.ok(bullets.length >= 4, `read ${bullets.length} bullets out of ${SOURCE}'s "${SUMMARY}" section: the document has moved, so this check saw nothing`)

  const html = builtPage(DIST)
  assert.ok(html !== null, `${BUILT} is not in the artifact: run \`npm run check\` from website/ so this check has a page to read`)
  const diagram = findAll(parse(html), (el) => el.attrs.has('data-security-diagram'))[0]
  assert.ok(diagram !== undefined, `the built page carries no "data-security-diagram" region, so nothing here can say which of the summary's sentences the page is allowed to carry`)
  const drawn = prose(text(diagram)).toLowerCase()

  for (const bullet of bullets) {
    const opening = bullet.split(/\s+/).slice(0, 6).join(' ')
    if (!prose(source).includes(prose(opening))) continue
    assert.ok(
      drawn.includes(prose(opening).toLowerCase()),
      `website/src/pages/security.astro carries the document's own summary text (${JSON.stringify(opening)}…) and the built page's boundary diagram does not render it: the summary is rendered from ${SOURCE}'s "${SUMMARY}" section and never written on the page (D189), and the one place the page may carry the document's sentences is the diagram, which is the document's own words drawn (the owner's brief of 2026-10-20)`,
    )
  }
})

test('a document the page cannot render stops the build', (t) => {
  // The page's own readers refuse two shapes of document edit, and the refusal has to be the
  // **build's**: `npm run check` builds the site first, so a document the page cannot render
  // stops there rather than shipping a page with the edit rendered in the wrong place. Both
  // plants are made in the copy's generated document — `website/content/security.md`, the file
  // the page actually reads — because `SECURITY.md` itself is the owner's file and this gate
  // does not edit it.
  //
  // (a) A paragraph written under the summary's list, after a blank line so it is not a wrap.
  // The page's `bulletLines()` used to append any such line to the bullet above it, so the
  // paragraph was rendered inside the last bullet's `<li>` — and this file's reader merged
  // continuations by the same rule, so the two agreed and every check passed (round 5,
  // finding 1).
  const paragraph = buildCopy(
    t,
    ({ site }) => patchGenerated(site, /^(- Out of scope:.*)$/m, '$1\n\nA paragraph beside the list.\n'),
    'fugaro-security-paragraph-',
  )
  assert.notEqual(
    paragraph.status,
    0,
    `a paragraph written under ${SOURCE}'s "${SUMMARY}" list did not stop the build, so the page renders the document's paragraph inside the last bullet's list item:\n${paragraph.output}`,
  )
  assert.match(
    paragraph.output,
    /"## In short" section carries a line that is not a bullet and does not wrap one/,
    `the build failed, but not at the page's own reading of the section: ${paragraph.output}`,
  )
  assert.match(paragraph.output, /A paragraph beside the list/, 'the failure does not name the line the document added, so the person editing the document has to find it themselves')

  // (b) A reporting section with its paragraphs taken out. The page renders that section as
  // the document writes it and has no text of its own to fall back on (D189), so an emptied
  // section built a page with a bare `<h2>` and no instructions under it — the one thing a
  // reporter follows the page for.
  const emptied = buildCopy(
    t,
    ({ site }) => patchGenerated(site, /(## Reporting a vulnerability\n)[\s\S]*?(\n## Supported versions)/, '$1$2'),
    'fugaro-security-emptied-',
  )
  assert.notEqual(
    emptied.status,
    0,
    `an emptied "## ${REPORTING}" section did not stop the build, so the page ships a heading with nothing under it:\n${emptied.output}`,
  )
  assert.match(
    emptied.output,
    /"## Reporting a vulnerability" section carries no paragraph/,
    `the build failed, but not at the page's own reading of the section: ${emptied.output}`,
  )
})

test('a drawn phrase the document no longer carries stops the build', (t) => {
  // The mechanism every label of the boundary diagram rests on: `security.astro`'s `quoted()`
  // refuses a phrase the document does not carry, so a `SECURITY.md` edit that rewrites one of
  // the phrases the picture is drawn from stops the build rather than shipping a drawing that
  // makes a claim the policy no longer makes (D99). The plant is made in the copy's generated
  // document — `website/content/security.md`, the file the page reads — because `SECURITY.md`
  // itself is the owner's file and this gate does not edit it.
  //
  // The phrase planted out is the one the drawing is *named* with: `DIAGRAM_LABEL`, the
  // sentence the picture is announced as, whose words are also one of the facts under it. The
  // failure has to name it, because the person editing the document has to see which sentence
  // the page needs and no longer has.
  const drifted = buildCopy(
    t,
    ({ site }) => patchGenerated(site, /with its permission prompts turned off/g, 'with its permission prompts switched off'),
    'fugaro-security-drifted-',
  )
  assert.notEqual(
    drifted.status,
    0,
    `a phrase the boundary diagram draws, rewritten in ${SOURCE}, did not stop the build, so the page ships a picture drawn from a sentence the policy no longer carries:\n${drifted.output}`,
  )
  assert.match(
    drifted.output,
    /does not say "Fugaro runs a coding agent with its permission prompts turned off, in a fresh cloud container per run, and ends every run with a pull request"/,
    `the build failed, but not at the page's own reading of the drawing's words: ${drifted.output}`,
  )
  assert.match(
    drifted.output,
    /renders it as a label of its boundary diagram/,
    `the failure does not say the phrase is a label of the drawing, so the person editing the document cannot tell what the page needs it for: ${drifted.output}`,
  )
})

test('a wrapped bullet is one bullet to the page', (t) => {
  // The other side of (a), and the reason the fix is a rule about blank lines rather than a ban
  // on the second line of a bullet: `SECURITY.md` wraps its bullets, and a continuation line is
  // part of the bullet above it. The document is planted wrapped in a copy so that a rule
  // which refused every line after a bullet would be red here.
  const wrapped = buildCopy(
    t,
    ({ site }) => patchGenerated(site, /^(- Out of scope:[^\n]*)$/m, '$1\n  and this half of the same bullet.\n'),
    'fugaro-security-wrapped-',
  )
  assert.equal(
    wrapped.status,
    0,
    `a wrapped summary bullet stopped the build, so the page refuses the document's own line breaks:\n${wrapped.output}`,
  )
  const html = builtPage(wrapped.dist)
  assert.ok(html !== null, `the copy built no ${BUILT}, so there is nothing to read the wrapped bullet out of`)
  // The summary region by its own marker, not the page's every list: the menu and the footer
  // carry `<li>`s of their own.
  const summary = findAll(parse(html), (el) => el.attrs.has('data-security-summary'))[0]
  assert.ok(summary !== undefined, `the copy's page carries no "data-security-summary" region, so there is nothing to read the wrapped bullet out of`)
  const items = findAll(summary, 'li')
  assert.equal(items.length, 6, `the page renders ${items.length} bullets where the document writes 6: a wrapped bullet is one bullet and its continuation is not a seventh`)
  assert.match(
    prose(text(items[items.length - 1])),
    /and this half of the same bullet/,
    'the continuation line is not part of the bullet above it, so the page dropped a line the document wrote',
  )
})

test('the gate refuses each planted defect', (t) => {
  const html = builtPage(DIST)
  assert.ok(html !== null, `${BUILT} is not in the artifact: run \`npm run check\` from website/ so this gate has a page to read`)

  // A sentence the page adds to itself: the region balance catches it wherever it sits.
  const extra = planted(html, /(<article\b[^>]*data-security-page[^>]*>)/, '$1\n<p>A friendly note about the model.</p>')
  assert.match(verdict(t, extra).join('\n'), /carries text outside/, 'an extra paragraph on the page is not refused')

  // ---- the boundary diagram (the owner's brief of 2026-10-20) --------------------------
  //
  // The drawing's labels are the one place on this page where the page writes words of its
  // own, so each of the rules that holds them is planted here and seen to fire.

  // A label reworded into a claim the document does not make: the defect this gate exists for.
  // Both halves refuse it — the label is in neither the document's summary nor the page's own
  // text, and it is not a phrase of `SECURITY.md` at all.
  const claimed = planted(html, />the agent </, '>the agent can read every secret <')
  const claim = verdict(t, claimed).join('\n')
  assert.match(claim, /the drawing's label \d+ says "the agent can read every secret"/, 'a drawing label the document does not say is not refused, so the drawing is free to make a security claim of its own')
  assert.match(claim, /which SECURITY\.md does not say/, 'the label is refused for the wrong reason, or not at all')

  // A label drawn from a sentence the page renders, but not beside the picture: the reporting
  // section carries it and the document carries it, so the label rule is the only rule that can
  // refuse it — and it does, because "the words beside the drawing" are the diagram region's own
  // words and not the rest of the page. This is the plant the reading of that phrase is held by:
  // widen it to the whole article and this label passes, which is the defect it is here for.
  const rendered = planted(html, />can read its \/proc </, '>Please do not open a public issue <')
  const faraway = verdict(t, rendered).join('\n')
  assert.match(
    faraway,
    /the drawing's label \d+ says "Please do not open a public issue"/,
    'a label drawn from a sentence the page renders only in another region is not refused, so "the words beside the drawing" is reading the whole page rather than the words the drawing sits among',
  )
  assert.match(
    faraway,
    /is a phrase of the document's "In short" section or of the words the page renders beside the drawing/,
    'the label is refused for the wrong reason, or not at all',
  )

  // A label the drawing has no room for: the phrase is one `SECURITY.md` carries and the page
  // renders beside the picture, so the label rule above and the phrase rule below both pass it
  // — the box it is drawn in is the only rule here that sees it. This is what a longer phrase
  // in the document does to a hand-placed drawing, and it is the defect a phrase the owner
  // grows by a word would ship: a line over the card's border.
  const overwide = planted(html, />the agent </, '>Fugaro runs a coding agent with its permission prompts turned off <')
  assert.match(
    verdict(t, overwide).join('\n'),
    /the drawing's label \d+ says "Fugaro runs a coding agent with its permission prompts turned off" and is drawn from \d+\.\d+ to \d+\.\d+ units, and the card it is drawn in runs/,
    'a label wider than the card it is drawn in is not refused, so a phrase that grows in SECURITY.md overruns the picture with nothing here to notice',
  )

  // A label past its card's inset, vertically — the defect this drawing actually had. The plant
  // moves one line's own `y` and nothing else: the phrase, its size and its `x` are the page's,
  // so the width rule passes it and only the vertical one can refuse it. `352` is inside the
  // runner card's rect (262 to 358) and 10 units past the inset that card declares (16) — the
  // 10 units the last line was drawn past it before the review of 2026-10-20, when the line sat
  // at 328 in a card whose contents end at 342.
  const sunk = planted(html, /(<text[^>]*\sy=")336("[^>]*>can read its \/proc)/, '$1' + '352' + '$2')
  assert.match(
    verdict(t, sunk).join('\n'),
    /its baseline is drawn at y=352\.0, and the card it is drawn in holds a label between y=278\.0 and y=342\.0/,
    'a label drawn below the inset its card declares is not refused, so a card can carry one line more than it has room for and the gate sees nothing',
  )

  // And above the top inset, which is the same rule's other half — here on the boundary box
  // itself, whose own inset (20) is what holds the heading it draws.
  const risen = planted(html, /(<text[^>]*\sy=")166("[^>]*>one container per run)/, '$1' + '140' + '$2')
  assert.match(
    verdict(t, risen).join('\n'),
    /its baseline is drawn at y=140\.0, and the card it is drawn in holds a label between y=150\.0 and y=590\.0/,
    'a label drawn above the inset its box declares is not refused, so the boundary\'s own margin is a number nothing reads',
  )

  // The picture with its name taken off: a screen reader reads the drawing as nothing. The
  // plant is anchored on the drawing's own class, because the layout's own markup carries
  // `aria-label`s of its own (the consent dialog, the sections menu) and an unanchored pattern
  // would take one of those off instead and prove nothing about the drawing.
  const unnamed = planted(html, /(class="boundary-drawing"[^>]*?) aria-label="[^"]*"/, '$1')
  assert.match(verdict(t, unnamed).join('\n'), /role="img" and an aria-label of ""/, 'a drawing with no accessible name is not refused')

  // The picture with its role taken off: the labels become loose text rather than one image.
  const unroled = planted(html, /(class="boundary-drawing"[^>]*?) role="img"/, '$1')
  assert.match(verdict(t, unroled).join('\n'), /role="" and an aria-label/, 'a drawing that is not an image to a screen reader is not refused')

  // The labels gone: the drawing is still there and draws nothing the document describes.
  const unlabelled = planted(html, /<text[^>]*>[^<]*<\/text>/g, '')
  assert.match(verdict(t, unlabelled).join('\n'), /the drawing carries 0 labels/, 'a drawing with no labels is not refused')

  // The facts gone from under the picture: the brief's "the same facts as a short list".
  const unfacted = planted(html, /<ul class="boundary-facts">[\s\S]*?<\/ul>/, '')
  assert.match(verdict(t, unfacted).join('\n'), /where the brief asks for the picture, the facts as one list under it/, 'a diagram with the facts taken out from under the picture is not refused')

  // A second drawing beside the first: the brief asks for one.
  const twice = planted(html, /<figure>/, '<figure><svg role="img" aria-label="A second drawing"></svg>')
  assert.match(verdict(t, twice).join('\n'), /carries 2 drawings where the boundary diagram is one picture/, 'a second drawing in the diagram is not refused')

  // A script in the drawing: the brief's "No JavaScript", and the site loads one first-party
  // file and no other.
  const scripted = planted(html, /(<rect class="boundary-card"[^>]*x="12" y="12")/, '<script>void 0</script>$1')
  assert.match(verdict(t, scripted).join('\n'), /the boundary diagram carries a <script>/, 'a script inside the drawing is not refused')

  // A paragraph slipped into the diagram, where the balance counts it as the region's own text
  // and passes it: the shape rule is what sees this one.
  const inDiagram = planted(html, /(<section data-security-diagram>)/, '$1<p>A friendly note about the model.</p>')
  assert.match(verdict(t, inDiagram).join('\n'), /the boundary diagram carries a <p> that is not part of it/, 'a paragraph inside the diagram is not refused')

  // A second title. This is the one defect that used to switch the gate off: the title rule
  // was a `return`, so a page carrying a second `<h1>` came back with no failure at all and the
  // region balance, both word-for-word comparisons and the policy link never ran (round 4 of
  // the review). The rule is recorded now and the rest of the gate still reads the page, which
  // is what the second assertion holds: the extra heading is also text outside the three
  // regions, and both are named in the same verdict.
  const secondTitle = planted(html, /(<article\b[^>]*data-security-page[^>]*>)/, '$1\n<h1>Security notes</h1>')
  const titles = verdict(t, secondTitle).join('\n')
  assert.match(titles, /carries 2 <h1> headings/, 'a second title on the page is not refused')
  assert.match(titles, /carries text outside/, 'the title rule stopped the gate, so the region balance below it never ran')

  // And the same page with a sentence as well: the gate reports both, where before neither was
  // reported at all.
  const titledParagraph = planted(
    html,
    /(<article\b[^>]*data-security-page[^>]*>)/,
    '$1\n<h1>Security notes</h1>\n<p>A friendly note about the model.</p>',
  )
  const titleAndParagraph = verdict(t, titledParagraph).join('\n')
  assert.match(titleAndParagraph, /carries 2 <h1> headings/, 'a second title beside an added paragraph is not refused')
  assert.match(titleAndParagraph, /carries text outside/, 'the second title hid the added paragraph, so the balance never ran')

  // A paragraph slipped *inside* the summary, which the balance cannot see: its text is
  // counted as the summary's either way.
  const inside = planted(html, /(<section\b[^>]*data-security-summary[^>]*>)/, '$1<p>A friendly note about the model.</p>')
  assert.match(verdict(t, inside).join('\n'), /the summary carries a <p> that is not part of it/, 'a paragraph inside the summary is not refused')

  // A space the build removed at an inline boundary, which is the defect the round-2 reader
  // could not see: it joined a node's children with a space, and the first version of the
  // comparison deleted whitespace on both sides before comparing, so the invented space and
  // the deleted one cancelled and the page passed. The reader here joins verbatim and the
  // comparison collapses whitespace rather than removing it, so the missing space is a
  // difference — and this case is what holds both halves of that (round 3, findings 3 and 4).
  //
  // The plant is anchored inside the summary region, because the same sentence is a fact of
  // the boundary diagram as well now (the diagram quotes the document's own bullets), and an
  // unanchored pattern would glue the drawing's copy and prove this case about the wrong
  // region — the diagram's copy is refused by the diagram's own rule.
  const glued = planted(
    html,
    /(<section data-security-summary>[\s\S]*?)is listed in <code>followup\.trusted<\/code>/,
    '$1is listed infollowup.trusted',
  )
  assert.match(
    verdict(t, glued).join('\n'),
    /does not render SECURITY\.md's own summary/,
    'a summary bullet with the space before its inline element removed is not refused, so the reader or the comparison is repairing the artifact',
  )

  // Every cross-reference fragment dropped: the page still reads correctly, the bullets still
  // match the document word for word, and every link lands at the top of `SECURITY.md` instead
  // of the section it names (round 4, finding 1).
  const unfragmented = planted(html, /blob\/main\/SECURITY\.md#[a-z0-9-]+"/g, '/blob/main/SECURITY.md"')
  assert.match(
    verdict(t, unfragmented).join('\n'),
    /points at "[^"]*SECURITY\.md" and not "[^"]*SECURITY\.md#[a-z0-9-]+"/,
    'a cross-reference with its fragment dropped is not refused, so nothing holds the rewritten links to the sections they name',
  )

  // And one cross-reference aimed at another real heading: the fragment is a heading, so
  // `linkcheck.mjs` is satisfied, and the reader arrives at the wrong passage.
  const retargeted = planted(html, /SECURITY\.md#known-limits"/, 'SECURITY.md#scope"')
  assert.match(
    verdict(t, retargeted).join('\n'),
    /points at "https:\/\/github\.com\/[^"]*SECURITY\.md#scope" and not "[^"]*SECURITY\.md#known-limits"/,
    'a cross-reference aimed at another heading is not refused',
  )

  // The one cross-reference the document has no heading for, rendered as a link anyway: the
  // word reads like every other cross-reference on the page and the link lands at the top of
  // the file, a section away from what it names (round 4, finding 4).
  const moneyLinked = planted(
    html,
    /, Money\)/,
    ', <a href="https://github.com/dimipaun/fugaro/blob/main/SECURITY.md">Money</a>)',
  )
  assert.match(
    verdict(t, moneyLinked).join('\n'),
    /renders "Money" as a link/,
    'a cross-reference the document has no heading for is rendered as a link, which promises a section the link cannot reach',
  )

  // And the mirror: a cross-reference the document *does* have a heading for, rendered as bare
  // text — the link the reader came for is gone, and the words are unchanged.
  const scopeUnlinked = planted(
    html,
    /<a href="https:\/\/github\.com\/dimipaun\/fugaro\/blob\/main\/SECURITY\.md#scope">Scope<\/a>/,
    'Scope',
  )
  assert.match(
    verdict(t, scopeUnlinked).join('\n'),
    /carries 5 links where SECURITY\.md's "In short" section writes 6/,
    'a cross-reference with its link dropped is not refused',
  )

  // The one cross-reference into a section the page *renders itself*, sent to the document
  // anyway: the summary's pointer to "Reporting a vulnerability". The fragment resolves on
  // GitHub too and the section really is there, so `linkcheck.mjs` is satisfied and the words
  // are unchanged — but the reader is sent to the repository for a section rendered a
  // paragraph below the link they clicked, and the page's own `reporting-a-vulnerability`
  // anchor is left linked from nowhere (round 4 of the review).
  const reportingOffPage = planted(
    html,
    /<a href="#reporting-a-vulnerability">Reporting a vulnerability<\/a>/,
    '<a href="https://github.com/dimipaun/fugaro/blob/main/SECURITY.md#reporting-a-vulnerability">Reporting a vulnerability</a>',
  )
  assert.match(
    verdict(t, reportingOffPage).join('\n'),
    /points at "[^"]*SECURITY\.md#reporting-a-vulnerability" and not "#reporting-a-vulnerability"/,
    'a cross-reference into a section the page renders itself is sent to the document, so the page points past its own section to the file it was cut down from',
  )

  // A reworded summary bullet: the one edit a page that renders a file is always tempted by.
  const reworded = planted(html, 'Report a vulnerability through GitHub', 'Report a problem through GitHub')
  assert.match(verdict(t, reworded).join('\n'), /does not render SECURITY\.md's own summary/, 'a reworded summary bullet is not refused')

  // A summary section that is gone.
  const missing = planted(html, /<section\b[^>]*data-security-summary[^>]*>[\s\S]*?<\/section>\s*/, '')
  assert.match(verdict(t, missing).join('\n'), /data-security-summary/, 'a page with no summary section is not refused')

  // A second sentence slipped into the pointer, beside the link: the same hole, one region on.
  const pointed = planted(html, /(rel="noopener">SECURITY\.md<\/a>)/, '$1 Report it privately, never in a public issue.')
  assert.match(verdict(t, pointed).join('\n'), /the pointer to the document says \d+ words of its own/, 'a sentence added to the pointer is not refused')

  // A reworded instruction: the other half of the same rule.
  const instruction = planted(html, 'Report a vulnerability</strong>', 'Report a problem</strong>')
  assert.match(verdict(t, instruction).join('\n'), /does not render SECURITY\.md's own reporting/, 'a reworded reporting instruction is not refused')

  // A reporting section that is gone.
  const noReporting = planted(html, /<section\b[^>]*data-security-reporting[^>]*>[\s\S]*?<\/section>\s*/, '')
  assert.match(verdict(t, noReporting).join('\n'), /data-security-reporting/, 'a page with no reporting section is not refused')

  // A link to something other than the document.
  // The plant is anchored on the pointer's own region: a bare URL pattern would find the
  // summary's cross-references first, and the case would then prove nothing about the link.
  const elsewhere = planted(
    html,
    /(data-security-policy[^<]*<a href=")[^"]*"/,
    '$1https://github.com/dimipaun/fugaro/blob/main/docs/design/v1.md"',
  )
  assert.match(verdict(t, elsewhere).join('\n'), /sends a reader to/, 'a policy link to another file is not refused')
})

test('the page points at no file the build does not produce', () => {
  // The rule is Global Constraint 9 — a page names an artifact that exists — the same rule
  // `website/test/layout.mjs` holds the layout's probe page to. This is the page a reader
  // actually gets, so the head is checked on the built artifact.
  //
  // The sitemap hint is the one pointer the layout composes, and whether the build ships a
  // sitemap is read from the **resolved configuration** — imported, not pattern-matched,
  // which is what `website/test/readme.mjs` does with the same file and for the same
  // reason: a module is what the build runs, and a comment that names an integration is
  // not an integration. A check written over the config's text cannot tell those apart,
  // and the failure it misses is this one: a page pointing at a sitemap that no build
  // produces, which a reader's browser asks for and the host answers with 404.
  return import('../astro.config.mjs').then((module) => {
    const integrations = module.default?.integrations ?? []
    const sitemap = integrations.some((integration) => String(integration?.name ?? integration).includes('sitemap'))
    const html = builtPage(DIST)
    assert.ok(html !== null, `${BUILT} is not in the artifact: run \`npm run check\` from website/ so this check has a page to read`)

    assert.equal(
      /<link[^>]*rel="sitemap"/.test(html),
      sitemap,
      sitemap
        ? 'astro.config.mjs ships a sitemap integration and the built page carries no sitemap hint, so the sitemap the build produces is one no reader is told about'
        : "the built page points at a sitemap the build does not produce: astro.config.mjs's integration list carries none, and a hint that 404s is the download-link defect in a smaller size (Global Constraint 9)",
    )
  })
})
