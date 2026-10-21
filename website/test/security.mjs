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
// Eight things are checked, and each one is the mechanical form of a sentence in the brief:
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
//      section"). One named picture — two `<svg>`s carrying the same words in the same order,
//      one per layout, which is one picture drawn twice (item 8 below) — with the facts as one
//      list
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
//   7. **The page is drawn in the site's bold look** (the owner's brief of 2026-10-21): the
//      same look the owner approved on the homepage and the product pages, which is one set of
//      shapes in `src/styles/base.css` and not a look each page invents. The page's title is
//      the display scale's largest step with the accent's gradient over it, each section
//      heading is the step below, the page's four blocks are the site's card, and the room
//      between them is a step of the site's own spacing scale. This is the one check here
//      that is about how the page looks rather than about what it says, and it is written
//      because the page's shape is held by five gates that read its words: a restyle that
//      dropped the shared classes would leave every one of them green. **The visual match to
//      the owner's boards is the owner's check at the preview** (the redesign's 70a–70c, P9):
//      what this holds is that the page wears the site's shapes, not that it wears them well.
//   8. **The drawing is one picture at two layouts, and the wide one fits the first screen**
//      (the owner's feedback of 2026-10-21: "The image is tall (works well in mobile) but on
//      desktop doesn't fit above the fold, can we do it more horizontally for desktop?"). The
//      tall drawing is the phone's and the wide one is the desktop's: the stylesheet is read to
//      find which of the two it renders outside every media query (the tall one is `display:
//      none`, falling back to the shared class's own rule the way a browser resolves it), the
//      `hidden` attribute has to sit on the drawing the stylesheet turns off, the changeover
//      has to be one width with the tall drawing block below it and the wide one above it, the
//      wide drawing's smallest label has to stay legible in the column it is shown in at that
//      width, and the headline plus the whole wide drawing has to fit 1440 × 800. **The fold
//      itself is the owner's live-browser check** (D187, the redesign's 70b): nothing here can
//      see a viewport, so what this holds is the arithmetic the layout rests on — the column
//      the drawing is given, the drawing's own aspect ratio, and the room above it, each read
//      from the built page's own stylesheet and the drawing's own `viewBox`.
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
import { DIST, REPO, SITE, cssDeclarations, cssRules, findAll, parse, verbatimText } from './lib/html.mjs'
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
function checkLabelBoxes(picture, drawn, fail, where = 'the drawing') {
  const view = (picture.attrs.get('viewbox') ?? '').trim().split(/\s+/).map(Number)
  const cards = findAll(picture, 'rect').map((el) => ({
    x: Number(el.attrs.get('x')),
    y: Number(el.attrs.get('y')),
    width: Number(el.attrs.get('width')),
    height: Number(el.attrs.get('height')),
    pad: Number(el.attrs.get('data-pad')),
  }))
  for (const [i, node] of drawn.entries()) {
    const what = `${where}'s label ${i + 1}`
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

// ---------------------------------------------------------------------------------------
// The drawing's two layouts (the owner's feedback of 2026-10-21).
// ---------------------------------------------------------------------------------------

/**
 * The two layouts the boundary drawing is drawn in, by the class that marks each `<svg>`. The
 * wide one is the run read left to right, which is what a desktop shows; the tall one is the
 * same run read top to bottom at a phone's own width, which is what a phone shows. The owner's
 * feedback of 2026-10-21 is why there are two: the tall drawing "works well in mobile", but "on
 * desktop doesn't fit above the fold", so a desktop asks for "a horizontal layout of the same
 * drawing". `what` is what the refusals below call each of them by, so a failure names the
 * drawing a reader would have to look at.
 */
const LAYOUTS = [
  { key: 'wide', klass: 'boundary-drawing-wide', what: 'the wide drawing' },
  { key: 'tall', klass: 'boundary-drawing-tall', what: 'the tall drawing' },
]

/**
 * The first screen the owner's feedback names: a 1440-wide desktop, 800 tall, with the headline
 * over the drawing inside it. **The fold itself is still the owner's check on a real browser**
 * (the redesign's 70b, D187: no test in this repository can see a viewport). What this file
 * reads is the arithmetic the layout rests on, all of it off the built page's own stylesheet:
 * the room between the top of that screen and the drawing, the width the page gives the drawing,
 * and the height the drawing's own `viewBox` sets it at.
 */
const FIRST_SCREEN = { width: 1440, height: 800 }

/**
 * The smallest label a reader reads, in px. The width at which the two drawings change over is
 * derived from it — the wide drawing is shown from the width at which its own smallest label
 * reaches it, which is the whole of what the changeover buys — so a wide drawing nobody can read
 * is the defect this floor exists for, and it is the same floor the How It Works drawing's wide
 * layout is held to (`website/test/how-it-works.mjs`).
 */
const MIN_LABEL_PX = 12

/** The width a media query's conditions name, in px, or `null` for conditions that name no width
 *  or name two that disagree. The built stylesheet writes `max-width: 70rem` as
 *  `(width<=70rem)`, so the comparison and its operands are what is read rather than a form of
 *  words. `website/test/how-it-works.mjs` reads the run drawing's queries this way, off the same
 *  parser (`test/lib/html.mjs`'s `cssRules`), because the width alone cannot say which drawing a
 *  reader gets: a query read from the wrong side is the same width and another page. */
function queryWidth(conditions) {
  let found = null
  for (const [, minimum, maximum, kind, value, unit] of (conditions ?? '').matchAll(
    /(min-)?(max-)?width\s*(:|<=|>=|<|>)\s*([\d.]+)(rem|px)/g,
  )) {
    const at = Number(value) * (unit === 'rem' ? 16 : 1)
    if (kind !== ':') found = at
    else if (maximum) found = at
    else if (minimum) found = at
    else found = null
  }
  return found
}

/** Which side of the width a media query's condition is on: `'below'` for one that holds at the
 *  widths up to the one it names (`max-width`), `'above'` for one that holds from that width up
 *  (`min-width`), and `null` for a condition this check cannot place. */
function querySide(conditions) {
  let side = null
  for (const [, minimum, maximum, kind] of (conditions ?? '').matchAll(
    /(min-)?(max-)?width\s*(:|<=|>=|<|>)\s*([\d.]+)(rem|px)/g,
  )) {
    const here =
      kind === '>=' || kind === '>' || (kind === ':' && minimum)
        ? 'above'
        : kind === '<=' || kind === '<' || (kind === ':' && maximum)
          ? 'below'
          : null
    if (here === null || (side !== null && side !== here)) return null
    side = here
  }
  return side
}

/**
 * Every `display` a built stylesheet gives one class, in the order the cascade has them, each
 * with the media query it sits in: the conditions as written, the width they name and the side
 * they read from. Only a rule whose **subject** is the class counts — the last compound of each
 * comma-separated alternative — so a rule that names the class as an ancestor (the shared
 * `.boundary-drawing` rule, or anything above it in a descendant selector) is not read as a
 * declaration about this layout. `website/test/spacing.mjs` reads a class's own `display` the
 * same way, for the same reason: a declaration belongs to the selector's subject.
 */
function displayDeclarations(css, klass) {
  const out = []
  for (const rule of cssRules(css)) {
    const subject = (rule.selector ?? '')
      .split(',')
      .map((part) => part.trim().split(/[\s>+~]+/).filter(Boolean).pop() ?? '')
    if (!subject.some((compound) => new RegExp(`\\.${klass}(?![\\w-])`).test(compound))) continue
    const display = cssDeclarations(rule.body ?? '').find(([property]) => property === 'display')?.[1]
    if (display === undefined) continue
    const conditions = (rule.conditions ?? []).join(' ')
    out.push({ display: display.trim(), query: conditions, at: queryWidth(conditions), side: querySide(conditions) })
  }
  return out
}

/** The `display` in force for a class outside every media query — the value a reader whose width
 *  no query of the page's holds gets — or `null` when no unqueried rule declares one. */
function unconditionalDisplay(css, klass) {
  return displayDeclarations(css, klass).filter((rule) => rule.query === '').at(-1) ?? null
}

/** A `display` declaration, as the sentence a refusal names it by. */
function described(rule) {
  return `\`display: ${rule.display}\` ${rule.query === '' ? 'outside every media query' : `in \`${rule.query}\``}`
}

/** A selector list split on its top-level commas: a comma inside `()` or `[]` is part of the
 *  selector and not a separator (the site writes `:where(p, ul, ol)` in its prose rule). */
function selectorParts(selector) {
  const parts = []
  let depth = 0
  let start = 0
  for (let i = 0; i < selector.length; i += 1) {
    const ch = selector[i]
    if (ch === '(' || ch === '[') depth += 1
    else if (ch === ')' || ch === ']') depth -= 1
    else if (ch === ',' && depth === 0) {
      parts.push(selector.slice(start, i))
      start = i + 1
    }
  }
  parts.push(selector.slice(start))
  return parts
}

/**
 * A length as px at a viewport: the forms the page's own stylesheet writes — a bare zero, a
 * token, a number with a unit, and a `clamp()` of the three. A `vw` is a different number at the
 * first screen than at the width a type step was written for, which is why the viewport is part
 * of the reading. `null` for a value this check cannot turn into pixels, which the callers report
 * rather than comparing a first screen against a guess. `em` is read at the root, the same 16 px
 * `lengthPx` and the drawing's own geometry are read at.
 */
function lengthAtViewport(css, value, viewport) {
  const v = value.trim()
  const clamp = /^clamp\(([\s\S]+)\)$/.exec(v)
  if (clamp !== null) {
    const parts = selectorParts(clamp[1])
    if (parts.length !== 3) return null
    const px = parts.map((part) => lengthAtViewport(css, part, viewport))
    if (px.some((one) => one === null)) return null
    return Math.min(Math.max(px[0], px[1]), px[2])
  }
  const resolved = v.replace(/var\(--([\w-]+)\)/g, (whole, name) => {
    const declared = new RegExp(`--${name}:\\s*([^;}]+)`).exec(css)
    return declared === null ? whole : declared[1].trim()
  })
  if (/^0[a-z%]*$/.test(resolved)) return 0
  const length = /^(-?[\d.]+)(rem|px|em|vw)$/.exec(resolved)
  if (length === null) return null
  const unit = length[2]
  const number = Number(length[1])
  if (unit === 'rem' || unit === 'em') return number * 16
  if (unit === 'vw') return (number / 100) * viewport
  return number
}

/** The value a built stylesheet declares for one property of one selector, or `null` when no
 *  rule for that selector declares one. Every rule for the selector is read in the stylesheet's
 *  own order and the last declaration wins: the minifier splits a class's declarations across
 *  rules (`.display-md` carries its `line-height` in the shared display rule and its `font-size`
 *  in its own), so a reading that stopped at the first rule would miss the half it came for. A
 *  rule that names the selector as one of several in a list is a rule about it, so the
 *  alternatives are compared one at a time. */
function declaredValue(css, selector, property) {
  const wanted = selector.replace(/\s+/g, '')
  let found = null
  for (const rule of cssRules(css)) {
    const alternatives = selectorParts(rule.selector ?? '').map((part) => part.replace(/\s+/g, ''))
    if (!alternatives.includes(wanted)) continue
    for (const [name, value] of cssDeclarations(rule.body ?? '')) if (name === property) found = value
  }
  return found
}

/** A shorthand's four sides as the values it writes: `a` is all four, `a b` is vertical and
 *  horizontal, `a b c` is top, horizontal, bottom. Read for `padding` and for `margin`, which
 *  are written the same way. */
function boxSides(value) {
  const parts = value.trim().split(/\s+/)
  if (parts.length === 1) return [parts[0], parts[0], parts[0], parts[0]]
  if (parts.length === 2) return [parts[0], parts[1], parts[0], parts[1]]
  if (parts.length === 3) return [parts[0], parts[1], parts[2], parts[1]]
  return [parts[0], parts[1], parts[2], parts[3]]
}

/** A unitless factor a stylesheet declares — a line height — as a number, or `null` for a value
 *  this check cannot read. The site's type scale writes them as tokens
 *  (`line-height: var(--leading-display)`), so the token is resolved before the value is read. */
function factorOf(css, value) {
  if (value === null || value === undefined) return null
  const resolved = value.trim().replace(/var\(--([\w-]+)\)/g, (whole, name) => {
    const declared = new RegExp(`--${name}:\\s*([^;}]+)`).exec(css)
    return declared === null ? whole : declared[1].trim()
  })
  const number = Number(resolved)
  return Number.isFinite(number) ? number : null
}

/**
 * The width the page gives the drawing at a viewport, out of its own stylesheet: the page's
 * ceiling for the column (`--page-max`), the gutter inside it (the article's own padding, which
 * is what `main` does not add because this page's article takes it over) and the padding of the
 * card the figure sits on. The drawing is `width: 100%` of that box, so this is the width it is
 * scaled into — the number the label floor and the fold below are both taken at. `null` when any
 * part of it cannot be turned into pixels, which the caller reports rather than measuring against
 * a guess.
 */
function drawingColumn(css, viewport) {
  const px = (value) => (value === null ? null : lengthAtViewport(css, value, viewport))
  const padding = declaredValue(css, '[data-security-page]', 'padding')
  const card = declaredValue(css, '.card', 'padding')
  const ceiling = px(declaredValue(css, '[data-security-page]', 'max-width'))
  const gutter = padding === null ? null : px(boxSides(padding)[1])
  const inset = card === null ? null : px(boxSides(card)[1])
  if (ceiling === null || gutter === null || inset === null) return null
  return Math.min(viewport, ceiling) - 2 * gutter - 2 * inset
}

/**
 * The room between the top of a first screen and the top of the drawing, out of the built page's
 * own stylesheet: the site header's box and the control inside it, the title band's padding and
 * the headline that fills it, the step between the page's two blocks, the card's padding, and the
 * section heading over the picture with the room under it. Every part is a declaration of the
 * site's or of the page's own — `base.css` is inlined into the page, so the display scale, the
 * card and the header are read from the same stylesheet as the page's own rules — and every part
 * is turned into pixels at the viewport of the screen being measured, because a `clamp()` with a
 * `vw` step in it is not the same number at 1440 as at the width it was written for.
 *
 * The header is the site's chrome and not this page's, so its height is read from the two things
 * the site declares for it: `.site-header`'s own padding and border, and the `min-height` of the
 * tallest control in it, the nav's own disclosure. **A part this check cannot read is a refusal
 * and never a zero**: a header measured as nothing is a fold claim about a page nobody has.
 */
function roomAbove(css, viewport) {
  const px = (value) => (value === null ? null : lengthAtViewport(css, value, viewport))
  const total = (parts) => (parts.some((part) => part === null || !Number.isFinite(part)) ? null : parts.reduce((a, b) => a + b, 0))
  const sides = (selector, property) => boxSides(declaredValue(css, selector, property) ?? '')

  // The header: its own padding-block on both sides, its bottom border, and the row inside it.
  const headerPadding = px(declaredValue(css, '.site-header', 'padding-block'))
  const border = (declaredValue(css, '.site-header', 'border-bottom') ?? '').trim().split(/\s+/)[0]
  const header = total([headerPadding, headerPadding, border === undefined ? null : px(border), px(declaredValue(css, '.site-nav-group>details>summary', 'min-height'))])

  // The title band: its top padding, the headline it holds at the size the display scale sets it
  // at this viewport, and its bottom padding.
  const hero = sides('[data-security-page] .security-hero', 'padding')
  const title = factorOf(css, declaredValue(css, '.display-xl', 'line-height'))
  const titleSize = px(declaredValue(css, '.display-xl', 'font-size'))
  const band = total([px(hero[0]), title === null || titleSize === null ? null : titleSize * title, px(hero[2])])

  // The step between the two blocks, the card's own padding, and the section heading: its own
  // height at this viewport and the room below it.
  const gap = px(declaredValue(css, '[data-security-page]', 'gap'))
  const card = sides('.card', 'padding')
  const heading = factorOf(css, declaredValue(css, '.display-md', 'line-height'))
  const headingSize = px(declaredValue(css, '.display-md', 'font-size'))
  const margin = sides('[data-security-page] h2', 'margin')
  return total([
    header,
    band,
    gap,
    px(card[0]),
    heading === null || headingSize === null ? null : headingSize * heading,
    px(margin[2]),
  ])
}

/**
 * The two layouts as the built page renders them: **one picture drawn twice and read once** (the
 * owner's feedback of 2026-10-21). The rules are the four things the arrangement has to be for
 * that to hold, and each is read off the page's own markup and stylesheet:
 *
 *   1. **Each layout is switched by the page's stylesheet.** Both classes carry a `display`, and
 *      one of the two is `none` outside every media query while the other is not: a drawing the
 *      stylesheet never turns off is the same picture rendered twice, and a drawing that is off
 *      everywhere is a layout nothing shows.
 *   2. **The drawing the stylesheet turns off is the drawing marked `hidden`.** `hidden` is what
 *      a reader whose stylesheet never arrives gets, so the attribute and the CSS have to say the
 *      same thing; a `hidden` drawing the stylesheet renders anyway is an attribute that draws
 *      nothing.
 *   3. **They change over at one width, and the tall one is the one below it.** A phone gets the
 *      drawing at its own scale rather than a shrunk desktop one, and the width is read from
 *      below (`max-width`) — a query naming the same width from above shows the tall drawing to a
 *      desktop and the shrunk wide one to the phone it cannot be read on.
 *   4. **The wide drawing is readable where it is shown, and it fits the first screen.** Its
 *      smallest label is at least `MIN_LABEL_PX` in the narrowest column it is shown in, and the
 *      room above it plus its own height is inside a 1440 × 800 screen — the owner's ask, which
 *      is the whole reason a desktop layout exists.
 */
function checkDrawingLayout(drawings, css, fail) {
  const wide = drawings.find((drawing) => drawing.key === 'wide')
  const tall = drawings.find((drawing) => drawing.key === 'tall')
  if (wide === undefined || tall === undefined) return

  // 1. What the stylesheet says outside every media query: the tall drawing is off, the wide one
  //    is on, and the class both layouts carry is what renders a drawing at all. The value in
  //    force for a layout is its own class's declaration where it writes one and the shared
  //    `.boundary-drawing` rule under it — which is how the page is written, and how a reading
  //    that demanded a declaration per layout would refuse the stylesheet the run drawing is
  //    built to as well (`website/test/how-it-works.mjs`).
  const shared = unconditionalDisplay(css, 'boundary-drawing')
  const own = {
    wide: unconditionalDisplay(css, 'boundary-drawing-wide'),
    tall: unconditionalDisplay(css, 'boundary-drawing-tall'),
  }
  const plain = { wide: own.wide ?? shared, tall: own.tall ?? shared }
  if (plain.wide === null || plain.tall === null) {
    fail(
      `the page's stylesheet declares no \`display\` for ${plain.wide === null ? 'the wide drawing' : 'the tall drawing'} outside a media query — neither on its own class nor on the \`boundary-drawing\` class both layouts carry — so which of the two a reader at a width no query names gets is the browser's own rendering of an \`<svg>\` rather than the page's: the two drawings are one picture with exactly one of them rendered, and that is a rule the page's stylesheet has to make (the owner's feedback of 2026-10-21)`,
    )
  } else {
    if (plain.tall.display !== 'none') {
      fail(
        `the tall drawing is ${described(plain.tall)} outside every media query, and the tall drawing is the layout the changeover turns on for a phone: leaving it rendered where no query holds is both layouts at once — the picture drawn twice and read twice (the owner's feedback of 2026-10-21)`,
      )
    }
    if (plain.wide.display === 'none') {
      fail(
        `the wide drawing is ${described(plain.wide)} outside every media query, and the wide drawing is what a desktop is: turned off where no query holds, a reader at a width no query names is left with no drawing at all — both layouts off at once is the picture drawn nowhere (the owner's feedback of 2026-10-21)`,
      )
    }
  }

  // 2. And the attribute agrees with the stylesheet: `hidden` on the drawing that is off.
  for (const layout of LAYOUTS) {
    const hidden = layout.key === 'wide' ? wide.picture.attrs.has('hidden') : tall.picture.attrs.has('hidden')
    const switchedOff = plain[layout.key]?.display === 'none'
    if (hidden && !switchedOff) {
      fail(
        `${layout.what} carries the \`hidden\` attribute and the page's stylesheet renders it (${plain[layout.key] === null ? 'it declares no display for it outside a media query' : described(plain[layout.key])}): \`hidden\` is what a reader whose stylesheet never arrives gets, so the attribute and the stylesheet have to say the same thing, and an attribute the page's own CSS overrides draws nothing (the owner's feedback of 2026-10-21)`,
      )
    }
    if (!hidden && switchedOff) {
      fail(
        `${layout.what} is ${described(plain[layout.key])} and does not carry the \`hidden\` attribute: the drawing the stylesheet turns off is the one marked \`hidden\`, so that a reader whose stylesheet never arrives is left with the layout that carries the whole picture rather than with both of them or with none (the owner's feedback of 2026-10-21)`,
      )
    }
  }

  // 3. The changeover: one width, read from below, the tall drawing on at it and the wide off.
  const switches = new Set()
  for (const layout of LAYOUTS) {
    for (const rule of displayDeclarations(css, layout.klass)) {
      if (rule.query !== '' && rule.at !== null && rule.side !== null) switches.add(rule.at)
    }
  }
  const at = switches.size === 1 ? [...switches][0] : null
  if (at === null) {
    fail(
      `the page's stylesheet ${switches.size === 0 ? 'has no media query that declares either drawing, so the two layouts never change over' : `switches the two drawings at ${switches.size} different widths (${[...switches].map((px) => `${Math.round(px)} px`).join(', ')})`}: the changeover is one width, the one a phone is met with the tall drawing below and a desktop the wide one above (the owner's feedback of 2026-10-21)`,
    )
  } else {
    const tallShown = displayDeclarations(css, 'boundary-drawing-tall')
    const wideShown = displayDeclarations(css, 'boundary-drawing-wide')
    if (!tallShown.some((rule) => rule.display === 'block' && rule.at === at && rule.side === 'below')) {
      fail(
        `the tall drawing is ${tallShown.filter((rule) => rule.query !== '').map(described).join(', ') || 'not declared inside a media query'}, and a phone is met with the tall drawing rather than with the shrunk wide one: the query at ${Math.round(at)} px is the one that shows it, read from below — a query naming the same width from above shows the tall drawing to a desktop and the shrunk wide one to the phone it cannot be read on (the owner's feedback of 2026-10-21)`,
      )
    }
    if (!wideShown.some((rule) => rule.display === 'none' && rule.at === at && rule.side === 'below')) {
      fail(
        `the wide drawing is ${wideShown.filter((rule) => rule.query !== '').map(described).join(', ') || 'not declared inside a media query'}, and it is turned off below the same width the tall one is turned on below: both layouts on at once is the picture drawn twice and read twice, and a query naming the same width from above turns the wide one off for the desktop it is drawn for (the owner's feedback of 2026-10-21)`,
      )
    }

    // 4a. The floor: the smallest label of the wide drawing, in the narrowest column the page
    //     gives it — the column at the width the two change over at.
    const box = wide.viewBox.split(/\s+/).map(Number)
    const column = drawingColumn(css, at)
    const sizes = findAll(wide.picture, 'text').map((node) => Number(node.attrs.get('font-size')))
    const smallest = sizes.filter(Number.isFinite).sort((a, b) => a - b)[0]
    if (column === null) {
      fail(
        `the column the page gives the drawing at ${Math.round(at)} px — the width the two layouts change over at, and the narrowest viewport the wide drawing is shown at — cannot be read out of the page's own stylesheet (\`[data-security-page]\`'s \`max-width\` and padding, and the card's own padding): that column is where the wide drawing's label size is taken, and a width this check cannot turn into pixels is a floor it cannot take (the owner's feedback of 2026-10-21)`,
      )
    } else if (!Number.isFinite(box[2]) || box[2] <= 0 || smallest === undefined) {
      fail(
        `the wide drawing declares \`viewBox="${wide.viewBox}"\` and ${sizes.length} label sizes this check cannot read, so the size its labels are rendered at is not a number anything here can take: the drawing's units are what its labels scale by, and an unreadable one is a floor nothing holds (the owner's feedback of 2026-10-21)`,
      )
    } else if (smallest * (column / box[2]) < MIN_LABEL_PX) {
      fail(
        `the wide drawing's smallest label is ${smallest} units, which is ${(smallest * (column / box[2])).toFixed(2)} px in the ${Math.round(column)} px column the page gives the drawing at ${Math.round(at)} px — the narrowest viewport it is shown at: a label under ${MIN_LABEL_PX} px is a label a reader cannot read, and the wide drawing is shown from the width its own labels fit at or it is shown to a reader who cannot read it (the owner's feedback of 2026-10-21)`,
      )
    }

    // 4b. And the fold: the room above the drawing plus the drawing itself, inside the owner's
    //     1440 × 800 screen. All four numbers are the page's own — the room and the column out of
    //     its stylesheet, the height out of the drawing's own `viewBox`.
    const room = roomAbove(css, FIRST_SCREEN.width)
    const across = drawingColumn(css, FIRST_SCREEN.width)
    if (room === null || across === null) {
      fail(
        `the room above the drawing and the column it is drawn in at ${FIRST_SCREEN.width} px cannot be read out of the page's own stylesheet: the header's padding and border and the control inside it, the title band's padding and the headline's size, the step between the page's blocks, the card's padding and the section heading are what the fold is measured against, and one of them is a value this check cannot turn into pixels (the owner's feedback of 2026-10-21)`,
      )
    } else if (Number.isFinite(box[3]) && box[3] > 0) {
      const height = across * (box[3] / box[2])
      if (room + height > FIRST_SCREEN.height) {
        fail(
          `the drawing is drawn ${box[2]} × ${box[3]} units, which the ${Math.round(across)} px column renders ${Math.round(height)} px tall, and the page carries ${Math.round(room)} px of header, title band, step, card padding and section heading above it: the headline and the whole drawing do not fit a ${FIRST_SCREEN.width} × ${FIRST_SCREEN.height} first screen — the layout the owner asked for is one a desktop reads whole, which is why the wide drawing exists at all (the owner's feedback of 2026-10-21, D187: the fold itself is the owner's check on a real browser, and this is the arithmetic it rests on)`,
        )
      }
    } else {
      fail(
        `the wide drawing declares \`viewBox="${wide.viewBox}"\` with no height this check can read, so nothing here can say whether it fits a first screen: the drawing's own \`viewBox\` is its shape, and the fold is a claim about that shape (the owner's feedback of 2026-10-21)`,
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
    return []
  }
  const figure = figures[0]

  // What is under the picture, and in what order: the drawing, then the facts as one list,
  // then the two lines. The brief names all three, and a list above the picture, or a third
  // line, is text the reader meets in an order nobody chose.
  const inner = childElements(figure)
  const wanted = ['svg', 'svg', 'ul', 'p', 'p']
  if (inner.length !== wanted.length || inner.some((child, i) => child.name !== wanted[i])) {
    fail(
      `the boundary diagram's figure carries ${inner.map((child) => `<${child.name}>`).join(' ') || 'nothing'} where the brief asks for the picture — one drawing, one \`<svg>\` for each of its two layouts — the facts as one list under it, and the two lines below that: what the boundaries stop and what nothing stops (the owner's brief of 2026-10-20; the owner's feedback of 2026-10-21)`,
    )
  }

  // The picture: one drawing at two layouts, each with the name a screen reader reads before the
  // facts beside it, and the two of them carrying the same words — the same picture, so that
  // whichever layout the width gives a reader, the drawing says the same thing.
  const pictures = findAll(figure, 'svg')
  const labels = []
  const drawings = []
  if (pictures.length !== LAYOUTS.length) {
    fail(
      `the diagram carries ${pictures.length} drawings where the boundary diagram is one picture drawn twice — a tall layout a phone reads top to bottom and a wide one a desktop reads left to right — with the stylesheet rendering exactly one of them at a time: the brief asks for ONE inline-SVG diagram, and the owner's feedback of 2026-10-21 asks for its desktop layout beside the phone's (the owner's brief of 2026-10-20; the owner's feedback of 2026-10-21)`,
    )
  } else {
    for (const layout of LAYOUTS) {
      const found = pictures.filter((picture) => classList(picture).has(layout.klass))
      if (found.length !== 1) {
        fail(
          `the diagram carries ${found.length} drawings marked \`${layout.klass}\` where the picture is one drawing at two layouts: ${layout.what} is ${
            layout.key === 'wide'
              ? 'the run read left to right, which is what a desktop shows'
              : "the run read top to bottom at a phone's own width, which is what a phone shows"
          } (the owner's feedback of 2026-10-21)`,
        )
        continue
      }
      const picture = found[0]
      const drawing = { ...layout, picture, viewBox: (picture.attrs.get('viewbox') ?? '').trim(), drawn: findAll(picture, 'text') }
      const name = prose(picture.attrs.get('aria-label') ?? '')
      if (picture.attrs.get('role') !== 'img' || name === '') {
        fail(
          `${layout.what} carries role=${JSON.stringify(picture.attrs.get('role') ?? '')} and an aria-label of ${JSON.stringify(name)}: each layout of the drawing is one image with a name, so a screen reader reads what the picture is before it reads the facts beside it, whichever layout the width renders (the owner's brief of 2026-10-20)`,
        )
      } else {
        labels.push({ what: `${layout.what}'s accessible name`, text: name })
      }
      for (const [i, node] of drawing.drawn.entries()) {
        const said = prose(text(node))
        if (said === '') {
          fail(
            `${layout.what}'s label ${i + 1} is empty: every label of the drawing is a phrase of ${SOURCE}, and a blank one draws nothing a reader can read (the owner's brief of 2026-10-20)`,
          )
          continue
        }
        labels.push({ what: `${layout.what}'s label ${i + 1}`, text: said })
      }
      if (drawing.drawn.length < MIN_DIAGRAM_LABELS) {
        fail(
          `${layout.what} carries ${drawing.drawn.length} labels where the boundary diagram draws the trust boundary: the picture is this page's own, and a drawing with ${drawing.drawn.length} of them has stopped drawing what the document describes (the owner's brief of 2026-10-20)`,
        )
      }
      checkLabelBoxes(picture, drawing.drawn, fail, layout.what)
      drawings.push(drawing)
    }

    // The two layouts are one picture: the same accessible name, and the same words in the same
    // order. A label that moved in one of them and not the other is a claim the document makes in
    // one layout and not the other, which is the one thing a reader must not be able to get
    // depending on the width of their window.
    if (drawings.length === LAYOUTS.length) {
      const said = (drawing) => drawing.drawn.map((node) => prose(text(node))).join(' ')
      const [first, second] = drawings
      if (said(first) !== said(second)) {
        fail(
          `${first.what} and ${second.what} do not say the same thing: the first reads ${JSON.stringify(said(first))} and the second ${JSON.stringify(said(second))} — they are one picture drawn twice, so the same labels in the same order, or a reader gets one claim on a phone and another on a desktop (the owner's feedback of 2026-10-21)`,
        )
      }
      const names = drawings.map((drawing) => prose(drawing.picture.attrs.get('aria-label') ?? ''))
      if (names[0] !== names[1]) {
        fail(
          `the two layouts of the drawing carry different accessible names (${names.map((name) => JSON.stringify(name)).join(' and ')}): the drawing is one image with one name, and the name is what a screen reader reads instead of the labels, so two names are two pictures (the owner's brief of 2026-10-20)`,
        )
      }
    }
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

  // The two layouts come back to the caller, which is where the rest of what they are is read:
  // which of them the stylesheet renders, at which width, and at what size (`checkDrawingLayout`).
  return drawings
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
  const drawings = checkDiagram(diagram, {
    document: sourceDocument(),
    beside: prose(text(without(diagram, 'svg'))),
    fail,
  })

  // 1d. And the two layouts of that picture: one of them rendered at a time, the tall one below
  //     the width they change over at, the wide one above it, and the wide one readable and
  //     inside the first screen the owner's feedback of 2026-10-21 names. The drawing's own
  //     geometry is `checkLabelBoxes`' business above; this is the page's stylesheet and the
  //     arithmetic the desktop layout rests on.
  checkDrawingLayout(drawings, pageCSS(html, dist), fail)

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

// ---------------------------------------------------------------------------------------
// The page in the site's bold look (the owner's brief of 2026-10-21).
// ---------------------------------------------------------------------------------------

/** The CSS a built page carries: its inline `<style>` blocks, and any first-party stylesheet
 *  it links. Astro inlines today (`build.inlineStylesheets: 'always'`) and this reader does
 *  not depend on which of the two the build emits — `website/test/base-styles.mjs` reads the
 *  same pair the same way. The tokens are in it, so a step of the spacing scale is read from
 *  the artifact rather than from a second copy of the number here. */
function pageCSS(html, dist = DIST) {
  const inline = [...html.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((m) => m[1])
  const linked = [...html.matchAll(/<link[^>]*rel="stylesheet"[^>]*href="(\/[^"]+)"/g)].map((m) =>
    readFileSync(path.join(dist, m[1]), 'utf8'),
  )
  return [...inline, ...linked].join('\n')
}

/** An element's class tokens, as the set the built page carries. A class is matched on the
 *  whole token, because the page's own class and the site's are one attribute. */
function classList(el) {
  return new Set((el.attrs.get('class') ?? '').split(/\s+/).filter(Boolean))
}

/** The rule a built stylesheet carries for one selector, or null. The selectors are compared
 *  with their whitespace removed, because the minifier rewrites it (`[data-security-page] h1`
 *  and `[data-security-page]h1` are one selector). */
function ruleFor(css, selector) {
  const wanted = selector.replace(/\s+/g, '')
  return cssRules(css).find((rule) => rule.selector.replace(/\s+/g, '') === wanted) ?? null
}

/** A length token's value in px, out of the stylesheet that declares it — or null when the
 *  stylesheet does not declare it, which is a failure the caller reports rather than a zero
 *  it silently compares with. */
function tokenPx(css, name) {
  const m = new RegExp(`--${name}:\\s*([\\d.]+)(rem|px)`).exec(css)
  return m ? Number(m[1]) * (m[2] === 'rem' ? 16 : 1) : null
}

/** A length as px: a bare zero, a token reference, or a number with a unit — or null when the
 *  value is neither, which is a failure the caller reports rather than a zero it compares with.
 *  `rem` and `em` are read at the 16 px root, the same root `tokenPx` and the drawing's own
 *  geometry are read at. */
function lengthPx(css, value) {
  const v = value.trim()
  if (/^0[a-z%]*$/.test(v)) return 0
  const token = /^var\(--([a-z0-9-]+)\)$/.exec(v)
  if (token !== null) return tokenPx(css, token[1])
  const len = /^(-?[\d.]+)(rem|px|em)$/.exec(v)
  return len === null ? null : Number(len[1]) * (len[2] === 'px' ? 1 : 16)
}

/** One selector's weight in the cascade, as the triple a browser compares: ids, then classes,
 *  attributes and pseudo-classes, then type selectors. `:where(…)` counts as nothing — that is
 *  what the wrapper is for — and it is the reason this is read at all: `base.css` gives every
 *  paragraph a margin below through `:where(p, …)`, which is worth nothing, so the page's own
 *  `margin: 0` on the pointer is the declaration in force and a reading without specificity
 *  would report the site's margin as room the page had already taken away. */
function specificity(selector) {
  const s = selector.replace(/:where\([^)]*\)/g, '')
  return [
    (s.match(/#[\w-]+/g) ?? []).length,
    (s.match(/\.[\w-]+|\[[^\]]*\]|::?[\w-]+/g) ?? []).length,
    (s.match(/(?:^|[\s>+~,(])[a-z][\w-]*/g) ?? []).length,
  ]
}

function compareSpec(a, b) {
  for (let i = 0; i < a.length; i += 1) if (a[i] !== b[i]) return a[i] - b[i]
  return 0
}

/** Whether a rule's selector can match the block itself. A selector is read one alternative at
 *  a time, `:where(…)`/`:is(…)` unwrapped first (the site's prose rule is written as a wrapper
 *  around a list of tags), and the alternative reaches the block when its subject — the last
 *  compound, the part that says what the rule is about — names the block's marker attribute, a
 *  class the block carries, or is a bare type selector for the block's tag. A selector with an
 *  ancestor (`[data-security-policy] p`) is a rule about something *inside* the block and not
 *  about the block: reading it as the block's own margin would report the room inside a card as
 *  room between two cards. */
function reaches(block, selector) {
  const unwrapped = selector.replace(/:where\(([^)]*)\)|:is\(([^)]*)\)/g, ' $1 $2 ')
  return unwrapped.split(',').some((part) => {
    const compounds = part.trim().split(/[\s>+~]+/).filter(Boolean)
    const subject = compounds[compounds.length - 1] ?? ''
    // A subject that names a region is about **that** region and no other: the four blocks share
    // the `card` class, so a reading that stopped at the class would take `[data-security-policy]
    // .card` — the pointer's own `margin: 0` — for the summary's, and the doubled rhythm the
    // summary carries would be measured against the pointer's declaration and pass.
    const named = [...subject.matchAll(/\[(data-security-[\w-]+)\]/g)].map(([, name]) => name)
    if (named.length > 0) return named.includes(block.marker)
    if ([...classList(block.node)].some((klass) => new RegExp(`\\.${klass}(?![\\w-])`).test(subject))) return true
    return compounds.length === 1 && subject === block.node.name
  })
}

/** The vertical margin a block carries out of the built stylesheet, as `[edge, value]` pairs for
 *  the edges something declares. The winner per edge is the cascade's own: the more specific
 *  rule wins, and between two of one weight the later one does. A block nothing declares a
 *  margin for has none of its own — which is the answer for a `<section>`, and is why the
 *  pointer is the block that has to say `margin: 0` out loud. */
function verticalMargin(css, block) {
  const held = { top: null, bottom: null }
  const offer = (edge, value, spec, order) => {
    const now = held[edge]
    const weight = now === null ? 1 : compareSpec(spec, now.spec)
    if (now === null || weight > 0 || (weight === 0 && order >= now.order)) held[edge] = { value, spec, order }
  }
  cssRules(css).forEach((rule, index) => {
    if (!reaches(block, rule.selector)) return
    const spec = specificity(rule.selector)
    let top
    let bottom
    for (const [property, value] of cssDeclarations(rule.body)) {
      if (property === 'margin') {
        const parts = value.trim().split(/\s+/)
        top = parts[0]
        bottom = parts.length > 2 ? parts[2] : parts[1] ?? parts[0]
      } else if (property === 'margin-top') {
        top = value
      } else if (property === 'margin-bottom') {
        bottom = value
      }
    }
    if (top !== undefined) offer('top', top, spec, index)
    if (bottom !== undefined) offer('bottom', bottom, spec, index)
  })
  return ['top', 'bottom'].filter((edge) => held[edge] !== null).map((edge) => [edge, held[edge].value])
}

/**
 * Every way the built page departs from the site's bold look, as the sentences a reader of
 * the failure gets. One function over one artifact, so its refusals are kept with planted
 * defects rather than argued (the case below).
 *
 * The look itself is `src/styles/base.css`'s section 2c — the display scale, the gradient, the
 * card, the eyebrow, the pill and the step badge — and this gate reads the page against it:
 * **the page wears the site's shapes rather than shapes of its own**, which is the whole point
 * of a redesign that is meant to make the site look like one thing. So the rules below name
 * the shared classes by name, and the rules a class is worth are read out of the page's own
 * stylesheet (a class the stylesheet does not carry is a name that draws nothing, which is how
 * a rename in `base.css` would leave this page unstyled and every other gate green).
 *
 * The page's words are not this gate's business: `securityFailures` above holds the page to
 * the document, and the two are deliberately separate readings — a restyle must be able to
 * change nothing a reader reads.
 */
export function boldLookFailures(dist = DIST) {
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
  const css = pageCSS(html, dist)

  // The reading is scoped to the page's own `<article>`, exactly as `securityFailures`' is: the
  // layout draws chrome of its own around every page, and a heading the layout adds is markup
  // this page cannot change. Reading the whole document would report the site's chrome as this
  // page's hierarchy and demand `display-md` on a heading the page does not write.
  const article = findAll(doc, (el) => el.attrs.has('data-security-page'))[0]
  if (article === undefined) {
    fail('the page carries no `data-security-page` article, so there is no page here to read the bold look off: the article is the page\'s own markup, and `securityFailures` above reports its absence (the owner\'s brief of 2026-10-21)')
    return out
  }

  // The shared shapes have to be the stylesheet's and not only the markup's: a page that names
  // a class no rule carries is a page drawn by the browser's defaults.
  for (const [selector, what] of [
    ['.display-xl', "the display scale's largest step"],
    ['.display-md', "the display scale's middle step"],
    ['.gradient-text', "the accent's gradient over a headline's words"],
    ['.card', 'the card the site draws its blocks on'],
  ]) {
    if (ruleFor(css, selector) === null) {
      fail(`the page's stylesheet carries no \`${selector}\` rule, so ${what} draws nothing here: the bold look is one set of shapes in \`src/styles/base.css\` for the whole site, and a page that wears a name the stylesheet does not carry is a page the browser draws (the owner's brief of 2026-10-21)`)
    }
  }

  // 1. The title: the display scale's largest step, with the accent's gradient over the whole
  //    of it. The gradient is a span inside the heading, which is how `Hero.astro` splits its
  //    headline — and the whole word and not part of it, because this page's title is one word.
  const titles = findAll(article, 'h1')
  if (titles.length !== 1) {
    fail(`the page carries ${titles.length} <h1> headings, so the bold look's headline has no one title to be drawn on: the display scale is the page's title and nothing else (the owner's brief of 2026-10-21)`)
  } else {
    const title = titles[0]
    const classes = title.attrs.get('class') ?? ''
    if (!classList(title).has('display-xl')) {
      fail(`the page's title carries class ${JSON.stringify(classes)} and not the display scale's largest step (\`display-xl\`): the bold look's headline is the site's own display step — oversized, tightly tracked, at the heaviest weight the system stack draws — and a title left to the browser's \`h1\` is the page the redesign replaced (the owner's brief of 2026-10-21)`)
    }
    const gradient = findAll(title, (el) => classList(el).has('gradient-text'))
    const said = prose(text(title))
    if (gradient.length !== 1) {
      fail(`the page's title carries ${gradient.length} \`gradient-text\` spans where the bold look draws the headline's words in the accent's gradient, once: the gradient is the accent the redesign moved to the logo's own hue, and a headline without it is the page's title in one flat tone (the owner's brief of 2026-10-21)`)
    } else if (prose(text(gradient[0])) !== said) {
      fail(`the gradient covers ${JSON.stringify(prose(text(gradient[0])))} of the page's title ${JSON.stringify(said)}: the accent is the whole headline's, so the page's one word is not half gradient and half plain (the owner's brief of 2026-10-21)`)
    }
  }

  // 2. Every section heading under the title is the display scale's middle step, so the page
  //    has the board's hierarchy — the title at the top step, the sections below it — rather
  //    than three browser `h2`s.
  const headings = findAll(article, 'h2')
  if (headings.length === 0) {
    fail('the page carries no <h2>, so the hierarchy the bold look draws — a title and the sections under it — is a title over one flat block of text (the owner\'s brief of 2026-10-21)')
  }
  for (const heading of headings) {
    if (!classList(heading).has('display-md')) {
      fail(`a section heading of the page carries class ${JSON.stringify(heading.attrs.get('class') ?? '')} and not the display scale's middle step (\`display-md\`): the page's sections are set from the site's type scale, one step under its title, and a heading left to the browser's \`h2\` is a section that does not read as one (the owner's brief of 2026-10-21)`)
    }
  }

  // 3. The page's four blocks are the site's card — the same box the homepage's run band and
  //    "what you get" grid are drawn on. The regions are the ones `securityFailures` reads the
  //    page by, and a missing one is that gate's failure rather than this one's.
  const blocks = []
  for (const [marker, what] of [
    ['data-security-diagram', 'the boundary diagram'],
    ['data-security-summary', "the document's summary"],
    ['data-security-reporting', "the document's reporting section"],
    ['data-security-policy', 'the pointer to the document'],
  ]) {
    const found = findAll(article, (el) => el.attrs.has(marker))
    if (found.length !== 1) continue
    blocks.push({ marker, what, node: found[0] })
    if (!classList(found[0]).has('card')) {
      fail(`${what} carries class ${JSON.stringify(found[0].attrs.get('class') ?? '')} and not the site's card (\`card\`): every block of the bold look is drawn on the same box — one step above the ground, a hairline border and the larger radius — so a block of this page is a card like the homepage's blocks, and not a shape of its own (the owner's brief of 2026-10-21)`)
    }
  }

  // 4. The room between the page's blocks is a step of the site's spacing scale, and a
  //    generous one: the redesign's rhythm is the scale's own steps rather than numbers a page
  //    picks, and the page's blocks are set apart by at least the scale's `--space-lg`. The
  //    declaration is read off the page's own container rule, which is the one place the room
  //    between all four blocks is declared.
  const page = ruleFor(css, '[data-security-page]')
  const pageDecls = page === null ? [] : cssDeclarations(page.body)
  const declared = (property) => pageDecls.find(([name]) => name === property)?.[1]
  const display = declared('display')
  const gap = declared('gap')
  const step = gap === undefined ? null : /^var\(--(space-[a-z0-9]+)\)$/.exec(gap)
  const gapPx = step === null ? null : tokenPx(css, step[1])
  if (step === null) {
    fail(`the page sets the room between its blocks with ${gap === undefined ? 'no declaration this gate can read' : JSON.stringify(gap)} and not with a step of the site's spacing scale: the redesign's rhythm is the scale's — the room one block gives the next is a step, so two blocks' room is comparable — and a number here is the page inventing a rhythm of its own (the owner's brief of 2026-10-21)`)
  } else {
    const px = tokenPx(css, step[1])
    const floor = tokenPx(css, 'space-lg')
    if (px === null || floor === null) {
      fail(`the page sets the room between its blocks at \`--${step[1]}\`, and the stylesheet does not declare that step's value, so this gate cannot measure the room the page gives its blocks (the owner's brief of 2026-10-21)`)
    } else if (px < floor) {
      fail(`the page sets the room between its blocks at \`--${step[1]}\` (${px}px) where the bold look's blocks are set apart generously: the floor is the scale's \`--space-lg\` (${floor}px), and a page whose blocks sit closer than that is the page the redesign replaced (the owner's brief of 2026-10-21)`)
    }
  }

  // 5. And the container is one the `gap` means something in. A `gap` is room between blocks
  //    only in a flex or a grid box, and only in the direction the blocks are laid out in: on a
  //    block container the declaration is inert — nothing is spaced — and a page whose blocks
  //    sit flush against each other would read as one that sets its rhythm. The reading is
  //    here because the rule above cannot see the difference: it reads the declaration, and
  //    this one reads whether the declaration draws anything.
  const flexColumn = /^(column|column-reverse)$/.test(declared('flex-direction') ?? '')
  if (display !== 'flex' && display !== 'grid') {
    fail(`the page's container is ${display === undefined ? 'a box whose `display` the page does not declare' : JSON.stringify(display)} where the page's rhythm is a \`gap\`: a \`gap\` is room between blocks only in a flex or a grid container, and on a block container the declaration spaces nothing, so the page's blocks sit flush against each other while the page reads as one that sets the room between them (the owner's brief of 2026-10-21)`)
  } else if (display === 'flex' && !flexColumn) {
    fail(`the page's container is a flex row (${JSON.stringify(declared('flex-direction') ?? 'row')}) where its blocks are a column: a row lays the four blocks out side by side and turns the \`gap\` into room between columns, so the page has no vertical rhythm at all (the owner's brief of 2026-10-21)`)
  }

  // 6. And no block adds room of its own. A margin on a flex item is added to the gap, so a
  //    block that kept the margin it carried while the page was a plain flow gives the gap
  //    twice: the page says `--space-xl` between two blocks and a reader gets that step plus
  //    the block's margin. This is the doubled rhythm `website/test/base-styles.mjs`'s
  //    `homeRhythmFailures` refuses on the homepage, read the same way — the room between two
  //    blocks is one step of the scale and not that step plus a block's own margin — and it is
  //    the one defect the rule above cannot see, because the gap it reads is correct either
  //    way. A block whose margin is zero is the page keeping its own rhythm; the pointer is the
  //    one that has to say so, because it is a `<p>` and `base.css` gives every paragraph a
  //    margin below.
  for (const block of blocks) {
    for (const [edge, value] of verticalMargin(css, block)) {
      const px = lengthPx(css, value)
      if (px === null) {
        fail(`${block.what} carries a \`margin-${edge}\` of ${JSON.stringify(value)} this gate cannot measure, so it cannot say whether the block adds room of its own on top of the \`gap\` the container sets between blocks (the owner's brief of 2026-10-21)`)
      } else if (px !== 0) {
        const total = gapPx === null ? '' : `, so the room between two blocks renders at ${gapPx + px}px where the page says one step of the scale`
        fail(`${block.what} carries a \`margin-${edge}\` of ${JSON.stringify(value)} (${px}px) of its own on top of the ${gap === undefined ? 'room the container sets between blocks' : JSON.stringify(gap)}: a margin on a flex item is added to the gap${total} — the doubled rhythm \`website/test/base-styles.mjs\`'s \`homeRhythmFailures\` refuses on the homepage, where the room between two blocks is one step of the scale and not that step plus a block's own margin (the owner's brief of 2026-10-21)`)
      }
    }
  }

  return out
}

// ---- the refusals, kept ----------------------------------------------------------------

/** A temp dist holding one built page, and the gate's verdict on it — `securityFailures` by
 *  default, or the checker a case is proving. */
function verdict(t, html, checker = securityFailures) {
  const dist = mkdtempSync(path.join(tmpdir(), 'fugaro-security-'))
  t.after(() => rmSync(dist, { recursive: true, force: true }))
  mkdirSync(path.join(dist, 'security'), { recursive: true })
  writeFileSync(path.join(dist, BUILT), html)
  return checker(dist)
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
  assert.match(claim, /(the tall|wide) drawing's label \d+ says "the agent can read every secret"/, 'a drawing label the document does not say is not refused, so the drawing is free to make a security claim of its own')
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
    /(the tall|wide) drawing's label \d+ says "Please do not open a public issue"/,
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
    /(the tall|wide) drawing's label \d+ says "Fugaro runs a coding agent with its permission prompts turned off" and is drawn from \d+\.\d+ to \d+\.\d+ units, and the card it is drawn in runs/,
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
  const unnamed = planted(html, /(class="boundary-drawing boundary-drawing-tall"[^>]*?) aria-label="[^"]*"/, '$1')
  assert.match(verdict(t, unnamed).join('\n'), /role="img" and an aria-label of ""/, 'a drawing with no accessible name is not refused')

  // The picture with its role taken off: the labels become loose text rather than one image.
  const unroled = planted(html, /(class="boundary-drawing boundary-drawing-tall"[^>]*?) role="img"/, '$1')
  assert.match(verdict(t, unroled).join('\n'), /role="" and an aria-label/, 'a drawing that is not an image to a screen reader is not refused')

  // The labels gone: the drawing is still there and draws nothing the document describes.
  const unlabelled = planted(html, /<text[^>]*>[^<]*<\/text>/g, '')
  assert.match(verdict(t, unlabelled).join('\n'), /the tall drawing carries 0 labels/, 'a drawing with no labels is not refused')

  // The facts gone from under the picture: the brief's "the same facts as a short list".
  const unfacted = planted(html, /<ul class="boundary-facts">[\s\S]*?<\/ul>/, '')
  assert.match(verdict(t, unfacted).join('\n'), /where the brief asks for the picture/, 'a diagram with the facts taken out from under the picture is not refused')

  // A drawing too many beside the two layouts: the brief asks for one diagram, drawn twice.
  const twice = planted(html, /<figure>/, '<figure><svg role="img" aria-label="A third drawing"></svg>')
  assert.match(
    verdict(t, twice).join('\n'),
    /carries 3 drawings where the boundary diagram is one picture drawn twice/,
    'a third drawing in the diagram is not refused, so the figure can carry a picture nobody asked for',
  )

  // ---- the two layouts (the owner's feedback of 2026-10-21) ----------------------------
  //
  // The arrangement the owner asked for is one picture at two layouts with the stylesheet
  // rendering one of them at a time, so each of the four rules that holds it is planted here and
  // seen to fire — as the rules that hold the drawing's own geometry are above.

  // A layout with its class taken off: the drawing is there and which of the two it is is a
  // question nothing can answer, so the changeover and the fold below have no drawing to read.
  const unclassed = planted(html, 'class="boundary-drawing boundary-drawing-wide"', 'class="boundary-drawing"')
  assert.match(
    verdict(t, unclassed).join('\n'),
    /carries 0 drawings marked `boundary-drawing-wide`/,
    'a layout with no class of its own is not refused, so a page can carry one drawing and no second layout at all',
  )

  // The two layouts saying different things: the words are the document's on both sides and
  // every label is a phrase of it, so the only rule that can see this is the one that holds the
  // two to the same picture — which is what makes the owner's "the same drawing" true rather
  // than assumed.
  const mismatched = planted(html, />turned off </, '>not on by default <')
  assert.match(
    verdict(t, mismatched).join('\n'),
    /do not say the same thing/,
    'a label changed in one layout and not the other is not refused, so a reader gets one claim on a phone and another on a desktop',
  )

  // The tall drawing left rendered where no media query holds: both layouts at once. The plant
  // takes the `display` off the rule that turns it off, which is also what the `hidden` attribute
  // below is read against.
  const both = planted(html, 'max-width:22.5rem;display:none', 'max-width:22.5rem')
  assert.match(
    verdict(t, both).join('\n'),
    /the tall drawing is `display: block` outside every media query|the tall drawing is `display: [a-z]+` outside every media query/,
    'a tall drawing left rendered outside every media query is not refused, so a desktop can be shown both drawings at once',
  )
  assert.match(
    verdict(t, both).join('\n'),
    /carries the `hidden` attribute and the page's stylesheet renders it/,
    'the `hidden` attribute and the stylesheet are not held to the same answer, so `hidden` can name one drawing and the CSS render the other',
  )

  // The changeover read from the wrong side: the same width, and the two layouts swapped for a
  // desktop and a phone. This is the defect the side is read for.
  const above = planted(html, '@media (width<=70rem)', '@media (width>=70rem)')
  assert.match(
    verdict(t, above).join('\n'),
    /a phone is met with the tall drawing rather than with the shrunk wide one/,
    'a changeover read from above is not refused, so the tall drawing is shown to a desktop and the shrunk wide one to the phone it cannot be read on',
  )

  // The wide drawing turned off where no query holds: a reader above the changeover is left with
  // no drawing at all.
  const nothing = planted(html, '.boundary-drawing{width:100%;height:auto;display:block}', '.boundary-drawing{width:100%;height:auto;display:none}')
  assert.match(
    verdict(t, nothing).join('\n'),
    /the wide drawing is `display: none` outside every media query/,
    'a wide drawing turned off outside every query is not refused, so a desktop above the changeover gets no drawing',
  )

  // The first rule's other half: the two layouts given no `display` at all outside a media query.
  // The plant takes the value off the rule both classes fall back to, so which drawing a reader at
  // a width no query names gets is the browser's own rendering of an `<svg>` rather than the
  // page's — and the check says that rather than passing the pair.
  const undeclared = planted(
    html,
    '.boundary-drawing{width:100%;height:auto;display:block}',
    '.boundary-drawing{width:100%;height:auto}',
  )
  assert.match(
    verdict(t, undeclared).join('\n'),
    /declares no `display` for the wide drawing outside a media query/,
    'a page whose stylesheet gives the layouts no `display` where no query holds is not refused, so which drawing a reader gets can be the browser\'s choice rather than the page\'s',
  )

  // The second rule's other half: the drawing the stylesheet turns off with nothing on it to say
  // so. `hidden` is what a reader whose stylesheet never arrives gets, and that reader is left
  // with whichever drawing the markup and the CSS disagree about.
  const unmarked = planted(html, 'focusable="false" hidden>', 'focusable="false">')
  assert.match(
    verdict(t, unmarked).join('\n'),
    /the tall drawing is `display: none` outside every media query and does not carry the `hidden` attribute/,
    'a drawing the stylesheet turns off with no `hidden` on it is not refused, so a reader whose stylesheet never arrives gets no drawing at all',
  )

  // The third rule with no changeover at all, and with two. The query is the page's own built
  // text rather than a shape typed here: `planted` refuses an edit that does not land, so a
  // stylesheet that wrote its query another way fails this fixture rather than passing it.
  const QUERY =
    '@media (width<=70rem){[data-security-diagram] .boundary-drawing-wide{display:none}[data-security-diagram] .boundary-drawing-tall{display:block}}'
  const nowhere = planted(html, QUERY, '')
  assert.match(
    verdict(t, nowhere).join('\n'),
    /has no media query that declares either drawing, so the two layouts never change over/,
    'a page with no changeover at all is not refused, so a phone is left with the shrunk wide drawing above every width',
  )

  const two = planted(
    html,
    QUERY,
    `${QUERY}@media (width<=50rem){[data-security-diagram] .boundary-drawing-wide{display:none}}`,
  )
  assert.match(
    verdict(t, two).join('\n'),
    /switches the two drawings at 2 different widths/,
    'two changeovers are not refused, so a reader between the two widths gets a layout the page never chose',
  )

  // And the wide drawing left on below the changeover: both layouts at once, which is the picture
  // drawn twice and read twice.
  const wideshown = planted(html, QUERY, QUERY.replace('.boundary-drawing-wide{display:none}', '.boundary-drawing-wide{display:block}'))
  assert.match(
    verdict(t, wideshown).join('\n'),
    /turned off below the same width the tall one is turned on below/,
    'a wide drawing left on below the changeover is not refused, so a desktop is shown both drawings at once',
  )

  // And the wide drawing's own two numbers: its labels too small to read where it is shown, and
  // a picture too tall for the first screen. Both are the arithmetic the desktop layout rests on
  // rather than anything a reader can see in the markup.
  const tiny = planted(html, /font-size="14"/g, 'font-size="8"')
  assert.match(
    verdict(t, tiny).join('\n'),
    /the wide drawing's smallest label is 8 units, which is \d+\.\d+ px in the \d+ px column/,
    'a wide drawing whose labels are too small to read where it is shown is not refused, so the desktop layout can be one nobody can read',
  )

  const tall = planted(html, 'viewBox="0 0 1180 400"', 'viewBox="0 0 1180 900"')
  assert.match(
    verdict(t, tall).join('\n'),
    /the headline and the whole drawing do not fit a 1440 × 800 first screen/,
    'a wide drawing too tall for the first screen is not refused, which is the very thing the owner asked for: the drawing does not fit above the fold on a desktop',
  )

  // The four refusals that are about what this check **cannot read**: the page's own numbers, or
  // the drawing's own shape, turned into something nothing here can turn back into pixels. They
  // are guard rails rather than defects a reader meets, and each is planted the same way — the one
  // value the arithmetic is taken at is made unreadable — because a branch whose refusal has never
  // been seen to fire is a branch nobody knows the shape of, which is the same reason every other
  // rule here has a plant.
  const noCeiling = planted(html, '--page-max:72rem;', '--page-max:fit-content;')
  assert.match(
    verdict(t, noCeiling).join('\n'),
    /that column is where the wide drawing's label size is taken/,
    'a column this check cannot read is not refused, so the label floor is taken against a guess or against nothing',
  )

  // The plant takes the drawing's own **width** out of its `viewBox`: the labels are still the
  // page's, and the number they are scaled into is the one nothing here can take. (`nope` in the
  // height's place is a different refusal — the one below — because a missing height is a fold
  // nothing can be claimed about while a missing width is a label size nothing can be taken at.)
  const noNumbers = planted(html, 'viewBox="0 0 1180 400"', 'viewBox="0 0 nope 400"')
  assert.match(
    verdict(t, noNumbers).join('\n'),
    /and \d+ label sizes this check cannot read/,
    'a drawing whose own units cannot be read is not refused, so the size its labels are rendered at is not a number anything here can take',
  )

  const noHeight = planted(html, 'viewBox="0 0 1180 400"', 'viewBox="0 0 1180 0"')
  assert.match(
    verdict(t, noHeight).join('\n'),
    /with no height this check can read, so nothing here can say whether it fits a first screen/,
    'a drawing with no readable height is not refused, so the fold is claimed about a shape nothing here has',
  )

  const noLeading = planted(html, '--leading-display:.94;', '--leading-display:roomy;')
  assert.match(
    verdict(t, noLeading).join('\n'),
    /the room above the drawing and the column it is drawn in at 1440 px cannot be read out of the page's own stylesheet/,
    'a part of the room above the drawing that cannot be read is not refused, so the fold is measured with a header, a headline or a heading standing in as nothing',
  )

  // A script in the drawing: the brief's "No JavaScript", and the site loads one first-party
  // file and no other.
  const scripted = planted(html, /(<rect class="boundary-card"[^>]*x="12" y="12")/, '<script>void 0</script>$1')
  assert.match(verdict(t, scripted).join('\n'), /the boundary diagram carries a <script>/, 'a script inside the drawing is not refused')

  // A paragraph slipped into the diagram, where the balance counts it as the region's own text
  // and passes it: the shape rule is what sees this one.
  const inDiagram = planted(html, /(<section\b[^>]*data-security-diagram[^>]*>)/, '$1<p>A friendly note about the model.</p>')
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
    /(<section\b[^>]*data-security-summary[^>]*>[\s\S]*?)is listed in <code>followup\.trusted<\/code>/,
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

test('the page is drawn in the site’s bold look', () => {
  assert.deepEqual(
    boldLookFailures(DIST),
    [],
    `the security page is not drawn in the bold look the owner approved on the homepage and the product pages (the owner's brief of 2026-10-21):\n  ${boldLookFailures(DIST).join('\n  ')}`,
  )
})

test('the bold look refusals fire on the defect each one names', (t) => {
  const html = builtPage(DIST)
  assert.ok(html !== null, `${BUILT} is not in the artifact: run \`npm run check\` from website/ so this gate has a page to read`)
  const refusals = (edited) => verdict(t, edited, boldLookFailures).join('\n')

  // The title back at the browser's `h1`: the page as it was before the redesign.
  const plainTitle = planted(html, 'class="security-title display-xl"', 'class="security-title"')
  assert.match(refusals(plainTitle), /and not the display scale's largest step/, 'a title that is not the display scale is not refused')

  // The gradient narrowed to the page's own class rather than the site's — a page that drew
  // the accent locally is the same defect one level down: the class is named and the rule is
  // not, so the headline is drawn in one flat tone.
  const noGradient = planted(html, /<span class="gradient-text">Security<\/span>/, 'Security')
  assert.match(
    refusals(noGradient),
    /carries 0 `gradient-text` spans where the bold look draws the headline's words in the accent's gradient/,
    'a headline with no gradient accent is not refused, so the page could drop the accent the redesign is built on',
  )

  // A section heading left to the browser: one of the page's three, because one is enough to
  // fail the rule it names.
  const plainHeading = planted(html, /<h2 class="display-md"/, '<h2')
  assert.match(refusals(plainHeading), /and not the display scale's middle step/, 'a section heading that is not the display scale is not refused')

  // A block that is not the site's card: the summary taken off its box, which the rule reads
  // by the region's own marker rather than by the class alone.
  const bareSummary = planted(html, 'class="card security-summary"', 'class="security-summary"')
  assert.match(
    refusals(bareSummary),
    /the document's summary carries class "security-summary" and not the site's card/,
    'a block that is not drawn on the site\'s card is not refused, so the page could grow a shape of its own beside the homepage\'s',
  )

  // The room between the blocks cut to a number: the rhythm the gate is here for. The plant is
  // anchored on the page's own container rule, so it cannot be met by another `gap` in the
  // stylesheet, and the number it plants is below the floor as well as off the scale.
  const cramped = planted(
    html,
    /(\[data-security-page\]\{[^}]*?)gap:var\(--space-xl\)/,
    '$1gap:4px',
  )
  assert.match(
    refusals(cramped),
    /sets the room between its blocks with "4px" and not with a step of the site's spacing scale/,
    'a page whose blocks are set apart by a number rather than by a step of the scale is not refused',
  )

  // And the other half of the same rule: a step of the scale that is too small for the bold
  // look's room. This is the defect the floor exists for — a page that keeps the scale and
  // loses the room.
  const tight = planted(
    html,
    /gap:var\(--space-xl\)/,
    'gap:var(--space-3xs)',
  )
  assert.match(
    refusals(tight),
    /at `--space-3xs` \(\d+px\) where the bold look's blocks are set apart generously/,
    'a step of the scale below the floor is not refused, so the room the bold look gives its blocks is a taste nothing measures',
  )

  // The container the `gap` means nothing in: a page whose blocks are laid out in a block box
  // reads as one that sets its rhythm — the declaration is right there in the stylesheet — and
  // draws nothing. The rule above cannot see it, because it reads the declaration and not
  // whether the declaration spaces anything.
  const inert = planted(html, /(\[data-security-page\]\{[^}]*?)display:flex/, '$1display:block')
  assert.match(
    refusals(inert),
    /is "block" where the page's rhythm is a `gap`/,
    'a `gap` on a block container is not refused, so the page could carry the declaration that draws nothing and pass as one with a rhythm',
  )

  // And the container the `gap` is room in the wrong direction for: a flex row lays the four
  // blocks out side by side and the `gap` becomes room between columns.
  const row = planted(html, /(\[data-security-page\]\{[^}]*?)flex-direction:column/, '$1flex-direction:row')
  assert.match(
    refusals(row),
    /is a flex row \("row"\) where its blocks are a column/,
    'a flex row is not refused, so the page could turn its one column into a row and keep the `gap` it measures',
  )

  // **The doubled rhythm, which is the defect this half of the gate exists for.** A block that
  // keeps the margin it carried while the page was a plain flow gives the `gap` twice: the page
  // says `--space-xl` and a reader gets that step plus the block's `--space-lg`. This is the
  // plant that reddens the margin rule and nothing else — the `gap` it reads is correct.
  const doubled = planted(html, '[data-security-summary]{margin:0}', '[data-security-summary]{margin:0 0 var(--space-lg)}')
  assert.match(
    refusals(doubled),
    /the document's summary carries a `margin-bottom` of "var\(--space-lg\)" \(32px\)[\s\S]*renders at 80px where the page says one step of the scale/,
    'a block whose own margin is added to the gap is not refused, so the room between two blocks renders at 80 px where the page says one step — the doubled rhythm the homepage gate refuses',
  )

  // And the same defect arriving from `base.css` rather than from the page: the pointer is a
  // `<p>`, so the site's own prose rule gives it a margin below the moment the page stops
  // saying `margin: 0`. This is why the reading resolves the cascade instead of skipping it —
  // `:where(p, …)` is worth nothing, so the page's zero is what is in force, and the plant is
  // the page's zero taken away.
  const unmargined = planted(html, /(\[data-security-policy\]\.card\{[^}]*?)margin:0\}/, '$1}')
  assert.match(
    refusals(unmargined),
    /the pointer to the document carries a `margin-bottom` of "var\(--space-sm\)" \(16px\)/,
    "a block whose margin comes from base.css rather than from the page is not refused, so the site's own prose rule could pad the gap behind the page's back",
  )
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
