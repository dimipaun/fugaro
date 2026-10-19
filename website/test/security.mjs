// website/test/security.mjs — the security page (website plan, Task W1.8; the owner's brief
// of 2026-10-19).
//
// **The page is three things and nothing else**, and that is what this file holds it to: a
// short high-level description of the security model and its boundaries (at most eight short
// bullets), the document's own "Reporting a vulnerability" section, and a link to
// `SECURITY.md` in the repository. The page used to render `SECURITY.md` whole, behind a
// contents list built from its headings; the owner cut it on 2026-10-19, and the two checks
// that made the old shape safe are kept in their new form below.
//
// Five things are checked, and each one is the mechanical form of a sentence in the brief:
//
//   1. **The page carries its three regions and no other text** (the brief: "Nothing else").
//      Everything a reader sees inside the page's own `<article>` is its `h1`, the
//      description, the reporting section and the policy link; a paragraph of introduction,
//      a summary of a limit, or a second copy of anything fails here.
//   2. **The reporting section is `SECURITY.md`'s own words** (the brief: "the real
//      reporting instructions, unchanged in substance"). It is compared with the document in
//      the repository, paragraph by paragraph, through one normalisation — inline markup off,
//      whitespace removed — so the page cannot reword an instruction a reporter follows. The
//      page renders the section out of `website/content/security.md`, the file `go run
//      ./internal/clidoc` writes from `SECURITY.md`, and this check reads the source document
//      rather than the generator's output: a test written against the thing it is meant to
//      hold agrees with whatever that thing does (plan Review Focus).
//   3. **The description is short** (the brief: "at most about 8 short bullets"): at most
//      eight bullets, no nested list, each bullet at most 40 words, and the section at most
//      220 words in all. The numbers are the brief's own ceiling and a ceiling on "short";
//      what the bullets *claim* is the owner's reading, and every one of them names the
//      source it comes from in a comment in `website/src/pages/security.astro`.
//   4. **The link is `SECURITY.md`, at its own path in the repository** (the brief:
//      `https://github.com/dimipaun/fugaro/blob/main/SECURITY.md`). The path is read from the
//      generated file's own `source:` header and the repository from `site.yaml`, both of
//      which `website/src/pages/security.astro` builds the URL from, so the page cannot
//      point at a document this repository does not carry.
//   5. **The page's head points at no file the build does not produce** (Global Constraint
//      9). The one hint the layout composes is the sitemap, and whether the build ships one
//      is read from the resolved `astro.config.mjs` — imported, not pattern-matched, which is
//      the same rule `website/test/readme.mjs` applies to the same file.
//
// How it reads the page: the built artifact, `dist/security/index.html`, written by `npm run
// build` — which `npm run check` runs before it runs any file here — through `DIST`, so
// `FUGARO_GATES_DIST` points the whole gate at a planted copy and each of its refusals is
// mutation-proved rather than asserted (the mechanism `budget.mjs` and `fold.mjs` use; the
// case at the end of this file plants them). The regions are found by the marker attributes
// the page's own markup carries (`data-security-page`, `data-security-model`,
// `data-security-reporting`, `data-security-policy`).

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { DIST, REPO, SITE, findAll, parse } from './lib/html.mjs'

/** The document the page renders, the file it is generated into, and the built page. */
const SOURCE = 'SECURITY.md'
const GENERATED = 'website/content/security.md'
const BUILT = path.join('security', 'index.html')

/** The section of the document the page renders, and the id its heading carries. */
const REPORTING = 'Reporting a vulnerability'
const REPORTING_ID = 'reporting-a-vulnerability'

/** The ceilings of check 3, which are the brief's "at most about 8 short bullets". */
const MAX_BULLETS = 8
const MAX_BULLET_WORDS = 40
const MAX_MODEL_WORDS = 220

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

/** The text a reader sees inside a node, with an image's alt standing in for the image. */
function text(node) {
  if (node.text !== undefined) return node.text
  if (node.name === 'img') return node.attrs.get('alt') ?? ''
  if (node.raw !== undefined) return ''
  if (node.attrs?.has('hidden')) return ''
  return (node.children ?? []).map(text).join(' ')
}

/** Text reduced to the words themselves: no whitespace, so line breaks cannot differ. */
function flatten(s) {
  return s.replace(/\s+/g, '')
}

