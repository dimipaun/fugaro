// website/test/wording.mjs — the words the site uses for what a run is (website changes
// spec of 2026-10-18, item 2: "use 'jobs' instead of 'workers' consistently across the
// site", accepted as "the old wording no longer appears anywhere on the site or its
// metadata or README").
//
// **The rule: a run's unit of work is a job, and "worker" is retired from the site's own
// copy.** The old tagline — "Fleeting cloud workers for coding agents", the line under the
// README's `# Fugaro` title — described the product in a word the reader has to decode, and
// item 2 replaced it on the site with the headline and the subline (`Lambdas for coding
// agents` / `Ephemeral cloud jobs that turn tasks into pull requests.`). The word is the part
// a hero rewrite cannot retire on its own: it lives
// in a data file's answer, in a page's caption, in a README line and in a card. So this is
// a gate and not a search somebody ran once, in three parts:
//
//   1. **the built pages** — every page the artifact holds, except the three that render a
//      document of this repository (D188's list: `/docs/`, `/security/`, `/changelog/`),
//      carries the word nowhere a reader reads it: neither in its text nor in its `<title>`,
//      its description or its card;
//   2. **README.md**, which is where the site's own promise lines come from, and whose
//      "fleeting cloud workers" line was the old tagline itself;
//   3. **the site's own sources** — `website/src`, `website/data`, `website/public` and the
//      generated documents under `website/content` — so that the word cannot come back in a
//      component or a data file and only show up on the next build. One file of that last
//      directory is excepted by name: `FILES_THAT_KEEP_IT` below, the generated CLI
//      reference, which carries the root command's help text verbatim.
//
// **Why the three document-rendering pages are exempt, and how wide the exemption is.** They
// render a document of this repository rather than a page of the site: `/docs/` renders the
// generated CLI reference, whose first paragraph is the root command's own help text, and
// that help text still reads "Fleeting cloud workers for coding agents". The owner's ruling
// of 2026-10-18 takes that on purpose: the wording in the CLI's help, in the generated
// `website/content/cli-reference.md` and in the plugin skills that carry it — `routing`,
// `working` (its reference files too) and `parallelism`, the three
// `SKILLS_THAT_CARRY_IT` below names — moves in the 0.9.0 work, not in this one, and the
// generated file may not be hand-edited.
// The exemption is exactly D188's three pages and no others, and the word is still refused
// everywhere else — including `website/data/faq.yaml`, whose answer said "a fleet of
// ephemeral cloud workers" and whose page `/faq/` is not exempt. The file half of the same
// ruling is one line in one generated file, and it too is named and held to the tree
// (`FILES_THAT_KEEP_IT` and the case that reads it) rather than left to the gate's own
// sweep to miss.
//
// **The second rule: the claim a figure's caption may not make.** The hero's chat was an
// illustration, and its caption said "every line is the repository's own words, from the
// skills the agent reads" — which is false about two of the things the figure printed (the
// request is item 3's own example, and the three `Pull request n of 3` labels are the
// illustration's own). The caption was rewritten to say what the picture is and where the
// agent's behaviour comes from, and `RETIRED_CLAIMS` below refuses the phrases the false
// sentence was made of — in the same three places, and with its own refusal case, because a
// caption that is only fixed once is a caption that comes back the next time somebody
// rewrites a figure. The figure itself left the site with `AgentChat.astro` when the owner's
// approved draft of 2026-10-19 ("Home, draft 2 (slider)") folded the chat into the slider's
// first panel; the rule stays, because the claim is one any figure could make again — the
// screenshot of a run and the flow's captions are the two a rewording would reach for.
//
// A gate whose refusals have never been seen to fire is a gate nobody knows the shape of, so
// each part below has a case that plants the word — or the claim — where its rule looks and
// proves the failure arrives with the file's name in it. Standard library only; no network
// (Global Constraint 4).

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { DIST, REPO, SITE, builtPages, parse, textOf } from './lib/html.mjs'
import { DOCUMENT_PAGES, RECONCILIATION, documentPagesNamedIn, readReconciliation } from './lib/exempt.mjs'

/**
 * The retired word, as a word: `worker` and `workers`, and not `workered` or a substring of
 * a longer word. Case is not a distinction a reader makes.
 */
export const RETIRED = /\bworkers?\b/i

