// website/test/console-option.mjs — the console, offered on Getting Started (the owner's
// changes spec of 2026-10-18, item 6).
//
// Getting Started presents installation as two options side by side: the CLI — the install
// paths `install.yaml` carries, the Homebrew cask first, each command with its copy control —
// and the desktop console, whose downloads a reader can reach without leaving the page. The
// acceptance is exactly that: "from Getting Started a user can install either the CLI or the
// console without leaving the page" — and, while `features.console_downloads` is false, that
// the console option offers nothing to download at all.
//
// Five things are checked here, and the last three build a copy of the site with the flag
// FLIPPED, because the state the owner's flip produces is not the state `npm run check`
// builds: a gate that only ever saw the flag-off page would say nothing about the day the
// console ships.
//
//   1. **Two options, CLI first.** `#install` holds a `cli` option and a `console` option, in
//      that order — the order the stacked layout renders at 390 px — and the CLI option is
//      still the CLI: a command, in a copy block with a named control. P5's fold claim rests
//      on the section opening with that command, and `fold.mjs` is the gate that holds the
//      ordering; the check here is that the two-option shape did not take the command away.
//   2. **The unreleased console offers nothing.** No `releases/download/` URL anywhere on the
//      page, and inside the console option exactly one link — the way to `/console/` — and no
//      button. This is the rule the Go boundary test enforces over the whole tree while the
//      flag is false (Global Constraint 9), read off the page a reader gets.
//   3. **The released console offers the release's own assets.** With `site.yaml`'s flag and
//      the data file's `available` both true, the option renders one link per published asset
//      of `console-vX.Y.Z`, at the URL derived from the repository, the tag and the published
//      name — the same URLs the console page's section renders, from the same reader — and
//      the CLI option is untouched. That last part is the point of deriving the URLs: the
//      flip is the whole change, so it must not move anything else.
//   4. **The flag-on build passes the gates that read the artifact.** The two-option page is
//      eight words longer than the one-option page, and Getting Started sits close to D188's
//      250-word ceiling, so the budget gate is run over the flag-on build rather than assumed:
//      the owner's flip is a one-line change, and it may not be the change that breaks the
//      ceiling.
//   5. **A flag and an `available` that disagree stop the build.** While the repository's own
//      rule says the console is unreleased, a data file that says it has shipped is refused,
//      naming both files: rendering downloads requires the flag, not only the data file.
//
// The page is read through the same HTML reader the other gates use (`lib/html.mjs`), and the
// console's facts through `lib/console-downloads.mjs`, so this file carries no second spelling
// of a URL. The flag-on builds happen in a copy of the repository in a temp directory
// (`lib/build.mjs`, the shape `console.mjs` and `origins.mjs` use) and are network-free.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync, writeFileSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import path from 'node:path'
import { DIST, SITE, find, parse, textOf, walk } from './lib/html.mjs'
import { buildCopy } from './lib/build.mjs'
import {
  SITE_YAML,
  assertReleaseURL,
  downloadNeedle,
  downloadsData,
  publishedDownloads,
  repoFile,
} from './lib/console-downloads.mjs'

/** Getting Started as the gates read it: the page the last `npm run build` wrote. */
function gettingStarted(dist = DIST) {
  return readFileSync(path.join(dist, 'getting-started', 'index.html'), 'utf8')
}

/**
 * The two install options inside `#install`, in document order. Read from the section rather
 * than from the page, because "a reader can install from this page" is a claim about the
 * section the page's own heading opens.
 */
function installOptions(page) {
  const main = find(parse(page), 'main')
  assert.ok(main, 'the built page carries no `main` landmark')
  const section = [...walk(main)].find((el) => el.attrs.get('id') === 'install')
  assert.ok(section, 'Getting Started carries no `#install` section, and installing is what the page is for')
  return [...walk(section)].filter((el) => el.attrs.has('data-option'))
}

/** The option `data-option` names, with the failure naming the options that are there. */
function optionNamed(page, name) {
  const options = installOptions(page)
  const found = options.find((el) => el.attrs.get('data-option') === name)
  assert.ok(
    found,
    `#install carries no "${name}" option; it carries ${options.map((el) => JSON.stringify(el.attrs.get('data-option'))).join(', ') || 'none'}`,
  )
  return found
}

/** Every link in a subtree, in document order. */
function linksWithin(el) {
  return [...walk(el)].filter((node) => node.name === 'a')
}

/** Every command in a subtree, as a reader copies it. */
function commandsWithin(el) {
  return [...walk(el)].filter((node) => node.name === 'pre').map((node) => textOf(node))
}

/** Whether an element sits inside a copy block, which is what makes its command copyable. */
function inCopyBlock(el) {
  for (let node = el.parent; node; node = node.parent) {
    if (node.attrs?.has('data-copy')) return true
  }
  return false
}

/**
 * A copy of the repository with the console released — `site.yaml`'s flag and the data file's
 * `available` both true, which is the whole of the owner's change — built, with everything
 * else left as the checkout has it.
 */
