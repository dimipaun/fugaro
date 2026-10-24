// website/test/faq-community.mjs — the FAQ and the community page (website plan, W1.10).
//
// The two pages this file checks are the site's document pages, and the rules they answer to
// are about what a page may *say* — and, for the community page, how it is *set*. Eight of them
// are checked here, and each one reads its source rather than the page's own copy of it
// (Review Focus: "the test must read the source, not the generator's own output"):
//
//   1. **The FAQ carries no comparison section** (the owner's feedback of 2026-10-21). The
//      section headed "How Fugaro compares" was rendered from `website/data/faq.yaml`'s
//      `comparison:` list, and that list never carried a row: D103's provenance rule — a row
//      about another tool carries a `source:` and a `date:` or the build stops — is exactly
//      what kept it empty, so what a reader met was a heading over a panel that said nothing.
//      The owner had it removed: the section, the panel, the empty state and their styles are
//      gone from the page, `faq.yaml` carries no `comparison:` key, and the two cases below
//      hold both halves of that — the built page carries no leftover of the section, with a
//      plant per shape a later edit would put back, and a `comparison:` key written into the
//      file again stops the build rather than sitting in the tree with nothing rendering it.
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
//   6. **The community page is set in the bold redesign's look** (redesign P1, `base.css` §2c;
//      the owner's brief of 2026-10-21). The page has no board of its own, so what it is held
//      to is the look the boards did fix and the home page ships: the headline on the display
//      scale's largest step with the accent's gradient on it, the section headlines on the
//      display scale's middle step, the community surfaces drawn as cards in a grid, and the
//      page's four bands still its four bands. This is a rule about the artifact's classes and
//      structure rather than about a pixel — the pixels are the owner's check at the preview
//      (RU1: the model that runs this gate cannot view a picture) — and it is what a later
//      edit drops by accident: a page whose title falls back to the element default still
//      passes every other gate in this repository, because every one of them is about what
//      the page says. Each rule below has a case that plants the defect it refuses.
//   7. **The FAQ answers the cloud question, and both answers about the cloud reach the Why
//      GCP page** (the owner's brief of 2026-10-21; the plan's Part 2 step 3, ZF55-ZF56). The
//      seventh entry is `do-i-need-gcp`, its answer is plain text — the only links an answer
//      carries are the file it is traceable to and its `more_*` pair — and the two entries
//      whose subject is the cloud say it once and hand the argument to `/why-gcp/`.
//   8. **The Why GCP page renders the rows `website/data/why-gcp.yaml` names, in the file's
//      order, each citing a file that exists** (D243; the plan's Part 2 steps 1-2). The page
//      writes none of its own facts: it renders the file's rows, each source is a link at the
//      branch `ci.yml` pushes to, and the reader — `website/src/content.ts`'s `loadWhyGcp` —
//      stops the build, naming the row, for a source that is not in the tree, an unknown key,
//      an empty field, a date that is not a date or a fifth point. The sentence the page has
//      to carry, and the phrases it may not be made of, are `wording.mjs`'s (D249).
//
// How it reads a page: `npm run check` builds the site before it runs anything here, so the
// real pages are read from `website/dist/` — the artifact a reader gets, not a rendering of
// the sources. The cases that need to *break* a file build a copy of the repository in a temp
// directory (the plan's own words for the first case, and `website/test/layout.mjs`'s
// harness), with the installed `node_modules` symlinked rather than copied. Every build is
// network-free: `npm ci` is not run here, and the only executable started is the site's own
// installed Astro.
//
// The removal of 2026-10-21 is held the way the rest of this file holds a rule: over the
// artifact, with a plant per shape a later edit would put back — and the file half of it is a
// build case, because the page refuses a `faq.yaml` key it does not render, so re-adding the
// section's data fails the build naming the key rather than leaving a fact nothing reads. A
// gate that only read the built page would pass on the day the file grew the key back and the
// page grew the section back with it over some later edit; the two cases fail on either.

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
      // Which parts of `plugin/` a build reads, and why each one: READ_UNDER_PLUGIN's
      // own comment (website/test/lib/build.mjs).
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

// ---- the comparison section is gone (the owner's feedback of 2026-10-21) ------------------

/**
 * What is left of the FAQ's comparison section, as the failure lines a reader sees. The
 * section rendered `website/data/faq.yaml`'s `comparison:` list, the list never carried a row,
 * and the owner had the section removed on 2026-10-21 — so the shapes below are what a later
 * edit would put back, and each one is refused by name:
 *
 *   1. the section itself — a band of the page heading nothing;
 *   2. its heading, "How Fugaro compares" — an empty promise even if the band is renamed;
 *   3. **the word `comparison` anywhere in the artifact**, which is what the section was
 *      written in: its anchor and its class (`comparison`), its panel, its empty state and its
 *      row count (`comparison-panel`, `comparison-empty`, `data-comparison-rows`) and the
 *      seven stylesheet rule blocks that were inlined into the page (`.comparison`,
 *      `.comparison-title`, `.comparison-panel`, `.comparison-empty`, `.comparison table`,
 *      `.comparison caption`, `.comparison th, .comparison td`). The word is the rule rather
 *      than that list: the list a first cut of this check carried named the three shapes a
 *      reader sees and left five of the seven rule blocks free to come back unnoticed, which
 *      is the way a "removed" section returns — one selector at a time;
 *   4. and the anchor a reader could link to, which `website/test/linkcheck.mjs` refuses when
 *      another page links it and nothing on this one carries it.
 *
 * It reads the built artifact, like every other case in this file, and it is a rule about the
 * page a reader gets rather than about the template: a section deleted from `faq.astro` and
 * rebuilt from somewhere else still fails here.
 */