/**
 * The plugin skills whose own `SKILL.md` carries the retired word, which is the half of the
 * exemption the documents that describe it name — `website/README.md` and
 * `docs/website/redesign/README.md` both list them. They are the skills the ruling leaves
 * untouched until 0.9.0, so the word is expected in them and the case below holds this list
 * to the tree: a skill that starts or stops carrying it is a change the documents naming
 * these three have to make in the same commit, and this case is what says so.
 *
 * `setup` is neither in the list nor missing from it: its occurrences are a CI test suite's
 * own parallel workers — somebody else's build step, described in the skill's reference files
 * rather than about this product — and not the word item 2 retires, which is why the list is
 * read from each skill's own `SKILL.md` rather than from every file beneath it.
 */
export const SKILLS_THAT_CARRY_IT = ['parallelism', 'routing', 'working']

/**
 * The one file of the site's tree that still carries the retired word, by its path under
 * `website/`, as a named and dated exception rather than a gap in the sweep.
 *
 * `website/content/cli-reference.md` is generated (W0.4) from the CLI's own help text, and
 * its second paragraph is the root command's short description, which still reads "Fleeting
 * cloud workers for coding agents". The owner's ruling of 2026-10-18 leaves the CLI's help
 * text, this generated file and the `SKILLS_THAT_CARRY_IT` skills' wording to the 0.9.0
 * work, and the file may not be hand-edited: changing that line here would mean either
 * editing generated output or changing the command's help, both of which the ruling puts in
 * that later round. The website's own copy — the pages, the data files, the components — is
 * the whole of item 2 and is guarded as before.
 *
 * **Dated, and expiring.** The list is not a permanent hole: the case below reads
 * `website/content` and holds this list equal to the files there that carry the word, so when
 * the 0.9.0 work changes the root command's description and regenerates the file, that case
 * fails and says to delete the line — an exception still standing for a file that no longer
 * needs one is a hole nobody can see.
 *
 * This exception was written on 2026-10-19, in the third fix round of the website changes of
 * 2026-10-18, and is recorded for the owner in the pull request's deferred list.
 */
export const FILES_THAT_KEEP_IT = ['content/cli-reference.md']

/**
 * The claim a figure's caption may not make again, in the two halves a rewording would come
 * back through. The hero's chat caption said "every line is the repository's own words, from
 * the skills the agent reads", and it was false about two of the things the figure printed:
 * the request is item 3's own example (a line of the changes spec, not of a skill), and the
 * three `Pull request n of 3` labels are the illustration's own — a run that has not happened
 * has no pull request numbers to quote. The figure is an illustration, the caption said so,
 * and the only lines in it a document vouches for are the ones the agent speaks.
 *
 * The two patterns are the claim's own phrases rather than the whole sentence, because the
 * sentence can be reworded and the claim cannot: "the repository's own words" is also true of
 * the flow's captions and the run's steps, which are quotations of README.md, so the phrase
 * is refused only in the shape that claims it of *every line* — the caption's own sentence.
 */
