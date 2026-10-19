// website/test/checklist.mjs — the live checklist's website check names the site's own
// selectors, files and refusals, and the site still carries every one of them.
//
// The defect this file exists for: `docs/gcp-live-checklist.md`'s **Check 70a** is a list of
// instructions a person follows in a browser, and this branch took the menu's `Menu` drawer
// out (the owner's brief of 2026-10-19, item 1) — the three group labels are the phone's menu
// now, in the header's one row. A check that tells the reader to read a selector the page no
// longer has, or to run a refusal no test makes any more, is an instruction nobody can
// follow, and the reader is left deciding whether the check or the site is wrong; the review
// of 2026-10-19 found the check describing the retired drawer. A check is text, so no browser
// can be pointed at it, but the **names in it** can be: every `.site-…` class it writes,
// every file it writes and every `…Failures` refusal it names is read out of the document
// here, at test time, and held to the built pages and to the sources. Rename one of them in
// the site and this case fails, rather than the check quietly describing a site that is gone.
//
// The names are **derived** from the document and never typed here: a list of selectors
// written into this file would be a second place to forget, and the subject is the check's
// own text. The same holds for the count — a rewrite that drops every class name from the
// check is refused below rather than passing an empty loop.
//
// The pages are read from `dist/`, which `npm run check` builds before it runs this file, and
// the checklist from the repository itself. No browser, no network.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'
import { DIST, REPO, SITE, builtPages, filesUnder } from './lib/html.mjs'

const CHECKLIST = path.join(REPO, 'docs', 'gcp-live-checklist.md')

/**
 * One `## Check <n>` section of the checklist: its heading, up to the next `## ` heading.
 * The check is read as a section rather than as the whole document so that a name in another
 * check's text is that check's to answer for.
 */
function checkSection(doc, check) {
  const lines = doc.split('\n')
  const start = lines.findIndex((line) => line.startsWith(`## Check ${check}`))
  assert.notEqual(
    start,
    -1,
    `docs/gcp-live-checklist.md has no "## Check ${check}" section: the case below reads a section, and a check that was renumbered or moved is a section it cannot find (the register at the top of the file records which task writes which number)`,
  )
  const rest = lines.slice(start + 1)
  const end = rest.findIndex((line) => line.startsWith('## '))
  return (end === -1 ? rest : rest.slice(0, end)).join('\n')
}

/** Every class name a built page has: its markup's, and the ones its stylesheet styles. */
function builtClasses() {
  const names = new Set()
  const classes = (text) => {
    for (const m of text.matchAll(/class="([^"]*)"/g)) {
      for (const name of m[1].split(/\s+/)) if (name) names.add(name)
    }
  }
  const selectors = (text) => {
    for (const m of text.matchAll(/\.([a-z][\w-]*)/g)) names.add(m[1])
  }
  for (const { html } of builtPages()) {
    classes(html)
    // Astro inlines the stylesheets today (`build.inlineStylesheets: 'always'`); reading the
    // page's own text covers them either way, and a linked sheet is read below.
    selectors(html)
  }
  for (const file of filesUnder(DIST)) {
    if (file.endsWith('.css')) selectors(readFileSync(path.join(DIST, file), 'utf8'))
  }
  return names
}

test('Check 70a names classes the built pages have', () => {
  const section = checkSection(readFileSync(CHECKLIST, 'utf8'), '70a')
  const named = [...new Set([...section.matchAll(/\.site-[a-z0-9-]+/g)].map((m) => m[0].slice(1)))]
  assert.ok(
    named.length > 0,
    'Check 70a names no `.site-…` class at all: the case below exists to hold the check\'s own names to the build, and a check rewritten without any of them would pass it while telling the reader nothing they can read out of the page',
  )
  const have = builtClasses()
  for (const name of named) {
    assert.ok(
      have.has(name),
      `Check 70a tells the reader to read \`.${name}\`, and no built page has that class or styles it: the check names a selector the site no longer carries — rename it in docs/gcp-live-checklist.md in the commit that moves it, or restore it in the site (§ the review of 2026-10-19: the check still described the retired \`Menu\` drawer)`,
    )
  }
})

test('Check 70a names files the repository has', () => {
  const section = checkSection(readFileSync(CHECKLIST, 'utf8'), '70a')
  // Backticked paths with a directory in them: `website/test/nav-hover.mjs`, `src/styles/base.css`.
  const named = [
    ...new Set([...section.matchAll(/`([\w.-]+\/[\w./-]+\.(?:mjs|js|ts|astro|css|json|yaml|yml|html|md|sh))`/g)].map((m) => m[1])),
  ]
  assert.ok(named.length > 0, 'Check 70a names no file at all: its method block reads what it needs out of the tree, and a check that names none of it is a check the reader has to take on trust')
  for (const file of named) {
    // The check writes the site's paths both ways: from the repository (`website/test/…`) and
    // from the site (`src/styles/base.css`), because that is how the commands it runs are
    // written. Either root resolves, and nothing else does.
    const found = [path.join(REPO, file), path.join(SITE, file)].some((p) => existsSync(p))
    assert.ok(
      found,
      `Check 70a names \`${file}\`, and there is no such file in the repository or in the site: the check points the reader at a path the tree does not have — fix the name in docs/gcp-live-checklist.md in the commit that moves the file`,
    )
  }
})

test('Check 70a names the refusals the website tests make', () => {
  const section = checkSection(readFileSync(CHECKLIST, 'utf8'), '70a')
  // The refusals the check says already hold the structural half: `navLayoutFailures`,
  // `navFailures`. They are read out of the tests' sources, so a renamed refusal is a check
  // pointing at nothing.
  const named = [...new Set([...section.matchAll(/`([A-Za-z][\w]*Failures)`/g)].map((m) => m[1]))]
  assert.ok(
    named.length > 0,
    'Check 70a names no `…Failures` refusal at all: the case below exists to hold the refusals the check says already hold the structural half to the tests that make them, and a rewrite that stopped naming them would pass it while pointing the reader at nothing (the same vacuous loop its two sibling cases guard against)',
  )
  const tests = readdirSync(path.join(SITE, 'test')).filter((name) => name.endsWith('.mjs'))
  const source = tests.map((name) => readFileSync(path.join(SITE, 'test', name), 'utf8')).join('\n')
  for (const name of named) {
    assert.ok(
      new RegExp(`(?:export )?function ${name}\\s*\\(`).test(source),
      `Check 70a names the refusal \`${name}\`, and no file in website/test/ defines it: the check says the build already refuses the thing it is about, and a renamed refusal leaves the reader looking for a name that is gone — rename it in docs/gcp-live-checklist.md in the commit that renames it`,
    )
  }
})
