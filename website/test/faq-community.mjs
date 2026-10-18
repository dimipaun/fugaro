// website/test/faq-community.mjs — the FAQ and the community page (website plan, W1.10).
//
// The two pages this file checks are the site's document pages, and the rules they answer to
// are about what a page may *say*. Five of them are checked here, and each one reads its
// source rather than the page's own copy of it (Review Focus: "the test must read the
// source, not the generator's own output"):
//
//   1. **The FAQ's comparison section has provenance or it does not exist** (D103, F51–F52).
//      A row about another tool is a claim the repository cannot verify, so a row carries a
//      `source:` and a `date:` — and the page *refuses to build* without them, which is why
//      the cases below mutate `website/data/faq.yaml` in a temp copy and require the build to
//      stop with the row's own id in the message. The empty section is the honest state on
//      day one, so the section renders no rows until the owner supplies sourced, dated ones.
//   2. **The FAQ is owner-approved and says so by saying nothing** (D103; approved 2026-10-17).
//      The built page carries no pending-review notice, and `faq.yaml` carries no review flag:
//      a stale `review_pending:` key fails the build as a key the page does not read.
//   3. **The community page's CI description is `ci.yml`'s, not prose somebody typed**
//      (F46, D101). The section is rendered from the workflow's own `on:` block, and the case
//      that proves it edits `ci.yml` in a temp copy and requires the page to follow — because
//      the failure this rule exists to prevent is a page that repeats `CONTRIBUTING.md`'s
//      stale claim (finding 11) after the workflow has moved on. The same file decides the
//      branch both pages' links point at, and the cases that move the push trigger build the
//      page again: a source link at a branch the project stopped building on is a link into
//      nothing, and the branch is read from the workflow rather than typed into the page.
//   4. **Neither page restates a document** (F47, Global Constraint 6). The FAQ and the
//      community page link `CONTRIBUTING.md`, `SECURITY.md` and `README.md`; they do not carry
//      their sentences, and the last case below asserts that mechanically: no sentence of
//      eight words or more from any of the three appears in either built page. Both sides of
//      that comparison are normalized the same way, so the markdown punctuation a document
//      writes (backticks, hyphens, headings) cannot hide a sentence from the check.
//   5. **No link runs into the word after it.** Astro drops the whitespace between a closing
//      tag and the next line's text, so a sentence written as a link followed by a newline
//      builds as `LICENSEis the whole of it`: the page reads wrong while every fact on it is
//      still true, which is why this one is checked over the built HTML of both pages.
//
// How it reads a page: `npm run check` builds the site before it runs anything here, so the
// real pages are read from `website/dist/` — the artifact a reader gets, not a rendering of
// the sources. The cases that need to *break* a file build a copy of the repository in a temp
// directory (the plan's own words for the first case, and `website/test/layout.mjs`'s
// harness), with the installed `node_modules` symlinked rather than copied. Every build is
// network-free: `npm ci` is not run here, and the only executable started is the site's own
// installed Astro.
//
// W1.10's red step is kept as the first two cases: with a comparison row carrying no source
// in `faq.yaml`, the check reports `comparison row "rival-probe" carries no source` — and the
// build refuses the row rather than the test skipping it, so the refusal cannot rot into a
// comment about a check that no longer refuses anything.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { cpSync, existsSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { READ_UNDER_PLUGIN, linkNodeModules } from './lib/build.mjs'

const SITE = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const REPO = path.dirname(SITE)

const FAQ_FILE = 'website/data/faq.yaml'
const CI_FILE = '.github/workflows/ci.yml'

// The row every provenance fixture below is built from: a synthetic tool, because the page is
// where a *sourced* comparison would be published and a test fixture naming a real product
// would be an unsourced, undated claim about it in the repository (F51, D103).
const PROBE = 'rival-probe'

// ---- reading the repository's own sources ---------------------------------------------

/**
 * The triggers of a workflow's `on:` block, read from the file itself: the trigger keys, the
 * branches a `push` names, and whether the block carries on-demand dispatch and a
 * `pull_request` trigger. Comments are skipped — `ci.yml` says in a comment why it has no
 * pull-request trigger, and a comment is not a trigger — and the block ends at the next
 * top-level key, which is how YAML says the same thing.
 *
 * The page has its own reader for this (it renders the description at build time); this one is
 * deliberately a second, independent read of the same source, so the case below asserts the
 * page against the file rather than against the page's opinion of it.
 */