export const RETIRED_CLAIMS = [
  {
    claim: 'every line is the repository\'s own words',
    pattern: /every line is the (?:\w+'s )?own words/i,
  },
  {
    claim: 'from the skills the agent reads',
    pattern: /from the skills the agent reads/i,
  },
]

/**
 * Why the claim is refused, as the sentence a failure ends with. One copy, so the whole gate
 * says the same thing about the same defect.
 */
const CLAIM_WHY =
  'the figure is an illustration: its request is item 3\'s own example and its three results are labels for work a run would report, so only the lines the agent speaks are its skills\' words and the caption may not vouch for the rest (changes spec 2026-10-18, item 3; Global Constraint 6)'

/**
 * **The third rule: the sentence the Why GCP page answers with, and the phrases it may not
 * answer with instead** (the owner's decision D249 on the brief of 2026-10-21; the plan's Part
 * 2 step 5).
 *
 * The brief's own suggested framing — "no other cloud offers this as one integrated,
 * **disposable** project" — is made of a word the repository rejects: a managed project "is
 * **not disposable** in the way the spec means" (`docs/design/managed-projects.md:18`, and
 * `:211` lists what would be lost), so the sentence the page carries is the same sentence with
 * that word taken out (D248, ZF22). And the sentence the brief tells the page *not* to write —
 * "no real alternative" — is the other half: it is a claim about every other cloud, which is a
 * claim this repository has no sourced, dated fact for (ZF58, and `website/README.md`: "no
 * competitor comparison without a source and a date"), and the tree's own position reads the
 * other way — a second cloud is out of scope for 1.0 and wanted from contributors, which is
 * what `README.md`'s "Other clouds: help wanted" says (ZF21).
 *
 * So the rule has two halves and both are about what a page *says*: the phrases below are
 * refused wherever a built page carries them — its text, its title, its description, the same
 * three places the retired word is refused in — and the sentence below has to be on the page
 * the brief asked for, because a page whose framing row stopped rendering is a page whose
 * answer to the brief is missing. The exemption is the retired word's and the word ceiling's
 * (`DOCUMENT_PAGES`, D188): those three pages render a document of this repository rather
 * than the site's own copy, and a quotation of a document is not a page's own claim.
 *
 * The first three entries are the owner's list, and the fourth is D248's word: `disposable` is
 * refused for that decision's reason rather than as a word in general — the site may say a
 * container is thrown away, and what it may not say is that a project is. Each pattern is
 * written so that the entries do not overlap: "no real alternative" is refused as itself, and
 * "no alternative" is refused with the one word a rewrite would put between them, so a page
 * that says either is named once.
 */
export const BANNED_FRAMING = [
  { phrase: 'no real alternative', pattern: /\bno real alternative\b/i },
  { phrase: 'no alternative', pattern: /\bno (?:other |serious |viable |genuine |true )?alternative\b/i },
  { phrase: 'the only cloud', pattern: /\bthe only cloud\b/i },
  { phrase: 'disposable', pattern: /\bdisposable\b/i },
]

/**
 * The page the brief's framing is answered on, and the sentence it is answered with — the
 * brief's own sentence with "disposable" out of it, which the reconciliation says is the
 * supportable form (ZF22). The row it renders is `website/data/why-gcp.yaml`'s `framing` row,
 * and this is the pin that holds the page to it.
 */
export const REQUIRED_FRAMING = {
  url: '/why-gcp/',
  sentence: 'No other cloud offers this as one integrated project.',
}

/**
 * Why the phrasing is refused, as the sentence a failure ends with. One copy, so the whole
 * gate says the same thing about the same defect.
 */
const FRAMING_WHY =
  "the page's answer to the cloud question is that no other cloud offers this as one integrated project: a page that says there is no alternative is making a claim about every other cloud, which this repository has no sourced, dated fact for, and the repository's own position is that a second cloud is wanted rather than refused (D248, D249, ZF21, ZF22, ZF58)"

/**
 * The head's text that a reader meets outside the page: the `<title>`, the description and
 * the two cards. The metas are named one by one rather than swept up, because the head
 * carries values that are not text — `viewport`'s `width=device-width` is not a sentence
 * and a gate that read it would report noise.
 */
const HEAD_METAS = new Set(['description', 'og:title', 'og:description', 'og:site_name', 'twitter:card', 'twitter:title', 'twitter:description'])

function headText(html) {
  const out = []
  const title = /<title>([\s\S]*?)<\/title>/.exec(html)
  if (title) out.push(title[1])
  for (const m of html.matchAll(/<meta\b[^>]*>/g)) {
    const content = /\bcontent="([^"]*)"/.exec(m[0])
    const name = /\b(?:name|property)="([^"]*)"/.exec(m[0])
    if (content && name && HEAD_METAS.has(name[1])) out.push(content[1])
  }
  return out.join(' ')
}

/** The sentence the word appears in, so a failure shows what a reader would have read. */
function around(text, at) {
  const starts = Math.max(0, text.lastIndexOf('.', at) + 1)
  const end = text.indexOf('.', at)
  return text.slice(starts, end === -1 ? text.length : end + 1).trim().slice(0, 160)
}

/** Every way a text carries a retired claim, as the lines a failure lists. */
function claimFailures(text, where) {
  const out = []
  for (const { claim, pattern } of RETIRED_CLAIMS) {
    const found = pattern.exec(text)
    if (!found) continue
    out.push(`${where} carries ${JSON.stringify(found[0])} in ${JSON.stringify(around(text, found.index))}: ${CLAIM_WHY}.`)
  }
  return out
}