function releasedBuild(t) {
  return buildCopy(t, ({ site }) => {
    const yaml = path.join(site, 'data', 'site.yaml')
    writeFileSync(yaml, readFileSync(yaml, 'utf8').replace(/^(\s*console_downloads:\s*)false\s*$/m, '$1true'))
    const file = path.join(site, 'content', 'downloads.json')
    const data = JSON.parse(readFileSync(file, 'utf8'))
    data.available = true
    writeFileSync(file, `${JSON.stringify(data, null, 2)}\n`)
  })
}

test('Getting Started offers the CLI and the console side by side, the CLI first', () => {
  const options = installOptions(gettingStarted())

  assert.deepEqual(
    options.map((el) => el.attrs.get('data-option')),
    ['cli', 'console'],
    'the install section does not carry exactly the CLI option and the console option, in that order (the order the stacked layout renders at 390 px, where the CLI is the path a reader meets first)',
  )

  // The CLI option is still the CLI: at least one command, each in a copy block with a
  // control a reader can name. `copy.mjs` owns the full control contract; what is checked
  // here is that moving the paths into an option did not leave the console option as the
  // only thing on the page (the acceptance is that BOTH are installable from here).
  const cli = optionNamed(gettingStarted(), 'cli')
  assert.ok(commandsWithin(cli).length > 0, 'the CLI option carries no command, so the page no longer installs the CLI')
  for (const pre of [...walk(cli)].filter((el) => el.name === 'pre')) {
    assert.ok(
      inCopyBlock(pre),
      `the CLI option prints a command with no copy block around it (${JSON.stringify(textOf(pre).slice(0, 40))}), and the CLI option is the existing install path with its copy button`,
    )
  }
})

test('the console option offers nothing to download while the console is unreleased', () => {
  // The state the site ships in: `site.yaml` says the console is unreleased, and the Go
  // boundary test scans the whole of `website/` for a download URL while it does.
  assert.match(
    repoFile(SITE_YAML),
    /^\s*console_downloads:\s*false\s*$/m,
    `${SITE_YAML} does not have features.console_downloads false, and this case is about the unreleased console`,
  )

  const page = gettingStarted()
  const needle = downloadNeedle()
  assert.ok(
    !page.includes(needle),
    'Getting Started links releases/download/ while features.console_downloads is false',
  )

  // And inside the option: the data file's own unreleased sentence, one link — the way to the
  // page that says what the console is — and no button at all.
  const consoleOption = optionNamed(page, 'console')
  const note = downloadsData().not_released.short
  assert.ok(
    textOf(consoleOption).includes(note),
    `the console option does not render ${JSON.stringify(note)}, the sentence ${'website/content/downloads.json'} carries for the unreleased console`,
  )

  const links = linksWithin(consoleOption)
  assert.equal(
    links.length,
    1,
    `the console option carries ${links.length} link(s) while the console is unreleased, and the only one it may carry is the way to the console page (${links.map((el) => el.attrs.get('href')).join(', ')})`,
  )
  assert.equal(links[0].attrs.get('href'), '/console/', 'the console option\'s one link is not the way to the console page')
  assert.equal(textOf(links[0]).trim(), 'Learn more', 'the console option\'s link to the console page is not the "Learn more" the spec asks for')
  assert.ok(
    ![...walk(consoleOption)].some((el) => el.name === 'button'),
    'the console option carries a button while the console is unreleased',
  )
})

test('with the console released, the option renders the release\'s own assets and moves nothing else', (t) => {
  const built = releasedBuild(t)
  assert.equal(built.status, 0, `the build of the released site failed:\n${built.output}`)

  const page = gettingStarted(built.dist)
  const consoleOption = optionNamed(page, 'console')
  const expected = publishedDownloads()
  assert.ok(expected.length >= 2, `the data file names ${expected.length} asset(s), which is too few to prove the option renders the release`)

  const links = linksWithin(consoleOption)
  assert.equal(
    links.length,
    expected.length + 1,
    `the console option carries ${links.length} link(s) with ${expected.length} asset(s) published and the way to the console page (${links.map((el) => el.attrs.get('href')).join(', ')})`,
  )
  assert.deepEqual(
    links.map((el) => textOf(el).trim()),
    [...expected.map((download) => download.label), 'Learn more'],
    'the console option does not label each download with the platform and the asset\'s extension, in the order the release lists them, with the way to the console page last',
  )
  // Each download's URL is held to the release's own facts — the repository, the release path,
  // the tag and the published name — rather than compared with a URL composed in the test: see
  // `assertReleaseURL`. (`publishedDownloads` carries no URL for exactly that reason.)
  for (const [index, download] of expected.entries()) {
    assertReleaseURL(links[index].attrs.get('href'), download.name)
  }
  assert.equal(links[links.length - 1].attrs.get('href'), '/console/', 'the console option\'s last link is not the way to the console page')

  // The unreleased sentence is gone, and the way to the console page is not: "what is this
  // thing" is a question the released state answers no better than the unreleased one.
  assert.ok(
    !textOf(consoleOption).includes(downloadsData().not_released.short),
    'the console option still says the console is unreleased with the console released',
  )

  // One reader, two pages: the console page's own section renders each asset at the URL of the
  // release, held to the same facts as the option's links above.
  const consolePage = readFileSync(path.join(built.dist, 'console', 'index.html'), 'utf8')
  const section = /<section[^>]*id="downloads"[\s\S]*?<\/section>/.exec(consolePage)
  assert.ok(section, 'the console page carries no downloads section')
  const consoleHrefs = [...section[0].matchAll(/<a[^>]*href="([^"]+)"[^>]*>/g)].map((match) => match[1])
  assert.deepEqual(
    consoleHrefs,
    // The option's links, less the way to the console page: those two were just held to the
    // release's own facts, so this compares the console page against facts rather than against
    // a second derivation of them.
    links.slice(0, expected.length).map((el) => el.attrs.get('href')),
    'the console page and Getting Started\'s console option render different URLs for the same assets, and they are one reader of one data file',
  )

  // And the flip is the whole change: the CLI's commands are what they were before it.
  assert.deepEqual(
    commandsWithin(optionNamed(page, 'cli')),
    commandsWithin(optionNamed(gettingStarted(), 'cli')),
    'the CLI option changed when the console was released: the owner\'s change is the flag and a version bump, and it may not move the CLI',
  )
})