function comparisonLeftovers(html) {
  const out = []
  if (/<section[^>]*\bid="comparison"/.test(html)) {
    out.push('the FAQ renders a comparison section again: the owner removed it on 2026-10-21, because the section a reader met was a heading over a panel that carried no row')
  }
  if (/\bHow Fugaro compares\b/.test(pageText(html))) {
    out.push('the FAQ carries the heading "How Fugaro compares" again: the section it headed was removed on 2026-10-21, and a heading with nothing under it is an empty promise')
  }
  // The word, in whatever token carries it: the whole identifier around it is matched so that
  // the failure line names the shape that came back (`comparison-panel`, `data-comparison-rows`)
  // rather than only the word, and so that a selector the list above did not know about is
  // still caught.
  const word = [...html.matchAll(/[\w-]*comparison[\w-]*/gi)].map((m) => m[0])
  if (word.length > 0) {
    const shapes = [...new Set(word)].join(', ')
    out.push(`the FAQ still carries the removed section's own word (${shapes}, ${word.length} time(s)): every shape of the section was written in it — its anchor and class, its panel, its empty state, its row count and all seven of its stylesheet rule blocks — so the word coming back is the section coming back`)
  }
  return out
}

test('the FAQ carries no comparison section, and faq.yaml carries no comparison data', () => {
  const page = builtPage('faq')
  assert.deepEqual(
    comparisonLeftovers(page),
    [],
    `the FAQ still carries the comparison section the owner removed on 2026-10-21:\n  ${comparisonLeftovers(page).join('\n  ')}`,
  )

  // The file half of the same removal, and the half a page-level check cannot see: a
  // `comparison:` key written into `faq.yaml` again is refused by the build (the case below),
  // and this reads the file so that the day the key comes back the reason is on the failure
  // line rather than only in the build's own message.
  const faq = readFileSync(path.join(REPO, FAQ_FILE), 'utf8')
  assert.ok(
    !/^comparison:/m.test(faq),
    `${FAQ_FILE} carries a \`comparison:\` key again, and the page that rendered it was removed on 2026-10-21: a fact nobody renders is a fact nothing holds to its source (W1.10 step 2)`,
  )
})

test('the FAQ is approved, and says so by saying nothing', () => {
  // D103, approved 2026-10-17: no review flag in the file, no notice on the page. This used to
  // be asserted inside the comparison case, which the removal of 2026-10-21 took away; the
  // rule is the page's own and is held here.
  const page = builtPage('faq')
  const faq = readFileSync(path.join(REPO, FAQ_FILE), 'utf8')
  assert.ok(!/^review_pending:/m.test(faq), `${FAQ_FILE} carries a review_pending flag again, and the FAQ is approved (D103)`)
  assert.ok(!page.includes('id="review-pending"'), 'the built FAQ carries the pending-review notice, and the owner approved it on 2026-10-17 (D103)')
  assert.ok(!/pending review/i.test(page), 'the built FAQ says it is pending review, and the owner approved it on 2026-10-17 (D103)')
})

test('every leftover of the comparison section is refused by its own line', () => {
  const page = builtPage('faq')
  assert.deepEqual(comparisonLeftovers(page), [], 'the removal rule fails on the built page, so the plants below are not proving what they name')

  // Each plant is the defect as the edit that would bring the section back writes it, on a
  // string the built page really carries, so a page whose markup moved fails here loudly
  // rather than leaving a case passing over nothing.
  const cases = [
    {
      what: 'the section put back',
      plant: page.replace('<div class="faq', '<section id="comparison" class="comparison"></section><div class="faq'),
      expected: /renders a comparison section again/,
    },
    {
      what: 'the heading put back',
      plant: page.replace('<h1', '<h2 class="comparison-title display-lg">How Fugaro compares</h2><h1'),
      expected: /carries the heading "How Fugaro compares"/,
    },
    {
      what: 'the panel put back',
      plant: page.replace('<div class="questions', '<div class="comparison-panel"></div><div class="questions'),
      expected: /comparison-panel/,
    },
    {
      what: 'the empty state put back',
      plant: page.replace('<div class="questions', '<p class="comparison-empty" id="comparison-empty">No comparison is published.</p><div class="questions'),
      expected: /comparison-empty/,
    },
    {
      what: 'the row count put back',
      plant: page.replace('<div class="questions', '<section id="rivals" data-comparison-rows="0"></section><div class="questions'),
      expected: /data-comparison-rows/,
    },
    {
      // All seven of the rule blocks the section was drawn with, and not only the two the
      // shapes above are caught by: `.comparison`, `.comparison-title`, `.comparison table`,
      // `.comparison caption` and `.comparison th, .comparison td` are five a rule reading
      // three class tokens let through, which is why this plant carries them and not
      // `.comparison-panel` again.
      what: 'the section’s stylesheet left behind',
      plant: page.replace(
        '</style>',
        '.comparison { display: flex; }\n.comparison-title { margin: 0; }\n.comparison table { border-collapse: collapse; }\n.comparison caption { text-align: left; }\n.comparison th, .comparison td { text-align: left; }\n</style>',
      ),
      expected: /removed section's own word/,
    },
  ]

  for (const { what, plant, expected } of cases) {
    assert.notEqual(plant, page, `${what}: the plant changed nothing, so the case below would prove nothing`)
    const failures = comparisonLeftovers(plant)
    assert.ok(
      failures.some((line) => expected.test(line)),
      `${what} was not refused (${expected}); the rule said:\n  ${failures.join('\n  ') || '(nothing)'}`,
    )
  }
})