/** Every way one built page carries the retired word, as the lines a failure lists. */
export function pageWordingFailures(html, rel) {
  const out = []
  const where = [
    ['its text', textOf(parse(html)).replace(/\s+/g, ' ')],
    ['its head', headText(html).replace(/\s+/g, ' ')],
  ]
  for (const [what, text] of where) {
    out.push(...claimFailures(text, `${rel}'s ${what}`))
    const found = RETIRED.exec(text)
    if (!found) continue
    out.push(
      `${rel} carries ${JSON.stringify(found[0])} in ${what}: ${JSON.stringify(around(text, found.index))}. ` +
        "A run's unit of work is a job, and 'worker' is the word item 2 retired from the site (changes spec 2026-10-18).",
    )
  }
  return out
}

/** Every page of a built artifact that carries the retired word, D188's three pages excepted. */
export function wordingFailures(dist) {
  const out = []
  for (const page of builtPages(dist)) {
    if (DOCUMENT_PAGES.has(page.url)) continue
    out.push(...pageWordingFailures(page.html, page.rel))
  }
  return out
}

/**
 * Every way a built page breaks D249's framing rule, as the lines a failure lists: the phrases
 * a page may not carry, on every page, and the sentence the brief's page has to.
 *
 * The page's text and its head are read the way the retired word's rule reads them (its text,
 * its `<title>`, its description and its cards), and the exemption is the same one — D188's
 * three document-rendering pages, which are quotations rather than the site's own copy.
 */
export function framingFailures(dist) {
  const out = []
  for (const page of builtPages(dist)) {
    if (DOCUMENT_PAGES.has(page.url)) continue
    const text = [textOf(parse(page.html)), headText(page.html)].join(' ').replace(/\s+/g, ' ')
    for (const { pattern } of BANNED_FRAMING) {
      const found = pattern.exec(text)
      if (!found) continue
      out.push(`${page.rel} carries ${JSON.stringify(found[0])} in ${JSON.stringify(around(text, found.index))}: ${FRAMING_WHY}.`)
    }
    if (page.url !== REQUIRED_FRAMING.url) continue
    if (!text.includes(REQUIRED_FRAMING.sentence)) {
      out.push(
        `${page.rel} does not carry ${JSON.stringify(REQUIRED_FRAMING.sentence)}, which is the brief's own framing sentence with the word the repository rejects taken out of it: the page renders it from website/data/why-gcp.yaml's \`framing\` row, so a page without it is a page whose framing stopped rendering (D249, ZF22).`,
      )
    }
  }
  return out
}

/** Every way the site's own sources carry the retired word or a retired claim, as failure lines. */
export function sourceWordingFailures(files) {
  const out = []
  for (const [rel, body] of Object.entries(files)) {
    // The named exception, and only it: the generated CLI reference quotes the root
    // command's help text, which the ruling of 2026-10-18 leaves to the 0.9.0 work. Every
    // other file of the tree is read as before.
    if (FILES_THAT_KEEP_IT.includes(rel)) continue
    const flat = body.replace(/\s+/g, ' ')
    out.push(...claimFailures(flat, rel))
    const found = RETIRED.exec(body)
    if (!found) continue
    out.push(
      `${rel} carries ${JSON.stringify(found[0])} in ${JSON.stringify(around(flat, found.index))}: ` +
        "the site's own copy calls a run's unit of work a job (changes spec 2026-10-18, item 2).",
    )
  }
  return out
}

// ---- the built site ---------------------------------------------------------------------

/**
 * The site's own sources: every text file under `src/`, `data/`, `public/` and the generated
 * `content/`. The last is swept too because it is committed to the tree and rendered into
 * pages — a generated document is still a file of this site that a reader can read — and the
 * one file of it that carries the word is exempted by name, not by being outside the sweep
 * (`FILES_THAT_KEEP_IT`, and the case below that holds the list to what is there).
 */
function siteSources() {
  const files = {}
  const walk = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const file = path.join(dir, entry.name)
      if (entry.isDirectory()) walk(file)
      else if (/\.(astro|ts|mjs|css|yaml|json|txt|md|svg|webmanifest|html)$/.test(entry.name)) files[path.relative(SITE, file)] = readFileSync(file, 'utf8')
    }
  }
  for (const name of ['src', 'data', 'public', 'content']) walk(path.join(SITE, name))
  return files
}