function readCITriggers(file) {
  const lines = readFileSync(file, 'utf8').split('\n')
  const start = lines.findIndex((line) => line.trimEnd() === 'on:')
  assert.ok(start >= 0, `${file} has no top-level \`on:\` block: the community page's CI description is rendered from it`)

  const body = []
  for (let i = start + 1; i < lines.length; i++) {
    const line = lines[i]
    const trimmed = line.trim()
    if (trimmed === '' || trimmed.startsWith('#')) continue
    if (!/^\s/.test(line)) break
    body.push(line)
  }
  assert.ok(body.length > 0, `${file}'s \`on:\` block is empty, so there is nothing for the page to describe`)

  const indentOf = (line) => line.length - line.trimStart().length
  const triggerIndent = Math.min(...body.map(indentOf))
  const triggers = body.filter((line) => indentOf(line) === triggerIndent).map((line) => line.trim().replace(/:.*$/, ''))

  // The branches a `push` trigger names, when it names any: `branches: [main]`.
  const pushAt = body.findIndex((line) => indentOf(line) === triggerIndent && line.trim().startsWith('push:'))
  let branches = []
  if (pushAt >= 0) {
    for (let i = pushAt + 1; i < body.length && indentOf(body[i]) > triggerIndent; i++) {
      const m = /^branches:\s*\[(.*)\]\s*$/.exec(body[i].trim())
      if (m) branches = m[1].split(',').map((branch) => branch.trim()).filter((branch) => branch !== '')
    }
  }
  assert.ok(
    branches.length === 1,
    `${CI_FILE}'s push trigger names ${branches.length} branches (${branches.join(', ')}): the community page links one branch and describes one, so a second one is a decision this page has to be given rather than guessed at`,
  )

  return {
    triggers,
    branches,
    dispatch: triggers.includes('workflow_dispatch'),
    pullRequest: triggers.includes('pull_request'),
  }
}

const ciTriggers = (file = path.join(REPO, CI_FILE)) => readCITriggers(file)

// ---- reading a built page --------------------------------------------------------------

/** A page of the site's build output, or the message a reader needs to produce it. */
function builtPage(rel) {
  const file = path.join(SITE, 'dist', rel, 'index.html')
  assert.ok(
    existsSync(file),
    `${rel} was not built: run \`npm run check\`, which builds the site and then runs these tests (W1.1), so this check always reads the artifact a reader would get`,
  )
  return readFileSync(file, 'utf8')
}

/**
 * The text of a page — or of an element of one — as a reader reads it: the body, tags and
 * scripts gone, whitespace collapsed. A whole page is sliced from `<body`, because the head
 * carries the title and the meta description rather than prose a reader reads; a fragment
 * (`element(...)`, which is how the CI sentences below are read) has no head and no `<body>`,
 * so it is read whole.
 */
function pageText(html) {
  const body = html.includes('<body') ? html.slice(html.indexOf('<body')) : html
  return body
    .replace(/<script[\s\S]*?<\/script>/g, ' ')
    .replace(/<style[\s\S]*?<\/style>/g, ' ')
    .replace(/<[^>]+>/g, ' ')
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&nbsp;/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
}

/**
 * Text in the form the restatement comparison needs: lowercased, whitespace collapsed, and
 * the punctuation markdown uses for emphasis, code, headings and quoting turned into a space.
 * A document writes hyphenated names, backticked words and `#`-style headings that a page
 * renders without those characters, so normalizing one side only would leave those sentences
 * unmatchable — invisible to a check that claims to test them. Both sides of every comparison
 * go through this function, which is what makes the two the same text when the two are the
 * same sentence.
 */
function flatten(text) {
  return text
    .replace(/[*_`#>|-]/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
    .toLowerCase()
}

/** One element of the page, as written in the build output. */
function element(html, id) {
  const m = new RegExp(`<([a-z]+)[^>]*\\bid="${escaped(id)}"[\\s\\S]*?</\\1>`).exec(html)
  assert.ok(m, `the built page carries no element with id="${id}"`)
  return m[0]
}

/** An attribute of a fragment of markup, which is how the page's own facts are read. */
function attribute(markup, name) {
  const m = new RegExp(`${name}="([^"]*)"`).exec(markup)
  assert.ok(m, `${markup.slice(0, 120)} carries no ${name} attribute`)
  return m[1]
}

