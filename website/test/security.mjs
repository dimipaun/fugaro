// website/test/security.mjs — the security page (website plan, Task W1.8; the owner's brief
// of 2026-10-19 and ruling D189).
//
// **The page is three things and nothing else**, and all three of them are the document's:
// `SECURITY.md`'s own "In short" section, its own "Reporting a vulnerability" section, and a
// link to the document in the repository. That is the brief's cut of the page, and D189's
// answer to the one open question — where the summary comes from: **each long page's summary
// is rendered from a file, never written on the page**, and this page's file is `SECURITY.md`
// itself (`docs/website/redesign/reconciliation.md`, D189). The page used to render the
// document whole, behind a contents list built from its headings, and then wrote a summary of
// its own; both are gone, and this file is what holds the page to the new shape.
//
// Five things are checked, and each one is the mechanical form of a sentence in the brief:
//
//   1. **The page carries its three regions and no other text** (the brief: "Nothing else").
//      Everything a reader sees inside the page's own `<article>` is its `h1`, the summary,
//      the reporting section and the policy link; a paragraph of introduction, a summary of a
//      limit, or a second copy of anything fails here. The rule is applied twice over, because
//      the page can grow two ways: the article's text has to be exactly the four regions' text
//      (so nothing sits between them), and each region has to be the shape the brief gives it
//      — a heading and one list, a heading and the document's paragraphs, the link and the
//      sentence around it (so nothing sits inside one, where the first half of the rule would
//      count it as the region's own text and pass it).
//   2. **The summary is `SECURITY.md`'s own "In short" section, bullet for bullet** (the
//      brief: "a short high-level description of the security model and its boundaries";
//      D189: "rendered from a file, never written on the page"). Each rendered bullet is
//      compared with the document's own, through one normalisation — inline markup off,
//      whitespace collapsed — so the page cannot soften a limit the owner states, and cannot
//      lose a space either, and the section is held to a word ceiling so that "short" stays a
//      measured thing. The bullets are the owner's sentences and this file does not shorten
//      them: the ceiling is on the section, and a document edit that grows it re-derives the
//      number in that commit.
//   3. **The reporting section is `SECURITY.md`'s own words** (the brief: "the real reporting
//      instructions, unchanged in substance"). Compared with the document paragraph by
//      paragraph, the same way. The page renders both sections out of
//      `website/content/security.md`, the file `go run ./internal/clidoc` writes from
//      `SECURITY.md`, and this file reads the source document rather than the generator's
//      output: a test written against the thing it is meant to hold agrees with whatever that
//      thing does (plan Review Focus).
//   4. **The link is `SECURITY.md`, at its own path in the repository** (the brief:
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
//   5. **The page's head points at no file the build does not produce** (Global Constraint 9).
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
// the page's own markup carries (`data-security-page`, `data-security-summary`,
// `data-security-reporting`, `data-security-policy`).

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { DIST, REPO, SITE, findAll, parse, verbatimText } from './lib/html.mjs'
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
 * The document's own bullets, each one line joined back together when the file wrapped it and
 * the markdown left as it is. The two readers below this one take it apart: the text without
 * its inline markup, and the links it carries.
 */
function bulletLinesOf(document, heading) {
  const bullets = []
  for (const line of sectionLines(document, heading)) {
    const bullet = /^\s*[-*][ \t]+(.*)$/.exec(line)
    if (bullet !== null) bullets.push(bullet[1].trim())
    else if (line.trim() !== '' && bullets.length > 0) bullets[bullets.length - 1] += ` ${line.trim()}`
  }
  return bullets
}

/** The bullets of a section, as a reader reads them. */
function bulletsOf(document, heading) {
  return bulletLinesOf(document, heading).map(inlineText)
}

/** A section's own blocks of text, with the markdown left as it is: its bullets, or its
 *  paragraphs when the section is not a list. */