test('the framing D249 asks for is on the Why GCP page, and the phrases it may not be made of are refused', (t) => {
  // The artifact first, so the plants below are the only thing that can fail the gate: the
  // page the brief asked for carries the sentence, and no page of the site carries a phrase
  // that claims something about every other cloud.
  const shipped = framingFailures(DIST)
  assert.deepEqual(shipped, [], `the built site carries framing this gate refuses:\n  ${shipped.join('\n  ')}`)

  // One page per phrase, each in a place the rule looks, and the sentence's own page passing:
  // a gate that read nothing would pass all of these.
  const files = {
    'why-gcp/index.html': shell(`<h1>Why GCP</h1><p>${REQUIRED_FRAMING.sentence}</p>`),
    'faq/index.html': shell('<h1>FAQ</h1><p>There is no real alternative to one project.</p>'),
    'console/index.html': shell('<h1>Console</h1><p>It is the only cloud that offers this.</p>', '<meta name="description" content="a disposable project">'),
    'docs/index.html': shell('<h1>Reference</h1><p>There is no alternative to a disposable project.</p>'),
  }
  const failures = framingFailures(distOf(t, files))
  assert.equal(failures.length, 3, failures.join('\n'))
  for (const rel of ['faq/index.html', 'console/index.html']) {
    assert.ok(
      failures.some((line) => line.startsWith(`${rel} `)),
      `${rel} carries a banned phrase and the gate did not name it:\n${failures.join('\n')}`,
    )
  }
  assert.ok(
    failures.some((line) => line.startsWith('console/index.html ') && line.includes('"disposable"')),
    `a description that calls a project disposable was not refused:\n${failures.join('\n')}`,
  )
  assert.ok(
    !failures.some((line) => line.startsWith('docs/index.html ')),
    `the gate refused D188's document page, which renders a document rather than the site's own copy:\n${failures.join('\n')}`,
  )
})

test('the Why GCP page without the framing sentence, or with the brief’s own version of it, is refused', (t) => {
  // The sentence is the page's whole answer to the brief's "Suggested framing", so its absence
  // is the defect this half exists for — a page that dropped the row, or rendered the section
  // without it, is a page that answers nothing.
  const dropped = framingFailures(
    distOf(t, { 'why-gcp/index.html': shell('<h1>Why GCP</h1><p>One project, and here is what it holds.</p>') }),
  )
  assert.ok(
    dropped.some((line) => line.startsWith('why-gcp/index.html ') && /does not carry/.test(line)),
    `the page without the framing sentence was not refused:\n${dropped.join('\n')}`,
  )

  // The near miss: the brief's own sentence, which a check that only looked for "one integrated
  // project" would pass. It carries the word the repository rejects, and it is not the sentence
  // the page answers with — so both halves of the rule name it.
  const brief = framingFailures(
    distOf(t, { 'why-gcp/index.html': shell('<h1>Why GCP</h1><p>No other cloud offers this as one integrated, disposable project.</p>') }),
  )
  assert.ok(
    brief.some((line) => line.includes('"disposable"')),
    `the brief's own framing sentence was not refused for its word:\n${brief.join('\n')}`,
  )
  assert.ok(
    brief.some((line) => /does not carry/.test(line)),
    `the brief's own framing sentence passed as the sentence the page has to carry:\n${brief.join('\n')}`,
  )
})

test("no built page outside D188's three carries the retired word 'worker' or the retired claim", () => {
  const failures = wordingFailures(DIST)
  assert.deepEqual(failures, [], `the built site carries wording this gate refuses:\n  ${failures.join('\n  ')}`)
})

test("the site's own sources carry no retired word and no retired claim", () => {
  const failures = sourceWordingFailures(siteSources())
  assert.deepEqual(failures, [], `the site's sources carry wording this gate refuses:\n  ${failures.join('\n  ')}`)
})

test('README.md carries no retired word, and the claim is refused there too', () => {
  const failures = sourceWordingFailures({ 'README.md': readFileSync(path.join(SITE, '..', 'README.md'), 'utf8') })
  assert.deepEqual(failures, [], `README.md carries wording this gate refuses:\n  ${failures.join('\n  ')}`)
})