test('a comparison: key written into faq.yaml again stops the build', async (t) => {
  // The file half of the removal, and the one thing the page cannot say about itself: the page
  // reads `version` and `questions` and refuses every other key, so the data the removed
  // section was rendered from cannot come back with nothing rendering it. The build names the
  // key, which is what tells whoever added it why the page will not take it.
  const built = buildWith(t, (copy) => rewrite(path.join(copy.site, 'data', 'faq.yaml'), (text) => `${text}\ncomparison: []\n`))

  assert.match(
    built.output,
    /the key "comparison" is not one this page reads/,
    `Expected: FAIL — the key "comparison" is not one this page reads. The removed section's data came back and the build refused it without naming the key, so the failure line does not say what to remove (W1.10 step 2)`,
  )
  assert.notEqual(
    built.status,
    0,
    `the build accepted a \`comparison:\` key in ${FAQ_FILE}, so the removed section's data can sit in the tree with nothing rendering it (the owner's feedback of 2026-10-21, W1.10 step 2):\n${built.output}`,
  )
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

  // The three documents the page is for, the licence and the scope statement that goes with
  // it, named as the paths they are.
  for (const file of ['CONTRIBUTING.md', 'CODE_OF_CONDUCT.md', 'LICENSE', 'LICENSING.md']) {
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

// ---- the community page's look (redesign P1; the owner's brief of 2026-10-21) -------------

/** The bands of the page, in the order the document writes them: every `<section>` with an
 *  `id`, as the id and the markup up to its own closing tag. Sections are not nested here —
 *  one band of the page holds cards and panels, never another band — so the first `</section>`
 *  after an opening tag is that section's. */
function bands(html) {
  return [...html.matchAll(/<section[^>]*\bid="([^"]+)"[^>]*>([\s\S]*?)<\/section>/g)].map(([, id, body]) => ({ id, body }))
}

/**
 * The bold redesign's look on the community page, as the failure lines a reader sees (redesign
 * P1, `base.css` §2c; the owner's brief of 2026-10-21). The page has no board of its own, so
 * what it is held to is the look the boards did fix and the home page ships, in the four
 * shapes a later edit can drop without any other gate noticing:
 *
 *   1. the page's headline is the display scale's largest step, with the accent's gradient on
 *      it — a title that fell back to the element default passes every other gate here, because
 *      they are all about what the page says;
 *   2. every section headline is the display scale's middle step;
 *   3. every community surface — an entry of the documents or the templates grid — is drawn as
 *      `base.css`'s `.card` in a `community-grid` list, and both grids are still there, which
 *      is the one shape the redesign draws an entry in;
 *   4. the page's four bands are still its four bands, each opening with a heading, so a
 *      reader's link into one of them (`/community/#issues`) still lands on something.
 *
 * It reads the built artifact, like every other case in this file, and it is a rule about
 * classes and structure rather than about a pixel: the pixels are the owner's check at the
 * preview (RU1).
 */
function boldLookFailures(html) {
  const out = []

  /** The classes of an element's opening tag, as the tokens the `class` attribute holds — the
   *  tokens and not a substring, because `community-card` contains the word `card` and a rule
   *  that read it as one would pass on an entry that lost `base.css`'s class (the case below
   *  plants exactly that). */
  const classes = (tag) => (/class="([^"]*)"/.exec(tag)?.[1] ?? '').split(/\s+/).filter(Boolean)

  /** The opening tags of every list inside a piece of markup. */
  const listsIn = (body) => [...body.matchAll(/<ul[^>]*>/g)].map(([tag]) => tag)

  // 1. The headline.
  const h1 = /<h1[^>]*>([\s\S]*?)<\/h1>/.exec(html)
  if (h1 === null) out.push('the page has no h1 to set its headline on')
  else {
    if (!classes(h1[0]).includes('display-xl')) {
      out.push(`the headline is not the display scale's largest step (${h1[0].slice(0, 90)}): the bold redesign sets a page's title at \`display-xl\``)
    }
    const gradient = [...h1[1].matchAll(/<span[^>]*>/g)].some(([tag]) => classes(tag).includes('gradient-text'))
    if (!gradient) {
      out.push(`the headline carries no gradient-text (${h1[1].slice(0, 90)}): the bold redesign puts the accent's gradient on the page's title`)
    }
  }

  // 2. The section headlines.
  const h2s = [...html.matchAll(/<h2[^>]*>/g)].map(([tag]) => tag)
  if (h2s.length === 0) out.push('the page has no h2, so it has no section headline to set')
  for (const tag of h2s) {
    if (!classes(tag).includes('display-lg')) out.push(`a section headline is not on the display scale (${tag}): the bold redesign sets a section's heading at \`display-lg\``)
  }

  // 3. The surfaces. A community surface is a link to a file in this repository — the three
  // documents, the licence and the issue templates are exactly that — and the page draws them
  // in two lists. The lists are found by those links and not by the `community-grid` class,
  // because a list that lost the class is one of the defects this rule refuses: selecting the
  // lists by the class first would skip it, and its entries would never be inspected for
  // `.card` either (the cases below plant exactly that, on one list and on both).
  const pageBands = bands(html)
  const surfaceLists = [...html.matchAll(/(<ul[^>]*>)([\s\S]*?)<\/ul>/g)]
    .map(([, open, body]) => ({ open, body }))
    .filter(({ body }) => /href="[^"]*\/blob\//.test(body))
  if (surfaceLists.length === 0) {
    out.push('the page draws no list of community surfaces: the documents, the licence and the issue templates are `.card` entries in a grid')
  }
  for (const { open, body } of surfaceLists) {
    if (!classes(open).includes('community-grid')) {
      out.push(`a list of community surfaces is not a community-grid (${open}): the bold redesign draws the community surfaces as cards in a grid`)
    }
    const items = [...body.matchAll(/<li[^>]*>/g)].map(([tag]) => tag)
    if (items.length === 0) out.push(`a list of community surfaces carries no entry (${open})`)
    for (const tag of items) {
      if (!classes(tag).includes('card')) out.push(`a community entry is not a card (${tag}): the bold redesign draws every community surface as \`base.css\`'s \`.card\``)
    }
  }
  // And each band that draws surfaces still draws one: a grid deleted outright leaves no list
  // behind for the rule above to read, so the band would be empty rather than wrong — the
  // entries and their links gone, with every gate here still green.
  for (const id of ['repository', 'issues']) {
    const band = pageBands.find((one) => one.id === id)
    if (band !== undefined && !listsIn(band.body).some((tag) => classes(tag).includes('community-grid'))) {
      out.push(`the ${id} band draws no community-grid list: its entries are cards in a grid, and a band that lost the grid lost the cards with it`)
    }
  }

  // 4. The bands, and the ids a reader's link may name.
  const ids = pageBands.map((band) => band.id)
  if (ids.join(' ') !== 'repository issues ci licence') {
    out.push(`the page's bands are ${ids.join(', ') || '(none)'}: they are the repository, the issues, the CI description and the licence, in that order`)
  }
  for (const band of pageBands) {
    if (!/<h2[\s>]/.test(band.body)) out.push(`the ${band.id} band has no heading: every band of the page is a heading and what it holds`)
  }

  return out
}

test('the community page is set in the bold redesign’s look', () => {
  assert.deepEqual(
    boldLookFailures(builtPage('community')),
    [],
    `the community page is not set in the look the redesign fixed (redesign P1):\n  ${boldLookFailures(builtPage('community')).join('\n  ')}`,
  )
})

test('every look refusal fires on the defect it names', () => {
  const page = builtPage('community')
  assert.deepEqual(boldLookFailures(page), [], 'the look rule fails on the built page, so the cases below are not proving what they name')

  // Each case is one edit to the built page — the defect as a later edit would write it — and
  // the refusal it names. The plants are strings the page really carries, so a page whose
  // markup moved fails here loudly rather than leaving a case passing over nothing.
  const cases = [
    {
      what: 'the headline off the display scale',
      plant: page.replace('class="community-title display-xl"', 'class="community-title"'),
      expected: /headline is not the display scale's largest step/,
    },
    {
      what: 'the headline with no gradient on it',
      plant: page.replace('<span class="gradient-text"', '<span'),
      expected: /headline carries no gradient-text/,
    },
    {
      what: 'a section headline off the display scale',
      plant: page.replace('<h2 class="community-heading display-lg"', '<h2 class="community-heading"'),
      expected: /section headline is not on the display scale/,
    },
    {
      what: 'a community entry that is not a card',
      plant: page.replace('<li class="card community-card"', '<li class="community-card"'),
      expected: /community entry is not a card/,
    },
    {
      // One list, not both, and one case per list: the rule reads the surfaces by their links,
      // so a single grid that lost `community-grid` is refused rather than skipped over.
      what: 'the documents list leaving the grid',
      plant: page.replace('<ul class="community-grid documents"', '<ul class="documents"'),
      expected: /list of community surfaces is not a community-grid/,
    },
    {
      what: 'the templates list leaving the grid',
      plant: page.replace('<ul class="community-grid templates"', '<ul class="templates"'),
      expected: /list of community surfaces is not a community-grid/,
    },
    {
      what: 'a band whose grid was deleted outright',
      plant: page.replace(/<ul class="community-grid templates"[\s\S]*?<\/ul>/, ''),
      expected: /issues band draws no community-grid list/,
    },
    {
      what: 'a band that lost its heading',
      // Astro's scoping attribute sits between the class and the `>` of every tag it renders,
      // so the plant reaches the heading by what it says rather than by the whole tag.
      plant: page.replace(/<h2[^>]*>Licence<\/h2>/, ''),
      expected: /licence band has no heading/,
    },
    {
      what: 'a band that is no longer one of the page’s four',
      plant: page.replace('<section id="issues" class="community-section"', '<section id="questions" class="community-section"'),
      expected: /bands are repository, questions, ci, licence/,
    },
  ]

  for (const { what, plant, expected } of cases) {
    assert.notEqual(plant, page, `${what}: the plant changed nothing, so the case below would prove nothing`)
    const failures = boldLookFailures(plant)
    assert.ok(
      failures.some((line) => expected.test(line)),
      `${what} was not refused (${expected}); the rule said:\n  ${failures.join('\n  ') || '(nothing)'}`,
    )
  }
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

// ---- the two elements that state the licence (F7, R2 as amended by O2) --------------------

/** The page's two licence elements, as the markup the build wrote for each: the **Licence
 *  card** — the documents grid's entry whose link is `LICENSE` — and the **`#licence` band**.
 *  F7 names exactly these two, and the band's old sentence ("it is the only text that governs
 *  what you may do with the code") is the one that stopped being true the day anything outside
 *  `LICENSE` governed part of the repository (R2). */
function licenceElements(html) {
  const branch = ciTriggers().branches[0]
  const cards = [...html.matchAll(/<li\b[^>]*>[\s\S]*?<\/li>/g)]
    .map(([li]) => li)
    .filter((li) => li.includes(`/blob/${branch}/LICENSE"`))
  assert.equal(
    cards.length,
    1,
    `the community page draws ${cards.length} cards linking LICENSE: the Licence card is one entry of the community grid, and it is one of the two elements that state the licence (F7)`,
  )
  const band = bands(html).find((one) => one.id === 'licence')
  assert.ok(band, 'the community page has no `#licence` band, and it is the second element that states the licence (F7)')
  return [
    { what: 'the Licence card', html: cards[0] },
    { what: 'the `#licence` band', html: band.body },
  ]
}

/** The built page with one licence element edited, the way a later edit would write it. */
function editLicenceElement(html, what, edit) {
  const one = licenceElements(html).find((element) => element.what === what)
  const edited = edit(one.html)
  assert.notEqual(edited, one.html, `${what}: the plant changed nothing, so the case below would prove nothing`)
  return html.replace(one.html, edited)
}

/**
 * The rules the two licence elements answer to, as the failure lines a reader sees (L1.5 of
 * `docs/licensing/implementation-plan.md`; F7, R2 as amended by O2). Each element links
 * `LICENSING.md` beside the licence itself and says what the split is: the core is what
 * `LICENSE` licenses, `.agents/` and the reserved `ee/` directory are not. The band names the
 * reserved directory, and the sentence the split retired — that `LICENSE` is "the only text
 * that governs" — is gone from the page, because it is false from the day one file outside it
 * is under another licence. A licence note that drifts is a claim about the source that the
 * source no longer makes, which is why every failure line names the element it sits in.
 */
function licenceSplitFailures(html) {
  const out = []
  const branch = ciTriggers().branches[0]
  const links = (markup, file) => markup.includes(`/blob/${branch}/${file}"`)
  const elements = licenceElements(html)

  for (const { what, html: element } of elements) {
    const text = pageText(element)
    if (!links(element, 'LICENSING.md')) {
      out.push(`${what} does not link LICENSING.md: the element states the licence, and LICENSING.md is the file that says which parts of the repository the licence does not cover`)
    }
    if (!/\bcore\b/.test(text)) {
      out.push(`${what} does not say the licence is the core's (${JSON.stringify(text.slice(0, 90))}): the split is that the core stays MIT and two parts of the repository stand outside it`)
    }
    if (!/\bparts\b[^.]*\bnot\b/.test(text)) {
      out.push(`${what} does not say which parts of the repository the licence does not cover: a licence stated without its scope is the claim the split retired`)
    }
  }

  const band = pageText(elements.find((element) => element.what === 'the `#licence` band').html)
  if (!/\breserved\b/.test(band) || !/\bee\b/.test(band)) {
    out.push('the `#licence` band does not name the reserved ee/ directory among the parts of the repository the licence does not cover: the reserved directory is the part a reader of this page is most likely to meet')
  }
  if (/only text that governs/.test(pageText(html))) {
    out.push('the community page still says the licence is "the only text that governs" what a reader may do with the code: that sentence stopped being true when ee/ was reserved outside LICENSE (F7, R2)')
  }

  return out
}

test('both elements that state the licence carry the core/ee split', () => {
  const page = builtPage('community')
  assert.deepEqual(
    licenceSplitFailures(page),
    [],
    `the community page's licence elements do not state the split (L1.5):\n  ${licenceSplitFailures(page).join('\n  ')}`,
  )
})

test('every licence-element refusal fires on the defect it names', () => {
  const page = builtPage('community')
  assert.deepEqual(
    licenceSplitFailures(page),
    [],
    'the split rule fails on the built page, so the cases below are not proving what they name',
  )

  const CARD = 'the Licence card'
  const BAND = 'the `#licence` band'
  const LICENSING_LINK = /<a href="[^"]*\/LICENSING\.md"[^>]*>LICENSING\.md<\/a>/

  // Each case is one edit to one element of the built page — the defect as a later edit would
  // write it — and the refusal it names.
  const cases = [
    {
      what: `${CARD} without the LICENSING.md link`,
      plant: (html) => editLicenceElement(html, CARD, (one) => one.replace(LICENSING_LINK, 'LICENSING.md')),
      expected: /the Licence card does not link LICENSING\.md/,
    },
    {
      what: `${BAND} without the LICENSING.md link`,
      plant: (html) => editLicenceElement(html, BAND, (one) => one.replace(LICENSING_LINK, 'LICENSING.md')),
      expected: /the `#licence` band does not link LICENSING\.md/,
    },
    {
      what: `${CARD} saying nothing about the core`,
      plant: (html) => editLicenceElement(html, CARD, (one) => one.replace(/\bcore\b/, 'project')),
      expected: /the Licence card does not say the licence is the core's/,
    },
    {
      what: `${CARD} saying nothing about what the licence leaves out`,
      plant: (html) => editLicenceElement(html, CARD, (one) => one.replace(/\bnot\b/, 'as well')),
      expected: /the Licence card does not say which parts of the repository the licence does not cover/,
    },
    {
      what: `${BAND} no longer naming the reserved ee/ directory`,
      plant: (html) => editLicenceElement(html, BAND, (one) => one.replace(/\breserved\b/, 'planned')),
      expected: /the `#licence` band does not name the reserved ee\/ directory/,
    },
    {
      what: 'the retired sentence back in the band',
      plant: (html) => editLicenceElement(html, BAND, (one) => one.replace(/<\/p>/, ' It is the only text that governs what you may do with the code.</p>')),
      expected: /still says the licence is "the only text that governs"/,
    },
  ]

  for (const { what, plant, expected } of cases) {
    const failures = licenceSplitFailures(plant(page))
    assert.ok(
      failures.some((line) => expected.test(line)),
      `${what} was not refused (${expected}); the rule said:\n  ${failures.join('\n  ') || '(nothing)'}`,
    )
  }
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

test('the FAQ answers the cloud question, and both answers about the cloud reach the Why GCP page', () => {
  // The owner's brief of 2026-10-21 asks for the seventh answer — "Do I need to be on GCP?" —
  // and for the two entries that are about the cloud to send a reader to the page that argues
  // it (the plan's Part 2 step 3, ZF55-ZF56). Three things make that true rather than asserted:
  // the file's own entries, the artifact's `<article>` list in the file's order, and the link
  // each entry's `more_href` names resolving to a page the build produced.
  const entries = faqEntries()
  const ids = entries.map((entry) => entry.id)
  assert.deepEqual(ids, [...new Set(ids)], `website/data/faq.yaml carries a duplicate id: ${JSON.stringify(ids)} — an id is the anchor the page renders and the name a build error uses`)
  assert.equal(
    ids.length,
    7,
    `website/data/faq.yaml carries ${ids.length} answers: the FAQ answers the six W1.10 step 1 fixes and the brief's seventh, "Do I need to be on GCP?" (the owner's brief of 2026-10-21; the plan's Part 2 step 3)`,
  )

  const page = builtPage('faq')
  const articles = [...page.matchAll(/<article\b[^>]*\bid="([^"]+)"/g)].map((m) => m[1])
  assert.deepEqual(articles, ids, 'the FAQ renders its answers in an order that is not the file’s: the page maps `website/data/faq.yaml`’s `questions` in order, so the two lists are the same list')

  const links = entries.filter((entry) => entry.more_href)
  assert.ok(links.length >= 3, `only ${links.length} answers carry a second link: the two the redesign wrote and the cloud question's are what this case is about`)
  for (const entry of links) {
    assert.ok(
      existsSync(path.join(SITE, 'dist', entry.more_href, 'index.html')),
      `the FAQ's "${entry.id}" links ${entry.more_href}, and the build produced no page there: a \`more_href\` is a route this site serves (W1.10 step 1)`,
    )
    assert.notEqual(entry.more_label ?? '', '', `the FAQ's "${entry.id}" carries a \`more_href\` and no \`more_label\`, so the link a reader follows has no words`)
  }

  const where = entries.find((entry) => entry.id === 'where-it-runs')
  const gcp = entries.find((entry) => entry.id === 'do-i-need-gcp')
  assert.ok(where, 'website/data/faq.yaml carries no `where-it-runs` entry, which is the answer the brief asks to hand the argument over')
  assert.ok(gcp, 'website/data/faq.yaml carries no `do-i-need-gcp` entry: the brief asks the FAQ the cloud question and the page answers it')
  assert.equal(gcp.question, 'Do I need to be on GCP?', 'the seventh entry is not the brief’s question')
  assert.equal(gcp.more_href, '/why-gcp/', 'the seventh entry does not reach the Why GCP page, which is the page the brief asks it to reach')
  assert.equal(
    where.more_href,
    '/why-gcp/',
    'the `where-it-runs` answer already answers the cloud question and does not reach the Why GCP page: the brief asks the two to say it once and hand the argument over',
  )
  assert.notEqual(where.answer, gcp.answer, 'the two answers about the cloud are the same answer, which is the page saying the same thing twice')

  // The answer itself is plain text (ZF56): the only links an answer carries are the file it is
  // traceable to and its `more_*` pair, so a link inside the answer's own paragraph is the
  // defect — a page whose answer is a second copy of the page it points at.
  const article = element(page, 'do-i-need-gcp')
  const answer = /<p class="question-answer"[^>]*>([\s\S]*?)<\/p>/.exec(article)
  assert.ok(answer, 'the seventh answer is not drawn as the page’s answer paragraph')
  assert.ok(!/<a\b/.test(answer[1]), `the seventh answer carries a link inside itself: ${JSON.stringify(answer[1])} — an answer is plain text and its links are its source and its \`more_*\` pair (ZF56)`)
  assert.equal(
    [...article.matchAll(/<a\b/g)].length,
    2,
    'the seventh answer carries a number of links that is not the two it should: the file it is traceable to and the Why GCP page',
  )
})

// ---- the Why GCP page (the owner's brief of 2026-10-21; the plan's Part 2) ----------------

/**
 * The rows `website/data/why-gcp.yaml` writes, read from the file rather than from the page.
 * The page's own reader is `website/src/content.ts`'s `loadWhyGcp`; this is a second,
 * independent read of the same file — the pattern `readCITriggers` above is the same one — so
 * the case below holds the page to the file rather than to the page's opinion of it.
 */
function whyGcpRows(file = path.join(SITE, 'data/why-gcp.yaml')) {
  const rows = { points: [], closing: [] }
  let section = null
  let row = null
  for (const line of readFileSync(file, 'utf8').split('\n')) {
    if (line.trimStart().startsWith('#') || line.trim() === '') continue
    const top = /^([a-z]+):\s*$/.exec(line)
    if (top) {
      section = top[1]
      row = section === 'framing' ? (rows.framing = {}) : null
      continue
    }
    const item = /^\s+- id:\s*(\S+)\s*$/.exec(line)
    if (item) {
      row = { id: item[1] }
      assert.ok(section in rows, `website/data/why-gcp.yaml writes an \`id\` under \`${section}\`, which is not a list the page renders`)
      rows[section].push(row)
      continue
    }
    const field = /^\s+([a-z]+):\s*(.*)$/.exec(line)
    if (field && row) row[field[1]] = field[2]
  }
  return rows
}

/**
 * The FAQ's entries as `website/data/faq.yaml` writes them: one block per `- id:`, with the
 * folded `answer` joined the way the page's own reader joins it.
 */
function faqEntries(file = path.join(SITE, 'data/faq.yaml')) {
  const entries = []
  let row = null
  let folded = null
  for (const line of readFileSync(file, 'utf8').split('\n')) {
    if (line.trimStart().startsWith('#')) continue
    if (/^[a-z_]+:/.test(line)) {
      row = null
      folded = null
      continue
    }
    if (!row && !/^\s+- id:/.test(line)) continue
    const item = /^\s+- id:\s*(\S+)\s*$/.exec(line)
    if (item) {
      row = { id: item[1] }
      entries.push(row)
      folded = null
      continue
    }
    const field = /^\s{4}([a-z_]+):\s*(.*)$/.exec(line)
    if (field) {
      if (/^[>|][-+]?$/.test(field[2])) {
        row[field[1]] = ''
        folded = field[1]
      } else {
        row[field[1]] = field[2]
        folded = null
      }
      continue
    }
    if (folded && /^\s{6}\S/.test(line)) row[folded] = `${row[folded]} ${line.trim()}`.trim()
  }
  return entries
}

test('the Why GCP page renders the rows its file names, in order, each citing a file that exists', () => {
  const rows = whyGcpRows()
  const page = builtPage('why-gcp')
  const ci = ciTriggers()
  const repository = /^repository:\s*(\S+)$/m.exec(readFileSync(path.join(SITE, 'data/site.yaml'), 'utf8'))[1]

  // The four points are the brief's, and the page draws them as the brief's list: the count is
  // the file's, so a row added or dropped moves the two together or fails here.
  assert.equal(rows.points.length, 4, `website/data/why-gcp.yaml carries ${rows.points.length} points: the page's \`<ol>\` is the brief's four numbered points, as the tree supports them (the plan's Part 2 step 2)`)
  assert.ok(rows.closing.length >= 1 && rows.framing, 'website/data/why-gcp.yaml carries no closing rows or no framing row, so the page has nothing to render below its list')

  const readList = (tag, cls) => {
    const list = new RegExp(`<${tag} class="claims ${cls}"[^>]*>([\\s\\S]*?)</${tag}>`).exec(page)
    assert.ok(list, `the built page carries no \`<${tag} class="claims ${cls}">\`: the page renders the file's rows in the two lists \`loadWhyGcp\` returns`)
    return [...list[1].matchAll(/<li\b([^>]*)>([\s\S]*?)<\/li>/g)].map((m) => ({
      id: /\bid="([^"]+)"/.exec(m[1])?.[1],
      html: m[2],
    }))
  }

  for (const [cls, wanted] of [['points', rows.points], ['closing', rows.closing]]) {
    const items = readList(cls === 'points' ? 'ol' : 'ul', cls)
    assert.deepEqual(
      items.map((item) => item.id),
      wanted.map((row) => row.id),
      `the ${cls} the page renders are not the rows website/data/why-gcp.yaml names, in the file's order: the page maps the file's rows, so the two lists are the same list`,
    )
    for (const [at, item] of items.entries()) {
      const row = wanted[at]
      assert.match(item.html, new RegExp(`Source: <a href="${escaped(`${repository}/blob/${ci.branches[0]}/${row.source}`)}"`), `the page's "${row.id}" does not link ${row.source} at ${ci.branches[0]}, the branch ${CI_FILE} pushes to: a source link at another branch is a link into nothing (W1.10)`)
      assert.ok(existsSync(path.join(REPO, row.source)), `website/data/why-gcp.yaml's "${row.id}" names ${row.source} as the file that proves it, and this repository does not have that file`)
      assert.match(item.html, new RegExp(`<time datetime="${escaped(row.date)}"`), `the page's "${row.id}" does not carry ${row.date} as its date: a claim about another cloud is one whose age a reader has to be able to judge (ZF58)`)
    }
  }

  // The framing row is the page's one-sentence answer, and it renders as itself with its own
  // source: the sentence's text is pinned by `wording.mjs` (D249), so what is held here is that
  // it is a row of the file rather than a sentence written into the template.
  assert.match(page, new RegExp(`>${escaped(rows.framing.text)}<`), `the built page does not render website/data/why-gcp.yaml's framing sentence ${JSON.stringify(rows.framing.text)}: the page renders the file's rows and writes none of them`)
  assert.match(page, new RegExp(`href="${escaped(`${repository}/blob/${ci.branches[0]}/${rows.framing.source}`)}"`), `the framing sentence does not carry its source (${rows.framing.source}) as a link`)
})

test('the Why GCP reader refuses a row the file cannot support, naming it', (t) => {
  // The reader is `website/src/content.ts`'s `loadWhyGcp`, and what it refuses is the shape
  // `loadFaq` refuses in (the plan's Part 2 step 2): an unknown key, an empty field, a `source`
  // that is not a file in the tree, a date that is not a date. Each case below plants one of
  // them in a copy of the repository and requires the build to stop with the row named —
  // because a row nothing renders, or a citation into nothing, is the defect a page of facts
  // has to fail on rather than ship.
  const file = path.join('website', 'data', 'why-gcp.yaml')
  const cases = [
    {
      what: 'a source that is not a file in this repository',
      edit: (text) => text.replace('    source: docs/backends.md\n', '    source: docs/backends-that-do-not-exist.md\n'),
      wanted: /names "docs\/backends-that-do-not-exist\.md"/,
    },
    {
      what: 'an unknown key',
      edit: (text) => text.replace('    source: docs/backends.md\n', '    source: docs/backends.md\n    reviewed_by: nobody\n'),
      wanted: /reviewed_by/,
    },
    {
      what: 'an empty field',
      edit: (text) => text.replace(/    text: >-\n      Fugaro's control plane[\s\S]*?backend seam\.\n/, "    text: ''\n"),
      wanted: /\.text is empty/,
    },
    {
      what: 'a date that is not a date',
      edit: (text) => text.replace('    date: 2026-10-22\n', '    date: the 22nd of October\n'),
      wanted: /\.date is "the 22nd of October"/,
    },
    {
      what: 'a fifth point',
      edit: (text) => text.replace('\nclosing:', "\n  - id: one-more-point\n    text: >-\n      A fifth point the brief does not make.\n    source: docs/backends.md\n    date: 2026-10-22\n\nclosing:"),
      // The reader's own message counts the rows it read, so the case can only pass on it: a
      // loose pattern would match some other failure's output and hold nothing.
      wanted: /carries 5 points, and the page renders the brief's four/,
    },
    {
      // A row's id is rendered as the row's own `id`, so two rows sharing one are two
      // elements with one name: a link to either resolves to the first, and the page has an
      // anchor a reader cannot reach the second claim by.
      what: 'two rows with one id',
      edit: (text) => text.replace('  - id: gcp-project\n', '  - id: control-plane\n'),
      wanted: /two rows share the id "control-plane"/,
    },
  ]

  for (const one of cases) {
    const built = buildWith(t, (copy) => rewrite(path.join(copy.root, file), one.edit), 'why-gcp')
    assert.notEqual(built.status, 0, `the build passed with ${one.what} in ${file}, and the page renders facts a reader acts on: a row the file cannot support has to stop the build:\n${built.output}`)
    assert.match(built.output, one.wanted, `the build stopped for ${one.what} without naming it, so nothing says which row to fix:\n${built.output}`)
  }
})

// ---- the bold look (the owner's approved boards of 2026-10-20) ----------------------------

test('the FAQ wears the site’s bold look, in the shapes the site writes once', () => {
  // The redesign gives the page the shapes `base.css` §2c writes for the whole site rather
  // than a set of its own: the display scale's headline with the accent's gradient on its last
  // word, and one card per answer. A page that drew its own copy of either would be the second
  // definition of a shape the site has, which is what the owner's "one look" is about — and a
  // page that quietly lost one would look like the site it was before. So the three are read
  // off the built page, and the headline's words are held to the string they were.
  const page = builtPage('faq')

  const h1 = /<h1[^>]*>([\s\S]*?)<\/h1>/.exec(page)
  assert.ok(h1, 'the built FAQ carries no `h1`')
  assert.match(
    h1[0],
    /\bdisplay-xl\b/,
    'the FAQ’s headline is not set in the site’s display scale (`base.css`’s `.display-xl`), which is the oversized heavy line every redesigned page opens with',
  )
  assert.match(
    h1[1],
    /<span[^>]*\bclass="gradient-text"[^>]*>[^<]+<\/span>/,
    'the FAQ’s headline carries no word in the accent’s gradient, which is the emphasis the boards put on part of a headline',
  )
  assert.equal(
    pageText(h1[1]),
    'Frequently asked questions',
    'the FAQ’s headline is not the heading it was: the redesign is a change of styling and layout, and not a word of the page moved with it',
  )

  // Each answer's opening tag, read for the two things it carries rather than for the order
  // they are written in: which answer it is, and whether it is drawn as a card. Astro writes
  // the attributes in the order the source has them — and adds one of its own — so an edit
  // that moved `id` above `class` is not a defect and must not read as one.
  const answers = [...page.matchAll(/<article\b[^>]*>/g)].map((m) => ({
    id: /\bid="([^"]+)"/.exec(m[0])?.[1],
    card: /\bclass="[^"]*\bcard\b[^"]*"/.test(m[0]),
  }))
  assert.ok(answers.length >= 5, `the FAQ renders ${answers.length} answers, which is too few for this check to mean anything`)
  assert.ok(
    answers.every((answer) => answer.id !== undefined),
    'an answer on the FAQ carries no `id`, which is the anchor the page’s own source links and the entry `website/data/faq.yaml` names it by',
  )
  assert.deepEqual(
    answers.filter((answer) => !answer.card).map((answer) => answer.id),
    [],
    'these answers are not drawn as the site’s card (`base.css`’s `.card`): the redesign lays each answer on one, so the six read as a set a reader scans rather than as a document',
  )
})

/**
 * The narrowest column a page of this site is read in: the 320 px viewport the checklist
 * measures the chrome at (`docs/gcp-live-checklist.md`), less `main`'s own gutter — `base.css`
 * gives the content container `--space-sm`, which is 1rem, on each side.
 */
const NARROWEST_COLUMN_PX = 320 - 2 * 16

/** A CSS length in the two units a track can demand one in, in px; null for anything else. */
function trackLengthInPx(value) {
  const m = /^([\d.]+)(rem|px)$/.exec(value)
  if (!m) return null
  return Number(m[1]) * (m[2] === 'rem' ? 16 : 1)
}

test('the FAQ’s answers grid cannot be wider than the column it sits in', () => {
  // The answers are the page's one grid, and a `grid-template-columns` that names a bare
  // length is a demand the column has to meet: a fixed 20rem track is 320 px whatever the
  // column is, and at the narrowest viewport the site is read at the column is 288 px — so
  // the cards would run off the right of the page and the document would scroll sideways at
  // the one width there is least room for it. `min(<length>, 100%)` is the form that yields
  // to the column; `0`, `auto`, a percentage and an `fr` cannot exceed it; and this reads the
  // page's own inlined stylesheet, which is the CSS the artifact a reader gets carries.
  const page = builtPage('faq')
  const css = [...page.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((m) => m[1]).join('\n')

  const tracks = [...css.matchAll(/grid-template-columns\s*:\s*([^;}]+)/g)].map((m) => m[1].trim())
  assert.ok(
    tracks.length > 0,
    'the built FAQ names no `grid-template-columns` at all, so this case proved nothing: the answers’ grid is the one this page has',
  )

  const tooWide = []
  for (const track of tracks) {
    // The values that yield come out first — `min(20rem, 100%)`, `var(…)`, `clamp(…)` — so
    // what is left is the lengths the declaration demands outright. `minmax(…)` is left in
    // place on purpose: it is the demand, and the lookbehind is what keeps `minmax(` from
    // being read as a call to `max(`.
    const demanded = track.replace(/(?<![A-Za-z-])(?:min|max|clamp|var)\([^()]*\)/g, ' ')
    for (const m of demanded.matchAll(/(?:^|[\s,(])([\d.]+(?:rem|px))/g)) {
      const px = trackLengthInPx(m[1])
      if (px !== null && px > NARROWEST_COLUMN_PX) tooWide.push(`${m[1]} in “${track}”`)
    }
  }
  assert.deepEqual(
    tooWide,
    [],
    `the FAQ asks for a grid track wider than the ${NARROWEST_COLUMN_PX} px the narrowest column has: ${tooWide.join(', ')} — write the minimum as \`min(<length>, 100%)\` so the track takes the column where the column is narrower than the length, which is the phone’s single column`,
  )
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