test('the released site still passes the gates that read the artifact', (t) => {
  // The flag-on build is the build the owner's flip produces, and every gate that reads the
  // built artifact is run over it rather than assumed to hold: the two-option page costs
  // words and Getting Started sits AT D188's 250-word ceiling, and a released console puts
  // two external links and a list on the page that the a11y, link and copy gates read.
  //
  // The gates are run in one nested `node --test` with `FUGARO_GATES_DIST` pointed at the
  // copy, which is the mechanism they document for reading a different build. `console.mjs`
  // is NOT among them: it holds the checkout's own `site.yaml` to `false`, and this build is
  // the one where the flag is on. `NODE_TEST_CONTEXT` is stripped, because a nested
  // `node --test` that inherits this process's test context refuses to run the files it is
  // given and exits zero — the trap the pass-count check below closes.
  const built = releasedBuild(t)
  assert.equal(built.status, 0, `the build of the released site failed:\n${built.output}`)

  const gates = ['a11y', 'budget', 'copy', 'fold', 'linkcheck', 'meta', 'origins']
  const env = { ...process.env, FUGARO_GATES_DIST: built.dist }
  delete env.NODE_TEST_CONTEXT
  const run = spawnSync(
    process.execPath,
    ['--test', ...gates.map((name) => path.join(SITE, 'test', `${name}.mjs`))],
    { env, encoding: 'utf8' },
  )
  const output = `${run.stdout ?? ''}${run.stderr ?? ''}`
  assert.equal(
    run.status,
    0,
    `a gate that reads the artifact refuses the site the owner's flag flip produces, so the flip is not the whole change:\n${output}`,
  )

  // And the run really ran: the cases above are the artifact gates' own, and a child that
  // skipped its files would report nothing while exiting zero. (`#` is escaped in captured
  // TAP output, hence the unescaping before both reads below.)
  const counts = /# pass (\d+)/.exec(output.replace(/\\#/g, '#'))
  assert.ok(counts, `the nested run reported no pass count, so nothing above says the gates read the released build:\n${output}`)
  assert.ok(
    Number(counts[1]) >= 70,
    `the nested run reported ${counts[1]} passing cases, and the gates that read the artifact declare far more than that: a run that skipped the files it was given exits zero`,
  )
  assert.match(
    output.replace(/\\#/g, '#'),
    /ok \d+ - every page is within the word ceilings the README states/,
    'the budget gate did not run its word-ceiling case over the released build, so nothing above says the two-option page fits the ceiling',
  )
})

test('a data file that says the console has shipped while the flag says it has not stops the build', (t) => {
  // The dangerous direction: `available` true while `site.yaml` is false would render a
  // download URL on a site the repository's own rule still refuses one. The build refuses the
  // pair, naming both files (W3.1's flag and W1.6's data file are one fact).
  const built = buildCopy(t, ({ site }) => {
    const file = path.join(site, 'content', 'downloads.json')
    const data = JSON.parse(readFileSync(file, 'utf8'))
    data.available = true
    writeFileSync(file, `${JSON.stringify(data, null, 2)}\n`)
  })

  assert.notEqual(built.status, 0, `the build rendered a released console while site.yaml says it is unreleased:\n${built.output}`)
  assert.match(built.output, /downloads\.json/, 'the failure does not name the data file')
  assert.match(built.output, /site\.yaml/, 'the failure does not name the site facts the flag lives in')
  assert.ok(
    !existsSync(path.join(built.dist, 'getting-started', 'index.html')),
    'the build wrote Getting Started with a download link on it',
  )
})