test('the pages this rule skips are D188’s own list, and no wider', () => {
  // The exemption (`DOCUMENT_PAGES`, shared with the word ceiling through
  // `test/lib/exempt.mjs`) is the one place this gate looks away, so it is the one place a
  // defect could hide: a page added here is a page whose words nobody checks. It is held
  // equal to the pages the ruling names rather than trusted, so widening it means editing
  // D188's row in the same commit — and taking a page out of the ruling fails this too,
  // because the gates would then be exempting a page the ruling no longer does.
  const named = documentPagesNamedIn(readReconciliation())
  assert.ok(named.length > 0, `${RECONCILIATION}'s D188 row names no page and the exemption is read from it (website/test/lib/exempt.mjs)`)
  assert.deepEqual(
    [...DOCUMENT_PAGES].sort(),
    named.sort(),
    `the pages this rule skips, ${JSON.stringify([...DOCUMENT_PAGES].sort())}, are not the pages ${RECONCILIATION}'s D188 row names, ${JSON.stringify(named.sort())}: the exemption is that ruling's list, and the ceiling in test/budget.mjs reads the same set (D188)`,
  )
})

test('the plugin skills the documents name as carrying the old wording are the ones that do', () => {
  // The exemption is written down in three places — this file's header, `website/README.md`
  // and the redesign's README — as the skills whose wording the working spec's item 2 leaves
  // alone until 0.9.0. A list in prose is a list nobody re-reads, so the prose names three and
  // this reads the tree: the word is where they say, and a fourth skill carrying it (or one of
  // these three losing it) fails here rather than in a reader's head.
  const dir = path.join(REPO, 'plugin', 'skills')
  const carrying = readdirSync(dir, { withFileTypes: true })
    .filter((entry) => entry.isDirectory())
    .map((entry) => entry.name)
    .filter((name) => {
      const skill = path.join(dir, name, 'SKILL.md')
      return existsSync(skill) && RETIRED.test(readFileSync(skill, 'utf8'))
    })
    .sort()
  assert.deepEqual(
    carrying,
    [...SKILLS_THAT_CARRY_IT].sort(),
    `the plugin skills whose own SKILL.md carries ${RETIRED}, ${JSON.stringify(carrying)}, are not the ones the site's documents name, ${JSON.stringify([...SKILLS_THAT_CARRY_IT].sort())}: the word stays in those skills until 0.9.0 and the documents that name them move in the same commit (changes spec 2026-10-18, item 2)`,
  )
})

test('the generated file that keeps the old wording is the one named here, and only until 0.9.0', () => {
  // The exception is one path in a list, and a list is what this holds to the tree: the files
  // under `website/content` that carry the word are exactly `FILES_THAT_KEEP_IT`. Two
  // directions, and both matter. A second generated document that starts carrying the word is
  // a defect the sweep would otherwise look past; and when the 0.9.0 work changes the root
  // command's description and regenerates `cli-reference.md`, this fails with an empty list on
  // the left and says to delete the line — an exception nobody needs is an exemption that goes
  // on hiding whatever lands behind it.
  const carrying = Object.entries(siteSources())
    .filter(([rel]) => rel.startsWith('content/'))
    .filter(([, body]) => RETIRED.test(body))
    .map(([rel]) => rel)
    .sort()
  assert.deepEqual(
    carrying,
    [...FILES_THAT_KEEP_IT].sort(),
    `the generated files under website/content carrying ${RETIRED}, ${JSON.stringify(carrying)}, are not the named exception(s), ${JSON.stringify([...FILES_THAT_KEEP_IT].sort())}: the CLI reference quotes the root command's help text, the owner's ruling of 2026-10-18 leaves that wording to the 0.9.0 work, and this list is the whole of the exemption (changes spec 2026-10-18, item 2)`,
  )
})

// ---- the refusals, kept -----------------------------------------------------------------

function distOf(t, files) {
  const dist = mkdtempSync(path.join(tmpdir(), 'fugaro-wording-'))
  t.after(() => rmSync(dist, { recursive: true, force: true }))
  for (const [rel, body] of Object.entries(files)) {
    mkdirSync(path.dirname(path.join(dist, rel)), { recursive: true })
    writeFileSync(path.join(dist, rel), body)
  }
  return dist
}