/** The words of a piece of text, the way a reader counts them. */
function words(s) {
  return s.trim().split(/\s+/).filter(Boolean).length
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
 * The document's lines under its `## Reporting a vulnerability` heading, up to the next
 * heading of the same level or higher — the section the page renders, read from `SECURITY.md`
 * rather than from the generated file or from the page.
 */
function reportingLines(document) {
  const lines = document.split('\n')
  const start = lines.findIndex((line) => line.trim() === `## ${REPORTING}`)
  if (start === -1) {
    throw new Error(`${SOURCE} has no "## ${REPORTING}" heading, so the page this file checks has no reporting section to render`)
  }
  const rest = lines.slice(start + 1)
  const end = rest.findIndex((line) => /^#{1,2}[ \t]/.test(line))
  return end === -1 ? rest : rest.slice(0, end)
}

/** The paragraphs of a run of Markdown lines, as a reader reads them. */
function paragraphsOf(lines) {
  return lines
    .join('\n')
    .split(/\n[ \t]*\n/)
    .map((paragraph) => inlineText(paragraph.replace(/\s+/g, ' ').trim()))
    .filter((paragraph) => paragraph !== '')
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
  const model = marked(article, 'data-security-model', fail)
  const reporting = marked(article, 'data-security-reporting', fail)
  const policy = marked(article, 'data-security-policy', fail)
  if (h1s.length !== 1 || model === null || reporting === null || policy === null) return out

  const regions = [h1s[0], model, reporting, policy]
  if (flatten(text(article)) !== flatten(regions.map(text).join(' '))) {
    fail(
      `the page carries text outside its title, its description, the document's reporting section and the link to it: the page is those four things and nothing else (the owner's brief of 2026-10-19), so a sentence written anywhere else on it is a security claim the document does not make (D99)`,
    )
  }

  // 2. The reporting section is the document's own, word for word.
  const expected = paragraphsOf(reportingLines(sourceDocument()))
  const rendered = findAll(reporting, 'p')
  const headings = findAll(reporting, 'h2')
  if (headings.length !== 1 || flatten(text(headings[0])) !== flatten(REPORTING)) {
    fail(`the reporting section is not under the document's own "${REPORTING}" heading: the page names the section a reporter is looking for (${SOURCE})`)
  } else if (headings[0].attrs.get('id') !== REPORTING_ID) {
    fail(`the reporting section's heading carries the id ${JSON.stringify(headings[0].attrs.get('id') ?? '')} and not "${REPORTING_ID}": the id is what a link into the section points at`)
  }
  if (rendered.length === 0) {
    fail(`the reporting section carries no paragraph: ${SOURCE} writes the instructions to report a vulnerability there, and a section with no text sends a reporter nowhere`)
  } else if (expected.length === 0) {
    fail(`read no paragraph out of ${SOURCE}'s "${REPORTING}" section: the document has moved, so this check saw nothing`)
  } else if (rendered.map((p) => flatten(text(p))).join('\u0000') !== expected.map(flatten).join('\u0000')) {
    fail(
      `${BUILT} does not render ${SOURCE}'s own reporting instructions: the page renders the document's section and does not reword it, because a reporter follows those words and a paraphrase is a claim the document does not make (D99)`,
    )
  }

  // 3. The description is short: at most eight bullets, no nested list, each bullet at most
  //    forty words, and the section at most two hundred and twenty.
  const lists = findAll(model, 'ul')
  const bullets = findAll(model, 'li')
  if (lists.length !== 1) {
    fail(`the description carries ${lists.length} lists; the page's description is one list of bullets (the owner's brief of 2026-10-19)`)
  } else if (bullets.some((b) => findAll(b, 'li').length > 0)) {
    fail('the description carries a nested list: each boundary is one bullet, and a bullet inside a bullet is a boundary the reader has to assemble')
  }
  if (bullets.length === 0) {
    fail('the description carries no bullet: the page describes the security model in at most eight bullets, and a description with nothing in it describes nothing')
  } else if (bullets.length > MAX_BULLETS) {
    fail(`the description carries ${bullets.length} bullets; the page describes the security model in at most ${MAX_BULLETS} (the owner's brief of 2026-10-19)`)
  }
  for (const bullet of bullets) {
    const n = words(text(bullet))
    if (n > MAX_BULLET_WORDS) {
      fail(`a bullet of the description is ${n} words long: a bullet is short (at most ${MAX_BULLET_WORDS} words), because the description is high level and the detail is in the document (the owner's brief of 2026-10-19)`)
    }
    if (text(bullet).trim() === '') fail('the description carries an empty bullet')
  }
  const modelWords = words(text(model))
  if (modelWords > MAX_MODEL_WORDS) {
    fail(`the description is ${modelWords} words long; the page's description is at most ${MAX_MODEL_WORDS} words (the owner's brief of 2026-10-19)`)
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
    if (flatten(text(links[0])) !== flatten(SOURCE)) {
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

test('the page renders SECURITY.md\'s own reporting instructions', () => {
  const document = sourceDocument()
  const expected = paragraphsOf(reportingLines(document))
  assert.ok(expected.length >= 2, `read ${expected.length} paragraphs out of ${SOURCE}'s "${REPORTING}" section: the document has moved, so this check saw nothing`)
  assert.ok(expected.join(' ').length > 200, `the "${REPORTING}" section of ${SOURCE} is ${expected.join(' ').length} characters: the section this page renders is the document's, so a stub would make every check in this file vacuous`)
  assert.deepEqual(securityFailures(DIST), [])
})

test('the description is the page\'s own, and the source of each bullet is written beside it', () => {
  // The description is the page's own copy — the owner reviews it — so what this file can
  // hold is its shape, which the gate above does. This case holds the audit trail instead:
  // every bullet in `website/src/pages/security.astro` names the document it came from in a
  // comment directly above it, because a high-level summary of a security model is only
  // worth having if a reader can check each line.
  const source = readFileSync(path.join(SITE, 'src', 'pages', 'security.astro'), 'utf8')
  const section = /<section[^>]*data-security-model[^>]*>([\s\S]*?)<\/section>/.exec(source)?.[1]
  assert.ok(section, 'website/src/pages/security.astro carries no `data-security-model` section: the page has moved, so this check saw nothing')

  // The bullet, and the comment line directly above it: `<li>` here is the page's own markup
  // and not a nested list, so the first closing tag is the bullet's own.
  const bullets = [...section.matchAll(/(\{\/\*([\s\S]*?)\*\/\}\s*)?<li>([\s\S]*?)<\/li>/g)].map((m) => ({ note: m[2] ?? '', body: m[3] }))
  assert.ok(bullets.length >= 5, `read ${bullets.length} bullets out of website/src/pages/security.astro: the page has moved, so this check saw nothing`)
  for (const bullet of bullets) {
    assert.match(
      bullet.note,
      /SECURITY\.md|docs\//,
      `the bullet starting ${JSON.stringify(bullet.body.trim().slice(0, 60))} carries no comment above it naming the source it comes from: every claim in the description names the SECURITY.md section (or the design document) it is a shorter form of, so a reader can check each line against the document`,
    )
  }
})

test('the gate refuses each planted defect', (t) => {
  const html = builtPage(DIST)
  assert.ok(html !== null, `${BUILT} is not in the artifact: run \`npm run check\` from website/ so this gate has a page to read`)

  // A sentence the page adds to itself: the region balance catches it wherever it sits.
  const extra = planted(html, /(<article\b[^>]*data-security-page[^>]*>)/, '$1\n<p>A friendly note about the model.</p>')
  assert.match(verdict(t, extra).join('\n'), /carries text outside/, 'an extra paragraph on the page is not refused')

  // A ninth bullet.
  const ninth = planted(html, /(<section\b[^>]*data-security-model[^>]*>[\s\S]*?)<\/ul>/, '$1<li>One more boundary.</li><li>And one more.</li></ul>')
  assert.match(verdict(t, ninth).join('\n'), /at most 8/, 'bullets past the ceiling are not refused')

  // A bullet that is no longer short.
  const long = 'x '.repeat(45).trim()
  const rambling = planted(html, /(<section\b[^>]*data-security-model[^>]*>[\s\S]*?)<\/ul>/, `$1<li>${long}</li></ul>`)
  assert.match(verdict(t, rambling).join('\n'), /is short \(at most/, 'a bullet over the word ceiling is not refused')

  // A reworded instruction: the one edit a security page is always tempted by.
  const reworded = planted(html, 'Report a vulnerability</strong>', 'Report a problem</strong>')
  assert.match(verdict(t, reworded).join('\n'), /does not render .*own reporting instructions/, 'a reworded reporting instruction is not refused')

  // A reporting section that is gone.
  const missing = planted(html, /<section\b[^>]*data-security-reporting[^>]*>[\s\S]*?<\/section>\s*/, '')
  assert.match(verdict(t, missing).join('\n'), /data-security-reporting/, 'a page with no reporting section is not refused')

  // A link to something other than the document.
  const elsewhere = planted(html, /https:\/\/github\.com\/[^/]+\/[^/]+\/blob\/main\/[^"]+/, 'https://github.com/dimipaun/fugaro/blob/main/docs/design/v1.md')
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