/** A literal string, as a regular expression that matches it and nothing else. */
function escaped(text) {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/** A directory's entries, sorted, so a case over "every template" is stable. */
function entries(dir) {
  assert.ok(existsSync(dir), `${dir} is missing: the community page links the issue templates it holds`)
  return readdirSync(dir).sort()
}

// ---- breaking a file and building again --------------------------------------------------

// What a site build cannot read, and therefore what the copies below leave out: the Go
// module's own tree and test data, the console application, and the installed and built
// directories (the last two are symlinked and rebuilt instead). `.github/` is *not* skipped:
// the community page renders `ci.yml` and the issue templates, which is what one of the cases
// below mutates.
const SKIP = new Set(['.git', 'internal', 'cmd', 'console', 'schemas', 'deploy', 'testdata'])
const SKIP_UNDER_WEBSITE = new Set(['node_modules', 'dist', '.astro'])

/** A copy of the repository in a temp directory, ready to be mutated and built. */
function copyOfTheRepository() {
  const dir = mkdtempSync(path.join(tmpdir(), 'fugaro-faq-community-'))
  const root = path.join(dir, 'repo')
  cpSync(REPO, root, {
    recursive: true,
    filter: (src) => {
      const name = path.basename(src)
      const parent = path.basename(path.dirname(src))
      if (path.dirname(src) === REPO && SKIP.has(name)) return false
      // `plugin/skills` is the one part of `plugin/` a build reads: the hero's chat
      // quotes the skills (website/test/lib/build.mjs).
      if (parent === 'plugin' && !READ_UNDER_PLUGIN.has(name)) return false
      if (parent === 'website' && SKIP_UNDER_WEBSITE.has(name)) return false
      return true
    },
  })
  linkNodeModules(path.join(root, 'website', 'node_modules'))
  return { dir, root, site: path.join(root, 'website') }
}

/** Build a copy, and hand back what the build said and what it produced. */
function build(copy, page) {
  const result = spawnSync(process.execPath, [path.join(copy.site, 'node_modules', 'astro', 'bin', 'astro.mjs'), 'build'], {
    cwd: copy.site,
    encoding: 'utf8',
    env: { ...process.env, ASTRO_TELEMETRY_DISABLED: '1' },
  })
  const output = `${result.stdout ?? ''}${result.stderr ?? ''}`
  const file = path.join(copy.site, 'dist', page, 'index.html')
  const html = result.status === 0 && existsSync(file) ? readFileSync(file, 'utf8') : null
  return { status: result.status, output, page: html }
}

/** Copy the repository, mutate one file, build the copy, and clean up after the test. */
function buildWith(t, mutate, page = 'faq') {
  const copy = copyOfTheRepository()
  t.after(() => rmSync(copy.dir, { recursive: true, force: true }))
  mutate(copy)
  return { ...build(copy, page), copy }
}

/** A file of the copy, rewritten through `replace`, which has to change something. */
function rewrite(file, replace) {
  const before = readFileSync(file, 'utf8')
  const after = replace(before)
  assert.notEqual(after, before, `the fixture changed nothing in ${file}, so the case below would prove nothing`)
  writeFileSync(file, after)
}

/** The comparison rows of `faq.yaml`, as the file writes them. */
function withComparison(rows) {
  return (text) => text.replace(/^comparison: \[\]$/m, `comparison:\n${rows}`)
}

/** One comparison row, indented as `faq.yaml` writes one. */
function row(id, fields) {
  return Object.entries({ id, ...fields })
    .map(([key, value]) => `    ${key}: ${value}\n`)
    .join('')
    .replace(/\n$/, '')
}

// ---- the provenance rule (D103) ----------------------------------------------------------

test('the FAQ refuses a comparison row that carries no source', async (t) => {
  const rows = `  - ${row(PROBE, { name: 'A rival tool', claim: 'It runs agents somewhere.', date: '2026-10-01' })}`
  const built = buildWith(t, (copy) => rewrite(path.join(copy.site, 'data', 'faq.yaml'), withComparison(rows)))

  assert.match(
    built.output,
    new RegExp(`comparison row "${PROBE}" carries no source`),
    `Expected: FAIL — comparison row "${PROBE}" carries no source. The build refused the row without naming it, so the author of a new row cannot find it (D103)`,
  )
  assert.notEqual(
    built.status,
    0,
    `the build accepted a comparison row with no source, so a comparison can be added without provenance (D103, F51):\n${built.output}`,
  )
})

test('the FAQ refuses a comparison row that carries no date', async (t) => {
  const rows = `  - ${row(PROBE, { name: 'A rival tool', claim: 'It runs agents somewhere.', source: 'https://example.invalid/rival' })}`
  const built = buildWith(t, (copy) => rewrite(path.join(copy.site, 'data', 'faq.yaml'), withComparison(rows)))

  assert.match(
    built.output,
    new RegExp(`comparison row "${PROBE}" carries no date`),
    `Expected: FAIL — comparison row "${PROBE}" carries no date: a claim about another tool without the date it was checked is a claim with no expiry (D103)`,
  )
  assert.notEqual(built.status, 0, `the build accepted a comparison row with no date:\n${built.output}`)
})

test('a comparison row with a source and a date is rendered, and the page carries no pending-review notice', async (t) => {
  // The positive half of the same rule, in one build: a row that carries both fields is
  // published, so the page is not merely refusing rows, it is rendering the sourced ones —
  // and, with a row in it, the page still carries no pending-review notice.
  const rows = `  - ${row(PROBE, {
    name: 'A rival tool',
    claim: 'It runs agents somewhere, according to its own documentation.',
    source: 'https://example.invalid/rival',
    date: '2026-10-01',
  })}`
  const built = buildWith(t, (copy) => {
    rewrite(path.join(copy.site, 'data', 'faq.yaml'), withComparison(rows))
  })

  assert.equal(built.status, 0, `the build refused a sourced, dated comparison row:\n${built.output}`)
  assert.ok(built.page, 'the build produced no page')
  assert.ok(
    built.page.includes('It runs agents somewhere, according to its own documentation.'),
    'the sourced row is not on the page, so the section renders nothing even when faq.yaml carries a row',
  )
  assert.ok(built.page.includes('https://example.invalid/rival'), 'the row is rendered without its source')
  assert.ok(built.page.includes('2026-10-01'), 'the row is rendered without its date')
  assert.ok(
    !built.page.includes('id="review-pending"') && !/pending review/i.test(built.page),
    "the owner approved the FAQ on 2026-10-17 and the page still says it is pending the owner's review (D103)",
  )
})

test('the comparison section renders no rows while faq.yaml carries none', async (t) => {
  // The real file, as the owner has it: no rows. The section is empty and says so rather than
  // rendering a table with nothing in it, and the two states are read from the same page:
  // `data-comparison-rows` is the page's own count, and the empty state exists with it.
  const page = builtPage('faq')
  const faq = readFileSync(path.join(REPO, FAQ_FILE), 'utf8')
  const empty = /^comparison:\s*\[\s*\]\s*$/m.test(faq)
  const section = element(page, 'comparison')

  if (empty) {
    assert.equal(attribute(section, 'data-comparison-rows'), '0', 'faq.yaml carries no comparison row and the page counts a different number')
    assert.ok(section.includes('id="comparison-empty"'), 'faq.yaml carries no comparison row and the section does not say so')
  } else {
    assert.notEqual(attribute(section, 'data-comparison-rows'), '0', 'faq.yaml carries comparison rows and the page renders none of them')
    assert.ok(!section.includes('id="comparison-empty"'), 'faq.yaml carries comparison rows and the section still renders its empty state')
  }

  // The FAQ is approved (D103, 2026-10-17): no review flag in the file, no notice on the page.
  assert.ok(!/^review_pending:/m.test(faq), `${FAQ_FILE} carries a review_pending flag again, and the FAQ is approved (D103)`)
  assert.ok(!page.includes('id="review-pending"'), 'the built FAQ carries the pending-review notice, and the owner approved it on 2026-10-17 (D103)')
  assert.ok(!/pending review/i.test(page), 'the built FAQ says it is pending review, and the owner approved it on 2026-10-17 (D103)')
})

// ---- the community page's facts -----------------------------------------------------------

test("the CI description is rendered from ci.yml's on: block", () => {
  const ci = ciTriggers()
  const page = builtPage('community')
  const section = element(page, 'ci')

  // The section's own facts, mechanically: the branch the workflow pushes to, and whether the
  // block carries on-demand dispatch and a pull-request trigger. A page that described CI
  // from memory would hold these on the day it was written and drift afterwards (F46).
  assert.equal(attribute(section, 'data-push-branches'), ci.branches.join(' '), `${CI_FILE} pushes to ${ci.branches.join(', ')} and the page names another branch`)
  assert.equal(attribute(section, 'data-on-demand'), String(ci.dispatch), `${CI_FILE}'s workflow_dispatch is ${ci.dispatch} and the page disagrees`)
  assert.equal(attribute(section, 'data-on-pull-requests'), String(ci.pullRequest), `${CI_FILE}'s pull_request trigger is ${ci.pullRequest} and the page disagrees`)

  // And in the sentences a reader reads: every branch the workflow names, "on demand" when
  // the workflow can be dispatched, and a negation in the pull-request sentence exactly when
  // the workflow has no pull-request trigger — because `CONTRIBUTING.md`'s stale paragraph
  // (finding 11) is what this page would repeat if it stopped reading the file.
  const triggers = pageText(element(page, 'ci-triggers'))
  for (const branch of ci.branches) {
    assert.ok(triggers.includes(branch), `the CI description does not name the branch ${JSON.stringify(branch)} that ${CI_FILE} pushes to`)
  }
  assert.equal(/\bon demand\b/.test(triggers), ci.dispatch, `${CI_FILE}'s workflow_dispatch is ${ci.dispatch} and the page's sentence says otherwise: ${JSON.stringify(triggers)}`)

  const pullRequests = pageText(element(page, 'ci-pull-requests'))
  assert.equal(
    /\b(?:not|no)\b/i.test(pullRequests),
    !ci.pullRequest,
    `ci.yml ${ci.pullRequest ? 'runs' : 'does not run'} CI on pull requests and the page says ${JSON.stringify(pullRequests)} (F46, D101)`,
  )
})

test('the CI section follows ci.yml when its triggers change', async (t) => {
  // The drift case: give the workflow a pull-request trigger in a copy and the page has to
  // describe that instead. A page that typed its description would keep the old claim, which
  // is the defect this rule exists to prevent.
  const built = buildWith(
    t,
    (copy) => rewrite(path.join(copy.root, CI_FILE), (text) => text.replace(/^on:\n/m, 'on:\n  pull_request:\n')),
    'community',
  )

  assert.equal(built.status, 0, `the build failed with a pull_request trigger in ci.yml:\n${built.output}`)
  const ci = ciTriggers(path.join(built.copy.root, CI_FILE))
  assert.equal(ci.pullRequest, true, 'the fixture did not add a pull-request trigger, so the case below would prove nothing')

  const section = element(built.page, 'ci')
  assert.equal(attribute(section, 'data-on-pull-requests'), 'true', 'ci.yml triggers on pull_request and the page still says it does not')
  assert.ok(
    !/\b(?:not|no)\b/i.test(pageText(element(built.page, 'ci-pull-requests'))),
    'ci.yml triggers on pull_request and the page still says no job runs on one (F46)',
  )
})

test("the community page links the repository's own community files, and each one exists", () => {
  const ci = ciTriggers()
  const page = builtPage('community')
  const repository = /^repository:\s*(\S+)$/m.exec(readFileSync(path.join(REPO, 'website/data/site.yaml'), 'utf8'))[1]

  // Every link to a file in the repository, as the page writes it: the branch it points at is
  // the one CI pushes to, and the path it points at is a file this repository has. A
  // community page whose link 404s is the download-link defect in a smaller size (Global
  // Constraint 9: the site never points at an artifact that does not exist).
  const links = [...page.matchAll(new RegExp(`href="${escaped(repository)}/(blob|tree)/([^/"]+)/([^"#]+)"`, 'g'))]
  assert.ok(links.length >= 4, `the community page links ${links.length} files in the repository: the community surfaces the page is for (CONTRIBUTING.md, CODE_OF_CONDUCT.md, the licence, the issue templates) are links, not prose (D101)`)

  for (const [, kind, branch, file] of links) {
    assert.equal(kind, 'blob', `the community page links ${file} as a ${kind}: every one of these is a file a reader opens`)
    assert.equal(branch, ci.branches[0], `the community page links ${file} at branch ${branch}, and ${CI_FILE} pushes to ${ci.branches[0]}`)
    assert.ok(existsSync(path.join(REPO, file)), `the community page links ${file}, which this repository does not have`)
  }

  // The three documents the page is for, named as the paths they are.
  for (const file of ['CONTRIBUTING.md', 'CODE_OF_CONDUCT.md', 'LICENSE']) {
    assert.ok(
      links.some(([, , , linked]) => linked === file),
      `the community page does not link ${file}: D101's answer is link, and do not restate`,
    )
  }

  // The issue templates are read from the directory rather than typed: every template the
  // repository has is linked, and `config.yml` — which is the chooser's configuration and not
  // a template — is not presented as one.
  const templates = entries(path.join(REPO, '.github/ISSUE_TEMPLATE')).filter((name) => name.endsWith('.yml') && name !== 'config.yml')
  assert.ok(templates.length > 0, '.github/ISSUE_TEMPLATE/ holds no template, so there is nothing for the page to link')
  for (const template of templates) {
    assert.ok(
      links.some(([, , , linked]) => linked === `.github/ISSUE_TEMPLATE/${template}`),
      `the community page does not link the issue template .github/ISSUE_TEMPLATE/${template}`,
    )
  }
  assert.ok(
    !links.some(([, , , linked]) => linked === '.github/ISSUE_TEMPLATE/config.yml'),
    'the community page links the template chooser\'s config.yml as an issue template',
  )
})

test('no link on either page runs into the word that follows it', () => {
  // Astro drops the whitespace between a closing tag and the text on the next line, so a
  // sentence written as `<a>LICENSE</a>` followed by a newline and `is the whole of it` is
  // built as "LICENSEis the whole of it" — the page reads wrong and no fact-check notices,
  // because every fact on it is still there. The punctuation a sentence may legitimately run
  // into (`</a>.`, `</a>,`, `</a>)`) is not a word, so the rule is a letter or digit.
  for (const page of ['faq', 'community']) {
    const glued = /<\/a>[A-Za-z0-9][^<]{0,40}/.exec(builtPage(page))
    assert.equal(
      glued,
      null,
      `website/src/pages/${page}.astro renders a link that runs into the word after it: ${JSON.stringify(glued && glued[0])} — write the space after the link as \`{' '}\``,
    )
  }
})

test('the licence is named, and it is the identifier LICENSE carries', () => {
  const page = builtPage('community')
  const first = readFileSync(path.join(REPO, 'LICENSE'), 'utf8').split('\n', 1)[0].trim()
  const identifier = first.replace(/\s+Licen[cs]e$/i, '').trim()
  assert.ok(identifier !== '', `LICENSE's first line is ${JSON.stringify(first)}, which names no licence identifier`)

  // The page states the licence inline — D101's answer, because the licence is a fact rather
  // than a claim about a third party — and the fact comes from the file, not from the page.
  assert.match(
    pageText(page),
    new RegExp(`\\b${identifier}\\b`),
    `the community page does not name the licence ${identifier} that LICENSE carries: D101's answer is that the licence is stated inline`,
  )
})

// ---- neither page restates a document -----------------------------------------------------

test('the FAQ names a source for every answer, and every source exists', () => {
  const ci = ciTriggers()
  const page = builtPage('faq')
  const repository = /^repository:\s*(\S+)$/m.exec(readFileSync(path.join(REPO, 'website/data/site.yaml'), 'utf8'))[1]
  const prefix = `${repository}/blob/`

  const answers = [...page.matchAll(/<article[^>]*\bid="([^"]+)"[\s\S]*?<\/article>/g)]
  assert.ok(answers.length >= 5, `the FAQ renders ${answers.length} questions: it answers what the repository can prove, and that is more than a handful of them (W1.10 step 1)`)

  for (const [article, id] of answers) {
    const sources = [...article.matchAll(new RegExp(`href="${escaped(prefix)}([^/"]+)/([^"#]+)"`, 'g'))]
    assert.ok(sources.length > 0, `the FAQ's "${id}" names no source: every answer is traceable to a file in this repository or it is not written (D102, Global Constraint 6)`)
    for (const [, branch, source] of sources) {
      // The branch is read from ci.yml by the page, and it is the branch CI pushes to: a
      // source link at a branch the project stopped building on is a link into nothing.
      assert.equal(branch, ci.branches[0], `the FAQ's "${id}" links ${source} at branch ${branch}, and ${CI_FILE} pushes to ${ci.branches[0]}`)
      assert.ok(existsSync(path.join(REPO, source)), `the FAQ's "${id}" names ${source} as its source, and this repository does not have that file`)
    }
  }
})

test('the FAQ follows ci.yml when the branch it pushes to changes', async (t) => {
  // The FAQ reads its branch from the workflow file rather than typing it, and this is the
  // case that holds the two together: move the push trigger to another branch in a copy and
  // every source link has to point at that branch instead. A page that carried `main` as a
  // string would keep pointing at it, which is the drift the reader exists to prevent.
  const built = buildWith(
    t,
    (copy) => rewrite(path.join(copy.root, CI_FILE), (text) => text.replace(/^(\s+branches:\s*\[)main(\])/m, '$1trunk$2')),
    'faq',
  )

  assert.equal(built.status, 0, `the build failed with another branch in ci.yml's push trigger:\n${built.output}`)
  const ci = ciTriggers(path.join(built.copy.root, CI_FILE))
  assert.equal(ci.branches[0], 'trunk', 'the fixture did not move the push trigger, so the case below would prove nothing')

  const links = [...built.page.matchAll(/href="[^"]*\/blob\/([^/"]+)\//g)].map((m) => m[1])
  assert.ok(links.length > 0, 'the FAQ renders no source link at all, so this case would prove nothing')
  for (const branch of links) {
    assert.equal(branch, 'trunk', `ci.yml pushes to trunk and the FAQ still links its sources at ${branch}`)
  }
})

test('the FAQ links the security page and the caps guide rather than summarizing them', () => {
  const page = builtPage('faq')

  // F41 and D99, the page-level form: a security claim is never paraphrased, so the FAQ's
  // answer points at the security page — which renders SECURITY.md — and the caps answer
  // points at the page that carries the figures and their provenance.
  const security = element(page, 'security-limits')
  assert.match(security, /href="\/security\/"/, 'the FAQ\'s security answer does not link the security page, so it either summarizes the model or says nothing (D99)')
  const caps = element(page, 'cost-and-caps')
  assert.match(caps, /href="\/caps-and-costs\/"/, "the FAQ's cost answer does not link the caps-and-costs page")
})

test('neither page restates README.md, CONTRIBUTING.md or SECURITY.md', () => {
  const pages = [
    { name: 'website/src/pages/faq.astro', text: flatten(pageText(builtPage('faq'))) },
    { name: 'website/src/pages/community.astro', text: flatten(pageText(builtPage('community'))) },
  ]

  // Every sentence of eight words or more a document carries, markdown stripped and
  // whitespace collapsed: a page that carried one of them would be the second copy of the
  // document — the copy that drifts first (F37, F47, Global Constraint 6). Eight words is
  // where "the same sentence" starts: shorter runs are phrases any two texts share.
  //
  // Both sides go through `flatten`, which is the point: a document writes `$HOME`-style
  // hyphens and backticks that the page renders without them, so normalizing only the
  // document side would hide every sentence carrying one of those characters from this
  // check — a check that cannot see 57% of the sentences it claims to test is a check that
  // passes for the wrong reason.
  for (const file of ['README.md', 'CONTRIBUTING.md', 'SECURITY.md']) {
    const sentences = sentencesOf(readFileSync(path.join(REPO, file), 'utf8'))
    assert.ok(sentences.length > 5, `${file} yields ${sentences.length} sentences of eight words or more, which is too few for this check to mean anything`)

    for (const page of pages) {
      for (const sentence of sentences) {
        assert.ok(
          !page.text.includes(sentence),
          `${page.name} carries a sentence of ${file}: ${JSON.stringify(sentence)}. The page links the document; it does not restate it (F37, F47)`,
        )
      }
    }
  }
})

/**
 * The sentences of a markdown document, as the page would have to carry them to restate it.
 * Each sentence goes through `flatten` so that it is compared to the page's text the way the
 * page comparison flattens it too.
 */
function sentencesOf(markdown) {
  return markdown
    .replace(/```[\s\S]*?```/g, '\n\n')
    .replace(/`[^`\n]*`/g, '')
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/^#{1,6}\s.*$/gm, '')
    .split(/(?<=[.!?])\s+/)
    .map(flatten)
    .filter((sentence) => sentence.split(' ').length >= 8)
}