const shell = (body, head = '') => `<!DOCTYPE html><html lang="en"><head><title>t</title>${head}</head><body><main>${body}</main></body></html>`

test('the word is refused in a page’s text, in its title and in its card, and only where the rule looks', (t) => {
  const files = {
    // The defect as it shipped once: a data file's answer, rendered into a page a reader
    // reads, and a description that says the same thing in the head.
    'faq/index.html': shell('<h1>Fugaro</h1><p>A fleet of ephemeral cloud workers for coding agents.</p>'),
    'caps/index.html': shell('<h1>Caps</h1><p>the figures</p>', '<meta name="description" content="one worker per task">'),
    'ok/index.html': shell('<h1>Fugaro</h1><p>Ephemeral cloud jobs that turn tasks into pull requests.</p>'),
    // The exemption, exactly as wide as D188's list: the page that renders the CLI
    // reference keeps the root command's help text, and its own copy is not the site's.
    'docs/index.html': shell('<h1>Reference</h1><p>Fleeting cloud workers for coding agents</p>'),
  }
  const failures = wordingFailures(distOf(t, files))
  assert.equal(failures.length, 2, failures.join('\n'))
  assert.ok(failures.some((line) => line.startsWith('faq/index.html ')), failures.join('\n'))
  assert.ok(failures.some((line) => line.startsWith('caps/index.html ')), failures.join('\n'))
})

test('the word is refused in README.md and in the site’s own sources', () => {
  const readme = ['# Fugaro', '', '**Fleeting cloud workers for coding agents: one task, one container, one PR, then gone.**', ''].join('\n')
  assert.match(sourceWordingFailures({ 'README.md': readme }).join('\n'), /README\.md carries "workers"/)

  const sources = {
    'data/faq.yaml': 'answer: >-\n  A fleet of ephemeral cloud workers for coding agents.\n',
    'src/components/Hero.astro': '<p>Ephemeral cloud jobs</p>\n',
  }
  const failures = sourceWordingFailures(sources)
  assert.equal(failures.length, 1, failures.join('\n'))
  assert.ok(failures[0].startsWith('data/faq.yaml '), failures.join('\n'))
})

test('the retired claim is refused where a caption is written, and in the sources', (t) => {
  // The caption as it shipped, on a built page: the sentence claimed every line of the figure
  // was a quotation, and two of them are not. A wording that comes back anywhere a reader
  // reads it — a component's caption, a data file's answer — fails here with the file named.
  const shipped = 'An illustration of the workflow, not a recording of a session: every line is the repository\'s own words, from the skills the agent reads.'
  const failures = wordingFailures(distOf(t, { 'index.html': shell(`<h1>Fugaro</h1><figcaption>${shipped}</figcaption>`) }))
  assert.equal(failures.length, RETIRED_CLAIMS.length, failures.join('\n'))
  for (const { claim } of RETIRED_CLAIMS) {
    assert.ok(
      failures.some((line) => line.startsWith("index.html's its text ") && line.includes(claim)),
      `the caption came back and the gate did not name ${JSON.stringify(claim)}:\n${failures.join('\n')}`,
    )
  }

  // The same sentence in the site's own sources, which is where a caption is written: a
  // component that puts the claim back in fails before the next build, not after it. The
  // components named here are the site's other captions. `AgentChat.astro`, whose caption this
  // rule was written for, left the site with the owner's approved draft of 2026-10-19, and
  // `ArchitectureDiagram.astro` — whose true sentence about the flow's own captions stood here
  // as the second file, the one the gate must *not* refuse — left it with the owner's ruling of
  // 2026-10-20. The run's picture is what carries the repository's words now, and its build-time
  // guard is where the same claim would be written.
  const sources = {
    'src/components/ScreenshotSlider.astro': `<figcaption>${shipped}</figcaption>\n`,
    'src/components/RunDiagram.astro': "<p>every word of the picture is the repository's own words</p>\n",
  }
  const inSources = sourceWordingFailures(sources)
  assert.equal(inSources.length, RETIRED_CLAIMS.length, `the claim in a component was not refused:\n${inSources.join('\n')}`)
  assert.ok(
    inSources.every((line) => line.startsWith('src/components/ScreenshotSlider.astro ')),
    `the gate refused a true sentence about the run's labels, which are quotations of the repository's documents:\n${inSources.join('\n')}`,
  )
})