function blocksOf(document, heading, kind) {
  if (kind === 'bullet') return bulletLinesOf(document, heading)
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
 * Where the page sends a reader for one of the document's own links: the target as the document
 * writes it, with a fragment resolved against the document in the repository. A fragment the
 * document does not carry a heading for resolves to the document itself, which is the one
 * `SECURITY.md` ships — `[Money](#money)` names a bullet in the "In short" list and not a
 * heading, so GitHub scrolls nowhere and the page points at the document instead.
 */
function resolvedTarget(raw, anchors, base) {
  if (!raw.startsWith('#')) return raw
  const fragment = raw.slice(1)
  return anchors.has(fragment) ? `${base}#${fragment}` : base
}

/**
 * The page's cross-reference links, pinned to the sections they name (round 4 of the review).
 *
 * The page does not render the document's `[text](#fragment)` links as the document writes
 * them: `security.astro` rewrites each one to point into `SECURITY.md` in the repository, so a
 * reader who follows "Known limits" arrives at the document's own "Known limits" section rather
 * than at the top of the file. That rewriting is the one part of the page no other check reads:
 * `checkSection` compares the words with the inline markup taken off, and
 * `website/test/linkcheck.mjs` asks only that a fragment which is *present* is a heading. So a
 * rewrite that dropped every fragment — or pointed one at a different real heading — would send
 * readers to the top of the document, or to the wrong section, with every gate still green.
 *
 * Both halves are held here: the anchor is the document's own link, text and order, and its
 * href is that link's target resolved against the document's URL in the repository — the same
 * resolution the page makes, because both sides go through the same slug module.
 */
function checkLinks(region, { document, heading, kind, fail, what }) {
  const base = `${repository()}/blob/main/${SOURCE}`
  const anchors = documentAnchors(document)
  const expected = blocksOf(document, heading, kind)
    .flatMap(linksOf)
    .map((link) => ({ text: link.text, href: resolvedTarget(link.target, anchors, base) }))
  const rendered = findAll(region, 'a')

  if (rendered.length !== expected.length) {
    fail(
      `the ${what} carries ${rendered.length} links and ${SOURCE}'s "${heading}" section writes ${expected.length}: the links on the page are the document's own cross-references, and a link added or lost here is one the document does not carry (the owner's brief of 2026-10-19)`,
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

// ---------------------------------------------------------------------------------------
// The gate: every way a built security page breaks the three-things rule.
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

  // 1. The three regions, and nothing else. The `h1` is the page's own title and takes no
  //    marker: it is the whole of what the page says above its first section.
  const h1s = findAll(article, 'h1')
  const summary = marked(article, 'data-security-summary', fail)
  const reporting = marked(article, 'data-security-reporting', fail)
  const policy = marked(article, 'data-security-policy', fail)
  if (h1s.length !== 1 || summary === null || reporting === null || policy === null) return out

  const regions = [h1s[0], summary, reporting, policy]
  if (prose(text(article)) !== prose(regions.map(text).join(' '))) {
    fail(
      `the page carries text outside its title, the document's summary, its reporting section and the link to it: the page is those four things and nothing else (the owner's brief of 2026-10-19), so a sentence written anywhere else on it is a security claim the document does not make (D99)`,
    )
  }

  // 1b. And each region is the shape the brief gives it: the summary is its heading and its
  //     one list, the reporting section is its heading and the document's own paragraphs
  //     (compared below), and the pointer is the link and the few words that point at it. The
  //     balance above cannot see a sentence added *inside* a region — its text is counted
  //     either way — so without this rule a paragraph slipped into the summary, or a second
  //     sentence slipped into the pointer, would read as part of the page's own text and pass.
  for (const { region, what, shape, allowed, own } of [
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

// ---------------------------------------------------------------------------------------

test('the built security page is three things and nothing else', () => {
  assert.deepEqual(securityFailures(DIST), [], `the security page is not the three things the owner's brief of 2026-10-19 asks for:\n  ${securityFailures(DIST).join('\n  ')}`)
})

test('the summary the page renders is SECURITY.md\'s own "In short" section', () => {
  const document = sourceDocument()
  const bullets = bulletsOf(document, SUMMARY)
  assert.ok(bullets.length >= 4, `read ${bullets.length} bullets out of ${SOURCE}'s "${SUMMARY}" section: the document has moved, so this check saw nothing`)
  assert.ok(bullets.join(' ').length > 200, `the "${SUMMARY}" section of ${SOURCE} is ${bullets.join(' ').length} characters: the section this page renders is the document's, so a stub would make every check in this file vacuous`)
  assert.deepEqual(securityFailures(DIST), [])
})

test('the summary is rendered from the document, and is not written on the page', () => {
  // D189's rule, held from the other side: the summary lives in `SECURITY.md` and the page
  // renders it, so the page's own source may not carry the summary's sentences. This is the
  // check that notices the shape the owner's ruling rejected — a summary typed into the
  // `.astro` file — which `securityFailures()` cannot see, because the built page is the same
  // either way.
  const source = readFileSync(path.join(SITE, 'src', 'pages', 'security.astro'), 'utf8')
  const bullets = bulletsOf(sourceDocument(), SUMMARY)
  assert.ok(bullets.length >= 4, `read ${bullets.length} bullets out of ${SOURCE}'s "${SUMMARY}" section: the document has moved, so this check saw nothing`)

  for (const bullet of bullets) {
    const opening = bullet.split(/\s+/).slice(0, 6).join(' ')
    assert.ok(
      !prose(source).includes(prose(opening)),
      `website/src/pages/security.astro carries the document's own summary text (${JSON.stringify(opening)}…): the summary is rendered from ${SOURCE}'s "${SUMMARY}" section and never written on the page (D189), because a sentence typed here is one the document does not hold and cannot be kept in step with it`,
    )
  }
})

test('the gate refuses each planted defect', (t) => {
  const html = builtPage(DIST)
  assert.ok(html !== null, `${BUILT} is not in the artifact: run \`npm run check\` from website/ so this gate has a page to read`)

  // A sentence the page adds to itself: the region balance catches it wherever it sits.
  const extra = planted(html, /(<article\b[^>]*data-security-page[^>]*>)/, '$1\n<p>A friendly note about the model.</p>')
  assert.match(verdict(t, extra).join('\n'), /carries text outside/, 'an extra paragraph on the page is not refused')

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
  const glued = planted(html, /is listed in <code>followup\.trusted<\/code>/, 'is listed in<code>followup.trusted</code>')
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
